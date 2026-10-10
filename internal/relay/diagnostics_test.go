package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var defaultTrusted, _ = ParsePrefixes([]string{"127.0.0.0/8", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"})

func testDiagnostics(logs io.Writer) (*diagnostics, *time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := newDiagnostics(slog.New(slog.NewTextHandler(logs, nil)))
	d.now = func() time.Time { return now }
	return d, &now
}

func TestWarnLogsOncePerWindow(t *testing.T) {
	var logs bytes.Buffer
	d, now := testDiagnostics(&logs)

	d.warn("k", "something is off")
	d.warn("k", "something is off")
	if n := strings.Count(logs.String(), "something is off"); n != 1 {
		t.Fatalf("logged %d times, want 1", n)
	}

	*now = now.Add(warningWindow)
	d.warn("k", "something is off")
	if n := strings.Count(logs.String(), "something is off"); n != 2 {
		t.Fatalf("logged %d times after the window, want 2", n)
	}
}

func TestActiveExpiresRequestWarnings(t *testing.T) {
	d, now := testDiagnostics(io.Discard)

	d.configWarning("c", "config problem")
	d.warn("r", "request problem")
	if got := d.active(); len(got) != 2 {
		t.Fatalf("got %v, want both warnings", got)
	}

	*now = now.Add(warningWindow)
	got := d.active()
	if len(got) != 1 || got[0] != "config problem" {
		t.Fatalf("got %v, want only the config warning", got)
	}
}

func TestCheckConfig(t *testing.T) {
	d, _ := testDiagnostics(io.Discard)
	d.checkConfig("X-Forwarded-For", []netip.Prefix{})
	d.checkConfig("", []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")})

	got := strings.Join(d.active(), "\n")
	if !strings.Contains(got, "trusted_proxies is empty") || !strings.Contains(got, "every address") {
		t.Fatalf("missing config warnings: %q", got)
	}

	clean, _ := testDiagnostics(io.Discard)
	clean.checkConfig("X-Forwarded-For", defaultTrusted)
	if got := clean.active(); len(got) != 0 {
		t.Fatalf("unexpected warnings for a sane config: %v", got)
	}
}

func TestCheckRequest(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		header  string            // ip_header setting
		headers map[string]string // sent with the request
		want    string            // substring of the expected warning, or "" for none
	}{
		{
			name:    "proxy forwarding but ip_header unset",
			remote:  "127.0.0.1:5000",
			headers: map[string]string{"X-Real-IP": "203.0.113.9"},
			want:    "ip_header isn't set",
		},
		{
			name:    "public client sending forwarding headers is just a client",
			remote:  "198.51.100.4:5000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9"},
		},
		{
			name:   "direct connection with no proxy",
			remote: "198.51.100.4:5000",
		},
		{
			name:   "trusted proxy without the header",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			want:   "without ip_header",
		},
		{
			name:    "untrusted address sends the header",
			remote:  "198.51.100.4:5000",
			header:  "X-Forwarded-For",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:    "not in trusted_proxies",
		},
		{
			name:    "working proxy setup",
			remote:  "127.0.0.1:5000",
			header:  "X-Forwarded-For",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9"},
		},
		{
			name:   "direct visitor while behind a proxy is fine",
			remote: "198.51.100.4:5000",
			header: "X-Forwarded-For",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := testDiagnostics(io.Discard)
			req := httptest.NewRequest("GET", "/d/x", nil)
			req.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			d.checkRequest(req, tc.header, defaultTrusted)

			got := d.active()
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected warnings %v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("got %v, want one warning containing %q", got, tc.want)
			}
		})
	}
}

func TestIPEndpoint(t *testing.T) {
	r := &Relay{
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		IPSourceHeader: "X-Forwarded-For",
		TrustedProxies: defaultTrusted,
	}
	h := r.Routes()

	req := httptest.NewRequest("GET", "/ip", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 || rec.Body.String() != "203.0.113.9\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}

	// a proxy that forgets the header should show up in the verbose output
	req = httptest.NewRequest("GET", "/ip?verbose", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var res IPResponse
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.IP != "127.0.0.1" || res.Source != "connection" {
		t.Fatalf("unexpected response %+v", res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "without ip_header") {
		t.Fatalf("unexpected warnings %v", res.Warnings)
	}
	if strings.Contains(rec.Body.String(), "127.0.0.1:5000") {
		t.Fatal("verbose output leaked the proxy's address and port")
	}

	req = httptest.NewRequest("GET", "/ip?verbose", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res = IPResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.IP != "203.0.113.9" || res.Source != "header" {
		t.Fatalf("unexpected response %+v", res)
	}
}
