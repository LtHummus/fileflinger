package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lthummus/fileflinger/internal/relay"
)

// toServer sends every request to srv, whatever host it was addressed to, so
// checks can be run against a public-looking base_url
func toServer(srv *httptest.Server) *http.Client {
	target, _ := url.Parse(srv.URL)
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		return http.DefaultTransport.RoundTrip(req)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func fakeIPServer(t *testing.T, res relay.IPResponse) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ip" || !req.URL.Query().Has("verbose") {
			http.NotFound(w, req)
			return
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runServerCheck(httpc *http.Client, baseURL string) (string, *report) {
	var out bytes.Buffer
	rep := &report{w: &out}
	checkServer(context.Background(), rep, httpc, baseURL)
	return out.String(), rep
}

func TestCheckServer(t *testing.T) {
	public := fakeIPServer(t, relay.IPResponse{IP: "203.0.113.9", Source: "header", Warnings: []string{}})
	private := fakeIPServer(t, relay.IPResponse{IP: "10.0.0.5", Source: "connection", Warnings: []string{"ip_header isn't set"}})
	notFound := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notFound.Close)
	notUs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>welcome to nginx</html>"))
	}))
	t.Cleanup(notUs.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	tests := []struct {
		name     string
		httpc    *http.Client
		baseURL  string
		failures int
		warnings int
		want     []string
	}{
		{
			name:    "working setup",
			httpc:   toServer(public),
			baseURL: "https://files.example.com/",
			want:    []string{"reached fileflinger at https://files.example.com/", "attributed to 203.0.113.9 (from the header)"},
		},
		{
			name:     "proxy without ip_header",
			httpc:    toServer(private),
			baseURL:  "https://files.example.com",
			warnings: 2,
			want:     []string{"sees this machine as 10.0.0.5", "server says: ip_header isn't set"},
		},
		{
			name:     "private address is expected for a local base_url",
			httpc:    toServer(private),
			baseURL:  "http://192.168.1.10:6446",
			warnings: 1,
			want:     []string{"attributed to 10.0.0.5"},
		},
		{
			name:     "base_url not set",
			httpc:    http.DefaultClient,
			baseURL:  "",
			failures: 1,
			want:     []string{"base_url is not set"},
		},
		{
			name:     "proxy only forwards downloads",
			httpc:    toServer(notFound),
			baseURL:  "https://files.example.com",
			failures: 1,
			want:     []string{"answered 404"},
		},
		{
			name:     "something else answers",
			httpc:    toServer(notUs),
			baseURL:  "https://files.example.com",
			failures: 1,
			want:     []string{"not like fileflinger"},
		},
		{
			name:     "unreachable",
			httpc:    http.DefaultClient,
			baseURL:  closed.URL,
			failures: 1,
			want:     []string{"could not reach"},
		},
		{
			name:     "plain http to a public host",
			httpc:    toServer(public),
			baseURL:  "http://files.example.com",
			warnings: 1,
			want:     []string{"not encrypted", "reached fileflinger"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, rep := runServerCheck(tc.httpc, tc.baseURL)
			if rep.failures != tc.failures || rep.warnings != tc.warnings {
				t.Fatalf("got %d failures and %d warnings, want %d and %d:\n%s",
					rep.failures, rep.warnings, tc.failures, tc.warnings, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Fatalf("output missing %q:\n%s", w, out)
				}
			}
		})
	}
}
