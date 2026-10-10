package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lthummus/fileflinger/internal/fdpath"
	"github.com/lthummus/fileflinger/internal/fdsend"
	"github.com/lthummus/fileflinger/internal/peercred"
	"github.com/lthummus/fileflinger/internal/spool"

	"github.com/lthummus/fileflinger/internal/codegen"
	"github.com/lthummus/fileflinger/internal/store"
	pb "github.com/lthummus/fileflinger/proto"
)

type Admin struct {
	pb.UnimplementedAdminServer

	FDRecv     fdsend.Receiver
	Store      store.Store
	Log        *slog.Logger
	BaseURL    string
	DefaultTTL time.Duration

	BuildVersion string
	BuildCommit  string

	backing *backing
}

func (a *Admin) Version(_ context.Context, _ *pb.VersionRequest) (*pb.VersionResponse, error) {
	return &pb.VersionResponse{Version: a.BuildVersion, Commit: a.BuildCommit}, nil
}

func (a *Admin) StartShare(ctx context.Context, req *pb.StartShareRequest) (*pb.StartShareResponse, error) {
	peerCreds, err := peercred.FromGRPCContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not get peer credentials")
	}

	slog.Debug("got start share request", "uid", peerCreds.UID)

	fdShareKey, err := codegen.GenerateCode()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not generate FD share key")
	}

	a.FDRecv.Expect(fdShareKey, peerCreds.UID)

	return &pb.StartShareResponse{ShareKey: fdShareKey}, nil
}

const maxFilenameLen = 255

