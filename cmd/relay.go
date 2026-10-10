package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/config"
	"github.com/lthummus/fileflinger/internal/filesource"
	"github.com/lthummus/fileflinger/internal/relay"
)

func relayCommand() *cli.Command {
	flags := slices.Concat(relayFlags(), drainFlag())
	return &cli.Command{
		Name:  "relay",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			trustedProxies, err := relay.ParsePrefixes(cmd.StringSlice("trusted-proxies"))
			if err != nil {
				return fmt.Errorf("trusted-proxies: %w", err)
			}

			cfg := config.Relay{
				DrainTimeout:   cmd.Duration("drain-timeout"),
				SocketPath:     cmd.String("socket-path"),
				Port:           cmd.Int("port"),
				IPHeader:       cmd.String("ip-header"),
				TrustedProxies: trustedProxies,

				FailedLookupsPerMinute: cmd.Int("failed-lookups-per-minute"),
			}

			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))
			grpcClient, err := filesource.NewGRPC(ctx, cfg.SocketPath, log)
			if err != nil {
				return err
			}

			return relay.Run(ctx, &cfg, grpcClient, log)
		},
	}
}
