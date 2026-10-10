package filesource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/lthummus/fileflinger/proto"
)

type GRPC struct {
	client pb.FileSourceClient
	log    *slog.Logger
}

func NewGRPC(ctx context.Context, socketPath string, log *slog.Logger) (*GRPC, error) {
	conn, err := grpc.NewClient(fmt.Sprintf("unix://%s", socketPath),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(4<<20),
		grpc.WithInitialConnWindowSize(8<<20),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(8<<20)),
	)
	if err != nil {
		return nil, fmt.Errorf("filesource: GRPC: NewGRPC: could not initialize grpc connection: %w", err)
	}
	client := pb.NewFileSourceClient(conn)

	return &GRPC{
		client: client,
		log:    log,
	}, nil
}

func (g *GRPC) Stat(ctx context.Context, code string, remoteIP netip.Addr) (StatResponse, error) {
	metadata, err := g.client.Stat(ctx, &pb.StatRequest{Code: code, RemoteIp: remoteIP.String()})
	if err != nil {
		return StatResponse{}, g.convertGRPCError(err)
	}

	return StatResponse{
		Filename: metadata.Filename,
		Size:     metadata.Size,
		ETag:     metadata.Etag,
		Modified: metadata.Modified.AsTime(),
	}, nil
}

func (g *GRPC) Read(ctx context.Context, code string, offset int64, length int64, etag string, remoteIP netip.Addr) (<-chan []byte, error) {
	s, err := g.client.Read(ctx, &pb.ReadRequest{
		Code:        code,
		Offset:      offset,
		Length:      length,
		IfMatchEtag: etag,
		RemoteIp:    remoteIP.String(),
	})
	if err != nil {
		return nil, g.convertGRPCError(err)
	}

	byteChannel := make(chan []byte)

	go g.pumpBytes(s, byteChannel)

	return byteChannel, nil
}

func (g *GRPC) pumpBytes(s grpc.ServerStreamingClient[pb.Chunk], dest chan<- []byte) {
	for {
		chunk, err := s.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				slog.Debug("hit EOF")
				break
			}
			g.log.Warn("chunk stream terminated prematurely", "error", err)
			break
		}

		dest <- chunk.Data
	}

	slog.Debug("closing pump goroutine")
	close(dest)
}

func (g *GRPC) convertGRPCError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return ErrNotFound
	case codes.ResourceExhausted:
		// http.Error(w, "download limit reached", http.StatusGone)
		return ErrDownloadExhausted
	case codes.FailedPrecondition:
		// http.Error(w, "underlying file has changed, please restart", http.StatusConflict)
		return ErrFileChanged
	case codes.OutOfRange:
		return ErrBadRange
	case codes.Unauthenticated:
		//http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return ErrUpstreamGRPCError
	default:
		g.log.Error("upstream call failed", "error", err)
		return fmt.Errorf("some unknown error: %w", err)
	}
}
