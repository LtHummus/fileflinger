package config

import (
	"net/netip"
	"os"
	"time"
)

type Daemon struct {
	SocketPath           string
	FdSocketPath         string
	BaseURL              string
	SpoolDirectory       string
	DefaultTTL           time.Duration
	ExhaustedGracePeriod time.Duration
	AllowedUIDs          []uint32
	AllowedGIDs          []uint32
	SocketMode           os.FileMode
	DrainTimeout         time.Duration
	DatabaseFile         string
}

type Relay struct {
	SocketPath     string
	DrainTimeout   time.Duration
	Port           int
	IPHeader       string
	TrustedProxies []netip.Prefix

	FailedLookupsPerMinute int // 0 disables the limit
}
