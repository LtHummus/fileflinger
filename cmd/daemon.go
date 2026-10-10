package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/config"
	"github.com/lthummus/fileflinger/internal/daemon"
)

func parseFileMode(s string) (fs.FileMode, error) {
	original := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0o")
	s = strings.TrimPrefix(s, "0O")

	n, err := strconv.ParseUint(s, 8, 12)
	if err != nil {
		return 0, fmt.Errorf("invalid file mode %q: use octal, e.g. 0644 or 0755: %w",
			original, err)
	}

	mode := fs.FileMode(n & 0o777)
	if n&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if n&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if n&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	return mode, nil
}

func daemonCommand() *cli.Command {
	flags := slices.Concat(daemonFlags(), drainFlag())
	return &cli.Command{
		Name:  "daemon",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			parsedMode, err := parseFileMode(cmd.String("socket-mode"))
			if err != nil {
				return err
			}

			cfg := config.Daemon{
				SocketPath:           cmd.String("socket-path"),
				FdSocketPath:         cmd.String("fd-socket-path"),
				BaseURL:              cmd.String("base-url"),
				SpoolDirectory:       cmd.String("spool-directory"),
				DefaultTTL:           cmd.Duration("default-ttl"),
				ExhaustedGracePeriod: cmd.Duration("exhausted-grace-period"),
				AllowedUIDs:          cmd.Uint32Slice("allowed-uids"),
				AllowedGIDs:          cmd.Uint32Slice("allowed-gids"),
				DrainTimeout:         cmd.Duration("drain-timeout"),
				SocketMode:           parsedMode,
				DatabaseFile:         cmd.String("database-file"),
			}

			slog.Info("starting in daemon mode",
				"version", build.Version,
				"commit", build.Commit,
				"socket_path", cfg.SocketPath,
				"base_url", cfg.BaseURL,
				"default_ttl", cfg.DefaultTTL,
				"exhausted_grace_period", cfg.ExhaustedGracePeriod,
				"spool_directory", cfg.SpoolDirectory,
				"allowed_uids", cfg.AllowedUIDs,
				"allowed_gids", cfg.AllowedGIDs,
				"drain_timeout", cfg.DrainTimeout,
				"socket_mode", cfg.SocketMode,
				"database_file", cfg.DatabaseFile,
			)

			level := slog.LevelInfo
			if cmd.Bool("debug") {
				level = slog.LevelDebug
				slog.Warn("debug logging enabled")
			}

			return daemon.RunDaemon(ctx, cfg, nil, build.Version, build.Commit, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: level,
			})))
		},
	}
}
