package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/lthummus/fileflinger/cmd"
)

// version and commit are stamped at build time with
// -ldflags "-X main.version=<version> -X main.commit=<short hash>".
var (
	version = "dev"
	commit  = ""
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// once we're draining, restore default handling so a second signal kills us outright
		<-ctx.Done()
		stop()
	}()

	cmd.Execute(ctx, os.Args, cmd.Build{Version: version, Commit: buildCommit()})
}

// buildCommit falls back to the vcs info go embeds when building from a git
// checkout, so unstamped local builds still say what they were built from
func buildCommit() string {
	if commit != "" {
		return commit
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	var rev string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if rev != "" && modified {
		rev += "-dirty"
	}
	return rev
}
