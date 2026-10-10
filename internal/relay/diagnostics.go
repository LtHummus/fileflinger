package relay

import (
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"time"
)

// how often a recurring warning is logged, and how long it's reported by /ip
// after it was last seen
const warningWindow = time.Hour

// headers proxies commonly use to pass along the client address
var forwardingHeaders = []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"}

type warning struct {
	msg        string
	config     bool // comes from the config, so it holds until restart
	lastSeen   time.Time
	lastLogged time.Time
}

// diagnostics collects misconfiguration warnings. Messages may be shown to
// anyone through /ip, so details like internal addresses go in the log attrs only.
type diagnostics struct {
	log *slog.Logger
	now func() time.Time

	mu       sync.Mutex
	warnings map[string]*warning
}

func newDiagnostics(log *slog.Logger) *diagnostics {
	return &diagnostics{
		log:      log,
		now:      time.Now,
		warnings: map[string]*warning{},
	}
}

// configWarning records a problem with the config itself
func (d *diagnostics) configWarning(key string, msg string, attrs ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.warnings[key] = &warning{msg: msg, config: true}
	d.log.Warn(msg, attrs...)
}

// warn records a problem seen in a request, logging it at most once per window
func (d *diagnostics) warn(key string, msg string, attrs ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	w, ok := d.warnings[key]
	if !ok {
		w = &warning{msg: msg}
		d.warnings[key] = w
	}
	w.lastSeen = now
	if now.Sub(w.lastLogged) >= warningWindow {
		w.lastLogged = now
		d.log.Warn(msg, attrs...)
	}
}

// active returns config warnings plus any seen in a request recently
func (d *diagnostics) active() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	ret := []string{}
	for _, w := range d.warnings {
		if w.config || now.Sub(w.lastSeen) < warningWindow {
			ret = append(ret, w.msg)
		}
	}
	slices.Sort(ret)
	return ret
}

func (d *diagnostics) checkConfig(header string, trusted []netip.Prefix) {
	if header != "" && len(trusted) == 0 {
		d.configWarning("no-trusted-proxies",
			"ip_header is set but trusted_proxies is empty, so the header is never used",
			"header", header)
	}
	for _, p := range trusted {
		if p.Bits() == 0 {
			d.configWarning("trust-everyone",
				"trusted_proxies includes every address, so any client can choose the IP it appears to come from",
				"range", p)
		}
	}
}

// checkRequest looks for signs that the proxy setup and ip_header don't match
func (d *diagnostics) checkRequest(req *http.Request, header string, trusted []netip.Prefix) {
	ap, err := netip.ParseAddrPort(req.RemoteAddr)
	if err != nil {
		return
	}
	peer := normalize(ap.Addr())
	peerTrusted := isTrusted(peer, trusted)

	if header == "" {
		if !peer.IsLoopback() && !peer.IsPrivate() {
			return
		}
		for _, h := range forwardingHeaders {
			if req.Header.Get(h) != "" {
				d.warn("proxy-without-ip-header",
					"requests look like they come through a proxy, but ip_header isn't set, so every download appears to come from the proxy",
					"proxy", peer, "header_seen", h)
				return
			}
		}
		return
	}

	hasHeader := len(req.Header.Values(header)) > 0
	switch {
	case peerTrusted && !hasHeader:
		d.warn("proxy-missing-header",
			"a trusted proxy sent a request without ip_header; check the proxy sets it, and that ip_header names the right header",
			"proxy", peer, "header", header)
	case !peerTrusted && hasHeader:
		d.warn("untrusted-sent-header",
			"a request carrying ip_header came from an address not in trusted_proxies, so the header was ignored; add your proxy to trusted_proxies, or firewall the relay port if it shouldn't be reachable directly",
			"from", peer, "header", header)
	}
}
