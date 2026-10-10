package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("share not found")
	ErrExpired   = errors.New("share expired")
	ErrExhausted = errors.New("download limit reached")
)

type Share struct {
	Code         string
	Filename     string
	Path         string // absolute path
	Size         int64
	Created      time.Time
	Expires      time.Time // 0 = no expiration
	Downloads    int
	MaxDownloads int
}

func (s *Share) Expired(now time.Time) bool {
	return !s.Expires.IsZero() && now.After(s.Expires)
}

type Store interface {
	Put(ctx context.Context, share *Share) error
	List(ctx context.Context, includeExpired bool) ([]*Share, error)
	Delete(ctx context.Context, code string) (*Share, error)

	DeleteExpired(ctx context.Context, now time.Time) ([]*Share, error)
	DeleteExhausted(ctx context.Context, idleSince time.Time) ([]*Share, error)
	GetPath(ctx context.Context, code string) (string, error)

	Peek(ctx context.Context, code string, remoteIP string) (*Share, error)
	Claim(ctx context.Context, code string, remoteIP string) (*Share, error)

	AllowedRoots(ctx context.Context) ([]string, error)
	AddAllowedRoot(ctx context.Context, root string) error
	RemoveAllowedRoot(ctx context.Context, root string) (bool, error)
}
