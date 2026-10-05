//go:build contract

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"vibrance/internal/api"
)

// How long the suite waits for Vibrance: to be ready after a start, and for
// a cycle of the scanner it asked for.
const (
	readyTimeout = 3 * time.Minute
	scanTimeout  = 5 * time.Minute
)

// vibrance is a client of Vibrance's API, signed in as the first admin with
// a session cookie. Every JSON answer is decoded strictly into the types
// generated from api/openapi.yaml: a key the specification does not have
// fails. The password is sent only in the body of the sign-in and never
// appears in an error.
type vibrance struct {
	base, username, password string
	client                   *http.Client
}

func newVibrance(t *testing.T, base, username, password string) *vibrance {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &vibrance{base: base, username: username, password: password, client: &http.Client{Jar: jar, Timeout: time.Minute}}
}

// vbError is an answer of Vibrance other than the one expected.
type vbError struct {
	method, path string
	status       int
	code         string
}

func (e *vbError) Error() string {
	return fmt.Sprintf("Vibrance: %s %s: %d %s", e.method, e.path, e.status, e.code)
}

// isError says whether err is an answer status with the error code code.
func isError(err error, status int, code string) bool {
	e := (*vbError)(nil)
	return errors.As(err, &e) && e.status == status && e.code == code
}

// send makes one request and reads the whole answer. A request other than
// GET and HEAD carries X-Vibrance-Request.
func (v *vibrance) send(ctx context.Context, method, path string, header http.Header, body any) (*http.Response, []byte, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.base+path, rd)
	if err != nil {
		return nil, nil, err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Vibrance-Request", "1")
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("Vibrance: %s %s: %w", method, path, err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, nil, fmt.Errorf("Vibrance: %s %s: reading the answer: %w", method, path, err)
	}
	return resp, data, nil
}

// failure is the error of an answer that is not the one expected: its
// status and the code of its error body.
func failure(method, path string, resp *http.Response, data []byte) error {
	var e api.Error
	if err := json.Unmarshal(data, &e); err != nil {
		e.Code = "(no error body)"
	}
	return &vbError{method: method, path: path, status: resp.StatusCode, code: string(e.Code)}
}

// strict decodes data into out, refusing unknown keys and trailing data.
func strict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("data after the JSON value")
	}
	return nil
}

// call sends a request that must answer want, and decodes its body into out
// (when not nil).
func (v *vibrance) call(ctx context.Context, method, path string, body any, want int, out any) error {
	resp, data, err := v.send(ctx, method, path, nil, body)
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return failure(method, path, resp, data)
	}
	if out != nil {
		if err := strict(data, out); err != nil {
			return fmt.Errorf("Vibrance: %s %s: the answer does not match the specification: %w", method, path, err)
		}
	}
	return nil
}

func (v *vibrance) get(t *testing.T, path string, out any) {
	t.Helper()
	if err := v.call(t.Context(), http.MethodGet, path, nil, http.StatusOK, out); err != nil {
		t.Fatal(err)
	}
}

// waitReady waits for /health/ready to answer 200.
func (v *vibrance) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	for {
		resp, _, err := v.send(t.Context(), http.MethodGet, "/health/ready", nil, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Vibrance is not ready after %s: %v", readyTimeout, err)
		}
		time.Sleep(pollEvery)
	}
}

// login signs in as the first admin. The answer, which holds the session,
// is decoded and dropped.
func (v *vibrance) login(t *testing.T) {
	t.Helper()
	var out api.LoginResult
	err := v.call(t.Context(), http.MethodPost, "/api/v1/auth/login",
		api.LoginRequest{Username: v.username, Password: v.password}, http.StatusOK, &out)
	if err != nil {
		t.Fatal(err)
	}
}

func (v *vibrance) status(t *testing.T) api.LibraryStatus {
	t.Helper()
	var st api.LibraryStatus
	v.get(t, "/api/v1/admin/library", &st)
	return st
}

// requestScan asks for a cycle of the scanner and does not wait for it.
func (v *vibrance) requestScan(t *testing.T) {
	t.Helper()
	var st api.LibraryStatus
	if err := v.call(t.Context(), http.MethodPost, "/api/v1/admin/library/scan", nil, http.StatusAccepted, &st); err != nil {
		t.Fatal(err)
	}
}

