package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/client"
)

func rootsCmd() *cli.Command {
	return &cli.Command{
		Name:  "roots",
		Usage: "manipulate roots",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "list all roots",
				Flags: []cli.Flag{
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

					roots, err := cl.ListRoots(ctx)
					if err != nil {
						return nil
					}

					if cmd.Bool("json") {
						return json.NewEncoder(os.Stdout).Encode(roots)
					}

					tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
					fmt.Fprintln(tw, "ROOT")
					for _, s := range roots {
						fmt.Fprintf(tw, "%s\n", s)
					}
					return tw.Flush()
				},
			},
			{
				Name:      "add",
				Usage:     "add a new, valid root",
				ArgsUsage: "<path>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					path := cmd.Args().First()
					if path == "" {
						return cli.Exit("need to specify a path to enable", 5)
					}

					abs, err := client.ResolveDir(path)
					if err != nil {
						return err
					}

					cl, err := connect(ctx, cmd.String("socket-path"), "")
					if err != nil {
						return err
					}
					defer cl.Close()

					return cl.AddRoot(ctx, abs)
				},
			},
			{
				Name:      "delete",
				Usage:     "delete an existing root",
				ArgsUsage: "<path>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					path := cmd.Args().First()
					if path == "" {
						return cli.Exit("need to specify a path to enable", 5)
					}

					abs, err := filepath.Abs(path)
					if err != nil {
						return err
					}
					// roots are stored resolved, but older ones (and roots whose
					// directory is gone) can only be matched as written
					candidates := []string{abs}
					if resolved, err := client.ResolveDir(abs); err == nil && resolved != abs {
						candidates = []string{resolved, abs}
					}

					cl, err := connect(ctx, cmd.String("socket-path"), "")
					if err != nil {
						return err
					}
					defer cl.Close()

					for _, c := range candidates {
						existed, err := cl.DeleteRoot(ctx, c)
						if err != nil {
							return err
						}
						if existed {
							fmt.Printf("%s deleted\n", c)
							return nil
						}
					}

					fmt.Printf("%s not allowed root\n", candidates[0])
					return nil
				},
			},
		},
	}
}
