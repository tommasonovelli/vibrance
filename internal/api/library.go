package api

import (
	"context"

	"vibrance/internal/library"
)

// The operations of the state of the library (DESIGN.md §8.7), for admins.
// What they say is the scanner's; these only translate. No other operation
// changes the scanner.

// GetLibraryStatus says what the scanner is doing, how its last cycle went
// and which album folders it could not index (§6.5).
func (s Server) GetLibraryStatus(context.Context, GetLibraryStatusRequestObject) (GetLibraryStatusResponseObject, error) {
	return GetLibraryStatus200JSONResponse{Body: libraryStatusOf(s.scanner.Load().Status())}, nil
}

// ScanLibrary asks the scanner for a cycle and answers at once, with the
// state as it is. The requests coalesce in the scanner: one cycle runs and
// at most one waits, however many are asked for (§6.1).
func (s Server) ScanLibrary(context.Context, ScanLibraryRequestObject) (ScanLibraryResponseObject, error) {
	scanner := s.scanner.Load()
	scanner.Trigger(library.ReasonRequest)
	return ScanLibrary202JSONResponse{Body: libraryStatusOf(scanner.Status())}, nil
}

// libraryStatusOf is the status of the scanner as the API gives it.
func libraryStatusOf(st library.Status) LibraryStatus {
	body := LibraryStatus{
		State:               LibraryStatusState(st.State),
		Albums:              LibraryCounts{Available: st.Albums.Available, Unavailable: st.Albums.Unavailable},
		Tracks:              LibraryCounts{Available: st.Tracks.Available, Unavailable: st.Tracks.Unavailable},
		MusiclibMaintenance: st.Maintenance,
		Problems:            make([]LibraryProblem, 0, len(st.Problems)),
	}
	if scan := st.LastScan; scan != nil {
		body.LastScan = &LibraryScan{StartedAt: timestamp(scan.StartedAt), Ok: scan.OK}
		if scan.FinishedAt != nil {
			finished := timestamp(*scan.FinishedAt)
			body.LastScan.FinishedAt = &finished
		}
		if scan.Error != "" {
			body.LastScan.Error = &scan.Error
		}
	}
	if p := st.Progress; p != nil {
		body.Progress = &LibraryProgress{Discovered: p.Discovered, Pending: p.Pending, Indexed: p.Indexed}
	}
	for _, p := range st.Problems {
		body.Problems = append(body.Problems, LibraryProblem{RelPath: p.RelPath, Code: p.Code, Message: p.Message})
	}
	return body
}
