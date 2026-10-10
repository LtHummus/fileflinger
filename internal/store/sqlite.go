package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

const recordDownloadSQL = `
    INSERT INTO downloads (code, client_ip, last_seen_at)
    SELECT s.code, ?, unixepoch()
    FROM shares AS s
    WHERE s.code = ?
      AND (s.expires_at IS NULL OR s.expires_at > unixepoch())
      AND (
          s.max_downloads IS NULL
          OR (
              SELECT COUNT(*)
              FROM downloads AS d
              WHERE d.code = s.code
          ) < s.max_downloads
      )
    ON CONFLICT (code, client_ip) DO NOTHING
`

// recordDownloadSQL can't refresh a returning IP: once the share is exhausted
// its SELECT matches nothing, so the conflict clause never runs
const touchDownloadSQL = `
    UPDATE downloads
    SET last_seen_at = unixepoch()
    WHERE code = ? AND client_ip = ?
`

const deleteExhaustedSQL = `
    DELETE FROM shares
    WHERE max_downloads IS NOT NULL
      AND (SELECT COUNT(*) FROM downloads AS d WHERE d.code = shares.code) >= max_downloads
      AND (SELECT MAX(d.last_seen_at) FROM downloads AS d WHERE d.code = shares.code) <= $1
`

const resolveShareSQL = `
    SELECT s.filename,
           s.spool_path,
           s.size,
           s.created_at,
           s.expires_at,
           s.max_downloads,
           (SELECT COUNT(*) FROM downloads AS d WHERE d.code = s.code),
           EXISTS (SELECT 1 FROM downloads AS d WHERE d.code = s.code AND d.client_ip = ?)
    FROM shares AS s
    WHERE s.code = ?
`

// queryer is satisfied by both *sql.DB and *sql.Tx, so resolve can run either
// standalone (Peek) or inside a transaction (Claim).
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

//go:embed sqlite_migrations/*
var migrations embed.FS

const latestVersion = 2

func migrateDatabase(db *sql.DB) error {
	d, err := iofs.New(migrations, "sqlite_migrations")
	if err != nil {
		return fmt.Errorf("db: migrateDatabase: could not load migration files: %w", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("db: migrateDatabase: could not open db for migration: %w", err)
	}
	defer driver.Close()

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("db: migrateDatabase: could not create migration instance: %w", err)
	}

	if err := m.Migrate(latestVersion); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			slog.Info("database is at latest version", "latest_version", latestVersion)
			return nil
		}

		slog.Info("could not migrate database to latest version", "latest_version", latestVersion)
		return fmt.Errorf("db: migrateDatabase: could not migrate to current database version: %w", err)
	}

	return nil
}

type SQLite struct {
	setupLock sync.Mutex
	db        *sql.DB
	log       *slog.Logger

	setupNeeded *bool
}

func NewSQLite(path string, log *slog.Logger) (*SQLite, error) {
	asbDBFile, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: NewSQLite: could not make abs path for db file: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_pragma=journal_mode(wal)", url.PathEscape(asbDBFile))

	slog.Info("opening database", "db_file", asbDBFile)

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: NewSQLite: could not open db: %w", err)
	}

	err = migrateDatabase(database)
	if err != nil {
		err2 := database.Close()
		if err2 != nil {
			log.Warn("error closing db after migration", "error", err)
		}
		return nil, fmt.Errorf("db: NewSQLiteFromConfig: could not check configuration state: %w", err)
	}

	// reopen database now that migration is complete (`migrateDatabase` will close)
	database, err = sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: NewSQLiteFromConfig: could not open db: %w", err)
	}

	db := &SQLite{
		db:  database,
		log: log,
	}

	return db, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) Put(ctx context.Context, share *Share) error {
	var expiresAt sql.NullInt64
	if !share.Expires.IsZero() {
		expiresAt = sql.NullInt64{Int64: share.Expires.Unix(), Valid: true}
	}

	var maxDownloads sql.NullInt64
	if share.MaxDownloads > 0 {
		maxDownloads = sql.NullInt64{Int64: int64(share.MaxDownloads), Valid: true}
	}

	// held-fd shares have no path on disk, stored as NULL
	var spoolPath sql.NullString
	if share.Path != "" {
		spoolPath = sql.NullString{String: share.Path, Valid: true}
	}

	created := share.Created
	if created.IsZero() {
		created = time.Now()
	}

	_, err := s.db.ExecContext(ctx, "INSERT INTO shares (code, filename, spool_path, size, created_at, expires_at, max_downloads) VALUES ($1, $2, $3, $4, $5, $6, $7)", share.Code, share.Filename, spoolPath, share.Size, created.Unix(), expiresAt, maxDownloads)
	return err
}

func (s *SQLite) Delete(ctx context.Context, code string) (*Share, error) {
	shares, err := s.deleteReturning(ctx, "DELETE FROM shares WHERE code = $1", code)
	if err != nil {
		return nil, err
	}
	if len(shares) == 0 {
		return nil, ErrNotFound
	}
	return shares[0], nil
}

func (s *SQLite) DeleteExpired(ctx context.Context, now time.Time) ([]*Share, error) {
	return s.deleteReturning(ctx, "DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at <= $1", now.Unix())
}

func (s *SQLite) DeleteExhausted(ctx context.Context, idleSince time.Time) ([]*Share, error) {
	return s.deleteReturning(ctx, deleteExhaustedSQL, idleSince.Unix())
}

