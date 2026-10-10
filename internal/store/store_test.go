package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func stores(t *testing.T) map[string]Store {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sq, err := NewSQLite(filepath.Join(t.TempDir(), "test.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sq.Close() })

	return map[string]Store{
		"memory": NewInMemoryStore(log),
		"sqlite": sq,
	}
}

func TestDeleteReturnsShare(t *testing.T) {
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			err := st.Put(ctx, &Share{Code: "a", Filename: "f.txt", Path: "/spool/a", Size: 3, Created: time.Now()})
			if err != nil {
				t.Fatal(err)
			}

			sh, err := st.Delete(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}
			if sh.Code != "a" || sh.Filename != "f.txt" || sh.Path != "/spool/a" {
				t.Fatalf("unexpected share %+v", sh)
			}

			if _, err := st.Delete(ctx, "a"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		})
	}
}

func TestDeleteExpired(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			for _, sh := range []*Share{
				{Code: "old", Filename: "old", Created: now, Expires: now.Add(time.Minute)},
				{Code: "new", Filename: "new", Created: now, Expires: now.Add(time.Hour)},
				{Code: "forever", Filename: "forever", Created: now},
			} {
				if err := st.Put(ctx, sh); err != nil {
					t.Fatal(err)
				}
			}

			expired, err := st.DeleteExpired(ctx, now.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(expired) != 1 || expired[0].Code != "old" || expired[0].Path != "" {
				t.Fatalf("unexpected expired set %+v", expired)
			}

			left, err := st.List(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 2 {
				t.Fatalf("expected 2 remaining, got %d", len(left))
			}
			for _, sh := range left {
				if sh.Filename == "" {
					t.Fatalf("List dropped filename for %s", sh.Code)
				}
			}
		})
	}
}

// backdate pretends ip's last download of code happened at when
func backdate(t *testing.T, st Store, code string, ip string, when time.Time) {
	t.Helper()
	switch s := st.(type) {
	case *InMemoryStore:
		s.lock.Lock()
		s.downloads[code][ip] = when
		s.lock.Unlock()
	case *SQLite:
		_, err := s.db.Exec("UPDATE downloads SET last_seen_at = ? WHERE code = ? AND client_ip = ?", when.Unix(), code, ip)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown store %T", st)
	}
}

func TestDeleteExhausted(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			for _, sh := range []*Share{
				{Code: "used", Filename: "used", Created: now, MaxDownloads: 1},
				{Code: "partial", Filename: "partial", Created: now, MaxDownloads: 2},
				{Code: "unlimited", Filename: "unlimited", Created: now},
				{Code: "untouched", Filename: "untouched", Created: now, MaxDownloads: 1},
			} {
				if err := st.Put(ctx, sh); err != nil {
					t.Fatal(err)
				}
			}
			for _, code := range []string{"used", "partial", "unlimited"} {
				if _, err := st.Claim(ctx, code, "1.1.1.1"); err != nil {
					t.Fatalf("claim %s: %v", code, err)
				}
			}

			gone, err := st.DeleteExhausted(ctx, now.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(gone) != 0 {
				t.Fatalf("deleted shares still in their grace period: %+v", gone)
			}

			gone, err = st.DeleteExhausted(ctx, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(gone) != 1 || gone[0].Code != "used" {
				t.Fatalf("unexpected exhausted set %+v", gone)
			}

			left, err := st.List(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 3 {
				t.Fatalf("expected 3 remaining, got %d", len(left))
			}
		})
	}
}

func TestResumeExtendsGrace(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			if err := st.Put(ctx, &Share{Code: "a", Filename: "a", Created: now, MaxDownloads: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.Claim(ctx, "a", "1.1.1.1"); err != nil {
				t.Fatal(err)
			}
			backdate(t, st, "a", "1.1.1.1", now.Add(-time.Hour))

			// the share is exhausted, but the same IP coming back counts as activity
			if _, err := st.Claim(ctx, "a", "1.1.1.1"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.Claim(ctx, "a", "2.2.2.2"); !errors.Is(err, ErrExhausted) {
				t.Fatalf("expected ErrExhausted for a new IP, got %v", err)
			}

			gone, err := st.DeleteExhausted(ctx, now.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(gone) != 0 {
				t.Fatalf("resume did not refresh last download time: %+v", gone)
			}
		})
	}
}

func TestMigrationBackfillsLastSeen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrations, "sqlite_migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(1); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
        INSERT INTO shares (code, filename, size, max_downloads) VALUES ('a', 'a', 1, 1);
        INSERT INTO downloads (code, client_ip) VALUES ('a', '1.1.1.1');
    `)
	if err != nil {
		t.Fatal(err)
	}
	driver.Close()

	st, err := NewSQLite(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	gone, err := st.DeleteExhausted(ctx, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 0 {
		t.Fatalf("pre-migration download should start its grace period at migration: %+v", gone)
	}

	gone, err = st.DeleteExhausted(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 {
		t.Fatalf("pre-migration download was not backfilled: %+v", gone)
	}
}
