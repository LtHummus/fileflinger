package fdsend

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
)

// TODO: this package was ... grown organically rather than truly designed, so this whole thing (and its true
//       surface) probably needs a rethink. But it was also a pain to do, so I don't want to touch it for now

func SendFD(conn *net.UnixConn, file *os.File, key string) error {
	fd := int(file.Fd())

	rights := syscall.UnixRights(fd)

	rawConn, err := conn.SyscallConn()
	if err != nil {
		return err
	}

	var sendErr error
	err = rawConn.Write(func(socketFd uintptr) bool {
		sendErr = syscall.Sendmsg(int(socketFd), []byte(key), rights, nil, 0)
		// the socket is non-blocking, so if the kernel tells us to try again, we should
		return !errors.Is(sendErr, syscall.EAGAIN)
	})

	if err != nil {
		return err
	}
	if sendErr != nil {
		return sendErr
	}

	// we'll get a single byte back to indicate success or failure....as a BONUS, the server does not
	// send the byte back until the FD has been accepted in to the map on the server side, so we can
	// make the follow up "AcceptShare" rpc call immediately upon returning and don't have to worry about
	// a data race
	resp := make([]byte, 1)
	if _, err := io.ReadFull(conn, resp); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("receiver closed connection without responding")
		}
		return fmt.Errorf("could not read response: %w", err)
	}

	switch resp[0] {
	case SocketMsgOk[0]:
		return nil
	case SocketMsgFailed[0]:
		return ErrRejected
	default:
		return fmt.Errorf("unexpected response byte: %d", resp[0])
	}
}

func RecvFD(conn *net.UnixConn) (*os.File, string, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, "", err
	}

	oobBuffer := make([]byte, syscall.CmsgSpace(4))
	keyBuffer := make([]byte, 128)

	var keyn, oobn, flags int
	var recvErr error

	err = rawConn.Read(func(fd uintptr) bool {
		// recvmsg marks received fds close-on-exec so they don't leak into any child processes, but that is fine
		// because we don't spawn any child processes
		keyn, oobn, flags, recvErr = recvmsg(int(fd), keyBuffer, oobBuffer)
		return !errors.Is(recvErr, syscall.EAGAIN)
	})

	if err != nil {
		return nil, "", err
	}

	if recvErr != nil {
		return nil, "", recvErr
	}

	// parse fds before checking anything else so they're always closed on error. the kernel has already installed them in our fd table
	var recvdFds []int
	scms, err := syscall.ParseSocketControlMessage(oobBuffer[:oobn])
	if err != nil {
		return nil, "", err
	}
	for _, curr := range scms {
		fds, err := syscall.ParseUnixRights(&curr)
		if err != nil {
			closeAll(recvdFds)
			return nil, "", err
		}
		recvdFds = append(recvdFds, fds...)
	}

	if flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
		closeAll(recvdFds)
		return nil, "", fmt.Errorf("message truncated")
	}

	if len(recvdFds) != 1 {
		closeAll(recvdFds)
		return nil, "", fmt.Errorf("expected 1 file descriptor, got %d", len(recvdFds))
	}

	if keyn == 0 {
		closeAll(recvdFds)
		return nil, "", fmt.Errorf("no key received")
	}

	f := os.NewFile(uintptr(recvdFds[0]), "received-fd")
	return f, string(keyBuffer[:keyn]), nil
}

func closeAll(fds []int) {
	for _, fd := range fds {
		_ = syscall.Close(fd)
	}
}
