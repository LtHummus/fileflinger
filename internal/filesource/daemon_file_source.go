package filesource

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"

	"github.com/lthummus/fileflinger/internal/handles"
	"github.com/lthummus/fileflinger/internal/store"
)

type Store struct {
	log     *slog.Logger
	ss      store.Store
	handles *handles.Handles
}

func NewLocalFileSource(log *slog.Logger, s store.Store, h *handles.Handles) *Store {
	return &Store{
		log:     log,
		ss:      s,
		handles: h,
	}
}

func (s *Store) Stat(ctx context.Context, code string, remoteIP netip.Addr) (StatResponse, error) {
	sh, err := s.ss.Peek(ctx, code, remoteIP.String())
	if err != nil {
		return StatResponse{}, convertStoreError(err)
	}

	var stats os.FileInfo
	if sh.Path == "" {
		var f *os.File
		f, err = s.handles.Get(code)
		if err != nil {
			s.log.Warn("could not get fd from handles", "code", code, "error", err)
			return StatResponse{}, ErrNotFound
		}
		defer f.Close()

		stats, err = f.Stat()
	} else {
		stats, err = os.Stat(sh.Path)

	}
	if err != nil {
		s.log.Warn("shared file is missing", "code", code, "path", sh.Path, "error", err)
		return StatResponse{}, ErrNotFound
	}

	return StatResponse{
		Filename: sh.Filename,
		Size:     stats.Size(),
		ETag:     ETag(stats),
		Modified: stats.ModTime(),
	}, nil
}

func (s *Store) Read(ctx context.Context, code string, offset int64, length int64, ifMatchEtag string, remoteIP netip.Addr) (<-chan []byte, error) {
	sh, err := s.ss.Claim(ctx, code, remoteIP.String())
	if err != nil {
		return nil, convertStoreError(err)
	}

	var sf *SharedFile
	if sh.Path == "" {
		var f *os.File
		f, err = s.handles.Get(code)
		if errors.Is(err, os.ErrNotExist) {
			// revoked or shut down since Claim
			s.log.Warn("could not get fd from shares", "code", code, "error", err)
			return nil, ErrNotFound
		}
		if err != nil {
			s.log.Warn("could not get fd from shares", "code", code, "error", err)
			return nil, err
		}
		sf, err = OpenShareFromFile(f, offset, length, ifMatchEtag)
	} else {
		sf, err = OpenShare(sh.Path, offset, length, ifMatchEtag)
	}
	if err != nil {
		s.log.Warn("could not open shared file", "code", code, "path", sh.Path, "error", err)
		return nil, err
	}

	// this works by alternating between two byte buffers. While one slice is being written to, the consumer is
	// reading from. To wit, this channel MUST NEVER BE BUFFERED EVER or you will introduce a bad race condition
	bytesChan := make(chan []byte)

	buffers := [2][]byte{
		make([]byte, ChunkSize),
		make([]byte, ChunkSize),
	}
	var currBuffer int

	go func() {
		defer close(bytesChan)
		defer sf.Close()

		err := CopyChunks(ctx, sf, func(p []byte) error {
			currBuffer = 1 - currBuffer
			n := copy(buffers[currBuffer], p)

			select {
			case bytesChan <- buffers[currBuffer][:n]:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Error("read stream failed", "code", code, "error", err)
		}
	}()

	return bytesChan, nil
}

func convertStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrExpired):
		return ErrNotFound
	case errors.Is(err, store.ErrExhausted):
		return ErrDownloadExhausted
	default:
		return err
	}
}
