//go:build darwin

package fdpath

import (
	"bytes"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

var pinner runtime.Pinner

// macOS has no /proc, but fcntl(F_GETPATH) fills a MAXPATHLEN buffer with
// the same thing.
func internalPath(fd uintptr) (string, error) {
	buf := make([]byte, unix.PathMax)

	// pinning is needed here because we give the kernel a pointer as uintptr and
	// the GC doesn't know what that means...so tell the GC to not mess with our
	// buffer
	pinner.Pin(&buf[0])
	defer pinner.Unpin()

	if _, err := unix.FcntlInt(fd, unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0])))); err != nil {
		return "", err
	}

	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf), nil
}
