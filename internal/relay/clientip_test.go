package relay

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted, err := ParsePrefixes([]string{"127.0.0.0/8", "::1", "10.0.0.0/8", "192.168.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		remote  string
		header  string
		values  []string // sent as separate header lines
		trusted []netip.Prefix
		want    string
	}{
		{
			name:   "no header configured ignores forwarded headers",
			remote: "127.0.0.1:5000",
			values: []string{"203.0.113.9"},
			want:   "127.0.0.1",
		},
		{
			name:   "direct connection can't spoof the header",
			remote: "198.51.100.4:5000",
			header: "X-Forwarded-For",
			values: []string{"203.0.113.9"},
			want:   "198.51.100.4",
		},
		{
			name:   "trusted proxy",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"203.0.113.9"},
			want:   "203.0.113.9",
		},
		{
			name:   "client-supplied entries left of the real client are ignored",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"6.6.6.6, 203.0.113.9"},
			want:   "203.0.113.9",
		},
		{
			name:   "trusted hops are skipped",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"6.6.6.6, 203.0.113.9, 10.0.0.5"},
			want:   "203.0.113.9",
		},
		{
			name:   "multiple header lines are read as one list",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"6.6.6.6", "203.0.113.9"},
			want:   "203.0.113.9",
		},
		{
			name:   "client on the LAN behind the proxy",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"192.168.1.20"},
			want:   "192.168.1.20",
		},
		{
			name:   "garbage stops the walk at the last good hop",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"203.0.113.9, garbage, 10.0.0.5"},
			want:   "10.0.0.5",
		},
		{
			name:   "garbage alone falls back to the connection",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"garbage"},
			want:   "127.0.0.1",
		},
		{
			name:   "empty entries are skipped",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"203.0.113.9, "},
			want:   "203.0.113.9",
		},
		{
			name:   "header missing",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			want:   "127.0.0.1",
		},
		{
			name:   "hops with ports",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"203.0.113.9:4444"},
			want:   "203.0.113.9",
		},
		{
			name:   "bracketed IPv6 hop with port",
			remote: "[::1]:5000",
			header: "X-Forwarded-For",
			values: []string{"[2001:db8::1]:443"},
			want:   "2001:db8::1",
		},
		{
			name:   "bracketed IPv6 hop without port",
			remote: "[::1]:5000",
			header: "X-Forwarded-For",
			values: []string{"[2001:db8::1]"},
			want:   "2001:db8::1",
		},
		{
			name:   "IPv4-mapped proxy address counts as trusted",
			remote: "[::ffff:127.0.0.1]:5000",
			header: "X-Forwarded-For",
			values: []string{"203.0.113.9"},
			want:   "203.0.113.9",
		},
		{
			name:   "IPv4-mapped client is unmapped",
			remote: "127.0.0.1:5000",
			header: "X-Forwarded-For",
			values: []string{"::ffff:203.0.113.9"},
			want:   "203.0.113.9",
		},
		{
			name:    "nothing trusted means the header is never used",
			remote:  "127.0.0.1:5000",
			header:  "X-Forwarded-For",
			values:  []string{"203.0.113.9"},
			trusted: []netip.Prefix{},
			want:    "127.0.0.1",
		},
		{
			name:   "single-value header",
			remote: "127.0.0.1:5000",
			header: "X-Real-IP",
			values: []string{"203.0.113.9"},
			want:   "203.0.113.9",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/d/x", nil)
			req.RemoteAddr = tc.remote
			name := tc.header
			if name == "" {
				name = "X-Forwarded-For"
			}
			for _, v := range tc.values {
				req.Header.Add(name, v)
			}

			tr := trusted
			if tc.trusted != nil {
				tr = tc.trusted
			}

			got, _, err := clientIP(req, tc.header, tr)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestClientIPBadRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/d/x", nil)
	req.RemoteAddr = "not-an-address"
	if _, _, err := clientIP(req, "", nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes([]string{"10.1.2.3/8", " 192.0.2.7 ", "::1", "::ffff:198.51.100.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.7/32", "::1/128", "198.51.100.1/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	if _, err := ParsePrefixes([]string{"nope"}); err == nil {
		t.Fatal("expected an error for an invalid entry")
	}
}
