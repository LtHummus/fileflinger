package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/client"
)

func shareCmd() *cli.Command {
	return &cli.Command{
		Name:      "share",
		Usage:     "make a file available",
		ArgsUsage: "<path>",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:  "ttl",
				Usage: "expire after this long (unset uses daemon default)",
			},
			&cli.Int32Flag{
				Name:    "max-downloads",
				Aliases: []string{"n"},
				Usage:   "expire after N downloads (unset uses daemon default)",
			},
			&cli.BoolFlag{
				Name:    "persistent",
				Aliases: []string{"p"},
				Usage:   "should the share survive restarts (default no, requires copying file if enabled)",
			},
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"N"},
				Usage:   "filename the download is saved as (default is the file's own name)",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "only print the URL",
			},
		},
		Action: runShare,
	}
}

func runShare(ctx context.Context, cmd *cli.Command) error {
	path := cmd.Args().First()
	if path == "" {
		return cli.Exit("need to specify a path to share", 4)
	}

	abs, err := client.ResolvePath(path)
	if err != nil {
		return err
	}

	cl, err := connect(ctx, cmd.String("socket-path"), cmd.String("fd-socket-path"))
	if err != nil {
		return err
	}
	defer cl.Close()

	name := cmd.String("name")
	res, err := cl.Share(ctx, abs, cmd.Duration("ttl"), cmd.Int32("max-downloads"), cmd.Bool("persistent"), name)
	if err != nil {
		return err
	}

	// a daemon from before --name existed drops the field without complaint, so
	// check what it actually stored. warn even with --quiet since the share is
	// not what was asked for
	if name != "" && res.Filename != name {
		fmt.Fprintf(os.Stderr, "warning: daemon ignored --name and will serve this as %s; restart or upgrade the daemon\n", client.SanitizeFilename(res.Filename))
	}

	fmt.Printf("%s\n", res.URL)
	if !cmd.Bool("quiet") {
		fmt.Fprintf(os.Stderr, "Name: %s\tSize: %s\tExpires: %s\n", client.SanitizeFilename(res.Filename), client.HumanSize(res.Size), client.HumanExpiry(res.Expires))
	}

	return nil
}
