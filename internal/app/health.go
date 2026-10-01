package app

import (
	"encoding/json"
	"net/http"
)

// routes are the infrastructure paths (DESIGN.md §8.3): the two health
// endpoints (§11.3) and `/`, which leads to the API documentation until a
// web interface exists (D20). They are public and outside the API's Host
// check: a probe addresses the container by IP.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	// 302, not 301: browsers cache a permanent redirect, and `/` is where
	// the web interface will be.
	mux.Handle("GET /{$}", http.RedirectHandler("/api/docs", http.StatusFound))
	return mux
}

type healthBody struct {
	Status string `json:"status"`
}

// errorBody is the standard error of the API, {code, message, details}
// (§8.1). These errors have no details.
type errorBody struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details struct{} `json:"details"`
}

// handleLive says that the process answers.
func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, healthBody{Status: "live"})
}

// handleReady is positive only once the startup is complete and until the
// stop begins.
func (s *server) handleReady(w http.ResponseWriter, _ *http.Request) {
	switch s.state.Load() {
	case stateReady:
		s.writeJSON(w, http.StatusOK, healthBody{Status: "ready"})
	case stateStopping:
		s.writeJSON(w, http.StatusServiceUnavailable,
			errorBody{Code: "shutting_down", Message: "the server is shutting down"})
	default:
		s.writeJSON(w, http.StatusServiceUnavailable,
			errorBody{Code: "not_ready", Message: "the server is starting"})
	}
}

func (s *server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The client went away: there is nobody left to tell.
		s.log.Debug("writing a health response", "err", err.Error())
	}
}
