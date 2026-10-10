package relay

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/lthummus/fileflinger/internal/filesource"
)

func testLimiter(perMinute int) (*failureLimiter, *time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newFailureLimiter(perMinute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	l.now = func() time.Time { return now }
	return l, &now
}

func TestLimiterBlocksAfterBurst(t *testing.T) {
	l, now := testLimiter(10)
	ip := netip.MustParseAddr("203.0.113.9")

	for i := range 10 {
		if ok, _ := l.allow(ip); !ok {
			t.Fatalf("blocked after %d failures, want 10 allowed", i)
		}
		l.fail(ip)
	}

	ok, wait := l.allow(ip)
	if ok {
		t.Fatal("not blocked after using up the burst")
	}
	if wait <= 0 || wait > 6*time.Second {
		t.Fatalf("wait = %s, want up to 6s (one token at 10/min)", wait)
	}

	// retrying while blocked mustn't push the wait back
	for range 5 {
		l.allow(ip)
	}
	if _, again := l.allow(ip); again != wait {
		t.Fatalf("wait grew from %s to %s while blocked", wait, again)
	}

	*now = now.Add(wait)
	if ok, _ := l.allow(ip); !ok {
		t.Fatal("still blocked after waiting")
	}

	// other clients are unaffected
	if ok, _ := l.allow(netip.MustParseAddr("203.0.113.10")); !ok {
		t.Fatal("unrelated client blocked")
	}
}

func TestLimiterKeys(t *testing.T) {
	l, _ := testLimiter(1)

	l.fail(netip.MustParseAddr("2001:db8:1:2::1"))
	if ok, _ := l.allow(netip.MustParseAddr("2001:db8:1:2::ffff")); ok {
		t.Fatal("address in the same /64 wasn't limited")
	}
	if ok, _ := l.allow(netip.MustParseAddr("2001:db8:1:3::1")); !ok {
		t.Fatal("address in a different /64 was limited")
	}

	l.fail(netip.MustParseAddr("198.51.100.1"))
	if ok, _ := l.allow(netip.MustParseAddr("198.51.100.2")); !ok {
		t.Fatal("neighbouring IPv4 address was limited")
	}
}

func TestLimiterSweep(t *testing.T) {
	l, now := testLimiter(10)
	l.fail(netip.MustParseAddr("203.0.113.9"))

	*now = now.Add(limiterIdle)
	l.fail(netip.MustParseAddr("203.0.113.10")) // triggers the sweep

	if _, ok := l.clients[limiterKey(netip.MustParseAddr("203.0.113.9"))]; ok {
		t.Fatal("idle client wasn't swept")
	}
	if len(l.clients) != 1 {
		t.Fatalf("got %d clients, want 1", len(l.clients))
	}
}

func TestLimiterCap(t *testing.T) {
	l, _ := testLimiter(10)
	for i := range limiterMaxClients + 5 {
		l.fail(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}))
	}
	if len(l.clients) != limiterMaxClients {
		t.Fatalf("got %d clients, want the cap of %d", len(l.clients), limiterMaxClients)
	}
}

func TestLimiterDisabled(t *testing.T) {
	l := newFailureLimiter(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if l != nil {
		t.Fatal("expected a nil limiter for 0")
	}
	ip := netip.MustParseAddr("203.0.113.9")
	for range 100 {
		l.fail(ip)
	}
	if ok, _ := l.allow(ip); !ok {
		t.Fatal("nil limiter blocked")
	}
}

// fakeSource knows exactly one code, and serves it as an empty file
type fakeSource struct{}

func (fakeSource) Stat(_ context.Context, code string, _ netip.Addr) (filesource.StatResponse, error) {
	if code != "real" {
		return filesource.StatResponse{}, filesource.ErrNotFound
	}
	return filesource.StatResponse{Filename: "f", ETag: `"e"`}, nil
}

func (fakeSource) Read(context.Context, string, int64, int64, string, netip.Addr) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func TestDownloadRateLimit(t *testing.T) {
	r := &Relay{
		FileSource: fakeSource{},
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter:    newFailureLimiter(3, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	h := r.Routes()

	get := func(code string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/d/"+code, nil)
		req.RemoteAddr = "203.0.113.9:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// successful downloads never count
	for range 20 {
		if rec := get("real"); rec.Code != 200 {
			t.Fatalf("download got %d", rec.Code)
		}
	}

	for range 3 {
		if rec := get("guess"); rec.Code != 404 {
			t.Fatalf("guess got %d, want 404", rec.Code)
		}
	}

	rec := get("guess")
	if rec.Code != 429 {
		t.Fatalf("got %d after using up the limit, want 429", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "20" {
		t.Fatalf("Retry-After = %q, want 20 (one token at 3/min)", ra)
	}

	// while blocked, the client is turned away before the lookup, even for a real code
	if rec := get("real"); rec.Code != 429 {
		t.Fatalf("real code while blocked got %d, want 429", rec.Code)
	}
}
