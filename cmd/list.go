package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/client"
)

func listCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "show active shares",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "all",
				Aliases: []string{"a"},
				Usage:   "include expired",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "output in JSON",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cl, err := connect(ctx, cmd.String("socket-path"), "")
			if err != nil {
				return err
			}
			defer cl.Close()

			shares, err := cl.List(ctx, cmd.Bool("all"))
			if err != nil {
				return err
			}
			if len(shares) == 0 {
				fmt.Fprintln(os.Stderr, "no active shares")
				return nil
			}
			if cmd.Bool("json") {
				return client.WriteJSON(os.Stdout, shares)
			}
			return client.WriteTable(os.Stdout, shares)
		},
	}
}
