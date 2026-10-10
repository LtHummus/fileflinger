//go:build darwin

package peercred

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// macOS differs from Linux in three ways that we care about....
//
// The option lives at SOL_LOCAL rather than SOL_SOCKET, and is called
// LOCAL_PEERCRED. It returns a struct xucred, which carries a uid and
// a group list but no pid -- the pid needs a second call to
// LOCAL_PEERPID. And xucred is captured at connect() time, so if the
// peer changes its uid afterwards we still get the original...that's
// more-or-less what we want for auth, so :)
func readPeerCred(raw syscall.RawConn) (PeerCred, error) {

	var xu *unix.Xucred
	var pid int
	var sockErr error

	err := raw.Control(func(fd uintptr) {
		xu, sockErr = unix.GetsockoptXucred(
			int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if sockErr != nil {
			return
		}
		// Best effort: a missing pid is not worth failing the
		// connection over, since authorization keys on uid.
		pid, _ = unix.GetsockoptInt(
			int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})

	if err != nil {
		return PeerCred{}, err
	}

	if sockErr != nil {
		return PeerCred{}, sockErr
	}

	// Groups[0] is the effective gid. Ngroups can be zero on some
	// paths, so guard the index.
	var gid uint32
	if xu.Ngroups > 0 {
		gid = xu.Groups[0]
	}
	groups := make([]uint32, min(int(xu.Ngroups), len(xu.Groups)))
	copy(groups, xu.Groups[:])

	return PeerCred{UID: xu.Uid, GID: gid, PID: int32(pid), Groups: groups}, nil
}
