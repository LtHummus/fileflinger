package peercred

import (
	"context"
	"errors"
	"net"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// unfortunately, gRPC does not expose any sort of unix peer credentials when using a socket, so we have to wrap the connection
// and fetch it ourselves at accept time

type PeerCred struct {
	UID uint32
	GID uint32
	PID int32

	Groups []uint32
}

func (PeerCred) AuthType() string { return "peercred" }

type credConn struct {
	net.Conn
	cred PeerCred
}

type credListener struct{ net.Listener }

func NewCredListener(l net.Listener) net.Listener { return &credListener{l} }

func (l *credListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		c.Close()
		return nil, err
	}

	cred, err := readPeerCred(raw)
	if err != nil {
		c.Close()
		return nil, err
	}

	return &credConn{Conn: c, cred: cred}, nil
}

// peerCredTC is a no-op TransportCredentials that exists only to pass
// the already-collected PeerCred into the RPC context as AuthInfo.
type peerCredTC struct{}

func NewPeerCredCreds() credentials.TransportCredentials { return peerCredTC{} }

func (peerCredTC) ServerHandshake(c net.Conn) (net.Conn, credentials.AuthInfo, error) {
	cc, ok := c.(*credConn)
	if !ok {
		return nil, nil, errors.New("connection missing peer credentials")
	}
	return c, cc.cred, nil
}

func (peerCredTC) ClientHandshake(_ context.Context, _ string, c net.Conn) (
	net.Conn, credentials.AuthInfo, error) {
	return c, PeerCred{}, nil
}

func (peerCredTC) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "peercred"}
}

func (peerCredTC) Clone() credentials.TransportCredentials { return peerCredTC{} }
func (peerCredTC) OverrideServerName(string) error         { return nil }

// TODO for later: there's probably a better place to put FromUnixCon, but we'll leave it here for now

func FromUnixConn(c *net.UnixConn) (PeerCred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return PeerCred{}, err
	}
	return readPeerCred(raw)
}

func FromGRPCContext(ctx context.Context) (PeerCred, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return PeerCred{}, errors.New("could not get peer from context")
	}

	ai, ok := p.AuthInfo.(PeerCred)
	if !ok {
		return PeerCred{}, errors.New("invalid auth info type")
	}

	return ai, nil
}
