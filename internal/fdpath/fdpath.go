package fdpath

import "os"

// This code resolves an open file back to the path it currently lives at.
// We can't trust the client telling us the path (since it could lie), so
// resolve the true path ourselves and compare that to the roots that are
// sharable.

// Path returns the absolute, symlink-free path of f as the kernel sees it now.
// If the file has been renamed since it was opened, that is the new name.
func Path(f *os.File) (string, error) {
	raw, err := f.SyscallConn()
	if err != nil {
		return "", err
	}

	var path string
	var pathErr error
	if err := raw.Control(func(fd uintptr) {
		path, pathErr = internalPath(fd)
	}); err != nil {
		return "", err
	}
	return path, pathErr
}