// scan asks for a cycle of the scanner and waits until a cycle that
// started after the request has ended: the cycle sees every change MusicLib
// published before. It returns the state the cycle left.
func (v *vibrance) scan(t *testing.T) api.LibraryStatus {
	t.Helper()
	asked := time.Now().UTC().Truncate(time.Millisecond)
	v.requestScan(t)
	deadline := time.Now().Add(scanTimeout)
	for {
		st := v.status(t)
		if st.State != api.LibraryStatusStateScanning && st.LastScan != nil && st.LastScan.FinishedAt != nil {
			started, err := time.Parse(time.RFC3339Nano, st.LastScan.StartedAt)
			if err != nil {
				t.Fatalf("Vibrance: last_scan.started_at %q: %v", st.LastScan.StartedAt, err)
			}
			if !started.Before(asked) {
				return st
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Vibrance: no cycle of the scanner ended in %s: %+v", scanTimeout, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// scanMaintenance asks for cycles of the scanner until the state is
// maintenance: such a cycle stops at its first step and records no scan.
func (v *vibrance) scanMaintenance(t *testing.T) api.LibraryStatus {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		v.requestScan(t)
		time.Sleep(500 * time.Millisecond)
		st := v.status(t)
		if st.State == api.LibraryStatusStateMaintenance {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("Vibrance: the state is not maintenance during MusicLib's rebuild: %+v", st)
		}
	}
}

// pages reads every page of a list: page decodes one page into its own
// value and returns its next cursor.
func (v *vibrance) pages(t *testing.T, path string, page func(data []byte) (*string, error)) {
	t.Helper()
	after := ""
	for {
		p := path + "?limit=200"
		if after != "" {
			p += "&after=" + url.QueryEscape(after)
		}
		resp, data, err := v.send(t.Context(), http.MethodGet, p, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatal(failure(http.MethodGet, p, resp, data))
		}
		next, err := page(data)
		if err != nil {
			t.Fatalf("Vibrance: GET %s: the answer does not match the specification: %v", p, err)
		}
		if next == nil {
			return
		}
		after = *next
	}
}

func (v *vibrance) albums(t *testing.T) []api.AlbumSummary {
	t.Helper()
	var all []api.AlbumSummary
	v.pages(t, "/api/v1/albums", func(data []byte) (*string, error) {
		var l api.AlbumList
		err := strict(data, &l)
		all = append(all, l.Albums...)
		return l.Next, err
	})
	return all
}

func (v *vibrance) artists(t *testing.T) []api.ArtistSummary {
	t.Helper()
	var all []api.ArtistSummary
	v.pages(t, "/api/v1/artists", func(data []byte) (*string, error) {
		var l api.ArtistList
		err := strict(data, &l)
		all = append(all, l.Artists...)
		return l.Next, err
	})
	return all
}

func (v *vibrance) favorites(t *testing.T) []api.Favorite {
	t.Helper()
	var all []api.Favorite
	v.pages(t, "/api/v1/me/favorites/tracks", func(data []byte) (*string, error) {
		var l api.FavoriteList
		err := strict(data, &l)
		all = append(all, l.Favorites...)
		return l.Next, err
	})
	return all
}

func (v *vibrance) items(t *testing.T, playlist string) []api.PlaylistItem {
	t.Helper()
	var all []api.PlaylistItem
	v.pages(t, "/api/v1/playlists/"+playlist+"/items", func(data []byte) (*string, error) {
		var l api.PlaylistItemList
		err := strict(data, &l)
		all = append(all, l.Items...)
		return l.Next, err
	})
	return all
}

// album is GET /albums/{id}; an answer other than 200 is the error.
func (v *vibrance) album(ctx context.Context, id string) (api.AlbumDetail, error) {
	var a api.AlbumDetail
	err := v.call(ctx, http.MethodGet, "/api/v1/albums/"+id, nil, http.StatusOK, &a)
	return a, err
}

func (v *vibrance) mustAlbum(t *testing.T, id string) api.AlbumDetail {
	t.Helper()
	a, err := v.album(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (v *vibrance) track(t *testing.T, id string) api.Track {
	t.Helper()
	var tr api.Track
	v.get(t, "/api/v1/tracks/"+id, &tr)
	return tr
}

func (v *vibrance) playlist(t *testing.T, id string) api.Playlist {
	t.Helper()
	var p api.Playlist
	v.get(t, "/api/v1/playlists/"+id, &p)
	return p
}

// wantError checks that a GET of path answers status with the code code.
func (v *vibrance) wantError(t *testing.T, path string, status int, code string) {
	t.Helper()
	err := v.call(t.Context(), http.MethodGet, path, nil, http.StatusOK, nil)
	if !isError(err, status, code) {
		t.Errorf("GET %s: want %d %s, got %v", path, status, code, err)
	}
}
