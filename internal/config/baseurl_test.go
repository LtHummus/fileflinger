package config

import (
	"strings"
	"testing"
)

func TestCheckBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		wantErr string // substring of the error, or ""
		warn    string // substring of the first warning, or "" for none
	}{
		{baseURL: "https://files.example.com"},
		{baseURL: "https://files.example.com/fling"},
		{baseURL: "http://localhost:6446"},
		{baseURL: "http://192.168.1.10:6446"},
		{baseURL: "", wantErr: "not set"},
		{baseURL: "files.example.com", wantErr: "not an http(s) URL"},
		{baseURL: "ftp://files.example.com", wantErr: "not an http(s) URL"},
		{baseURL: "https://", wantErr: "not an http(s) URL"},
		{baseURL: "http://files.example.com", warn: "not encrypted"},
		{baseURL: "https://files.example.com/?x=1", warn: "query or fragment"},
	}

	for _, tc := range tests {
		t.Run(tc.baseURL, func(t *testing.T) {
			warnings, err := CheckBaseURL(tc.baseURL)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if tc.warn == "" {
				if len(warnings) != 0 {
					t.Fatalf("unexpected warnings %v", warnings)
				}
				return
			}
			if len(warnings) == 0 || !strings.Contains(warnings[0], tc.warn) {
				t.Fatalf("got %v, want first warning containing %q", warnings, tc.warn)
			}
		})
	}
}
