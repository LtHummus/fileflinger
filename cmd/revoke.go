package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func revokeCmd() *cli.Command {
	return &cli.Command{
		Name:      "revoke",
		Usage:     "revoke one or more codes",
		ArgsUsage: "<code>...",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "all",
				Aliases: []string{"a"},
				Usage:   "revoke all shares",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cl, err := connect(ctx, cmd.String("socket-path"), "")
			if err != nil {
				return err
			}
			defer cl.Close()

			codes := cmd.Args().Slice()
			if cmd.Bool("all") {
				shares, err := cl.List(ctx, true)
				if err != nil {
					return err
				}
				codes = codes[:0]
				for _, s := range shares {
					codes = append(codes, s.Code)
				}
			}
			if len(codes) == 0 {
				return errors.New("need a code, or --all")
			}

			var failed int
			for _, code := range codes {
				existed, err := cl.Revoke(ctx, code)
				switch {
				case err != nil:
					fmt.Fprintf(os.Stderr, "%s: %v\n", code, err)
					failed++
				case !existed:
					fmt.Fprintf(os.Stderr, "%s: not found\n", code)
				default:
					fmt.Fprintf(os.Stderr, "%s: revoked\n", code)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d failed", failed, len(codes))
			}
			return nil
		},
	}
}
