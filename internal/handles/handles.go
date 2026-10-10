package handles

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type Handles struct {
	handles map[string]*os.File
	lock    sync.Mutex

	log *slog.Logger
}

func New(log *slog.Logger) *Handles {
	return &Handles{
		handles: map[string]*os.File{},
		log:     log,
	}
}

func (h *Handles) Cleanup() {
	h.lock.Lock()
	defer h.lock.Unlock()

	h.log.Debug("running handles cleanup")

	var cleaned []string
	for k, v := range h.handles {
		_ = v.Close()
		cleaned = append(cleaned, k)
		h.log.Debug("cleanup made us clean a file", "key", k)
	}

	for _, curr := range cleaned {
		delete(h.handles, curr)
	}
}

func (h *Handles) Get(k string) (*os.File, error) {
	h.lock.Lock()
	defer h.lock.Unlock()

	f, ok := h.handles[k]
	if !ok {
		return nil, os.ErrNotExist
	}

	raw, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}

	var newFd int
	var dupErr error
	err = raw.Control(func(fd uintptr) {
		newFd, dupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	})
	if err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, fmt.Errorf("could not dup fd: %w", dupErr)
	}

	h.log.Debug("pulling FD out of handles", "key", k)

	return os.NewFile(uintptr(newFd), f.Name()), nil
}

func (h *Handles) Put(k string, f *os.File) {
	h.lock.Lock()
	defer h.lock.Unlock()

	h.log.Debug("added FD to handles", "key", k)

	h.handles[k] = f
}

func (h *Handles) Remove(k string) bool {
	h.lock.Lock()
	defer h.lock.Unlock()

	f, existed := h.handles[k]
	if existed {
		_ = f.Close()
		delete(h.handles, k)

		h.log.Debug("removed FD from handles", "key", k)
	}

	return existed
}
