package daemon

import "testing"

func TestBuildURLTrailingSlash(t *testing.T) {
	a := &Admin{BaseURL: "https://files.example.com/"}
	if got := a.BuildURL("abc"); got != "https://files.example.com/d/abc" {
		t.Fatalf("got %s", got)
	}
}
