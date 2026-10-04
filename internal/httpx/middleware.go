package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RequestHeader is the header every request other than GET and HEAD must
// carry, with the value "1" (DESIGN.md §7.6, I4). A page of another origin
// cannot send it without a CORS preflight, which this server never grants.
const RequestHeader = "X-Vibrance-Request"

const requestIDHeader = "X-Request-Id"

// Recover answers a panic of next with 500 internal and nothing of its
// cause: the value and the stack go to the log. It is the outermost
// middleware, so that a panic of any other is caught too.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen := &written{ResponseWriter: w}
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					// The way a handler asks net/http to drop the connection.
					panic(v)
				}
				// RequestID has set the header before anything could panic.
				log.Error("panic in a request", "code", codeInternal, "request_id", w.Header().Get(requestIDHeader),
					"panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				if seen.done {
					// A response has begun: it cannot become a 500 any more.
					// Dropping the connection tells the client it is not whole.
					panic(http.ErrAbortHandler)
				}
				for _, name := range staleHeaders {
					w.Header().Del(name)
				}
				setSecurityHeaders(w.Header())
				WriteJSON(w, log, http.StatusInternalServerError,
					errorBody{Code: codeInternal, Message: internalMessage, Details: map[string]any{}})
			}()
			next.ServeHTTP(seen, r)
		})
	}
}

// written remembers whether a response has begun.
type written struct {
	http.ResponseWriter
	done bool
}

func (w *written) WriteHeader(status int) {
	w.done = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *written) Write(b []byte) (int, error) {
	w.done = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection (DESIGN.md T12).
func (w *written) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type requestIDKey struct{}

// requestID returns the id RequestID gave the request, or "".
func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RequestID gives every request an id, a UUIDv7, in its context and in the
// X-Request-Id header of its response (DESIGN.md §8.1). The header is set
// at once, so that an answer written by anything has it, and again when the
// response begins: the generated code writes every header an operation
// declares, this one included, with whatever the handler left in it.
func RequestID(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.NewV7()
			if err != nil {
				WriteError(w, r, log, fmt.Errorf("making the id of a request: %w", err))
				return
			}
			w.Header().Set(requestIDHeader, id.String())
			ctx := context.WithValue(r.Context(), requestIDKey{}, id.String())
			next.ServeHTTP(&identified{ResponseWriter: w, id: id.String()}, r.WithContext(ctx))
		})
	}
}

// identified sets X-Request-Id last.
type identified struct {
	http.ResponseWriter
	id string
}

func (w *identified) WriteHeader(status int) {
	w.Header().Set(requestIDHeader, w.id)
	w.ResponseWriter.WriteHeader(status)
}

func (w *identified) Write(b []byte) (int, error) {
	// A Write without WriteHeader sends the headers as they are now.
	w.Header().Set(requestIDHeader, w.id)
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection (DESIGN.md T12).
func (w *identified) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// AccessLog writes one line per request (DESIGN.md §11.5): the id of the
// request, the method, the route, the status, the duration and the bytes of
// the body. The route is the pattern the router matched, never the path:
// neither the path nor the query string nor any header is logged (T28).
// The line is at INFO, and at DEBUG for the routes in quiet, which are
// asked too often to be worth a line each.
func AccessLog(log *slog.Logger, quiet []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			counted := &counted{ResponseWriter: w}
			returned := false
			defer func() {
				status := counted.status
				switch {
				case status != 0:
				case returned:
					// net/http sends 200 for a handler that wrote nothing.
					status = http.StatusOK
				default:
					// A panic before any response: Recover answers 500.
					status = http.StatusInternalServerError
				}
				level := slog.LevelInfo
				if slices.Contains(quiet, r.Pattern) {
					level = slog.LevelDebug
				}
				log.Log(r.Context(), level, "request", "request_id", requestID(r.Context()), "method", r.Method,
					"route", r.Pattern, "status", status, "duration_ms", time.Since(start).Milliseconds(),
					"bytes", counted.bytes)
			}()
			// The routers below write the pattern they match into this
			// same request.
			next.ServeHTTP(counted, r)
			returned = true
		})
	}
}

// counted remembers the status and the size of a response.
type counted struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *counted) WriteHeader(status int) {
	// An informational status (1xx) comes before the status of the response.
	if w.status == 0 && status >= http.StatusOK {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *counted) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the connection (DESIGN.md T12).
func (w *counted) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// SecurityHeaders sets the response headers of DESIGN.md §7.6 on every
// response: X-Content-Type-Options and Referrer-Policy always, and on a JSON
// response also Cache-Control: private, no-store and a Content-Security-
// Policy that allows nothing. No CORS header is ever set.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		next.ServeHTTP(&secured{ResponseWriter: w}, r)
	})
}

func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

func setJSONHeaders(h http.Header) {
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
}

// secured adds the headers of a JSON response when the response begins:
// only then is its Content-Type known.
type secured struct {
	http.ResponseWriter
	done bool
}

func (w *secured) begin() {
	if w.done {
		return
	}
	w.done = true
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		setJSONHeaders(w.Header())
	}
}

// begun tells WriteError that an error can no longer be answered.
func (w *secured) begun() bool { return w.done }

func (w *secured) WriteHeader(status int) {
	w.begin()
	w.ResponseWriter.WriteHeader(status)
}

func (w *secured) Write(b []byte) (int, error) {
	w.begin()
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection (DESIGN.md T12).
func (w *secured) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Boundary is the browser boundary of DESIGN.md §7.6 (I4), in this order:
//
//   - Host must be the host of the public origin (ASCII case-insensitive):
//     421 host_not_allowed otherwise, the defense against DNS rebinding;
//   - Origin, if present, must be the public origin exactly: "null",
//     another origin, an empty value or two Origin fields are
//     403 origin_not_allowed;
//   - a request other than GET and HEAD must carry X-Vibrance-Request: 1,
//     once: 403 request_header_required otherwise. A client that is not a
//     browser may omit Origin, not this header.
//
// The X-Forwarded-* headers are never read. publicOrigin is
// VIBRANCE_PUBLIC_ORIGIN, already validated: scheme://host[:port].
func Boundary(log *slog.Logger, publicOrigin string) (func(http.Handler) http.Handler, error) {
	u, err := url.Parse(publicOrigin)
	if err != nil || u.Host == "" || u.Scheme+"://"+u.Host != publicOrigin {
		// The value is not repeated: the configuration has refused it
		// already, with the reason.
		return nil, errors.New("the public origin is not scheme://host[:port]")
	}
	host := u.Host
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if e := checkBoundary(r, publicOrigin, host); e != nil {
				WriteError(w, r, log, e)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func checkBoundary(r *http.Request, origin, host string) *Error {
	if !strings.EqualFold(r.Host, host) {
		return &Error{Status: http.StatusMisdirectedRequest, Code: codeHostNotAllowed,
			Message: "This host is not the public origin of the server."}
	}
	if origins := r.Header.Values("Origin"); len(origins) > 1 || (len(origins) == 1 && origins[0] != origin) {
		return &Error{Status: http.StatusForbidden, Code: codeOriginNotAllowed,
			Message: "Requests from another origin are refused."}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if v := r.Header.Values(RequestHeader); len(v) != 1 || v[0] != "1" {
			return &Error{Status: http.StatusForbidden, Code: codeRequestHeaderRequired,
				Message: "The header " + RequestHeader + ": 1 is required."}
		}
	}
	return nil
}
