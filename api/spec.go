// Package apispec holds the OpenAPI specification of the API, the file
// openapi.yaml of this folder, inside the binary: the server validates the
// requests against these bytes, and they are the bytes it serves.
package apispec

import _ "embed"

// YAML is api/openapi.yaml as it was when the binary was built.
//
//go:embed openapi.yaml
var YAML string
