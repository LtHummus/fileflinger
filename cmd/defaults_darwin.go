//go:build darwin

package cmd

// the root filesystem is read-only on macOS, so there's no /run
const defaultSocketDir = "/var/run/fileflinger"
