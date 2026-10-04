package app

import (
	"net/http"
	"path"

	"vibrance/internal/api"
	"vibrance/internal/httpx"
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
//   - behind those checks, the operations of the API under /api/v1 and `/`,
//     which leads to the API documentation until a web interface exists
//     (D20). The router of these is where that interface will be added.
//
// A path that is not there answers 404 not_found and a method a path does
// not take 405 method_not_allowed, both in the error model. publicOrigin is
// VIBRANCE_PUBLIC_ORIGIN.
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
	api.Register(mux, doc, api.Server{}, s.log)
	// 302, not 301: browsers cache a permanent redirect, and `/` is where
	// the web interface will be.
	mux.Handle("GET /{$}", http.RedirectHandler("/api/docs", http.StatusFound))
	mux.Handle("/{$}", getOnly)
	mux.Handle("/", notFound)
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
	switch s.state.Load() {
	case stateReady:
		httpx.WriteJSON(w, s.log, http.StatusOK, healthBody{Status: "ready"})
	case stateStopping:
		httpx.WriteError(w, r, s.log, &httpx.Error{Status: http.StatusServiceUnavailable,
			Code: "shutting_down", Message: "the server is shutting down"})
	default:
		httpx.WriteError(w, r, s.log, &httpx.Error{Status: http.StatusServiceUnavailable,
			Code: "not_ready", Message: "the server is starting"})
	}
}
