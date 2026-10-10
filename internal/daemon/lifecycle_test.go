package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lthummus/fileflinger/internal/handles"
	"github.com/lthummus/fileflinger/internal/spool"
	"github.com/lthummus/fileflinger/internal/store"
)

func TestReconcile(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	st, err := store.NewSQLite(filepath.Join(t.TempDir(), "test.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sp, err := spool.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	b := &backing{handles: handles.New(log), spool: sp, log: log}

	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	kept := filepath.Join(sp.Dir(), "kept")
	expiredFile := filepath.Join(sp.Dir(), "expired")
	orphan := filepath.Join(sp.Dir(), "orphan")
	leftoverTmp := filepath.Join(sp.Dir(), "incoming-0123456789abcdef0123456789abcdef.tmp")
	stray := filepath.Join(sp.Dir(), "notes.txt")
	userFile := filepath.Join(t.TempDir(), "users-file")
	for _, p := range []string{kept, expiredFile, orphan, leftoverTmp, stray, userFile} {
		write(p)
	}

	now := time.Now()
	for _, sh := range []*store.Share{
		{Code: "kept", Filename: "k", Path: kept, Size: 4},
		{Code: "expired", Filename: "e", Path: expiredFile, Size: 4, Created: now.Add(-2 * time.Hour), Expires: now.Add(-time.Hour)},
		{Code: "fd", Filename: "fd"},
		{Code: "missing", Filename: "m", Path: filepath.Join(sp.Dir(), "missing"), Size: 4},
		{Code: "user", Filename: "u", Path: userFile, Size: 4},
	} {
		if sh.Created.IsZero() {
			sh.Created = now
		}
		if err := st.Put(ctx, sh); err != nil {
			t.Fatalf("put %s: %v", sh.Code, err)
		}
	}

	if err := b.reconcile(ctx, st); err != nil {
		t.Fatal(err)
	}

	left, err := st.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, sh := range left {
		codes[sh.Code] = true
	}
	if len(codes) != 2 || !codes["kept"] || !codes["user"] {
		t.Fatalf("expected only kept and user to survive, got %v", codes)
	}

	for _, p := range []string{kept, stray, userFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should still exist: %v", p, err)
		}
	}
	for _, p := range []string{expiredFile, orphan, leftoverTmp} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should have been removed, got %v", p, err)
		}
	}
}

func TestReleaseClosesHandle(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := handles.New(log)
	b := &backing{handles: h, log: log}

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	h.Put("code", f)

	b.release(&store.Share{Code: "code"})

	if _, err := h.Get("code"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("handle should be gone, got %v", err)
	}
	if _, err := f.Stat(); err == nil {
		t.Fatal("file should have been closed")
	}
}
