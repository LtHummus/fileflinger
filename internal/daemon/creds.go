package daemon

import (
	"context"
	"log/slog"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/lthummus/fileflinger/internal/daemon/peercred"
)

func peerCredUnary(allowedUIDs []uint32, allowedGIDs []uint32, log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		h grpc.UnaryHandler) (any, error) {
		log.Debug("checking peer uid from unary message")
		if err := checkIdentity(ctx, allowedUIDs, allowedGIDs, log); err != nil {
			return nil, err
		}
		return h(ctx, req)
	}
}

func peerCredStream(allowedUIDs []uint32, allowedGIDs []uint32, log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		h grpc.StreamHandler) error {
		log.Debug("checking peer uid from stream message")
		if err := checkIdentity(ss.Context(), allowedUIDs, allowedGIDs, log); err != nil {
			return err
		}
		return h(srv, ss)
	}
}

func checkIdentity(ctx context.Context, allowedUIDs []uint32, allowedGIDs []uint32, log *slog.Logger) error {
	uid, ok := peerUID(ctx)
	if !ok {
		log.Warn("peer uid unavailable, rejecting")
		return status.Error(codes.PermissionDenied, "peer UID unavailable")
	}
	gid, ok := peerGID(ctx)
	if !ok {
		log.Warn("peer gid unavailable, rejecting")
		return status.Error(codes.PermissionDenied, "peer GID unavailable")
	}
	pid, ok := peerPID(ctx)
	if !ok {
		log.Warn("peer pid unavailable, rejecting")
		return status.Error(codes.PermissionDenied, "peer PID unavailable")
	}
	groups, _ := peerGroups(ctx)
	log.Debug("found peer info", "allowed_uids", allowedUIDs, "allowed_gids", allowedGIDs, "uid", uid, "gid", gid, "groups", groups, "pid", pid)
	if len(allowedUIDs) == 0 && len(allowedGIDs) == 0 {
		return nil
	}
	if slices.Contains(allowedUIDs, uid) {
		return nil
	}
	if slices.Contains(allowedGIDs, gid) {
		return nil
	}
	// the socket's filesystem permissions honor supplementary groups, so we should too
	for _, g := range groups {
		if slices.Contains(allowedGIDs, g) {
			return nil
		}
	}
	log.Warn("rejected peer", "uid", uid, "gid", gid, "groups", groups)
	return status.Error(codes.PermissionDenied, "not permitted")
}

func peerUID(ctx context.Context) (uint32, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return 0, false
	}
	pc, ok := p.AuthInfo.(peercred.PeerCred)
	if !ok {
		return 0, false
	}
	return pc.UID, true
}

func peerGID(ctx context.Context) (uint32, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return 0, false
	}
	pc, ok := p.AuthInfo.(peercred.PeerCred)
	if !ok {
		return 0, false
	}
	return pc.GID, true
}

func peerGroups(ctx context.Context) ([]uint32, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, false
	}
	pc, ok := p.AuthInfo.(peercred.PeerCred)
	if !ok {
		return nil, false
	}
	return pc.Groups, true
}

func peerPID(ctx context.Context) (int32, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return 0, false
	}
	pc, ok := p.AuthInfo.(peercred.PeerCred)
	if !ok {
		return 0, false
	}
	return pc.PID, true
}
