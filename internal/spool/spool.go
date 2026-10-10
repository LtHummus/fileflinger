package spool

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// The spool deletes files, so we have three protections to protect us:
//   - the directory must be empty or carry markerName, so pointing the spool
//     at a real directory by mistake refuses to start instead of wiping it
//   - every operation goes through an os.Root, which cannot escape the
//     directory via "..", absolute paths, or symlinks
//   - only names we could have created (share codes, temp files) are ever
//     removed; anything else is left alone
const markerName = ".fileflinger-spool"

var (
	codeName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	tempName = regexp.MustCompile(`^incoming-[0-9a-f]{32}\.tmp$`)

	ErrSizeMismatch = errors.New("spool: file changed size while copying")
	ErrNotOurs      = errors.New("spool: path is not a spool file")
)

type Spool struct {
	dir  string
	root *os.Root
}

func New(dir string) (*Spool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("spool: New: could not make abs path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("spool: New: could not create spool directory: %w", err)
	}

	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("spool: New: could not open spool directory: %w", err)
	}

	if err := claim(root); err != nil {
		root.Close()
		return nil, fmt.Errorf("spool: New: %s: %w", abs, err)
	}

	return &Spool{dir: abs, root: root}, nil
}

// claim makes sure the directory is ours: either it already has the marker,
// or it is empty and we add one.
func claim(root *os.Root) error {
	if _, err := root.Lstat(markerName); err == nil {
		return nil
	}

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return fmt.Errorf("could not read directory: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("directory is not empty and has no %s marker; refusing to use it as a spool", markerName)
	}

	return root.WriteFile(markerName, []byte("managed by fileflinger; unreferenced files here are deleted\n"), 0o600)
}

func (s *Spool) Close() error {
	return s.root.Close()
}

func (s *Spool) Dir() string {
	return s.dir
}

func (s *Spool) Store(code string, src *os.File, size int64) (string, error) {
	if !codeName.MatchString(code) {
		return "", fmt.Errorf("spool: Store: invalid code %q", code)
	}

	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("spool: Store: could not generate temp name: %w", err)
	}
	// store to a temp file first so crashes don't leave half-written files in our spool directory
	tmpName := "incoming-" + hex.EncodeToString(rnd[:]) + ".tmp"

	tmp, err := s.root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("spool: Store: could not create temp file: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = s.root.Remove(tmpName)
		}
	}()

	// read from the start regardless of where the sender left the offset
	n, err := io.Copy(tmp, io.NewSectionReader(src, 0, size+1))
	if err != nil {
		return "", fmt.Errorf("spool: Store: copy failed: %w", err)
	}
	if n != size {
		return "", ErrSizeMismatch
	}

	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("spool: Store: could not sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("spool: Store: could not close: %w", err)
	}

	if err := s.root.Rename(tmpName, code); err != nil {
		return "", fmt.Errorf("spool: Store: could not rename into place: %w", err)
	}
	committed = true

	return filepath.Join(s.dir, code), nil
}

// name maps a full path back to a spool file name, or fails if the path is
// anything other than a file we could have created directly in the spool.
func (s *Spool) name(path string) (string, error) {
	if filepath.Clean(path) != path || filepath.Dir(path) != s.dir {
		return "", ErrNotOurs
	}
	base := filepath.Base(path)
	if !codeName.MatchString(base) && !tempName.MatchString(base) {
		return "", ErrNotOurs
	}
	return base, nil
}

// IsOwnedByUs determines if the file is owned by us because we can't delete
// anything else
func (s *Spool) IsOwnedByUs(path string) bool {
	_, err := s.name(path)
	return err == nil
}

func (s *Spool) Exists(path string) bool {
	name, err := s.name(path)
	if err != nil {
		return false
	}
	fi, err := s.root.Lstat(name)
	return err == nil && fi.Mode().IsRegular()
}

func (s *Spool) Remove(path string) error {
	name, err := s.name(path)
	if err != nil {
		return fmt.Errorf("spool: Remove: %q: %w", path, err)
	}
	err = s.root.Remove(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Files lists the files in the spool that it would be allowed to remove,
// including leftover temp files from interrupted copies. Anything else is
// returned separately so the caller can report it without touching it.
//
// Returns the spool's own files first, then the unexpected ones.
func (s *Spool) Files() ([]string, []string, error) {
	entries, err := fs.ReadDir(s.root.FS(), ".")
	if err != nil {
		return nil, nil, fmt.Errorf("spool: Files: could not read spool directory: %w", err)
	}

	var ours, unexpected []string
	for _, e := range entries {
		if e.Name() == markerName {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		if e.Type().IsRegular() && s.IsOwnedByUs(path) {
			ours = append(ours, path)
		} else {
			unexpected = append(unexpected, path)
		}
	}
	return ours, unexpected, nil
}
