package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"

	"github.com/lthummus/fileflinger/internal/fdsend"
	"github.com/lthummus/fileflinger/internal/handles"
	"github.com/lthummus/fileflinger/internal/peercred"
	"github.com/lthummus/fileflinger/internal/spool"

	"github.com/lthummus/fileflinger/internal/config"
	"github.com/lthummus/fileflinger/internal/filesource"
	"github.com/lthummus/fileflinger/internal/relay"
	"github.com/lthummus/fileflinger/internal/store"
	pb "github.com/lthummus/fileflinger/proto"
)

func makeSocket(socketPath string, socketMode os.FileMode) (*net.UnixListener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("daemon: RunDaemon: could not create socket path: %w", err)
	}
	// have to delete any sockets left over from crashes
	_ = os.Remove(socketPath)

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("daemon: RunDaemon: could not listen on socket: %w", err)
	}

	mode := socketMode
	if mode == 0 {
		mode = 0o660
	}
	if err := os.Chmod(socketPath, mode); err != nil {
		return nil, fmt.Errorf("daemon: RunDaemon: could not set socket mode: %w", err)
	}

	return ln, nil
}

func RunDaemon(ctx context.Context, cfg config.Daemon, relayCfg *config.Relay, version, commit string, log *slog.Logger) error {
	if cfg.DrainTimeout == 0 {
		cfg.DrainTimeout = 30 * time.Second
	}

	runningInSoloMode := relayCfg != nil

	if len(cfg.AllowedGIDs) == 0 && len(cfg.AllowedUIDs) == 0 {
		slog.Warn("no allowed_gids OR allowed_uids set. This will allow anyone with permissions to the socket to contact the daemon!")
	}
	baseURLWarnings, err := config.CheckBaseURL(cfg.BaseURL)
	if err != nil {
		log.Warn(err.Error())
	}
	for _, w := range baseURLWarnings {
		log.Warn(w, "base_url", cfg.BaseURL)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var storage store.Store

	if cfg.DatabaseFile == "" {
		storage = store.NewInMemoryStore(log)
	} else {
		sqlStorage, err := store.NewSQLite(cfg.DatabaseFile, log)
		if err != nil {
			return fmt.Errorf("daemon: RunDaemon: could not init sqlite: %w", err)
		}
		defer sqlStorage.Close()

		storage = sqlStorage
	}

	h := handles.New(log)
	b := &backing{handles: h, log: log, exhaustedGracePeriod: cfg.ExhaustedGracePeriod}

	switch {
	case cfg.SpoolDirectory != "" && cfg.DatabaseFile != "":
		sp, err := spool.New(cfg.SpoolDirectory)
		if err != nil {
			return fmt.Errorf("daemon: RunDaemon: could not init spool: %w", err)
		}
		defer sp.Close()
		b.spool = sp
	case cfg.SpoolDirectory != "":
		log.Warn("spool directory set without a database file; persistent shares are disabled since they would not survive a restart anyway")
	}

	if err := b.reconcile(ctx, storage); err != nil {
		return fmt.Errorf("daemon: RunDaemon: could not reconcile state: %w", err)
	}
	go b.reap(ctx, storage)

	ln, err := makeSocket(cfg.SocketPath, cfg.SocketMode)
	if err != nil {
		return err
	}

	fdln, err := makeSocket(cfg.FdSocketPath, cfg.SocketMode)
	if err != nil {
		return err
	}

	r := fdsend.NewReceiver(5*time.Second, log)
	go func() {
		if err := r.Serve(ctx, fdln); err != nil {
			log.Error("error from fd server", "error", err)
		}
	}()

	srv := grpc.NewServer(
		grpc.Creds(peercred.NewPeerCredCreds()),
		grpc.InitialWindowSize(4<<20),
		grpc.InitialConnWindowSize(8<<20),
		grpc.ChainUnaryInterceptor(peerCredUnary(cfg.AllowedUIDs, cfg.AllowedGIDs, log)),
		grpc.ChainStreamInterceptor(peerCredStream(cfg.AllowedUIDs, cfg.AllowedGIDs, log)),
	)

	if !runningInSoloMode {
		pb.RegisterFileSourceServer(srv, &FileSource{
			Store:   storage,
			Log:     log,
			handles: h,
		})
	}
	a := &Admin{
		Store:      storage,
		Log:        log,
		BaseURL:    cfg.BaseURL,
		DefaultTTL: cfg.DefaultTTL,
		FDRecv:     r,
		backing:    b,

		BuildVersion: version,
		BuildCommit:  commit,
	}
	pb.RegisterAdminServer(srv, a)

	// relay.Run only returns an error if it could not serve, so a failure
	// cancels ctx to bring the daemon down with it
	relayErrc := make(chan error, 1)
	if runningInSoloMode {
		slog.Info("i am in server mode, so running relay too")
		go func() {
			if err := relay.Run(ctx, relayCfg, filesource.NewLocalFileSource(log, storage, h), log); err != nil {
				relayErrc <- err
				cancel()
			}
		}()
	}

	go func() {
		<-ctx.Done()
		done := make(chan struct{})
		go func() {
			h.Cleanup()
		}()
		go func() {
			srv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(cfg.DrainTimeout):
			log.Warn("could not drain withint imeout, force stopping")
			srv.Stop()
		}
	}()

	log.Info("daemon is listening", "socket", cfg.SocketPath)
	serveErr := srv.Serve(peercred.NewCredListener(ln))

	select {
	case err := <-relayErrc:
		return fmt.Errorf("daemon: RunDaemon: relay failed: %w", err)
	default:
		return serveErr
	}
}
