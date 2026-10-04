// Package httpx holds the HTTP boundary: middleware, the error model,
// strict JSON decoding and cursors (DESIGN.md §8).
//
// A request crosses the boundary in this order (DESIGN.md §14, step S12):
//
//	Recover          a panic answers 500 internal, with nothing of its cause
//	RequestID        X-Request-Id on every response, errors included
//	AccessLog        one log line per request, without path, query or secrets
//	SecurityHeaders  nosniff, no referrer; no-store and a closed CSP on JSON
//	Boundary         Host (421), Origin (403), X-Vibrance-Request (403)
//	Contract.Check   body limit (413), strict JSON (400), the request
//	                 against the OpenAPI specification (400)
//
// The package knows nothing of the operations of the API: the specification
// is given to it.
package httpx