func (s *SQLite) deleteReturning(ctx context.Context, query string, args ...any) ([]*Share, error) {
	res, err := s.db.QueryContext(ctx, query+" RETURNING code, filename, spool_path, size, created_at, expires_at, max_downloads", args...)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	var ret []*Share
	for res.Next() {
		var sh Share
		var path sql.NullString
		var createdAt int64
		var expiresAt sql.NullInt64
		var maxDownloads sql.NullInt64
		if err := res.Scan(&sh.Code, &sh.Filename, &path, &sh.Size, &createdAt, &expiresAt, &maxDownloads); err != nil {
			return nil, err
		}
		sh.Path = path.String
		sh.Created = time.Unix(createdAt, 0)
		sh.MaxDownloads = int(maxDownloads.Int64)
		if expiresAt.Valid {
			sh.Expires = time.Unix(expiresAt.Int64, 0)
		}
		ret = append(ret, &sh)
	}

	return ret, res.Err()
}

func (s *SQLite) List(ctx context.Context, includeExpired bool) ([]*Share, error) {
	query := `
    SELECT s.code,
           s.filename,
           s.spool_path,
           s.size,
           s.created_at,
           s.expires_at,
           s.max_downloads,
           (SELECT COUNT(*) FROM downloads AS d WHERE d.code = s.code)
    FROM shares AS s
`
	if !includeExpired {
		query += "    WHERE s.expires_at IS NULL OR s.expires_at > unixepoch()\n"
	}

	res, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	var ret []*Share

	for res.Next() {
		var code string
		var filename string
		var path sql.NullString
		var size int64
		var createdAt int64
		var expiresAt sql.NullInt64
		var maxDownloads sql.NullInt64
		var downloads int
		err = res.Scan(&code, &filename, &path, &size, &createdAt, &expiresAt, &maxDownloads, &downloads)
		if err != nil {
			return nil, err
		}

		sh := &Share{
			Code:         code,
			Filename:     filename,
			Path:         path.String,
			Size:         size,
			Created:      time.Unix(createdAt, 0),
			Downloads:    downloads,
			MaxDownloads: int(maxDownloads.Int64),
		}
		if expiresAt.Valid {
			sh.Expires = time.Unix(expiresAt.Int64, 0)
		}
		ret = append(ret, sh)
	}

	return ret, res.Err()
}

func (s *SQLite) GetPath(ctx context.Context, code string) (string, error) {
	var path sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT spool_path FROM shares WHERE code = $1", code).Scan(&path)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}

	return path.String, nil
}

func (s *SQLite) resolve(ctx context.Context, q queryer, code string, remoteIP string) (*Share, bool, error) {
	var path sql.NullString
	var filename string
	var size int64
	var createdAt int64
	var expiresAt sql.NullInt64
	var maxDownloads sql.NullInt64
	var downloads int
	var alreadySeen bool

	err := q.QueryRowContext(ctx, resolveShareSQL, remoteIP, code).
		Scan(&filename, &path, &size, &createdAt, &expiresAt, &maxDownloads, &downloads, &alreadySeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("db: sqlite: resolve: could not load share: %w", err)
	}

	sh := &Share{
		Code:         code,
		Filename:     filename,
		Path:         path.String,
		Size:         size,
		Created:      time.Unix(createdAt, 0),
		Downloads:    downloads,
		MaxDownloads: int(maxDownloads.Int64), // NULL -> 0 = unlimited
	}
	if expiresAt.Valid {
		sh.Expires = time.Unix(expiresAt.Int64, 0)
	}

	if sh.Expired(time.Now()) {
		return nil, false, ErrExpired
	}

	// an IP that already downloaded is always let back in, even past the limit
	if !alreadySeen && sh.MaxDownloads > 0 && downloads >= sh.MaxDownloads {
		return nil, false, ErrExhausted
	}

	return sh, alreadySeen, nil
}

func (s *SQLite) Peek(ctx context.Context, code string, remoteIP string) (*Share, error) {
	s.log.Debug("running peek from sqlite", "code", code, "remote_ip", remoteIP)

	sh, _, err := s.resolve(ctx, s.db, code, remoteIP)
	return sh, err
}

func (s *SQLite) Claim(ctx context.Context, code string, clientIP string) (*Share, error) {
	s.log.Debug("running claim", "code", code, "remote_ip", clientIP)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("db: sqlite: Claim: could not begin transaction: %w", err)
	}
	defer tx.Rollback()

	sh, alreadySeen, err := s.resolve(ctx, tx, code, clientIP)
	if err != nil {
		return nil, err
	}

	if alreadySeen {
		_, err = tx.ExecContext(ctx, touchDownloadSQL, code, clientIP)
		if err != nil {
			return nil, fmt.Errorf("db: sqlite: Claim: could not update download time: %w", err)
		}
	} else {
		_, err = tx.ExecContext(ctx, recordDownloadSQL, clientIP, code)
		if err != nil {
			return nil, fmt.Errorf("db: sqlite: Claim: could not insert unique download: %w", err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return nil, fmt.Errorf("db: sqlite: Claim: could not commit transaction: %w", err)
	}

	if !alreadySeen {
		sh.Downloads++
	}

	return sh, nil
}

func (s *SQLite) AllowedRoots(ctx context.Context) ([]string, error) {
	res, err := s.db.QueryContext(ctx, "SELECT path FROM roots")
	if err != nil {
		return nil, err
	}
	defer res.Close()

	var ret []string
	for res.Next() {
		var path string
		err = res.Scan(&path)
		if err != nil {
			return nil, err
		}
		ret = append(ret, path)
	}

	return ret, nil
}

func (s *SQLite) AddAllowedRoot(ctx context.Context, root string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO roots (path) VALUES ($1) ON CONFLICT DO NOTHING", root)
	return err
}

func (s *SQLite) RemoveAllowedRoot(ctx context.Context, root string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM roots WHERE path = $1", root)
	if err != nil {
		return false, err
	}
	rowsDeleted, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsDeleted > 0, nil
}
