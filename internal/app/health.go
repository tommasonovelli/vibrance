package app

import (
	"net/http"
	"path"
	"sync"

	"vibrance/internal/api"
	"vibrance/internal/httpx"
	"vibrance/web"
)

// The health endpoints (DESIGN.md §11.3).
const (
	livePath  = "/health/live"
	readyPath = "/health/ready"
)

// routes is everything the server answers, behind the HTTP boundary of
// internal/httpx (DESIGN.md §7.6):
//
//   - the two health endpoints, which are outside the checks of Host, Origin
//     and X-Vibrance-Request: a probe addresses the container by IP;
//   - behind those checks, the operations of the API under /api/v1, the
//     specification and the page that documents it (§8.8), and the web
//     interface, which takes every other path (api.RegisterUI).
//
// A path under /api or /health that is not there answers 404 not_found and
// a method a path does not take 405 method_not_allowed, both in the error
// model. publicOrigin is VIBRANCE_PUBLIC_ORIGIN.
func (s *server) routes(publicOrigin string) (http.Handler, error) {
	doc, err := api.LoadSpec()
	if err != nil {
		return nil, err
	}
	boundary, err := httpx.Boundary(s.log, publicOrigin)
	if err != nil {
		return nil, err
	}
	notFound := httpx.NotFound(s.log)
	getOnly := httpx.MethodNotAllowed(s.log, "GET, HEAD")

	health := http.NewServeMux()
	health.HandleFunc("GET "+livePath, s.handleLive)
	health.Handle(livePath, getOnly)
	health.HandleFunc("GET "+readyPath, s.handleReady)
	health.Handle(readyPath, getOnly)

	mux := http.NewServeMux()
	s.access = api.Access(doc)
	api.Register(mux, doc, api.NewServer(&s.sessions, &s.catalog, &s.media, &s.scanner, publicOrigin), s.authenticated, s.log)
	api.RegisterDocs(mux, s.log)
	if err := api.RegisterUI(mux, web.UI, s.log); err != nil {
		return nil, err
	}
	guarded := boundary(canonical(mux, notFound))

	all := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == livePath || r.URL.Path == readyPath {
			health.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
	// A probe asks for the health every few seconds: its access log is at
	// DEBUG, like that of the audio and of the covers.
	quiet := append(api.QuietRoutes(), "GET "+livePath, "GET "+readyPath)
	return httpx.Recover(s.log)(httpx.RequestID(s.log)(httpx.AccessLog(s.log, quiet)(httpx.SecurityHeaders(all)))), nil
}

// authenticated is the authentication of an operation (auth.Service.
// Middleware). The service of the sessions needs the database, which is
// opened after HTTP answers: until the startup is complete, and after one
// that failed, every operation answers 503 not_ready or shutting_down
// (§8.4), the public ones included.
func (s *server) authenticated(next http.Handler) http.Handler {
	var (
		once   sync.Once
		authed http.Handler
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions := s.sessions.Load()
		if sessions == nil {
			httpx.WriteError(w, r, s.log, s.notServing())
			return
		}
		once.Do(func() { authed = sessions.Middleware(s.access, s.log)(next) })
		authed.ServeHTTP(w, r)
	})
}

// notServing is the 503 of a server that is not ready: not_ready while it
// starts, shutting_down once the stop has begun.
func (s *server) notServing() *httpx.Error {
	if s.state.Load() == stateStarting {
		return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "not_ready", Message: "the server is starting"}
	}
	return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "shutting_down", Message: "the server is shutting down"}
}

// canonical answers with notFound a path that is not in its canonical form:
// the router of net/http would redirect `//x` and `/a/../x` to the path it
// cleans them into, and no path of the server has such an alias.
func canonical(next, notFound http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == "" || p[0] != '/' || path.Clean(p) != p {
			notFound.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type healthBody struct {
	Status string `json:"status"`
}

// handleLive says that the process answers.
func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, s.log, http.StatusOK, healthBody{Status: "live"})
}

// handleReady is positive only once the startup is complete and until the
// stop begins.
func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.state.Load() == stateReady {
		httpx.WriteJSON(w, s.log, http.StatusOK, healthBody{Status: "ready"})
		return
	}
	httpx.WriteError(w, r, s.log, s.notServing())
}
