package relay

import (
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// a bucket refills completely within a minute, so an entry idle this long
	// is back to full and can be dropped
	limiterIdle = 10 * time.Minute
	// stops a flood of distinct addresses from growing the map without bound
	limiterMaxClients = 100_000
)

type limitedClient struct {
	lim      *rate.Limiter
	lastSeen time.Time
	blocked  int
}

// failureLimiter throttles clients that keep asking for codes that don't exist,
// which is what guessing looks like. Successful downloads are never counted, so
// resumes and multi-connection downloaders aren't affected. A nil limiter
// allows everything.
type failureLimiter struct {
	perMinute int
	log       *slog.Logger
	now       func() time.Time

	mu        sync.Mutex
	clients   map[netip.Prefix]*limitedClient
	lastSweep time.Time
}

// newFailureLimiter returns nil, meaning no limit, when perMinute is 0
func newFailureLimiter(perMinute int, log *slog.Logger) *failureLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &failureLimiter{
		perMinute: perMinute,
		log:       log,
		now:       time.Now,
		clients:   map[netip.Prefix]*limitedClient{},
	}
}

// limiterKey groups IPv6 clients by /64, since anyone with one address
// usually controls the whole /64 and could otherwise rotate through it
func limiterKey(addr netip.Addr) netip.Prefix {
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return p
	}
	return netip.PrefixFrom(addr, addr.BitLen())
}

// allow reports whether addr may look up a code, and if not, how long until it can
func (l *failureLimiter) allow(addr netip.Addr) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	c, ok := l.clients[limiterKey(addr)]
	if !ok {
		return true, 0
	}

	tokens := c.lim.TokensAt(l.now())
	if tokens >= 1 {
		return true, 0
	}

	c.blocked++
	if c.blocked == 1 {
		l.log.Info("rate limiting failed lookups", "client", limiterKey(addr))
	}
	// worked out rather than reserved, so retrying while blocked doesn't push
	// the wait back further
	wait := time.Duration((1 - tokens) / float64(c.lim.Limit()) * float64(time.Second))
	return false, wait
}

// fail records a lookup of a code that doesn't exist
func (l *failureLimiter) fail(addr netip.Addr) {
	if l == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.lastSweep) >= time.Minute {
		l.sweep(now)
	}

	key := limiterKey(addr)
	c, ok := l.clients[key]
	if !ok {
		if len(l.clients) >= limiterMaxClients {
			// only reachable with more distinct addresses than this; dropping
			// any one entry just gives that client a fresh bucket
			for k := range l.clients {
				delete(l.clients, k)
				break
			}
		}
		c = &limitedClient{lim: rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.perMinute)), l.perMinute)}
		l.clients[key] = c
	}
	c.lastSeen = now
	c.lim.AllowN(now, 1)
}

func (l *failureLimiter) sweep(now time.Time) {
	l.lastSweep = now
	for k, c := range l.clients {
		if now.Sub(c.lastSeen) < limiterIdle {
			continue
		}
		if c.blocked > 0 {
			l.log.Info("stopped rate limiting failed lookups", "client", k, "blocked_requests", c.blocked)
		}
		delete(l.clients, k)
	}
}
