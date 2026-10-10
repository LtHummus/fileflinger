//go:build linux

package peercred

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux returns uid, gid, and pid together in a single struct.
// Supplementary groups need a separate SO_PEERGROUPS call, or /proc on
// kernels too old to have it (looking at you, Synology)
func readPeerCred(raw syscall.RawConn) (PeerCred, error) {

	var ucred *unix.Ucred
	var groups []uint32
	var groupsErr error
	var sockErr error

	// Control hands us the fd without detaching the conn from the
	// runtime poller, which uc.File() would do.
	err := raw.Control(func(fd uintptr) {
		ucred, sockErr = unix.GetsockoptUcred(
			int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if sockErr != nil {
			return
		}
		groups, groupsErr = getPeerGroups(int(fd))
	})
	if err != nil {
		return PeerCred{}, err
	}
	if sockErr != nil {
		return PeerCred{}, sockErr
	}
	if groupsErr != nil {
		// SO_PEERGROUPS needs Linux 4.13+, and Synology still ships 4.4.
		// Try to get the groups in another way. If we don't, ahhhh well we'll just
		// go with the primary group. Which sucks but I hate synology at this point.
		groups, _ = procGroups(ucred.Pid, ucred.Uid)
	}

	return PeerCred{UID: ucred.Uid, GID: ucred.Gid, PID: ucred.Pid, Groups: groups}, nil
}

// x/sys has no typed getter for SO_PEERGROUPS, and GetsockoptString would
// truncate at the first zero byte, so call getsockopt directly. Like
// SO_PEERCRED, the list is captured at connect() time.
func getPeerGroups(fd int) ([]uint32, error) {
	buf := make([]uint32, 64)
	for {
		size := uint32(len(buf) * 4)
		_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd),
			unix.SOL_SOCKET, unix.SO_PEERGROUPS,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
		switch errno {
		case 0:
			return buf[:size/4], nil
		case unix.ERANGE:
			// size now holds the length the kernel needs
			buf = make([]uint32, size/4)
		default:
			return nil, errno
		}
	}
}

// procGroups reads the supplementary groups of a live process. Unlike
// SO_PEERGROUPS this is the process's state now, not at connect(), and the
// pid could in principle have been reused by then; requiring the effective
// uid to still match the peer's limits that to the same user's processes, so there's
// a bit of TOCTOU issue here, but for most cases, we should be ok
func procGroups(pid int32, uid uint32) ([]uint32, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseProcStatusGroups(f, uid)
}

func parseProcStatusGroups(r io.Reader, uid uint32) ([]uint32, error) {
	var groups []uint32
	var sawUID, seenGroup bool

	// status is a bunch of key-value pairs in the format "key:\tval1\tval2\tval3"
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(val)
		switch key {
		case "Uid":
			// real, effective, saved, fs; SO_PEERCRED reports the effective uid
			if len(fields) < 2 || fields[1] != strconv.FormatUint(uint64(uid), 10) {
				return nil, fmt.Errorf("peercred: pid no longer belongs to uid %d", uid)
			}
			sawUID = true
		case "Groups":
			for _, g := range fields {
				n, err := strconv.ParseUint(g, 10, 32)
				if err != nil {
					return nil, fmt.Errorf("peercred: bad group %q: %w", g, err)
				}
				groups = append(groups, uint32(n))
			}
			seenGroup = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawUID || !seenGroup {
		return nil, fmt.Errorf("peercred: status missing Uid or Groups")
	}
	return groups, nil
}
