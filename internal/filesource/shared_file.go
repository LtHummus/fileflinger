package filesource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const ChunkSize = 64 << 10

type SharedFile struct {
	sr   *io.SectionReader
	f    *os.File
	Info os.FileInfo
}

func (sf *SharedFile) Close() error {
	return sf.f.Close()
}

func OpenShareFromFile(f *os.File, offset int64, length int64, ifMatchETag string) (*SharedFile, error) {
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat failed: %w", err)
	}

	if ifMatchETag != "" && ifMatchETag != ETag(fi) {
		f.Close()
		return nil, ErrFileChanged
	}

	if offset < 0 || offset > fi.Size() {
		f.Close()
		return nil, ErrBadRange
	}

	// clamp rather than reject, since that's what HTTP does
	if length <= 0 || offset+length > fi.Size() {
		length = fi.Size() - offset
	}

	return &SharedFile{
		sr:   io.NewSectionReader(f, offset, length),
		f:    f,
		Info: fi,
	}, nil
}

func OpenShare(path string, offset int64, length int64, ifMatchETag string) (*SharedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrNotFound
	}

	return OpenShareFromFile(f, offset, length, ifMatchETag)
}

func (sf *SharedFile) Read(p []byte) (int, error) {
	return sf.sr.Read(p)
}

func CopyChunks(ctx context.Context, r io.Reader, sink func([]byte) error) error {
	buf := make([]byte, ChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf)
		if n > 0 {
			if err := sink(buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func ETag(fi os.FileInfo) string {
	var ino uint64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
	}
	return fmt.Sprintf(`"%d-%d-%d"`, ino, fi.ModTime().UnixNano(), fi.Size())
}
