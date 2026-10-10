//go:build darwin

package fdsend

import "syscall"

// macOS has no MSG_CMSG_CLOEXEC, so the received fds have to be marked
// close-on-exec after the fact. Holding ForkLock across both steps keeps a
// concurrent fork+exec from inheriting them in between, which is how the
// standard library covers accept() on platforms without accept4...so we
// crib it here and call it a day.
//
// Returns the data length, the control-message length, and the recv flags.
func recvmsg(fd int, p, oob []byte) (int, int, int, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()

	n, oobn, flags, _, err := syscall.Recvmsg(fd, p, oob, 0)
	if err != nil {
		return n, oobn, flags, err
	}

	// parse errors are left for RecvFD, which parses again and closes whatever arrived
	scms, perr := syscall.ParseSocketControlMessage(oob[:oobn])
	if perr != nil {
		return n, oobn, flags, nil
	}
	for i := range scms {
		fds, perr := syscall.ParseUnixRights(&scms[i])
		if perr != nil {
			continue
		}
		for _, received := range fds {
			syscall.CloseOnExec(received)
		}
	}
	return n, oobn, flags, nil
}