func validateFilename(name string) error {
	if name == "" {
		return errors.New("must not be empty")
	}

	if name == "." || name == ".." {
		return errors.New("must not be . or ..")
	}

	if len(name) > maxFilenameLen {
		return fmt.Errorf("must be at most %d bytes", maxFilenameLen)
	}

	if !utf8.ValidString(name) {
		return errors.New("must be valid UTF-8")
	}

	if strings.ContainsAny(name, `/\`) {
		return errors.New("must not contain path separators")
	}

	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New("must not contain control characters")
	}
	
	return nil
}

func (a *Admin) AcceptShare(ctx context.Context, req *pb.AcceptShareRequest) (*pb.AcceptShareResponse, error) {
	peerCreds, err := peercred.FromGRPCContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not get peer credentials")
	}

	f, err := a.FDRecv.Take(req.GetShareKey(), peerCreds.UID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not get transferred FD")
	}

	if req.Filename != nil {
		if err := validateFilename(req.GetFilename()); err != nil {
			f.Close()
			return nil, status.Errorf(codes.InvalidArgument, "invalid filename: %v", err)
		}
	}

	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, status.Error(codes.InvalidArgument, "could not stat file")
	}

	a.Log.Debug("stat file", "size", fi.Size(), "is_dir", fi.IsDir())

	if fi.IsDir() {
		f.Close()
		return nil, status.Error(codes.InvalidArgument, "can not be directory")
	}

	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, status.Error(codes.InvalidArgument, "must be regular file")
	}

	fullPath, err := fdpath.Path(f)
	if err != nil {
		f.Close()
		a.Log.Error("could not resolve path", "error", err)
		return nil, status.Errorf(codes.Internal, "could not resolve path")
	}

	a.Log.Debug("resolved path", "full_path", fullPath)

	allowed, err := a.allowed(ctx, fullPath)
	if err != nil {
		f.Close()
		return nil, status.Error(codes.PermissionDenied, "path not sharable, do you need to add it?")
	}

	if !allowed {
		f.Close()
		return nil, status.Error(codes.PermissionDenied, "path not sharable")
	}

	var spooledPath string
	if req.GetPersistent() {
		if a.backing.spool == nil {
			f.Close()
			return nil, status.Error(codes.FailedPrecondition, "persistent shares need both a database file and a spool directory configured")
		}

		spooledPath, err = a.backing.spool.Store(req.GetShareKey(), f, fi.Size())
		f.Close()
		if errors.Is(err, spool.ErrSizeMismatch) {
			return nil, status.Error(codes.FailedPrecondition, "file changed while copying, try again")
		}
		if err != nil {
			a.Log.Error("could not spool file", "error", err)
			return nil, status.Error(codes.Internal, "could not copy file to spool")
		}

		// the copy can take a while; don't leave a share behind for a client that gave up
		if ctx.Err() != nil {
			_ = a.backing.spool.Remove(spooledPath)
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}

	ttl := time.Duration(req.TtlSeconds) * time.Second
	if req.TtlSeconds == 0 {
		ttl = a.DefaultTTL
	}

	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}

	filename := filepath.Base(fullPath)
	if req.Filename != nil {
		filename = req.GetFilename()
	}

	sh := &store.Share{
		Code:         req.ShareKey,
		Filename:     filename,
		Path:         spooledPath,
		Size:         fi.Size(),
		Created:      time.Now(),
		Expires:      expires,
		MaxDownloads: int(req.MaxDownloads),
	}

	err = a.Store.Put(ctx, sh)
	if err != nil {
		if spooledPath != "" {
			_ = a.backing.spool.Remove(spooledPath)
		} else {
			f.Close()
		}
		a.Log.Error("could not store", "error", err)
		return nil, status.Error(codes.Internal, "could not store")
	}

	if spooledPath == "" {
		a.backing.handles.Put(sh.Code, f)
	}

	a.Log.Info("share created", "name", filename, "size", fi.Size(), "persistent", spooledPath != "")

	return &pb.AcceptShareResponse{
		Code: req.ShareKey,
		Url:  a.BuildURL(req.ShareKey),
		Info: asProto(sh),
	}, nil
}

func (a *Admin) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	shares, err := a.Store.List(ctx, req.IncludeExpired)
	if err != nil {
		a.Log.Error("could not list shares", "error", err, "include_expired", req.IncludeExpired)
		return nil, status.Errorf(codes.Internal, "list failed")
	}
	shareList := make([]*pb.ShareInfo, 0, len(shares))
	for _, curr := range shares {
		shareList = append(shareList, asProto(curr))
	}

	return &pb.ListResponse{Shares: shareList}, nil
}

func (a *Admin) Revoke(ctx context.Context, req *pb.RevokeRequest) (*pb.RevokeResponse, error) {
	sh, err := a.Store.Delete(ctx, req.Code)
	if errors.Is(err, store.ErrNotFound) {
		return &pb.RevokeResponse{Existed: false}, nil
	}
	if err != nil {
		slog.Error("could not revoke download", "code", req.Code, "error", err)
		return nil, status.Error(codes.Internal, "revoke failed")
	}

	a.backing.release(sh)
	return &pb.RevokeResponse{Existed: true}, nil
}

func (a *Admin) allowed(ctx context.Context, path string) (bool, error) {
	// TODO: restore with database functionality

	allowedRoots, err := a.Store.AllowedRoots(ctx)
	if err != nil {
		return false, err
	}

	if len(allowedRoots) == 0 {
		return false, nil // fail closed
	}

	for _, curr := range allowedRoots {
		// clients store roots already resolved; resolving again only matters
		// for roots added before that, and often fails anyway since we can't
		// see into users' homes. Comparing the literal root is still safe:
		// path is the fd's real path, so it can only fall under the root if
		// the file really lives there.
		root, err := filepath.EvalSymlinks(curr)
		if err != nil {
			a.Log.Debug("could not resolve root, comparing as-is", "root", curr, "error", err)
			root = filepath.Clean(curr)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		// Rel gives "../x" when path escapes root. Checking the prefix
		// is what stops /home/me/../etc/shadow slipping through.
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}

func (a *Admin) BuildURL(code string) string {
	// a trailing slash in base_url would otherwise give links like https://x//d/...
	return fmt.Sprintf("%s/d/%s", strings.TrimRight(a.BaseURL, "/"), code)
}

func (a *Admin) AddRoot(ctx context.Context, req *pb.AddRootRequest) (*pb.AddRootResponse, error) {
	err := a.Store.AddAllowedRoot(ctx, req.Root)
	if err != nil {
		slog.Error("could not add root", "error", err, "root", req.Root)
		return nil, status.Error(codes.Internal, "could not add root")
	}

	return &pb.AddRootResponse{Root: req.Root}, nil
}

func (a *Admin) DeleteRoot(ctx context.Context, req *pb.DeleteRootRequest) (*pb.DeleteRootResponse, error) {
	existed, err := a.Store.RemoveAllowedRoot(ctx, req.Root)
	if err != nil {
		slog.Error("could not remove root", "error", err, "root", req.Root)
		return nil, status.Error(codes.Internal, "could not remove root")
	}

	return &pb.DeleteRootResponse{Root: req.Root, Existed: existed}, nil
}

func (a *Admin) ListRoots(ctx context.Context, req *pb.ListRootsRequest) (*pb.ListRootsResponse, error) {
	roots, err := a.Store.AllowedRoots(ctx)
	if err != nil {
		slog.Error("could not list roots", "error", err)
		return nil, status.Error(codes.Internal, "could not list roots")
	}

	return &pb.ListRootsResponse{Roots: roots}, nil
}

func asProto(s *store.Share) *pb.ShareInfo {
	si := &pb.ShareInfo{
		Code:         s.Code,
		Filename:     s.Filename,
		Path:         s.Path,
		Persisted:    s.Path != "",
		Size:         s.Size,
		Created:      timestamppb.New(s.Created),
		Downloads:    int32(s.Downloads),
		MaxDownloads: int32(s.MaxDownloads),
	}
	if !s.Expires.IsZero() {
		si.Expires = timestamppb.New(s.Expires)
	}

	return si
}
