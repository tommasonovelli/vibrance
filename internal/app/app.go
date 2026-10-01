package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"vibrance/internal/buildinfo"
	"vibrance/internal/config"
)

// The stable codes of the failures that belong to the server itself.
const (
	// CodeRunAsRoot: the process runs as uid 0.
	CodeRunAsRoot = "run_as_root"
	// CodeHTTPListen: the HTTP server cannot listen, or stopped by itself.
	CodeHTTPListen = "http_listen"
)

// The limits of the HTTP server (DESIGN.md §11.1).
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10
)

// shutdownGrace is how long a stop waits for the open requests before it
// cuts them (§11.2): a stream may last longer than any reasonable wait.
const shutdownGrace = 10 * time.Second

// Error is a failure of the server itself, with its stable code.
type Error struct {
	Code string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Code returns the stable code of an error returned by Run, to be logged
// with it.
func Code(err error) string {
	var (
		ae *Error
		ce *config.Error
	)
	switch {
	case errors.As(err, &ae):
		return ae.Code
	case errors.As(err, &ce):
		return config.Code
	default:
		return "internal"
	}
}

// Run is the server: it starts in the order of §11.2, serves until ctx is
// cancelled, then stops in order. A nil error is a normal stop. Any other
// error has a stable code (Code); the caller logs it.
//
// euid is the effective user id of the process, and cpus the number of
// CPUs available to it.
func Run(ctx context.Context, getenv func(string) string, euid, cpus int, log *slog.Logger) error {
	// Step 1: refuse root, then read and validate the configuration.
	// Nothing is opened or written before both pass.
	if err := checkNotRoot(euid); err != nil {
		return err
	}
	cfg, err := config.Load(getenv, cpus)
	if err != nil {
		return err
	}
	log.Info("starting", "version", buildinfo.Version, "public_origin", cfg.PublicOrigin,
		"http_addr", cfg.HTTPAddr, "scan_interval", cfg.ScanInterval.String(), "workers", cfg.Workers)

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return &Error{Code: CodeHTTPListen, Msg: "cannot listen on " + cfg.HTTPAddr, Err: err}
	}
	if err := newServer(log).run(ctx, ln); err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

// checkNotRoot refuses uid 0: root bypasses the permission checks the
// server relies on, and would create files that the configured user
// cannot touch later.
func checkNotRoot(euid int) error {
	if euid == 0 {
		return &Error{Code: CodeRunAsRoot, Msg: "vibrance must not run as root: " +
			"set an unprivileged user (VIBRANCE_UID and VIBRANCE_GID in .env, never 0)"}
	}
	return nil
}

// The readiness of the server. It is positive only between the end of the
// startup and the beginning of the stop.
const (
	stateStarting int32 = iota
	stateReady
	stateStopping
)

// server is the state of one run.
type server struct {
	log   *slog.Logger
	http  *http.Server
	state atomic.Int32
	// grace is shutdownGrace; only the tests shorten it.
	grace time.Duration
}

func newServer(log *slog.Logger) *server {
	s := &server{log: log, grace: shutdownGrace}
	s.http = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// run serves on ln, completes the startup, waits for ctx to be cancelled
// and stops. It owns ln.
func (s *server) run(ctx context.Context, ln net.Listener) error {
	served := s.serveHTTP(ln)
	s.startup()
	select {
	case <-ctx.Done():
		s.log.Info("stopping")
		err := s.shutdown()
		// Serve returns as soon as the shutdown begins.
		<-served
		return err
	case err := <-served:
		return errors.Join(&Error{Code: CodeHTTPListen, Msg: "the HTTP server stopped", Err: err}, s.shutdown())
	}
}

// serveHTTP is step 2 of the startup: HTTP answers from the first moment,
// with negative readiness. The channel receives the error of Serve when
// it returns.
func (s *server) serveHTTP(ln net.Listener) <-chan error {
	served := make(chan error, 1)
	go func() { served <- s.http.Serve(ln) }()
	s.log.Info("http listening", "addr", ln.Addr().String())
	return served
}

// startup runs what is left of the startup once HTTP answers, in the order
// of §11.2. The steps below arrive with the components they start; each
// one that refuses will stop the startup there, with its code.
//
//	Step 3: check that /var/lib/vibrance is writable, open the database,
//	        apply the migrations (store_schema_too_new).
//	Step 4: verify ffmpeg and ffprobe at the pinned version
//	        (media_tool_unavailable, media_tool_version).
//	Step 5: create the admin if there is no user (admin_password_missing,
//	        admin_password_invalid).
//	Step 6: check meta.collate_version and meta.ffmpeg_version; delete the
//	        expired sessions.
//	Step 7: start the scanner and the hourly cleanup of the sessions.
//
// Readiness turns positive last. It depends neither on MusicLib nor on
// the state of the index (I14).
func (s *server) startup() {
	s.state.Store(stateReady)
	s.log.Info("ready")
}

// shutdown stops the server in the order of §11.2. Stopping is not a
// protocol the data depends on: a killed process recovers at the next
// start (crash-only).
//
//  1. Readiness turns negative.
//  2. The HTTP server stops accepting and waits for the open requests, for
//     at most the grace period; then it closes their connections. A
//     request cut this way is not a failure of the stop: streams outlive
//     any grace period.
//
// The steps below arrive with the components they stop:
//
//  3. Cancel the scanner and its jobs, which ends the child processes.
//  4. PRAGMA optimize and wal_checkpoint(TRUNCATE); close the database.
func (s *server) shutdown() error {
	s.state.Store(stateStopping)

	ctx, cancel := context.WithTimeout(context.Background(), s.grace)
	defer cancel()
	err := s.http.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("requests still open after the grace period: closing their connections", "grace", s.grace.String())
		err = s.http.Close()
	}
	if err != nil {
		return &Error{Code: CodeHTTPListen, Msg: "closing the HTTP server", Err: err}
	}
	s.log.Info("http server stopped")
	return nil
}
