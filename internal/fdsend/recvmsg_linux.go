//go:build linux

package fdsend

import "syscall"

// Returns the data length, the control-message length, and the recv flags.
func recvmsg(fd int, p, oob []byte) (int, int, int, error) {
	n, oobn, flags, _, err := syscall.Recvmsg(fd, p, oob, syscall.MSG_CMSG_CLOEXEC)
	return n, oobn, flags, err
}
