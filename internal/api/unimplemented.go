package api

import "context"

// The operations no step has implemented yet. Each one answers
// 501 not_implemented; the step that implements an operation moves it from
// here to the file of its area, and DESIGN.md S20 removes this file.

// GetLibraryStatus waits for step S20.
func (Server) GetLibraryStatus(context.Context, GetLibraryStatusRequestObject) (GetLibraryStatusResponseObject, error) {
	return nil, errNotImplemented
}

// ScanLibrary waits for step S20.
func (Server) ScanLibrary(context.Context, ScanLibraryRequestObject) (ScanLibraryResponseObject, error) {
	return nil, errNotImplemented
}
