package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

// The stable codes the boundary itself answers (DESIGN.md §8.4). The codes
// of the domain belong to the services that return them.
const (
	codeInvalidRequest        = "invalid_request"
	codeOriginNotAllowed      = "origin_not_allowed"
	codeRequestHeaderRequired = "request_header_required"
	codeNotFound              = "not_found"
	codeMethodNotAllowed      = "method_not_allowed"
	codeBodyTooLarge          = "body_too_large"
	codeHostNotAllowed        = "host_not_allowed"
	codeInternal              = "internal"
	codeShuttingDown          = "shutting_down"
)

// Error is an error of the API: the status and the {code, message, details}
// body of DESIGN.md §8.1. Every service returns its refusals as an *Error,
// and WriteError answers anything else as 500 internal. Nothing in it is an
// absolute path, a query or the output of a tool; Message is a sentence for
// people and never repeats what the client sent.
type Error struct {
	Status  int
	Code    string
	Message string
	// Details is the object of details; nil is the empty object.
	Details map[string]any
	// RetryAfter, when not 0, is the Retry-After header of the answer: the
	// seconds a client waits before it asks again.
	RetryAfter int
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// InvalidParameter is the 400 of a parameter of the path, of the query or
// of the headers that cannot have the value sent. name is the name the
// specification gives it, never the value; "" when it is not known.
func InvalidParameter(name string) *Error {
	message := "A parameter of the request is not valid."
	if name != "" {
		message = fmt.Sprintf("The parameter %q is not valid.", name)
	}
	return &Error{Status: http.StatusBadRequest, Code: codeInvalidRequest, Message: message}
}

// InvalidBody is the 400 of a body that is not what the operation takes.
func InvalidBody() *Error {
	return invalidBody("The request body does not match what this operation takes.")
}

func invalidBody(message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: codeInvalidRequest, Message: message}
}

const internalMessage = "Something went wrong."

// errorBody is the JSON of an Error.
type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// staleHeaders describe a body the handler meant to send before it failed:
// they would be wrong on the error that replaces it.
var staleHeaders = []string{"Content-Length", "Content-Encoding", "Content-Range", "ETag", "Last-Modified"}

// WriteError answers err in the error model. An *Error is answered as it
// is. Anything else is unexpected: it answers 500 internal, and its cause
// goes to the log only, with the id of the request (DESIGN.md §11.5).
//
// An error that arrives when the context of the request is over is not
// looked at: the client is gone, or the server is closing its connection,
// and what the interrupted work returned says nothing (a cancelled query
// can look like a missing row). Nobody reads that answer.
//
// An error that arrives when the response has begun cannot be answered any
// more: nothing is added to what was sent.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	if b, ok := w.(interface{ begun() bool }); ok && b.begun() {
		// The error is of the writing itself: the client went away.
		log.Debug("response interrupted", "request_id", requestID(r.Context()), "err", err.Error())
		return
	}
	var e *Error
	switch {
	case r.Context().Err() != nil:
		log.Debug("request ended early", "request_id", requestID(r.Context()), "err", err.Error())
		e = &Error{Status: http.StatusServiceUnavailable, Code: codeShuttingDown, Message: "The request was interrupted."}
	case errors.As(err, &e):
	default:
		log.Error("request failed", "code", codeInternal, "request_id", requestID(r.Context()), "err", err.Error())
		e = &Error{Status: http.StatusInternalServerError, Code: codeInternal, Message: internalMessage}
	}
	for _, name := range staleHeaders {
		w.Header().Del(name)
	}
	if e.RetryAfter != 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	WriteJSON(w, log, e.Status, errorBody{Code: e.Code, Message: e.Message, Details: details})
}

// WriteJSON writes body as a JSON response, with the headers every JSON
// response has (§7.6).
func WriteJSON(w http.ResponseWriter, log *slog.Logger, status int, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		// A programming error: every body is a plain struct or map.
		log.Error("encoding a response", "code", codeInternal, "err", err.Error())
		status = http.StatusInternalServerError
		b = []byte(`{"code":"` + codeInternal + `","message":"` + internalMessage + `","details":{}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	setJSONHeaders(w.Header())
	w.WriteHeader(status)
	if _, err := w.Write(append(b, '\n')); err != nil {
		// The client went away: there is nobody left to tell.
		log.Debug("writing a response", "err", err.Error())
	}
}

// NotFound answers 404 not_found: a path that does not exist (§8.1).
func NotFound(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, log, &Error{Status: http.StatusNotFound, Code: codeNotFound, Message: "There is no such path."})
	})
}

// MethodNotAllowed answers 405 method_not_allowed with Allow: a path that
// exists and does not take the method (§8.1). allow is the value of the
// header, the methods the path takes.
func MethodNotAllowed(log *slog.Logger, allow string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		WriteError(w, r, log, &Error{Status: http.StatusMethodNotAllowed, Code: codeMethodNotAllowed,
			Message: "This path does not take this method. It takes: " + allow + "."})
	})
}
