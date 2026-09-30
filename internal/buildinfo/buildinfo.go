// Package buildinfo holds the application version, its single source:
// `vibrance version` prints it (DESIGN.md §11.4).
package buildinfo

// Version is the application version. Release images set it at build time:
//
//	go build -ldflags "-X vibrance/internal/buildinfo.Version=0.1.0"
//
// through the Dockerfile's build argument VIBRANCE_VERSION, which the
// build-app stage checks is a non-empty token of [0-9A-Za-z.+-] and that the
// built binary reports it. Any other build, tests included, reports "devel".
var Version = "devel"
