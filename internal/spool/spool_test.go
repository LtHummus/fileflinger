package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s should still exist: %v", path, err)
	}
}

func TestStoreCopiesFromStartAndRemoves(t *testing.T) {
	sp, err := New(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()

	src := filepath.Join(t.TempDir(), "src.txt")
	writeFile(t, src, "hello world")
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// a sender that already read part of the file must not truncate the copy
	if _, err := f.Seek(6, 0); err != nil {
		t.Fatal(err)
	}

	path, err := sp.Store("bunt-67-lemon-spry", f, 11)
	if err != nil {
		t.Fatal(err)
	}
	if !sp.IsOwnedByUs(path) || !sp.Exists(path) {
		t.Fatalf("spool should own %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q", got)
	}

	ours, unexpected, err := sp.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(ours) != 1 || ours[0] != path || len(unexpected) != 0 {
		t.Fatalf("expected only %s, got %v / %v", path, ours, unexpected)
	}

	if err := sp.Remove(path); err != nil {
		t.Fatal(err)
	}
	if sp.Exists(path) {
		t.Fatal("expected file gone")
	}
	// removing twice is fine
	if err := sp.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSizeMismatchLeavesNothing(t *testing.T) {
	sp, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()

	src := filepath.Join(t.TempDir(), "src.txt")
	writeFile(t, src, "grew after stat")
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := sp.Store("code", f, 4); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("expected size mismatch, got %v", err)
	}
	ours, unexpected, _ := sp.Files()
	if len(ours)+len(unexpected) != 0 {
		t.Fatalf("expected empty spool, got %v / %v", ours, unexpected)
	}
}

func TestRefusesUnmarkedNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	precious := filepath.Join(dir, "family-photo")
	writeFile(t, precious, "x")

	if _, err := New(dir); err == nil {
		t.Fatal("expected refusal to adopt a non-empty directory")
	}
	mustExist(t, precious)
	if _, err := os.Stat(filepath.Join(dir, markerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("marker should not be written into a directory we refused")
	}
}

func TestReopensMarkedDirectory(t *testing.T) {
	dir := t.TempDir()
	sp, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "some-code"), "x")
	sp.Close()

	sp, err = New(dir)
	if err != nil {
		t.Fatalf("marked directory should be reusable: %v", err)
	}
	sp.Close()
}

func TestNeverTouchesAnythingOutside(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "users-real-file")
	writeFile(t, outside, "x")

	parent := t.TempDir()
	dir := filepath.Join(parent, "spool")
	sp, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()

	sibling := filepath.Join(parent, "sibling")
	writeFile(t, sibling, "x")

	// a symlink inside the spool that points outside, with a name that
	// looks exactly like a share code
	link := filepath.Join(dir, "evil-code")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	// a file whose name we would never create
	stray := filepath.Join(dir, "notes.txt")
	writeFile(t, stray, "x")

	for _, p := range []string{
		outside,
		filepath.Join(dir, "..", "sibling"),
		dir + "/../sibling",
		filepath.Join(dir, ".."),
		dir + "/..",
		dir + "/.",
		dir,
		stray,
		filepath.Join(dir, markerName),
		"",
		"relative-code",
	} {
		if sp.IsOwnedByUs(p) {
			t.Errorf("spool should not own %q", p)
		}
		if err := sp.Remove(p); err == nil {
			t.Errorf("expected refusal to remove %q", p)
		}
	}

	ours, unexpected, err := sp.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(ours) != 0 {
		t.Fatalf("symlink and stray file must not be removable, got %v", ours)
	}
	if len(unexpected) != 2 {
		t.Fatalf("expected symlink and stray reported as unexpected, got %v", unexpected)
	}

	// even removing the symlink by its code-shaped name only unlinks the
	// link itself, never its target
	if err := sp.Remove(link); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{outside, sibling, stray, filepath.Join(dir, markerName)} {
		mustExist(t, p)
	}

	for _, code := range []string{"", ".", "..", "../escape", "a/b", "Upper", "has space"} {
		if _, err := sp.Store(code, nil, 0); err == nil {
			t.Fatalf("expected code %q to be rejected", code)
		}
	}
}
