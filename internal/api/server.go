package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/getkin/kin-openapi/openapi3"

	apispec "vibrance/api"
	"vibrance/internal/auth"
	"vibrance/internal/httpx"
)

// BasePath is where the operations of the specification are served: the
// `servers` entry of api/openapi.yaml.
const BasePath = "/api/v1"

// errNotImplemented is what an operation returns until its step implements
// it. Its code is not in the specification, because no released server
// answers it: it leaves with the last unimplemented operation.
var errNotImplemented = &httpx.Error{Status: http.StatusNotImplemented, Code: "not_implemented",
	Message: "This operation is not implemented yet."}

// Server implements every operation of the specification.
type Server struct {
	// accounts is the service of the accounts and of their sessions. The
	// server publishes it once its startup is complete; until then the
	// authentication answers 503 and no operation runs.
	accounts *atomic.Pointer[auth.Service]
	// secureCookie says that the public origin is https: the session cookie
	// is Secure then, and only then (DESIGN.md §7.3, T14).
	secureCookie bool
}

// NewServer returns the operations of a server whose public origin is
// publicOrigin (VIBRANCE_PUBLIC_ORIGIN), on the service of the accounts
// that accounts holds once the startup has published it.
func NewServer(accounts *atomic.Pointer[auth.Service], publicOrigin string) Server {
	return Server{accounts: accounts, secureCookie: strings.HasPrefix(publicOrigin, "https://")}
}

// The compiler checks that no operation of the specification is missing.
var _ StrictServerInterface = Server{}

// QuietRoutes are the routes of the audio and of the covers, as the router
// names them: a player asks them all the time, so their access log is at
// DEBUG (DESIGN.md §11.5).
func QuietRoutes() []string {
	return []string{
		http.MethodGet + " " + BasePath + "/tracks/{id}/audio",
		http.MethodGet + " " + BasePath + "/albums/{id}/cover",
	}
}

// LoadSpec parses the specification the binary carries.
func LoadSpec() (*openapi3.T, error) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(apispec.YAML))
	if err != nil {
		return nil, fmt.Errorf("parsing the OpenAPI specification: %w", err)
	}
	return doc, nil
}

// Access says what each operation of doc asks of a request (DESIGN.md §8.3),
// by the pattern the router serves it at. An operation is for admins when
// its path is under /admin/, whatever else the specification says of it;
// public when the specification gives it no security requirement
// (`security: []`); and for any signed-in user otherwise.
func Access(doc *openapi3.T) map[string]auth.Access {
	access := map[string]auth.Access{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			needs := auth.Authenticated
			switch {
			case strings.HasPrefix(path, "/admin/"):
				needs = auth.AdminOnly
			case op.Security != nil && len(*op.Security) == 0:
				needs = auth.Public
			}
			access[method+" "+BasePath+path] = needs
		}
	}
	return access
}

// Register routes the operations of doc, under BasePath, to srv on mux.
//
// Every operation is behind httpx.Contract: the body limit, strict JSON and
// the validation of the request against doc. Then comes authenticate, the
// authentication of the request (auth.Service.Middleware): a request that
// is not well formed is refused before anyone asks who sent it. A request
// the generated code cannot bind (a path id that is not a UUID, a parameter
// of the wrong type or sent twice) answers 400 invalid_request before
// both. A path of the specification asked with a method it does not have
// answers 405 method_not_allowed, with Allow. Every other path is left to
// mux.
func Register(mux *http.ServeMux, doc *openapi3.T, srv StrictServerInterface, authenticate MiddlewareFunc, log *slog.Logger) {
	e := errorWriter{log: log}
	strict := NewStrictHandlerWithOptions(srv, []StrictMiddlewareFunc{withRemoteAddr}, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  e.badBody,
		ResponseErrorHandlerFunc: e.failed,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:    BasePath,
		BaseRouter: mux,
		// The generated code wraps the handler with each middleware in turn:
		// the last of the list runs first.
		Middlewares:      []MiddlewareFunc{authenticate, httpx.NewContract(doc, BasePath, log).Check},
		ErrorHandlerFunc: e.badParameter,
	})
	for path, item := range doc.Paths.Map() {
		mux.Handle(BasePath+path, httpx.MethodNotAllowed(log, allowed(item)))
	}
}

type remoteAddrKey struct{}

// withRemoteAddr gives an operation the address its request came from, for
// the log of a refused sign-in: the strict operations see only their
// context. It is the address of the connection; X-Forwarded-For is never
// read (§7.6).
func withRemoteAddr(f StrictHandlerFunc, _ string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		return f(context.WithValue(ctx, remoteAddrKey{}, r.RemoteAddr), w, r, request)
	}
}

// remoteAddr is the address withRemoteAddr put in ctx, or "".
func remoteAddr(ctx context.Context) string {
	addr, _ := ctx.Value(remoteAddrKey{}).(string)
	return addr
}

// allowed is the Allow header of a path: its methods, in a fixed order. The
// router of net/http answers HEAD wherever it answers GET.
func allowed(item *openapi3.PathItem) string {
	var methods []string
	for method := range item.Operations() {
		methods = append(methods, method)
		if method == http.MethodGet {
			methods = append(methods, http.MethodHead)
		}
	}
	slices.Sort(methods)
	return strings.Join(methods, ", ")
}

// errorWriter answers, in the error model of the API, what the generated
// code hands over.
type errorWriter struct {
	log *slog.Logger
}

// badParameter answers a parameter the generated code could not bind. Only
// its name is said, which is the one of the specification: the reason
// repeats the value the client sent.
func (e errorWriter) badParameter(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, e.log, httpx.InvalidParameter(parameterName(err)))
}

// parameterName is the name of the parameter an error of the generated
// binding is about.
func parameterName(err error) string {
	var (
		format      *InvalidParamFormatError
		required    *RequiredParamError
		header      *RequiredHeaderError
		tooMany     *TooManyValuesForParamError
		unmarshal   *UnmarshalingParamError
		cookieParam *UnescapedCookieParamError
	)
	switch {
	case errors.As(err, &format):
		return format.ParamName
	case errors.As(err, &required):
		return required.ParamName
	case errors.As(err, &header):
		return header.ParamName
	case errors.As(err, &tooMany):
		return tooMany.ParamName
	case errors.As(err, &unmarshal):
		return unmarshal.ParamName
	case errors.As(err, &cookieParam):
		return cookieParam.ParamName
	default:
		return ""
	}
}

// badBody answers a body the generated code could not decode into the type
// of the operation, after the validator accepted it: a number that does not
// fit, for one.
func (e errorWriter) badBody(w http.ResponseWriter, r *http.Request, _ error) {
	httpx.WriteError(w, r, e.log, httpx.InvalidBody())
}

// failed answers the error of an operation: an *httpx.Error as it is, and
// anything else as 500 internal, with the cause in the log and never in the
// response (DESIGN.md §8.1).
func (e errorWriter) failed(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, e.log, err)
}
