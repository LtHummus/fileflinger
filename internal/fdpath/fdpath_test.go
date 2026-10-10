package fdpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPath(t *testing.T) {
	// the temp dir itself may sit behind a symlink (/tmp -> /private/tmp on macOS)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "some file.txt")
	if err := os.WriteFile(want, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := Path(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestPathFollowsRename(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	orig := filepath.Join(dir, "before")
	moved := filepath.Join(dir, "after")
	if err := os.WriteFile(orig, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(orig)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := os.Rename(orig, moved); err != nil {
		t.Fatal(err)
	}

	got, err := Path(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != moved {
		t.Fatalf("Path() = %q, want %q", got, moved)
	}
}
