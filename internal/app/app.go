package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/auth"
	"vibrance/internal/buildinfo"
	"vibrance/internal/catalog"
	"vibrance/internal/config"
	"vibrance/internal/covers"
	"vibrance/internal/library"
	"vibrance/internal/media"
	"vibrance/internal/store"
)

// The stable codes of the failures that belong to the server itself.
const (
	// CodeRunAsRoot: the process runs as uid 0.
	CodeRunAsRoot = "run_as_root"
	// CodeHTTPListen: the HTTP server cannot listen, or stopped by itself.
	CodeHTTPListen = "http_listen"
	// CodeStateUnwritable: the server cannot create files in its state
	// folder.
	CodeStateUnwritable = "state_unwritable"
	// CodeMusicLibFolder: the folder MusicLib's data volume is mounted at
	// cannot be opened. Its content is not looked at: an empty folder is
	// fine (I14).
	CodeMusicLibFolder = "musiclib_folder"
	// CodeLibraryIndex: the index of the library could not be prepared for
	// serving (its sort keys).
	CodeLibraryIndex = "library_index"
)

// databaseFile is the name of the database in the state folder (§5.1), and
// thumbsDir that of the cache of the cover thumbnails (§9.2), which is only
// a cache: it can be removed at any time.
const (
	databaseFile = "vibrance.db"
	thumbsDir    = "thumbs"
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

// Error is a failure of the server, with its stable code: one of this
// package, or that of the failure of the store or of the media tools it
// wraps.
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
// CPUs available to it. stateDir is the folder of the server's state: the
// database and the cache of the thumbnails. musiclibDir is the folder MusicLib's
// data volume is mounted at, which the server only reads.
func Run(ctx context.Context, getenv func(string) string, euid, cpus int, stateDir, musiclibDir string, log *slog.Logger) error {
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

	srv, err := newServer(log, cfg.PublicOrigin, stateDir, musiclibDir, cfg.Workers, cfg.ScanInterval, getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return &Error{Code: CodeHTTPListen, Msg: "cannot listen on " + cfg.HTTPAddr, Err: err}
	}
	if err := srv.run(ctx, ln); err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

// OpenAccounts opens the accounts of the server whose state is in stateDir,
// for a subcommand that manages them (§7.4). The server may be running: the
// database takes several processes (§3.4). The function returned closes the
// database; with the server running it may report that the write-ahead log
// could not be emptied, which loses nothing.
func OpenAccounts(ctx context.Context, stateDir string, log *slog.Logger) (*auth.Service, func() error, error) {
	st, err := store.Open(ctx, filepath.Join(stateDir, databaseFile))
	if err != nil {
		return nil, nil, &Error{Code: store.Code(err), Msg: "opening the database", Err: err}
	}
	accounts, err := auth.NewService(st, auth.ProductionCost(), time.Now, log)
	if err != nil {
		return nil, nil, errors.Join(err, st.Close())
	}
	return accounts, st.Close, nil
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
	log      *slog.Logger
	http     *http.Server
	state    atomic.Int32
	stateDir string
	// musiclibDir is the folder of MusicLib's data volume (/musiclib).
	musiclibDir string
	// store is the database, from step 3 of the startup on.
	store *store.Store
	// workers is VIBRANCE_WORKERS: how many ffmpeg and ffprobe processes
	// run at once, and how many albums are indexed at once.
	workers int
	// scanInterval is VIBRANCE_SCAN_INTERVAL.
	scanInterval time.Duration
	// ffmpegPath and ffprobePath are media.FFmpegPath and
	// media.FFprobePath; only the tests point them elsewhere.
	ffmpegPath, ffprobePath string
	// tools is the adapter of ffmpeg and ffprobe, from step 4 of the
	// startup on.
	tools *media.Tools
	// scanner keeps the index aligned with the library, from step 7 of the
	// startup on; stopScanner stops it and waits for it.
	scanner     *library.Scanner
	stopScanner func() error
	// getenv reads the environment, for the two variables of the first
	// admin, which only the code that creates it reads (§7.4).
	getenv func(string) string
	// passwordCost is auth.ProductionCost; only the tests lower it.
	passwordCost auth.Cost
	// access is what each operation asks of a request, by its route.
	access map[string]auth.Access
	// sessions is the accounts and their sessions, published when the
	// startup is complete: until then the operations answer 503. A startup
	// that fails never publishes it.
	sessions atomic.Pointer[auth.Service]
	// catalog reads the index for the operations of the catalog. It is
	// published before sessions: an operation runs only once sessions is,
	// and then finds it.
	catalog atomic.Pointer[catalog.Service]
	// media serves the files of the library for the operations of the
	// audio, the covers and the lyrics. It is published with the scanner,
	// before sessions.
	media atomic.Pointer[api.Media]
	// stopSessions stops the hourly cleanup of the sessions and waits for
	// it.
	stopSessions func()
	// grace is shutdownGrace; only the tests shorten it.
	grace time.Duration
}

// newServer prepares a server that answers for publicOrigin, which is
// VIBRANCE_PUBLIC_ORIGIN. getenv is the environment. It fails only if the
// HTTP boundary cannot be built: the specification the binary carries does
// not parse, which the tests of internal/api exclude.
func newServer(log *slog.Logger, publicOrigin, stateDir, musiclibDir string, workers int, scanInterval time.Duration,
	getenv func(string) string) (*server, error) {
	s := &server{log: log, stateDir: stateDir, musiclibDir: musiclibDir, grace: shutdownGrace,
		workers: workers, scanInterval: scanInterval, ffmpegPath: media.FFmpegPath, ffprobePath: media.FFprobePath,
		getenv: getenv, passwordCost: auth.ProductionCost()}
	handler, err := s.routes(publicOrigin)
	if err != nil {
		return nil, fmt.Errorf("building the HTTP boundary: %w", err)
	}
	s.http = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s, nil
}

// run serves on ln, completes the startup, waits for ctx to be cancelled
// and stops. It owns ln.
func (s *server) run(ctx context.Context, ln net.Listener) error {
	served := s.serveHTTP(ln)
	if err := s.startup(ctx); err != nil {
		if ctx.Err() != nil {
			// A stop asked for during the startup interrupts the step that
			// was running. That is a stop like any other, not a failure
			// of the step.
			s.log.Info("stopping", "interrupted", err.Error())
			err = nil
		}
		err = errors.Join(err, s.shutdown())
		<-served
		return err
	}
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
// of §11.2. A step that refuses stops the startup there, with its code.
//
//	Step 3: check that the state folder is writable, open the database,
//	        apply the migrations (state_unwritable, store_open,
//	        store_schema_too_new, store_migrate).
//	Step 4: verify ffmpeg and ffprobe at the pinned version
//	        (media_tool_unavailable, media_tool_version).
//	Step 5: create the admin if there is no user (admin_password_missing,
//	        admin_password_invalid, admin_username_invalid).
//	Step 6: compute the sort keys again if the collation changed
//	        (library_index), and delete the expired sessions. The
//	        fingerprints of another ffmpeg are computed again by the
//	        scanner, in the background.
//	Step 7: start the scanner, and with it the making of the thumbnails
//	        of new covers (musiclib_folder), and the hourly cleanup of the
//	        sessions.
//
// Readiness turns positive last, and with it the operations of the API,
// which answer 503 not_ready until then. It depends neither on MusicLib nor
// on the state of the index (I14).
func (s *server) startup(ctx context.Context) error {
	if err := s.openStore(ctx); err != nil {
		return err
	}
	if err := s.checkTools(ctx); err != nil {
		return err
	}
	sessions, err := s.openAccounts(ctx)
	if err != nil {
		return err
	}
	if err := s.prepareIndex(ctx); err != nil {
		return err
	}
	if _, err := sessions.CleanupExpired(ctx); err != nil {
		return err
	}
	if err := s.startScanner(); err != nil {
		return err
	}
	s.startCleanup(sessions)
	s.catalog.Store(catalog.New(s.store))
	s.sessions.Store(sessions)
	s.state.Store(stateReady)
	s.log.Info("ready")
	return nil
}

// openStore is step 3 of the startup.
func (s *server) openStore(ctx context.Context) error {
	if err := checkWritable(s.stateDir); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(s.stateDir, databaseFile))
	if err != nil {
		return &Error{Code: store.Code(err), Msg: "opening the database", Err: err}
	}
	s.store = st
	s.log.Info("database open")
	return nil
}

// checkTools is step 4 of the startup: ffmpeg and ffprobe must be there,
// at the pinned version. They are the tools that verified the audio when
// MusicLib wrote the library, and a fingerprint is comparable only with
// those of the same ffmpeg (§5.4): the server does not start with others.
// The Runner made here is the one of the whole server.
func (s *server) checkTools(ctx context.Context) error {
	tools, err := media.NewTools(ctx, media.NewRunner(s.workers), s.ffmpegPath, s.ffprobePath)
	if err != nil {
		return &Error{Code: media.Code(err), Msg: "checking ffmpeg and ffprobe", Err: err}
	}
	s.tools = tools
	s.log.Info("media tools verified", "version", tools.Version())
	return nil
}

// openAccounts is step 5 of the startup: the service of the accounts and of
// their sessions and, in a database without accounts, the first admin
// (§7.4). Without a valid password for it the server does not start: it
// would be a server nobody can sign in to.
func (s *server) openAccounts(ctx context.Context) (*auth.Service, error) {
	sessions, err := auth.NewService(s.store, s.passwordCost, time.Now, s.log)
	if err != nil {
		return nil, err
	}
	if err := sessions.Bootstrap(ctx, s.getenv); err != nil {
		var refusal *auth.BootstrapError
		if errors.As(err, &refusal) {
			return nil, &Error{Code: refusal.Code, Msg: "creating the first admin", Err: err}
		}
		return nil, err
	}
	return sessions, nil
}

// startCleanup is the part of step 7 that deletes the expired sessions
// every hour (§7.3), until the stop.
func (s *server) startCleanup(sessions *auth.Service) {
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Go(func() { sessions.Run(ctx) })
	s.stopSessions = func() {
		cancel()
		running.Wait()
	}
}

// prepareIndex is what step 6 of the startup does to the index before the
// server serves: the sort keys of the lists are those of the compiled
// collation (§5.5, T26).
func (s *server) prepareIndex(ctx context.Context) error {
	recomputed, err := library.EnsureSortKeys(ctx, s.store)
	if err != nil {
		return &Error{Code: CodeLibraryIndex, Msg: "preparing the index of the library", Err: err}
	}
	if recomputed {
		s.log.Info("sort keys computed again")
	}
	return nil
}

// startScanner is step 7 of the startup. The scanner has a context of its
// own: the stop cancels it after the HTTP server has stopped (§11.2). The
// folder of MusicLib is opened once and only read (I1); what is in it, or
// that it is empty, is the business of the scanner and never keeps the
// server from starting (I14).
func (s *server) startScanner() error {
	root, err := library.OpenRoot(s.musiclibDir)
	if err != nil {
		return &Error{Code: CodeMusicLibFolder, Msg: "opening the folder of MusicLib " + s.musiclibDir, Err: err}
	}
	// The covers service makes, one at a time and in the background, the
	// thumbnails of the covers the indexer finds new (§9.2). It lives as
	// long as the scanner, which is the one that asks.
	thumbs := covers.New(root, s.store, filepath.Join(s.stateDir, thumbsDir), s.log)
	indexer := library.NewIndexer(root, s.store, s.tools, thumbs, time.Now)
	s.scanner = library.NewScanner(indexer, s.workers, s.scanInterval, s.log)
	// The files are served from the same Root, which the stop closes only
	// once the HTTP server has stopped. A file that is not the one of the
	// index asks the scanner to index its album again (§9.1).
	s.media.Store(&api.Media{Files: catalog.NewFiles(root, s.store, s.log), Covers: thumbs, Recheck: s.scanner.Recheck})
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Go(func() { s.scanner.Run(ctx) })
	running.Go(func() { thumbs.Run(ctx) })
	s.stopScanner = func() error {
		cancel()
		running.Wait()
		s.log.Info("scanner stopped")
		if err := root.Close(); err != nil {
			return &Error{Code: CodeMusicLibFolder, Msg: "closing the folder of MusicLib", Err: err}
		}
		return nil
	}
	s.log.Info("scanner started", "scan_interval", s.scanInterval.String())
	return nil
}

// writeProbe is the file that checkWritable creates and removes. Its name
// is fixed, so that a probe left behind by a killed process is taken over
// by the next one instead of piling up.
const writeProbe = ".write-check"

// checkWritable verifies that the server can create files in its state
// folder. Without it the first sign of a volume that belongs to another
// user, or of a read-only one, would be an obscure error of SQLite.
func checkWritable(dir string) error {
	probe := filepath.Join(dir, writeProbe)
	f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		err = errors.Join(f.Close(), os.Remove(probe))
	}
	if err != nil {
		return &Error{Code: CodeStateUnwritable, Msg: fmt.Sprintf(
			"the state folder %s is not writable: it must be a writable volume that belongs to the user the server runs as "+
				"(VIBRANCE_UID and VIBRANCE_GID in .env)", dir), Err: err}
	}
	return nil
}

// shutdown stops the server in the order of §11.2. Stopping is not a
// protocol the data depends on: a killed process recovers at the next
// start (crash-only). A step that fails does not keep the next ones from
// running.
//
//  1. Readiness turns negative.
//  2. The HTTP server stops accepting and waits for the open requests, for
//     at most the grace period; then it closes their connections. A
//     request cut this way is not a failure of the stop: streams outlive
//     any grace period.
//  3. The scanner, its job, the making of thumbnails and the cleanup of
//     the sessions are cancelled, which ends the child processes, and
//     waited for: nothing uses the database after this.
//  4. PRAGMA optimize and wal_checkpoint(TRUNCATE); close the database.
func (s *server) shutdown() error {
	s.state.Store(stateStopping)
	httpErr := s.stopHTTP()
	var scannerErr error
	if s.stopScanner != nil {
		scannerErr = s.stopScanner()
	}
	if s.stopSessions != nil {
		s.stopSessions()
	}
	return errors.Join(httpErr, scannerErr, s.closeStore())
}

func (s *server) stopHTTP() error {
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

// closeStore closes the database, if the startup got as far as opening it.
func (s *server) closeStore() error {
	if s.store == nil {
		return nil
	}
	if err := s.store.Close(); err != nil {
		return &Error{Code: store.Code(err), Msg: "closing the database", Err: err}
	}
	s.log.Info("database closed")
	return nil
}
