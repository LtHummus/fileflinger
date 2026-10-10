package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/client"
)

const devVersion = "dev"

// Build identifies this binary. Version is what gets compared against the
// daemon; Commit is only for display, so two builds of the same release
// don't warn about each other
type Build struct {
	Version string
	Commit  string
}

func (b Build) String() string {
	if b.Commit == "" {
		return b.Version
	}
	return fmt.Sprintf("%s (%s)", b.Version, b.Commit)
}

// build is set once by Execute
var build Build

func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "show client and daemon versions",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			fmt.Printf("client: %s\n", build)

			cl, err := client.Dial(cmd.String("socket-path"), "")
			if err != nil {
				return err
			}
			defer cl.Close()

			res, err := cl.DaemonVersion(ctx)
			if errors.Is(err, client.ErrDaemonPredatesVersion) {
				fmt.Println("daemon: unknown (too old to report one, restart or upgrade it)")
				return nil
			}
			if err != nil {
				fmt.Printf("daemon: unknown (%v)\n", err)
				return nil
			}

			fmt.Printf("daemon: %s\n", Build{Version: res.GetVersion(), Commit: res.GetCommit()})
			if versionsDiffer(build.Version, res.GetVersion()) {
				fmt.Fprintln(os.Stderr, "warning: versions differ, restart or upgrade the daemon")
			}
			return nil
		},
	}
}

// connect connects to the daemon and warns on stderr if it is running a different
// version than this binary, which usually means it was upgraded without a restart
func connect(ctx context.Context, grpcSocketPath string, fdSocketPath string) (*client.Client, error) {
	cl, err := client.Dial(grpcSocketPath, fdSocketPath)
	if err != nil {
		return nil, err
	}

	res, err := cl.DaemonVersion(ctx)
	switch {
	case errors.Is(err, client.ErrDaemonPredatesVersion):
		fmt.Fprintf(os.Stderr, "warning: daemon is older than this client (%s), restart or upgrade it\n", build.Version)
	case err != nil:
		// unreachable daemons and the like get reported by the actual command
		slog.Debug("could not check daemon version", "error", err)
	case versionsDiffer(build.Version, res.GetVersion()):
		fmt.Fprintf(os.Stderr, "warning: daemon is %s but this client is %s, restart or upgrade the daemon\n", res.GetVersion(), build.Version)
	}

	return cl, nil
}

// versionsDiffer ignores dev builds, which would otherwise warn on every local run
func versionsDiffer(clientVersion, daemonVersion string) bool {
	if clientVersion == devVersion || daemonVersion == devVersion {
		return false
	}
	return clientVersion != daemonVersion
}
