package filesource

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

var (
	ErrNotFound          = errors.New("download not found or expired")
	ErrDownloadExhausted = errors.New("download limit reached")
	ErrFileChanged       = errors.New("underlying file has changed, please restart")
	ErrBadRange          = errors.New("requested offset is outside the file")
	ErrUpstreamGRPCError = errors.New("error contacting daemon")
)

type StatResponse struct {
	Filename string
	Size     int64
	ETag     string
	Modified time.Time
}

type FileSource interface {
	Stat(ctx context.Context, code string, remoteIP netip.Addr) (StatResponse, error)
	Read(ctx context.Context, code string, offset int64, length int64, etag string, remoteIP netip.Addr) (<-chan []byte, error)
}
