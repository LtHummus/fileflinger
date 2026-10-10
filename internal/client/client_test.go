package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":        "report.pdf",
		"with space.txt":    "with space.txt",
		"naïve résumé.docx": "naïve résumé.docx",
		"tab\there":         `"tab\there"`,
		"new\nline":         `"new\nline"`,
		"\x1b[31mred":       `"\x1b[31mred"`,
	} {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestResolveDirResolvesSymlinks(t *testing.T) {
	testDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(testDir, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(testDir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ResolveDir(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ResolveDir(%q) = %q, want %q", link, got, want)
	}
}

func TestResolveDirMissing(t *testing.T) {
	if _, err := ResolveDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing directory")
	}
}
