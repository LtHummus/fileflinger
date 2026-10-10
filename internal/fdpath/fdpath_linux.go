//go:build linux

package fdpath

import (
	"fmt"
	"os"
)

func internalPath(fd uintptr) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
}
