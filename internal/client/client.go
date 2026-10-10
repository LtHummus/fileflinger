package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/lthummus/fileflinger/internal/fdsend"

	pb "github.com/lthummus/fileflinger/proto"
)

type Client struct {
	conn  *grpc.ClientConn
	admin pb.AdminClient

	fdSocketPath string
}

func Dial(socketPath string, fdSocketPath string) (*Client, error) {
	conn, err := grpc.NewClient(fmt.Sprintf("unix://%s", socketPath), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:  conn,
		admin: pb.NewAdminClient(conn),

		fdSocketPath: fdSocketPath,
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type ShareResult struct {
	Code     string
	URL      string
	Filename string
	Size     int64
	Expires  time.Time
}

var (
	ErrDaemonPredatesVersion = errors.New("daemon predates version reporting")
	ErrDaemonUnreachable     = errors.New("daemon not reachable — is fileflinger running?")
)

func (c *Client) DaemonVersion(ctx context.Context) (*pb.VersionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	res, err := c.admin.Version(ctx, &pb.VersionRequest{})
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented:
		return nil, ErrDaemonPredatesVersion
	case codes.Unavailable:
		// skip readableError's logging; this runs ahead of every command, which
		// will report an unreachable daemon itself
		return nil, ErrDaemonUnreachable
	default:
		return nil, readableError(err)
	}

	return res, nil
}

func (c *Client) ListRoots(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	roots, err := c.admin.ListRoots(ctx, &pb.ListRootsRequest{})
	if err != nil {
		return nil, readableError(err)
	}

	return roots.Roots, nil
}

func (c *Client) AddRoot(ctx context.Context, root string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.admin.AddRoot(ctx, &pb.AddRootRequest{Root: root})
	if err != nil {
		return readableError(err)
	}

	return nil
}

func (c *Client) DeleteRoot(ctx context.Context, root string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := c.admin.DeleteRoot(ctx, &pb.DeleteRootRequest{Root: root})
	if err != nil {
		return false, readableError(err)
	}

	return resp.Existed, nil
}

func (c *Client) Share(ctx context.Context, path string, ttl time.Duration, maxDownloads int32, persistent bool, filename string) (*ShareResult, error) {
	// persistent shares are copied into the spool during AcceptShare, which can
	// take far longer than passing an FD, so give ourselves some more time to accomplish
	// this, but keep the command's top-level ctx around for the shorter commands like StartShare (but slap
	// a 5 second timeout on it)
	acceptCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !persistent {
		acceptCtx = ctx
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open file for sharing: %w", err)
	}
	defer f.Close()

	startShareRes, err := c.admin.StartShare(ctx, &pb.StartShareRequest{
		Path: path,
	})
	if err != nil {
		return nil, fmt.Errorf("could not start share: %w", err)
	}

	slog.Debug("got start share result", "key", startShareRes.GetShareKey())

	fdSocket, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: c.fdSocketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("could not connect to fd socket: %w", err)
	}
	defer fdSocket.Close()

	err = fdsend.SendFD(fdSocket, f, startShareRes.GetShareKey())
	if err != nil {
		return nil, fmt.Errorf("could not send fd: %w", err)
	}

	acceptShareReq := &pb.AcceptShareRequest{
		ShareKey:     startShareRes.GetShareKey(),
		TtlSeconds:   int64(ttl.Seconds()),
		MaxDownloads: maxDownloads,
		Persistent:   persistent,
	}
	if filename != "" {
		acceptShareReq.Filename = &filename
	}

	acceptShareRes, err := c.admin.AcceptShare(acceptCtx, acceptShareReq)
	if err != nil {
		return nil, fmt.Errorf("could not accept share: %w", err)
	}

	ret := &ShareResult{
		Code:     acceptShareRes.GetCode(),
		URL:      acceptShareRes.GetUrl(),
		Filename: acceptShareRes.GetInfo().GetFilename(),
		Size:     acceptShareRes.GetInfo().GetSize(),
	}

	// a nil Expires means no expiry; AsTime on it would give 1970
	if expires := acceptShareRes.GetInfo().GetExpires(); expires != nil {
		ret.Expires = expires.AsTime()
	}

	return ret, nil
}

func (c *Client) List(ctx context.Context, includeExpired bool) ([]*pb.ShareInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	res, err := c.admin.List(ctx, &pb.ListRequest{IncludeExpired: includeExpired})
	if err != nil {
		return nil, readableError(err)
	}
	sort.Slice(res.Shares, func(i, j int) bool {
		return res.Shares[i].Created.AsTime().After(res.Shares[j].Created.AsTime())
	})
	return res.Shares, nil
}

func (c *Client) Revoke(ctx context.Context, code string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	res, err := c.admin.Revoke(ctx, &pb.RevokeRequest{Code: code})
	if err != nil {
		return false, readableError(err)
	}
	return res.Existed, nil
}

func readableError(err error) error {
	switch status.Code(err) {
	case codes.Unavailable:
		slog.Info("unavailable", "error", err)
		return ErrDaemonUnreachable
	case codes.PermissionDenied:
		return fmt.Errorf("not permitted: %s", status.Convert(err).Message())
	case codes.NotFound:
		return fmt.Errorf("not found: %s", status.Convert(err).Message())
	case codes.InvalidArgument:
		return fmt.Errorf("%s", status.Convert(err).Message())
	default:
		return err
	}
}

func ResolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s is a directory", abs)
	}
	return abs, nil
}

// ResolveDir returns the real path of p with symlinks resolved. That has to
// happen here, as the user... the daemon compares roots against the real path
// of each shared fd, and usually can't see into users' homes to resolve
// symlinks itself (e.g. ~ is a symlink on Synology) and with units we have access
// to home directories turned off so the daemon can't see it
func ResolveDir(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for c := n / unit; c >= unit; c /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func HumanExpiry(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Until(t)
	switch {
	case d <= 0:
		return "expired"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
}

// SanitizeFilename escapes control characters so a hostile filename can't
// break the table layout or inject terminal escape sequences.
func SanitizeFilename(name string) string {
	quoted := strconv.QuoteToGraphic(name)
	unquoted := quoted[1 : len(quoted)-1]
	if unquoted == name {
		return name
	}
	return quoted
}

func WriteTable(w io.Writer, shares []*pb.ShareInfo) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CODE\tFILENAME\tSIZE\tDOWNLOADS\tEXPIRES\tPERSISTED")
	for _, s := range shares {
		var expires time.Time
		if s.Expires != nil {
			expires = s.Expires.AsTime()
		}
		downloads := fmt.Sprintf("%d", s.Downloads)
		if s.MaxDownloads > 0 {
			downloads = fmt.Sprintf("%d/%d", s.Downloads, s.MaxDownloads)
		}
		persisted := "no"
		if s.Persisted {
			persisted = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Code, SanitizeFilename(s.Filename), HumanSize(s.Size), downloads,
			HumanExpiry(expires), persisted)
	}
	return tw.Flush()
}

func WriteJSON(w io.Writer, shares []*pb.ShareInfo) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(shares)
}
