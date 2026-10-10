package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lthummus/fileflinger/internal/filesource"
	"github.com/lthummus/fileflinger/internal/handles"
	"github.com/lthummus/fileflinger/internal/store"
	pb "github.com/lthummus/fileflinger/proto"
)

type FileSource struct {
	pb.UnimplementedFileSourceServer
	Store   store.Store
	Log     *slog.Logger
	handles *handles.Handles
}

func (f *FileSource) Stat(ctx context.Context, req *pb.StatRequest) (*pb.StatResponse, error) {
	f.Log.Debug("in file source to stat", "code", req.Code)
	if req.RemoteIp == "" {
		return nil, status.Error(codes.InvalidArgument, "remote ip must be specified")
	}

	sh, err := f.Store.Peek(ctx, req.Code, req.RemoteIp)
	if err != nil {
		return nil, storeError(err)
	}

	f.Log.Debug("got share from db", "share_path", sh.Path, "filename", sh.Filename)
	var fi os.FileInfo
	if sh.Path == "" {
		f.Log.Debug("taking empty path and fetching from handles struct")
		var heldFile *os.File
		heldFile, err = f.handles.Get(req.Code)
		if err != nil {
			f.Log.Warn("shared file is missing", "code", req.Code, "path", sh.Path)
			return nil, status.Error(codes.NotFound, "file is no longer available")
		}
		defer heldFile.Close()

		fi, err = heldFile.Stat()
		if err != nil {
			f.Log.Warn("could not stat shared handle", "code", req.Code)
			return nil, status.Error(codes.NotFound, "file is no longer available")
		}
	} else {
		fi, err = os.Stat(sh.Path)
		if err != nil {
			f.Log.Warn("spooled shared file is missing", "code", req.Code, "path", sh.Path)
			return nil, status.Error(codes.NotFound, "file is no longer available")
		}
	}

	return &pb.StatResponse{
		Size:     fi.Size(),
		Filename: sh.Filename,
		Etag:     filesource.ETag(fi),
		Modified: timestamppb.New(fi.ModTime()),
	}, nil
}

func (f *FileSource) Read(req *pb.ReadRequest, stream pb.FileSource_ReadServer) error {
	if req.RemoteIp == "" {
		return status.Error(codes.InvalidArgument, "source ip must not be blank")
	}

	ctx := stream.Context()

	sh, err := f.Store.Claim(ctx, req.Code, req.RemoteIp)
	if err != nil {
		return storeError(err)
	}

	var sf *filesource.SharedFile
	if sh.Path == "" {
		var hf *os.File
		hf, err = f.handles.Get(req.Code)
		if errors.Is(err, os.ErrNotExist) {
			// revoked or shut down since Claim
			f.Log.Warn("shared file is missing", "code", req.Code)
			return status.Error(codes.NotFound, "file is no longer available")
		}
		if err != nil {
			f.Log.Warn("could not open shared file", "code", req.Code, "error", err)
			return fileError(err)
		}
		sf, err = filesource.OpenShareFromFile(hf, req.Offset, req.Length, req.IfMatchEtag)
		if err != nil {
			f.Log.Warn("could not open shared file for real", "code", req.Code, "error", err)
			return fileError(err)
		}
		defer hf.Close()
	} else {
		sf, err = filesource.OpenShare(sh.Path, req.Offset, req.Length, req.IfMatchEtag)
		if err != nil {
			f.Log.Warn("could not open shared file", "code", req.Code, "error", err)
			return fileError(err)
		}
	}

	defer sf.Close()

	// Send serializes the chunk before returning, so the read buffer can be
	// reused and no copy is needed here.
	return filesource.CopyChunks(ctx, sf, func(p []byte) error {
		return stream.Send(&pb.Chunk{Data: p})
	})
}

func fileError(err error) error {
	switch {
	case errors.Is(err, filesource.ErrNotFound):
		return status.Error(codes.NotFound, "file is no longer available")
	case errors.Is(err, filesource.ErrFileChanged):
		return status.Error(codes.FailedPrecondition, "file has changed since share")
	case errors.Is(err, filesource.ErrBadRange):
		return status.Error(codes.OutOfRange, "bad offset")
	default:
		return status.Error(codes.Internal, "could not open file")
	}
}

func storeError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrExpired):
		return status.Error(codes.NotFound, "not found or expired")
	case errors.Is(err, store.ErrExhausted):
		return status.Error(codes.ResourceExhausted, "download limit reached")
	default:
		return status.Error(codes.Internal, "lookup failed")
	}
}
