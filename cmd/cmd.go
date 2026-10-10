package cmd

import (
	"context"
	"fmt"
	"log"

	"github.com/urfave/cli/v3"
)

var configFilePath string

var app = &cli.Command{
	Name:  "fling",
	Usage: "a program for quickly hosting files",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "debug",
			Usage:   "debug logging",
			Hidden:  true,
			Sources: cli.EnvVars("FILEFLINGER_DEBUG_LOGGING"),
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "config file path",
			Value:       "/etc/fileflinger/fileflinger.toml",
			Aliases:     []string{"c"},
			Destination: &configFilePath,
			Sources:     cli.EnvVars("FILEFLINGER_CONFIG_FILE"),
		},
		&cli.StringFlag{
			Name:    "socket-path",
			Usage:   "daemon socket path",
			Value:   defaultSocketDir + "/daemon.sock",
			Sources: buildSource("FILEFLINGER_SOCKET", "socket_path"),
		},
		&cli.StringFlag{
			Name:    "fd-socket-path",
			Usage:   "fd socket path",
			Value:   defaultSocketDir + "/fd.sock",
			Sources: buildSource("FILEFLINGER_FD_SOCKET", "fd_socket_path"),
		},
	},
	Commands: []*cli.Command{shareCmd(), relayCommand(), daemonCommand(), listCommand(), revokeCmd(), rootsCmd(), testCommand(), serveCommand(), versionCommand(), doctorCommand()},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Args().Len() == 0 {
			return cli.ShowAppHelp(cmd)
		}

		return runShare(ctx, cmd)
	},
}

func Execute(ctx context.Context, args []string, b Build) {
	build = b
	app.Version = b.Version
	cli.VersionPrinter = func(cmd *cli.Command) {
		_, _ = fmt.Fprintf(cmd.Root().Writer, "%v version %v\n", cmd.Name, build)
	}

	if err := app.Run(ctx, args); err != nil {
		log.Fatal(err)
	}
}
