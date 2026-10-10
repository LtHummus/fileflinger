package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/lthummus/fileflinger/internal/config"
	"github.com/lthummus/fileflinger/internal/filesource"
)

type Relay struct {
	FileSource     filesource.FileSource
	Log            *slog.Logger
	IPSourceHeader string
	TrustedProxies []netip.Prefix

	diag    *diagnostics
	limiter *failureLimiter
}

func Run(ctx context.Context, cfg *config.Relay, fs filesource.FileSource, log *slog.Logger) error {
	if cfg.DrainTimeout == 0 {
		cfg.DrainTimeout = 30 * time.Second
	}

	r := &Relay{
		FileSource:     fs,
		Log:            log,
		IPSourceHeader: cfg.IPHeader,
		TrustedProxies: cfg.TrustedProxies,
		diag:           newDiagnostics(log),
		limiter:        newFailureLimiter(cfg.FailedLookupsPerMinute, log),
	}
	r.diag.checkConfig(r.IPSourceHeader, r.TrustedProxies)

	mux := http.NewServeMux()
	mux.Handle("/", r.Routes())
	// mux.HandleFunc("GET /healthz", healthz(conn))

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("relay server listening", "port", cfg.Port, "socket", cfg.SocketPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("draining connections", "timeout", cfg.DrainTimeout)
	sctx, cancel := context.WithTimeout(context.Background(), cfg.DrainTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Warn("drain incomplete, forcing close", "error", err)
		return srv.Close()
	}

	return nil
}

func (r *Relay) Routes() http.Handler {
	if r.diag == nil {
		r.diag = newDiagnostics(r.Log)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ip", r.ip)
	mux.HandleFunc("GET /d/{code}", r.download)
	mux.HandleFunc("HEAD /d/{code}", r.download)
	return mux
}

func (r *Relay) download(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	code := req.PathValue("code")

	if code == "" {
		http.Error(w, "no code specified", http.StatusBadRequest)
		return
	}

	r.diag.checkRequest(req, r.IPSourceHeader, r.TrustedProxies)
	remoteIP, _, err := clientIP(req, r.IPSourceHeader, r.TrustedProxies)
	if err != nil {
		slog.Error("could not get remote IP", "error", err)
		http.Error(w, "could not get IP", http.StatusInternalServerError)
		return
	}

	if ok, wait := r.limiter.allow(remoteIP); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		http.Error(w, "too many requests for missing downloads, try again later", http.StatusTooManyRequests)
		return
	}

	metadata, err := r.FileSource.Stat(ctx, code, remoteIP)
	if err != nil {
		if errors.Is(err, filesource.ErrNotFound) {
			r.limiter.fail(remoteIP)
		}
		slog.Error("could not stat file", "error", err)
		writeFileSourceError(err, w)
		return
	}

	rangeHeader := req.Header.Get("Range")
	if ir := req.Header.Get("If-Range"); ir != "" && ir != metadata.ETag {
		rangeHeader = ""
	}

	start, length, err := parseRange(rangeHeader, metadata.Size)
	if err != nil {
		r.Log.Error("bad range header given", "error", err, "range_header", rangeHeader)
		if errors.Is(err, errUnsatisfiable) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", metadata.Size))
			http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		http.Error(w, "bad range header", http.StatusBadRequest)
		return
	}

	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("ETag", metadata.ETag)
	h.Set("Content-Disposition", contentDisposition(metadata.Filename))

	if rangeHeader != "" {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, metadata.Size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if req.Method == http.MethodHead {
		return
	}

	if err := r.stream(ctx, w, code, start, length, metadata.ETag, remoteIP); err != nil {
		r.Log.Warn("stream aborted", "code", code, "offset", start, "length", length, "error", err)
	}
}

// IPResponse is what /ip?verbose returns
type IPResponse struct {
	IP       string   `json:"ip"`
	Source   string   `json:"source"` // "header" or "connection"
	Warnings []string `json:"warnings"`
}

// ip reports the address downloads are attributed to, so a proxy setup can be
// checked from outside. ?verbose adds where it came from and any config warnings.
func (r *Relay) ip(w http.ResponseWriter, req *http.Request) {
	r.diag.checkRequest(req, r.IPSourceHeader, r.TrustedProxies)
	addr, fromHeader, err := clientIP(req, r.IPSourceHeader, r.TrustedProxies)
	if err != nil {
		r.Log.Error("could not get remote IP", "error", err)
		http.Error(w, "could not get IP", http.StatusInternalServerError)
		return
	}

	// a caching proxy or CDN would otherwise hand one visitor's address to the next
	w.Header().Set("Cache-Control", "no-store")

	if !req.URL.Query().Has("verbose") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, addr)
		return
	}

	res := IPResponse{
		IP:       addr.String(),
		Source:   "connection",
		Warnings: r.diag.active(),
	}
	if fromHeader {
		res.Source = "header"
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		r.Log.Warn("could not write ip response", "error", err)
	}
}

func (r *Relay) stream(ctx context.Context, w http.ResponseWriter, code string, start int64, length int64, etag string, sourceIP netip.Addr) error {
	byteChan, err := r.FileSource.Read(ctx, code, start, length, etag, sourceIP)
	if err != nil {
		return fmt.Errorf("could not open read stream: %w", err)
	}

	var written int64
	for chunk := range byteChan {
		var n int
		n, err = w.Write(chunk)
		written += int64(n)
		if err != nil {
			return fmt.Errorf("could not write chunk to client: %w", err)
		}

	}

	if written != length {
		return fmt.Errorf("short stream: expected to write %d bytes, wrote %d instead", length, written)
	}

	return nil
}

func healthz(conn *grpc.ClientConn) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		switch s := conn.GetState(); s {
		case connectivity.Ready, connectivity.Idle:
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "daemon %s\n", s)
		}
	}
}

func writeFileSourceError(err error, w http.ResponseWriter) {
	if errors.Is(err, filesource.ErrNotFound) {
		http.Error(w, "download not found or expired", http.StatusNotFound)
	} else if errors.Is(err, filesource.ErrDownloadExhausted) {
		http.Error(w, "download limit reached", http.StatusGone)
	} else if errors.Is(err, filesource.ErrFileChanged) {
		http.Error(w, "underlying file has changed, please restart", http.StatusConflict)
	} else {
		http.Error(w, "some unknown error", http.StatusInternalServerError)
	}
}
