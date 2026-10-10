package store

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type InMemoryStore struct {
	lock      sync.RWMutex
	shares    map[string]*Share
	downloads map[string]map[string]time.Time // code -> remote IP -> last download

	rootLock sync.RWMutex
	roots    map[string]struct{}

	log *slog.Logger
}

func (ims *InMemoryStore) RemoveAllowedRoot(ctx context.Context, root string) (bool, error) {
	ims.rootLock.Lock()
	defer ims.rootLock.Unlock()

	_, exists := ims.roots[root]
	delete(ims.roots, root)

	return exists, nil
}

func (ims *InMemoryStore) AddAllowedRoot(ctx context.Context, root string) error {
	ims.rootLock.Lock()
	defer ims.rootLock.Unlock()

	ims.roots[root] = struct{}{}
	return nil
}

func (ims *InMemoryStore) AllowedRoots(ctx context.Context) ([]string, error) {
	ims.rootLock.RLock()
	defer ims.rootLock.RUnlock()

	ret := make([]string, 0, len(ims.roots))
	for curr := range ims.roots {
		ret = append(ret, curr)
	}

	return ret, nil
}

func NewInMemoryStore(log *slog.Logger) *InMemoryStore {
	ims := &InMemoryStore{
		shares:    make(map[string]*Share),
		downloads: make(map[string]map[string]time.Time),
		roots:     make(map[string]struct{}),

		log: log,
	}
	return ims
}

func (ims *InMemoryStore) Put(ctx context.Context, share *Share) error {
	ims.lock.Lock()
	defer ims.lock.Unlock()

	ims.shares[share.Code] = share
	return nil
}

func (ims *InMemoryStore) Peek(ctx context.Context, code string, remoteIP string) (*Share, error) {
	ims.lock.RLock()
	defer ims.lock.RUnlock()

	ims.log.Debug("running peek", "code", code, "remote_ip", remoteIP)

	return ims.resolve(code, remoteIP, false)
}

func (ims *InMemoryStore) Claim(ctx context.Context, code string, remoteIP string) (*Share, error) {
	ims.lock.Lock()
	defer ims.lock.Unlock()

	ims.log.Debug("running claim", "code", code, "remote_ip", remoteIP)

	return ims.resolve(code, remoteIP, true)
}

func (ims *InMemoryStore) GetPath(ctx context.Context, code string) (string, error) {
	ims.lock.RLock()
	defer ims.lock.RUnlock()

	s, ok := ims.shares[code]
	if !ok {
		return "", ErrNotFound
	}

	return s.Path, nil
}

func allow(sh *Share, seen map[string]time.Time, remoteIP string, now time.Time) error {
	if sh.Expired(now) {
		return ErrExpired
	}

	if _, ok := seen[remoteIP]; ok {
		return nil
	}

	if sh.MaxDownloads > 0 && len(seen) >= sh.MaxDownloads {
		return ErrExhausted
	}

	return nil
}

func (ims *InMemoryStore) resolve(code string, remoteIP string, record bool) (*Share, error) {
	sh, ok := ims.shares[code]
	if !ok {
		return nil, ErrNotFound
	}

	seen := ims.downloads[code]
	if err := allow(sh, seen, remoteIP, time.Now()); err != nil {
		return nil, err
	}

	if record {
		if seen == nil {
			seen = make(map[string]time.Time)
			ims.downloads[code] = seen
		}
		seen[remoteIP] = time.Now()
	}

	cp := *sh
	cp.Downloads = len(ims.downloads[code])
	return &cp, nil
}

func (ims *InMemoryStore) Delete(ctx context.Context, code string) (*Share, error) {
	ims.lock.Lock()
	defer ims.lock.Unlock()

	sh, ok := ims.shares[code]
	if !ok {
		return nil, ErrNotFound
	}
	delete(ims.shares, code)
	delete(ims.downloads, code)
	return sh, nil
}

func (ims *InMemoryStore) List(ctx context.Context, includeExpired bool) ([]*Share, error) {
	ims.lock.RLock()
	defer ims.lock.RUnlock()

	now := time.Now()
	ret := make([]*Share, 0, len(ims.shares))
	for _, curr := range ims.shares {
		if !includeExpired && curr.Expired(now) {
			continue
		}
		cp := *curr
		cp.Downloads = len(ims.downloads[curr.Code])
		ret = append(ret, &cp)
	}

	return ret, nil
}

func (ims *InMemoryStore) DeleteExpired(ctx context.Context, now time.Time) ([]*Share, error) {
	ims.lock.Lock()
	defer ims.lock.Unlock()

	var ret []*Share
	for code, s := range ims.shares {
		if s.Expired(now) {
			ret = append(ret, s)
			delete(ims.shares, code)
			delete(ims.downloads, code)
		}
	}
	return ret, nil
}

func (ims *InMemoryStore) DeleteExhausted(ctx context.Context, idleSince time.Time) ([]*Share, error) {
	ims.lock.Lock()
	defer ims.lock.Unlock()

	var ret []*Share
	for code, s := range ims.shares {
		seen := ims.downloads[code]
		if s.MaxDownloads <= 0 || len(seen) < s.MaxDownloads {
			continue
		}

		var last time.Time
		for _, t := range seen {
			if t.After(last) {
				last = t
			}
		}
		if last.After(idleSince) {
			continue
		}

		ret = append(ret, s)
		delete(ims.shares, code)
		delete(ims.downloads, code)
	}
	return ret, nil
}
