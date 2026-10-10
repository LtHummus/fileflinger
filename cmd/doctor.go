package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/lthummus/fileflinger/internal/client"
	"github.com/lthummus/fileflinger/internal/config"
	"github.com/lthummus/fileflinger/internal/relay"
)

// report prints check results and remembers whether any failed
type report struct {
	w        io.Writer
	failures int
	warnings int
}

func (r *report) ok(format string, args ...any) {
	fmt.Fprintf(r.w, "  ok    %s\n", fmt.Sprintf(format, args...))
}

func (r *report) warn(format string, args ...any) {
	r.warnings++
	fmt.Fprintf(r.w, "  warn  %s\n", fmt.Sprintf(format, args...))
}

func (r *report) fail(format string, args ...any) {
	r.failures++
	fmt.Fprintf(r.w, "  FAIL  %s\n", fmt.Sprintf(format, args...))
}

func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:      "doctor",
		Usage:     "check the daemon, the shareable roots, and that base_url reaches this fileflinger",
		ArgsUsage: "[base-url]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "base-url",
				Usage:   "URL to check (defaults to base_url from the config)",
				Sources: buildSource("FILEFLINGER_BASE_URL", "base_url"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			rep := &report{w: os.Stdout}

			fmt.Println("daemon")
			checkDaemon(ctx, rep, cmd.String("socket-path"))

			baseURL := cmd.String("base-url")
			if cmd.Args().Present() {
				baseURL = cmd.Args().First()
			}
			fmt.Println("\nserver")
			checkServer(ctx, rep, &http.Client{Timeout: 10 * time.Second}, baseURL)

			fmt.Println()
			if rep.failures > 0 {
				return cli.Exit(fmt.Sprintf("%d problem(s) need fixing", rep.failures), 1)
			}

			if rep.warnings > 0 {
				fmt.Printf("%d warning(s), but sharing should work\n", rep.warnings)
			} else {
				fmt.Printf("everything looks good!\n")
			}

			return nil
		},
	}
}

func checkDaemon(ctx context.Context, rep *report, socketPath string) {
	cl, err := client.Dial(socketPath, "")
	if err != nil {
		rep.fail("could not set up a connection to %s: %v", socketPath, err)
		return
	}
	defer cl.Close()

	res, err := cl.DaemonVersion(ctx)
	switch {
	case errors.Is(err, client.ErrDaemonPredatesVersion):
		rep.warn("daemon is older than this client (%s); restart or upgrade it", build.Version)
	case err != nil:
		rep.fail("can't talk to the daemon at %s: %v", socketPath, err)
		if _, statErr := os.Stat(socketPath); statErr == nil {
			fmt.Fprintln(rep.w, "        the socket exists, so check your user is allowed to use it")
		}
		return
	case versionsDiffer(build.Version, res.GetVersion()):
		rep.warn("daemon is %s but this client is %s; restart or upgrade the daemon", res.GetVersion(), build.Version)
	default:
		rep.ok("daemon %s is running at %s", res.GetVersion(), socketPath)
	}

	roots, err := cl.ListRoots(ctx)
	switch {
	case err != nil:
		rep.fail("could not list roots: %v", err)
	case len(roots) == 0:
		rep.fail("no roots, so nothing can be shared; add one with `fling roots add <dir>`")
	default:
		rep.ok("files can be shared from %s", strings.Join(roots, ", "))
	}
}

func checkServer(ctx context.Context, rep *report, httpc *http.Client, baseURL string) {
	warnings, err := config.CheckBaseURL(baseURL)
	if err != nil {
		rep.fail("%v", err)
		return
	}
	for _, w := range warnings {
		rep.warn("%s", w)
	}

	ipURL := strings.TrimRight(baseURL, "/") + "/ip?verbose"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ipURL, nil)
	if err != nil {
		rep.fail("could not build a request for %s: %v", ipURL, err)
		return
	}
	resp, err := httpc.Do(req)
	if err != nil {
		rep.fail("could not reach %s: %v", baseURL, err)
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		rep.fail("%s answered 404 for /ip; the proxy may only forward /d/ paths, or the server is older than this client", baseURL)
		return
	case resp.StatusCode != http.StatusOK:
		rep.fail("%s answered %s for /ip", baseURL, resp.Status)
		return
	}

	var res relay.IPResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&res); err != nil || res.IP == "" {
		rep.fail("%s answered, but not like fileflinger does; is base_url pointing at the right server?", baseURL)
		return
	}
	rep.ok("reached fileflinger at %s", baseURL)

	addr, err := netip.ParseAddr(res.IP)
	switch {
	case err != nil:
		rep.warn("server reported an address it couldn't have meant: %q", res.IP)
	case (addr.IsLoopback() || addr.IsPrivate()) && !baseURLIsLocal(baseURL):
		rep.warn("the server sees this machine as %s, a private address. That's expected if you're on "+
			"the same network as the server; otherwise ip_header isn't set up, and every download looks like "+
			"it comes from the proxy. To check from outside, open %s/ip on a phone off Wi-Fi",
			addr, strings.TrimRight(baseURL, "/"))
	default:
		rep.ok("downloads from here are attributed to %s (from the %s)", addr, res.Source)
	}

	for _, w := range res.Warnings {
		rep.warn("server says: %s", w)
	}
}

func baseURLIsLocal(baseURL string) bool {
	u, err := url.Parse(baseURL)
	return err == nil && config.IsLocalhostOrPrivate(u.Hostname())
}
