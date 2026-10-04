package httpx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

// MaxBodyBytes is the largest request body accepted (DESIGN.md §8.1: 1 MiB);
// one byte more is 413 body_too_large.
const MaxBodyBytes = 1 << 20

// Contract checks requests against the OpenAPI specification.
type Contract struct {
	log *slog.Logger
	// operations are the operations of the specification by the pattern the
	// router of net/http serves them at ("GET /api/v1/tracks/{id}").
	operations map[string]operation
}

type operation struct {
	route *routers.Route
	// params are the names of the path parameters.
	params []string
}

var pathParam = regexp.MustCompile(`\{([^{}]+)\}`)

// NewContract reads the operations of doc, served under basePath by a router
// of net/http with the patterns "METHOD basePath/path", which are those of
// the generated code.
//
// The operation of a request is the one whose pattern the router matched:
// there is no second router that could match otherwise, and the `servers`
// of the specification are never compared with the request (DESIGN.md T23).
// A HEAD request is matched by the pattern of its GET, and is checked as
// that GET.
func NewContract(doc *openapi3.T, basePath string, log *slog.Logger) *Contract {
	c := &Contract{log: log, operations: map[string]operation{}}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			var params []string
			for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
				params = append(params, m[1])
			}
			c.operations[method+" "+basePath+path] = operation{
				route:  &routers.Route{Spec: doc, Path: path, PathItem: item, Method: method, Operation: op},
				params: params,
			}
		}
	}
	return c
}

// Check is the middleware of every operation, after the router has matched
// it. In this order:
//
//  1. The body limit: a body over MaxBodyBytes is 413 body_too_large.
//  2. Strict JSON (CheckJSON): 400 invalid_request.
//  3. The request against the specification, by kin-openapi: parameters,
//     enums, limits, the Content-Type and the schema of the body, whose
//     unknown keys every request schema refuses: 400 invalid_request. The
//     security requirements are not checked here: authentication is a
//     middleware of its own.
//
// Steps 1 and 2 are for the operations that take a body. The body of any
// other operation is neither read nor looked at.
//
// The 400 says which parameter, or that it is the body, and nothing more:
// the reasons of the validator repeat the values sent, a password among
// them, so they are neither answered nor logged (I5).
func (c *Contract) Check(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, ok := c.operations[r.Pattern]
		if !ok {
			// A route was registered that the specification does not have:
			// refuse rather than serve something unchecked.
			WriteError(w, r, c.log, fmt.Errorf("no operation of the specification for the route %q", r.Pattern))
			return
		}
		if op.route.Operation.RequestBody != nil {
			if err := readBody(w, r); err != nil {
				WriteError(w, r, c.log, err)
				return
			}
		}
		if err := c.validate(r, op); err != nil {
			WriteError(w, r, c.log, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// readBody reads the whole body, at most MaxBodyBytes, checks it with
// CheckJSON and puts it back for the validator and for the handler. An empty
// body is left to the validator, which knows that one is required.
func readBody(w http.ResponseWriter, r *http.Request) error {
	// A length declared over the limit is not refused before reading: a
	// client that is one byte over would find its connection reset, with
	// its body half sent, instead of the 413. At most the limit is read.
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		return &Error{Status: http.StatusRequestEntityTooLarge, Code: codeBodyTooLarge,
			Message: "The request body is larger than 1 MiB."}
	case err != nil:
		return invalidBody("The request body could not be read.")
	}
	if len(b) > 0 {
		if err := CheckJSON(b); err != nil {
			return err
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return nil
}

// validate checks the parameters and the body of r against the operation.
//
// It runs the steps of openapi3filter.ValidateRequest but the one on the
// security requirements: that step reads the whole body of the request into
// memory, without a limit, before it calls an authentication function, even
// one that does nothing. Authentication is a middleware of its own.
func (c *Contract) validate(r *http.Request, op operation) error {
	params := make(map[string]string, len(op.params))
	for _, name := range op.params {
		params[name] = r.PathValue(name)
	}
	input := &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: params,
		Route:      op.route,
		Options: &openapi3filter.Options{
			// The validator only checks: the handler sees the request as
			// the client sent it.
			SkipSettingDefaults: true,
		},
	}
	// The parameters of the path item, unless the operation declares them
	// again, then those of the operation.
	for _, p := range op.route.PathItem.Parameters {
		if op.route.Operation.Parameters.GetByInAndName(p.Value.In, p.Value.Name) != nil {
			continue
		}
		if err := openapi3filter.ValidateParameter(r.Context(), input, p.Value); err != nil {
			return refusal(op, err)
		}
	}
	for _, p := range op.route.Operation.Parameters {
		if err := openapi3filter.ValidateParameter(r.Context(), input, p.Value); err != nil {
			return refusal(op, err)
		}
	}
	if body := op.route.Operation.RequestBody; body != nil {
		if err := openapi3filter.ValidateRequestBody(r.Context(), input, body.Value); err != nil {
			return refusal(op, err)
		}
	}
	return nil
}

// refusal turns an error of the validator into the 400 of the API.
func refusal(op operation, err error) error {
	var re *openapi3filter.RequestError
	switch {
	case !errors.As(err, &re):
		// Not a refusal of the request: the validator could not do its work.
		// Only the type is kept, because its text may repeat the request.
		return fmt.Errorf("validating a request of %s: an error of type %T", op.route.Operation.OperationID, err)
	case re.Parameter != nil:
		return InvalidParameter(re.Parameter.Name)
	default:
		return InvalidBody()
	}
}
