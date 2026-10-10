package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/config"
	"github.com/lthummus/fileflinger/internal/daemon"
	"github.com/lthummus/fileflinger/internal/relay"
)

func serveCommand() *cli.Command {
	flags := slices.Concat(relayFlags(), daemonFlags(), drainFlag())
	return &cli.Command{
		Name:  "serve",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			parsedMode, err := parseFileMode(cmd.String("socket-mode"))
			if err != nil {
				return err
			}

			daemonCfg := config.Daemon{
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

			trustedProxies, err := relay.ParsePrefixes(cmd.StringSlice("trusted-proxies"))
			if err != nil {
				return fmt.Errorf("trusted-proxies: %w", err)
			}

			relayCfg := config.Relay{
				DrainTimeout:   cmd.Duration("drain-timeout"),
				SocketPath:     cmd.String("socket-path"),
				Port:           cmd.Int("port"),
				IPHeader:       cmd.String("ip-header"),
				TrustedProxies: trustedProxies,

				FailedLookupsPerMinute: cmd.Int("failed-lookups-per-minute"),
			}

			level := slog.LevelInfo
			if cmd.Bool("debug") {
				level = slog.LevelDebug
				slog.Warn("debug logging enabled")
			}

			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: level,
			}))

			slog.Info("starting in server mode",
				"version", build.Version,
				"commit", build.Commit,
				"socket_path", daemonCfg.SocketPath,
				"base_url", daemonCfg.BaseURL,
				"default_ttl", daemonCfg.DefaultTTL,
				"exhausted_grace_period", daemonCfg.ExhaustedGracePeriod,
				"spool_directory", daemonCfg.SpoolDirectory,
				"allowed_uids", daemonCfg.AllowedUIDs,
				"allowed_gids", daemonCfg.AllowedGIDs,
				"drain_timeout", daemonCfg.DrainTimeout,
				"socket_mode", daemonCfg.SocketMode,
				"port", relayCfg.Port,
				"ip_header", relayCfg.IPHeader,
				"trusted_proxies", relayCfg.TrustedProxies,
				"failed_lookups_per_minute", relayCfg.FailedLookupsPerMinute,
				"database_file", daemonCfg.DatabaseFile,
			)

			return daemon.RunDaemon(ctx, daemonCfg, &relayCfg, build.Version, build.Commit, log)
		},
	}
}
