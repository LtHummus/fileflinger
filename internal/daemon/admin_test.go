package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lthummus/fileflinger/internal/store"
)

func TestAllowed(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// a root reached through a symlink, which the daemon can resolve
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	st := store.NewInMemoryStore(log)
	for _, r := range []string{
		link,
		// stands in for a root the daemon can't resolve, like a path under
		// a home directory it isn't allowed into
		"/nonexistent/homes/alice/files",
	} {
		if err := st.AddAllowedRoot(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	a := &Admin{Store: st, Log: log}

	for path, want := range map[string]bool{
		filepath.Join(realResolved, "a.txt"):              true,
		filepath.Join(realResolved, "sub", "b.txt"):       true,
		"/nonexistent/homes/alice/files/c.png":            true,
		"/nonexistent/homes/alice/files/deep/d.png":       true,
		"/nonexistent/homes/alice/other/e.png":            false,
		"/nonexistent/homes/alice/files-not-really/f.png": false,
		"/etc/passwd": false,
	} {
		got, err := a.allowed(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("allowed(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestAllowedNoRoots(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Admin{Store: store.NewInMemoryStore(log), Log: log}
	got, err := a.allowed(context.Background(), "/anything")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("allowed with no roots configured")
	}
}

func TestValidateFilename(t *testing.T) {
	for _, name := range []string{
		"report.pdf",
		"my report (final); v2.pdf",
		"résumé.pdf",
		".hidden",
	} {
		if err := validateFilename(name); err != nil {
			t.Errorf("validateFilename(%q) = %v, want nil", name, err)
		}
	}

	for _, name := range []string{
		"",
		".",
		"..",
		"a/b.txt",
		`a\b.txt`,
		"a\nb.txt",
		"a\x7fb.txt",
		"\xff.txt",
		strings.Repeat("a", maxFilenameLen+1),
	} {
		if err := validateFilename(name); err == nil {
			t.Errorf("validateFilename(%q) = nil, want error", name)
		}
	}
}
