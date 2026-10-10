package relay

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// clientIP works out who is downloading. The ip header is only believed when the
// connection comes from a trusted proxy; otherwise anyone reaching the relay
// directly could claim to be any address they like. The bool reports whether
// the address came from the header rather than the connection.
func clientIP(req *http.Request, header string, trusted []netip.Prefix) (netip.Addr, bool, error) {
	ap, err := netip.ParseAddrPort(req.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false, fmt.Errorf("relay: clientIP: invalid remote address: %w", err)
	}
	peer := normalize(ap.Addr())

	if header == "" || !isTrusted(peer, trusted) {
		return peer, false, nil
	}

	values := req.Header.Values(header)
	if len(values) == 0 {
		return peer, false, nil
	}

	// each proxy appends the address it saw, so walk back from the nearest hop
	// and stop at the first one that isn't a proxy we trust. Anything further
	// left was written by the client and can't be believed.
	hops := strings.Split(strings.Join(values, ","), ",")
	client := peer
	fromHeader := false
	for _, curr := range slices.Backward(hops) {
		curr = strings.TrimSpace(curr)
		if curr == "" {
			continue
		}
		addr, ok := parseHop(curr)
		if !ok {
			break
		}
		client = addr
		fromHeader = true
		if !isTrusted(addr, trusted) {
			break
		}
	}

	return client, fromHeader, nil
}

// parseHop accepts a bare address, or one with a port as some proxies send
func parseHop(s string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return normalize(addr), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return normalize(ap.Addr()), true
	}
	if addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")); err == nil {
		return normalize(addr), true
	}
	return netip.Addr{}, false
}

func normalize(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	return slices.ContainsFunc(trusted, func(p netip.Prefix) bool {
		return p.Contains(addr)
	})
}

// ParsePrefixes reads addresses or CIDR ranges; a bare address means just that host
func ParsePrefixes(raw []string) ([]netip.Prefix, error) {
	ret := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if p, err := netip.ParsePrefix(s); err == nil {
			ret = append(ret, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid address or CIDR range %q", s)
		}
		addr = normalize(addr)
		ret = append(ret, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return ret, nil
}
