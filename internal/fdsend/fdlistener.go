package fdsend

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/lthummus/fileflinger/internal/peercred"
)

var (
	ErrUnknownKey = errors.New("fdsend: unknown or expired key")
	ErrNotYetSent = errors.New("fdsend: fd not received yet")
	ErrRejected   = errors.New("fdsend: receiver rejected fd")
)

var (
	SocketMsgOk     = []byte{0}
	SocketMsgFailed = []byte{1}
)

type entry struct {
	uid     uint32
	file    *os.File
	expires time.Time
}

type Receiver interface {
	Expect(key string, uid uint32)
	Take(key string, uid uint32) (*os.File, error)
}

type ReceiverImpl struct {
	ttl time.Duration
	log *slog.Logger

	lock    sync.Mutex
	pending map[string]*entry
}

func NewReceiver(ttl time.Duration, log *slog.Logger) *ReceiverImpl {
	return &ReceiverImpl{
		ttl:     ttl,
		log:     log,
		pending: map[string]*entry{},
	}
}

func (r *ReceiverImpl) Expect(key string, uid uint32) {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.pending[key] = &entry{
		uid:     uid,
		expires: time.Now().Add(r.ttl),
	}
}

func (r *ReceiverImpl) Take(key string, uid uint32) (*os.File, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	e, ok := r.pending[key]
	if !ok || e.uid != uid {
		return nil, ErrUnknownKey
	}

	if e.file == nil {
		return nil, ErrNotYetSent
	}
	delete(r.pending, key)
	return e.file, nil
}

func (r *ReceiverImpl) deliver(key string, uid uint32, f *os.File) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	e, ok := r.pending[key]
	if !ok || e.uid != uid || e.file != nil {
		return ErrUnknownKey
	}
	e.file = f
	return nil
}

func (r *ReceiverImpl) Serve(ctx context.Context, ln *net.UnixListener) error {
	r.log.Info("starting FD receiver server")
	go r.reap(ctx)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		go r.handle(conn)
	}
}

func (r *ReceiverImpl) handle(conn *net.UnixConn) {
	r.log.Debug("fd recv handle")
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	cred, err := peercred.FromUnixConn(conn)
	if err != nil {
		r.log.Warn("fdsocket: could not read peer creds", "error", err)
		return
	}

	r.log.Debug("got fd peer creds", "uid", cred.UID)

	f, key, err := RecvFD(conn)
	if err != nil {
		r.log.Warn("fdsocket: recv failed", "error", err)
		return
	}

	r.log.Debug("got fd handle + key", "key", key)

	if err := r.deliver(key, cred.UID, f); err != nil {
		f.Close()
		_, _ = conn.Write(SocketMsgFailed)
		return
	}

	r.log.Debug("delivered")

	_, _ = conn.Write(SocketMsgOk)
	r.log.Debug("written")
}

func (r *ReceiverImpl) reap(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			r.closeWhere(func(e *entry) bool {
				return true
			})
			return
		case now := <-t.C:
			r.closeWhere(func(e *entry) bool {
				return now.After(e.expires)
			})
		}
	}
}

func (r *ReceiverImpl) closeWhere(f func(*entry) bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	for k, e := range r.pending {
		if f(e) {
			if e.file != nil {
				e.file.Close()
			}
			delete(r.pending, k)
		}
	}
}
