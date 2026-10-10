package cmd

import (
	"time"

	altsrc "github.com/urfave/cli-altsrc/v3"
	"github.com/urfave/cli-altsrc/v3/toml"
	"github.com/urfave/cli/v3"
)

func buildSource(envVar string, tomlPath string) cli.ValueSourceChain {
	return cli.NewValueSourceChain(cli.EnvVar(envVar), toml.TOML(tomlPath, altsrc.NewStringPtrSourcer(&configFilePath)))
}

func daemonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "base-url",
			Usage:   "location where fileflinger is accessible to the world",
			Sources: buildSource("FILEFLINGER_BASE_URL", "base_url"),
		},
		&cli.DurationFlag{
			Name:    "default-ttl",
			Usage:   "how long shares should last unless otherwise specified",
			Value:   1 * time.Hour,
			Sources: buildSource("FILEFLINGER_DEFAULT_TTL", "default_ttl"),
		},
		&cli.DurationFlag{
			Name:    "exhausted-grace-period",
			Usage:   "how long a share that hit its download limit is kept after its last download, so downloads can resume",
			Value:   15 * time.Minute,
			Sources: buildSource("FILEFLINGER_EXHAUSTED_GRACE_PERIOD", "exhausted_grace_period"),
		},
		&cli.Uint32SliceFlag{
			Name:    "allowed-uids",
			Usage:   "uids allowed access to the socket",
			Sources: buildSource("FILEFLINGER_ALLOWED_UIDS", "allowed_uids"),
		},
		&cli.Uint32SliceFlag{
			Name:    "allowed-gids",
			Usage:   "gids allowed access to the socket (primary or supplementary)",
			Sources: buildSource("FILEFLINGER_ALLOWED_GIDS", "allowed_gids"),
		},
		&cli.StringFlag{
			Name:    "socket-mode",
			Usage:   "file mode in octal, e.g. 0644, 0755, or 0o755",
			Value:   "0660",
			Sources: buildSource("FILEFLINGER_SOCKET_MODE", "socket_mode"),
		},
		&cli.StringFlag{
			Name:    "database-file",
			Usage:   "where to store sqlite database; if blank, defaults to using an inmemory storage",
			Sources: buildSource("FILEFLINGER_DATABASE_FILE", "database_file"),
		},
		&cli.StringFlag{
			Name:    "spool-directory",
			Usage:   "temporary holding pen for persistent storage",
			Sources: buildSource("FILEFLINGER_SPOOL_DIRECTORY", "spool_directory"),
		},
	}
}

func relayFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:    "port",
			Value:   6446,
			Aliases: []string{"p"},
			Sources: buildSource("FILEFLINGER_PORT", "port"),
		},
		&cli.StringFlag{
			Name:    "ip-header",
			Usage:   "where to get the IP address from (leave blank to get from remote)",
			Sources: buildSource("FILEFLINGER_IP_HEADER", "ip_header"),
		},
		&cli.StringSliceFlag{
			Name:  "trusted-proxies",
			Usage: "addresses or CIDR ranges allowed to set ip-header; requests from anywhere else use their own address",
			// a proxy on the same host, a Docker bridge, or one elsewhere on the LAN
			Value:   []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"},
			Sources: buildSource("FILEFLINGER_TRUSTED_PROXIES", "trusted_proxies"),
		},
		&cli.IntFlag{
			Name:    "failed-lookups-per-minute",
			Usage:   "how many downloads of nonexistent codes each client may try per minute before being turned away (0 disables)",
			Value:   10,
			Sources: buildSource("FILEFLINGER_FAILED_LOOKUPS_PER_MINUTE", "failed_lookups_per_minute"),
		},
	}
}

func drainFlag() []cli.Flag {
	return []cli.Flag{
		&cli.DurationFlag{
			Name:    "drain-timeout",
			Usage:   "how long we should wait for connections to drain when exiting",
			Value:   30 * time.Second,
			Sources: buildSource("FILEFLINGER_DRAIN_TIMEOUT", "drain_timeout"),
		},
	}
}
