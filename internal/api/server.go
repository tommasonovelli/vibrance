package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// BasePath is where the operations of the specification are served: the
// `servers` entry of api/openapi.yaml.
const BasePath = "/api/v1"

// errNotImplemented is what an operation returns until its step implements
// it.
var errNotImplemented = errors.New("operation not implemented")

// codeNotImplemented is not in the specification, because no released
// server answers it: it leaves with the last unimplemented operation.
const codeNotImplemented ErrorCode = "not_implemented"

// Server implements every operation of the specification.
type Server struct{}

// The compiler checks that no operation of the specification is missing.
var _ StrictServerInterface = Server{}

// NewHandler routes the operations of api/openapi.yaml, under BasePath, to
// srv. A request the generated code cannot bind (a path id that is not a
// UUID, a parameter of the wrong type, a body that is not JSON) answers
// 400 invalid_request. Paths and methods outside the specification are left
// to the router of net/http.
func NewHandler(srv StrictServerInterface, log *slog.Logger) http.Handler {
	e := errorWriter{log: log}
	strict := NewStrictHandlerWithOptions(srv, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  e.badRequest,
		ResponseErrorHandlerFunc: e.failed,
	})
	return HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:          BasePath,
		ErrorHandlerFunc: e.badRequest,
	})
}

// errorWriter answers with the error model of the API: a stable code, a
// sentence for people and an object of details, here always empty.
type errorWriter struct {
	log *slog.Logger
}

// badRequest answers a request the generated code could not bind. What was
// wrong is not repeated: it would echo input of the client.
func (e errorWriter) badRequest(w http.ResponseWriter, _ *http.Request, _ error) {
	e.write(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "The request is not valid.")
}

// failed answers the error of an operation.
func (e errorWriter) failed(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, errNotImplemented) {
		e.write(w, http.StatusNotImplemented, codeNotImplemented, "This operation is not implemented yet.")
		return
	}
	// The cause goes to the log, never into the response (DESIGN.md §8.1).
	e.log.Error("operation failed", "code", string(ErrorCodeInternal), "err", err.Error())
	e.write(w, http.StatusInternalServerError, ErrorCodeInternal, "Something went wrong.")
}

func (e errorWriter) write(w http.ResponseWriter, status int, code ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := Error{Code: code, Message: message, Details: map[string]interface{}{}}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The client went away: there is nobody left to tell.
		e.log.Debug("writing an error response", "err", err.Error())
	}
}
