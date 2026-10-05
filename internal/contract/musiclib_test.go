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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// How long the suite waits for MusicLib's work in the background: an import,
// the renders that follow a change.
const (
	importTimeout = 5 * time.Minute
	settleTimeout = 5 * time.Minute
	pollEvery     = 300 * time.Millisecond
)

// musiclib is a minimal client of MusicLib's documented API (MusicLib's
// docs/operations.md, "The API"): the sign-in, the import, the changes of
// albums and artists, the trash, the render of everything and the move of
// tracks. The password is sent only in the body of POST /login and never
// appears in an error.
type musiclib struct {
	base, password string
	client         *http.Client
}

func newMusiclib(t *testing.T, base, password string) *musiclib {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &musiclib{base: base, password: password, client: &http.Client{
		Jar:     jar,
		Timeout: time.Minute,
		// The sign-in answers 303: the redirect is not followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// mlError is an answer of MusicLib other than 2xx.
type mlError struct {
	method, path string
	status       int
	body         string
}

func (e *mlError) Error() string {
	return fmt.Sprintf("MusicLib: %s %s: %d %s", e.method, e.path, e.status, e.body)
}

// rawBody is a request body that is not JSON: an upload.
type rawBody []byte

// signIn is POST /login, a form with the password.
func (m *musiclib) signIn(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.base+"/login",
		strings.NewReader(url.Values{"password": {m.password}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("MusicLib: POST /login: %w", err)
	}
	if err := drain(resp); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusSeeOther {
		return fmt.Errorf("MusicLib: POST /login answers %d, not 303", resp.StatusCode)
	}
	return nil
}

func drain(resp *http.Response) error {
	_, err := io.Copy(io.Discard, resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("reading a response: %w", err)
	}
	return nil
}

// call sends one request of the API and decodes a 2xx answer into out (when
// not nil). A restart of MusicLib ends its sessions: an answer 401 signs in
// again once and repeats the request, which MusicLib did not carry out.
func (m *musiclib) call(ctx context.Context, method, path, ifMatch string, body, out any) error {
	err := m.once(ctx, method, path, ifMatch, body, out)
	if e := (*mlError)(nil); errors.As(err, &e) && e.status == http.StatusUnauthorized {
		if err := m.signIn(ctx); err != nil {
			return err
		}
		return m.once(ctx, method, path, ifMatch, body, out)
	}
	return err
}

func (m *musiclib) once(ctx context.Context, method, path, ifMatch string, body, out any) error {
	var rd io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case rawBody:
		rd, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return fmt.Errorf("MusicLib: %s %s: %w", method, path, err)
		}
		rd, contentType = bytes.NewReader(data), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, m.base+path, rd)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet {
		req.Header.Set("X-Musiclib-Request", "1")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("MusicLib: %s %s: %w", method, path, err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("MusicLib: %s %s: reading the answer: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &mlError{method: method, path: path, status: resp.StatusCode, body: string(bytes.TrimSpace(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("MusicLib: %s %s: decoding the answer: %w", method, path, err)
		}
	}
	return nil
}

// The API's shapes, as far as the suite reads them.
type mlAlbum struct {
	ID          string    `json:"id"`
	ETag        string    `json:"etag"`
	ArtistID    string    `json:"artist_id"`
	ArtistName  string    `json:"artist_name"`
	Title       string    `json:"title"`
	Year        *int      `json:"year"`
	Genre       *string   `json:"genre"`
	Compilation bool      `json:"compilation"`
	Trashed     bool      `json:"trashed"`
	Tracks      []mlTrack `json:"tracks"`
}

type mlTrack struct {
	ID     string  `json:"id"`
	Disc   int     `json:"disc"`
	No     int     `json:"no"`
	Title  string  `json:"title"`
	Artist *string `json:"artist"`
	Genre  *string `json:"genre"`
}

// artistOf is the artist of track tr of album a: its own, or the album's.
func artistOf(a mlAlbum, tr mlTrack) string {
	if tr.Artist != nil {
		return *tr.Artist
	}
	return a.ArtistName
}

// track is the track of a at disc and no.
func (a mlAlbum) track(t *testing.T, disc, no int) mlTrack {
	t.Helper()
	for _, tr := range a.Tracks {
		if tr.Disc == disc && tr.No == no {
			return tr
		}
	}
	t.Fatalf("MusicLib: album %q has no track %d-%d", a.Title, disc, no)
	return mlTrack{}
}

// trackByID is the track of a with the MusicLib id id.
func (a mlAlbum) trackByID(t *testing.T, id string) mlTrack {
	t.Helper()
	for _, tr := range a.Tracks {
		if tr.ID == id {
			return tr
		}
	}
	t.Fatalf("MusicLib: album %q has no track %s", a.Title, id)
	return mlTrack{}
}

type mlStatus struct {
	Revision          int64 `json:"revision"`
	Trashed           bool  `json:"trashed"`
	PublishedRevision int64 `json:"published_revision"`
	Job               *struct {
		State        string  `json:"state"`
		ErrorCode    *string `json:"error_code"`
		ErrorMessage *string `json:"error_message"`
	} `json:"job"`
}

// albumUpdate is the body of PUT /api/albums/{id}: every key present.
type albumUpdate struct {
	ArtistID    *string       `json:"artist_id"`
	NewArtist   *string       `json:"new_artist"`
	Title       string        `json:"title"`
	Year        *int          `json:"year"`
	Genre       *string       `json:"genre"`
	Compilation bool          `json:"compilation"`
	Tracks      []trackUpdate `json:"tracks"`
}

type trackUpdate struct {
	ID     string  `json:"id"`
	Disc   int     `json:"disc"`
	No     int     `json:"no"`
	Title  string  `json:"title"`
	Artist *string `json:"artist"`
	Genre  *string `json:"genre"`
}

func (m *musiclib) login(t *testing.T) {
	t.Helper()
	if err := m.signIn(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (m *musiclib) album(t *testing.T, id string) mlAlbum {
	t.Helper()
	var a mlAlbum
	if err := m.call(t.Context(), http.MethodGet, "/api/albums/"+id, "", nil, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// albums lists the albums of the library, or of the trash, every page, each
// read whole.
func (m *musiclib) albums(t *testing.T, trash bool) []mlAlbum {
	t.Helper()
	var all []mlAlbum
	after := ""
	for {
		var page struct {
			Albums []mlAlbum `json:"albums"`
			Next   *string   `json:"next"`
		}
		q := fmt.Sprintf("/api/albums?limit=200&trash=%t", trash)
		if after != "" {
			q += "&after=" + url.QueryEscape(after)
		}
		if err := m.call(t.Context(), http.MethodGet, q, "", nil, &page); err != nil {
			t.Fatal(err)
		}
		for _, a := range page.Albums {
			all = append(all, m.album(t, a.ID))
		}
		if page.Next == nil {
			return all
		}
		after = *page.Next
	}
}

// edit saves album a with the changes change makes to its current form.
func (m *musiclib) edit(t *testing.T, a mlAlbum, change func(*albumUpdate)) {
	t.Helper()
	artist := a.ArtistID
	u := albumUpdate{ArtistID: &artist, Title: a.Title, Year: a.Year, Genre: a.Genre, Compilation: a.Compilation}
	for _, tr := range a.Tracks {
		u.Tracks = append(u.Tracks, trackUpdate{ID: tr.ID, Disc: tr.Disc, No: tr.No, Title: tr.Title, Artist: tr.Artist, Genre: tr.Genre})
	}
	change(&u)
	if err := m.call(t.Context(), http.MethodPut, "/api/albums/"+a.ID, a.ETag, u, nil); err != nil {
		t.Fatal(err)
	}
}

// renameArtist gives the artist id the name name.
func (m *musiclib) renameArtist(t *testing.T, id, name string) {
	t.Helper()
	var ar struct {
		ETag string `json:"etag"`
	}
	if err := m.call(t.Context(), http.MethodGet, "/api/artists/"+id, "", nil, &ar); err != nil {
		t.Fatal(err)
	}
	if err := m.call(t.Context(), http.MethodPut, "/api/artists/"+id, ar.ETag, map[string]string{"name": name}, nil); err != nil {
		t.Fatal(err)
	}
}

// do sends a change that needs the album's If-Match and has no answer the
// suite reads.
func (m *musiclib) do(t *testing.T, method, path string, a mlAlbum, body any) {
	t.Helper()
	if err := m.call(t.Context(), method, path, a.ETag, body, nil); err != nil {
		t.Fatal(err)
	}
}

// moveTracks moves the tracks ids of album from to the album to.
func (m *musiclib) moveTracks(t *testing.T, from mlAlbum, ids []string, to string) {
	t.Helper()
	m.do(t, http.MethodPost, "/api/albums/"+from.ID+"/move-tracks", from, map[string]any{"tracks": ids, "to": to})
}

// importFolder imports one folder of the import folder and returns the id
// of the album it made.
func (m *musiclib) importFolder(t *testing.T, folder string) string {
	t.Helper()
	type job struct {
		State         string  `json:"state"`
		ResultAlbumID *string `json:"result_album_id"`
		ErrorCode     *string `json:"error_code"`
	}
	var rep struct {
		State      string `json:"state"`
		Scan       job    `json:"scan"`
		Candidates []job  `json:"candidates"`
	}
	id := uuid.NewString()
	if err := m.call(t.Context(), http.MethodPost, "/api/imports", "", map[string]string{"id": id, "path": folder}, &rep); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(importTimeout)
	for rep.State != "completed" {
		if time.Now().After(deadline) {
			t.Fatalf("MusicLib: the import of %s did not complete in %s: %+v", folder, importTimeout, rep)
		}
		time.Sleep(pollEvery)
		if err := m.call(t.Context(), http.MethodGet, "/api/imports/"+id, "", nil, &rep); err != nil {
			t.Fatal(err)
		}
	}
	if len(rep.Candidates) != 1 || rep.Candidates[0].State != "done" || rep.Candidates[0].ResultAlbumID == nil {
		t.Fatalf("MusicLib: the import of %s did not make one album: %+v", folder, rep)
	}
	return *rep.Candidates[0].ResultAlbumID
}

// settle waits until MusicLib has written every change into library/: no
// job left; every album of the library published at its revision and on
// disk once, with that revision; every album of the trash, and every album
// deleted, out of library/. An album of rendered must also be on disk with
// a build other than the one rendered gives: a render that keeps the
// revision.
func (m *musiclib) settle(t *testing.T, library string, rendered map[string]string) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for {
		why := m.unsettled(t, library, rendered)
		if why == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MusicLib did not settle in %s: %s", settleTimeout, why)
		}
		time.Sleep(pollEvery)
	}
}

// unsettled says what MusicLib has still to write, or "".
func (m *musiclib) unsettled(t *testing.T, library string, rendered map[string]string) string {
	t.Helper()
	disk, err := readLibrary(library)
	if err != nil {
		return "library/ is changing: " + err.Error()
	}
	check := func(a mlAlbum, trashed bool) string {
		var st mlStatus
		if err := m.call(t.Context(), http.MethodGet, "/api/albums/"+a.ID+"/status", "", nil, &st); err != nil {
			t.Fatal(err)
		}
		if j := st.Job; j != nil {
			if j.State == "failed" {
				t.Fatalf("MusicLib: a job of album %q failed: %s %s", a.Title, deref(j.ErrorCode), deref(j.ErrorMessage))
			}
			return fmt.Sprintf("album %q has a job %s", a.Title, j.State)
		}
		folders := disk[a.ID]
		delete(disk, a.ID)
		if trashed {
			if len(folders) != 0 {
				return fmt.Sprintf("album %q is in the trash and still in library/", a.Title)
			}
			return ""
		}
		switch {
		case st.Trashed || st.PublishedRevision != st.Revision:
			return fmt.Sprintf("album %q is at revision %d, published %d", a.Title, st.Revision, st.PublishedRevision)
		case len(folders) != 1 || folders[0].Revision != st.Revision:
			return fmt.Sprintf("album %q is in library/ as %+v, not once at revision %d", a.Title, folders, st.Revision)
		case rendered[a.ID] != "" && folders[0].BuildID == rendered[a.ID]:
			return fmt.Sprintf("album %q is not rendered again yet", a.Title)
		}
		return ""
	}
	for _, trashed := range []bool{false, true} {
		for _, a := range m.albums(t, trashed) {
			if why := check(a, trashed); why != "" {
				return why
			}
		}
	}
	if len(disk) != 0 {
		return fmt.Sprintf("library/ has albums MusicLib does not list: %v", disk)
	}
	return ""
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// diskAlbum is an album folder of library/, as its receipt describes it.
type diskAlbum struct {
	Rel      string
	Revision int64
	BuildID  string
}

// readLibrary reads the receipt of every album folder of library/ and maps
// each album_id to its folders. An error is a library/ that is changing
// under the reader, or that is not there.
func readLibrary(root string) (map[string][]diskAlbum, error) {
	out := make(map[string][]diskAlbum)
	artists, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, ar := range artists {
		albums, err := os.ReadDir(filepath.Join(root, ar.Name()))
		if err != nil {
			return nil, err
		}
		for _, al := range albums {
			rel := ar.Name() + "/" + al.Name()
			data, err := os.ReadFile(filepath.Join(root, ar.Name(), al.Name(), ".musiclib.json"))
			if err != nil {
				return nil, err
			}
			var r struct {
				AlbumID       string `json:"album_id"`
				BuildID       string `json:"build_id"`
				AlbumRevision int64  `json:"album_revision"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return nil, fmt.Errorf("the receipt of %s: %w", rel, err)
			}
			out[r.AlbumID] = append(out[r.AlbumID], diskAlbum{Rel: rel, Revision: r.AlbumRevision, BuildID: r.BuildID})
		}
	}
	return out, nil
}

// builds is the build of every album folder of library/.
func builds(t *testing.T, library string) map[string]string {
	t.Helper()
	disk, err := readLibrary(library)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(disk))
	for id, folders := range disk {
		if len(folders) != 1 {
			t.Fatalf("album %s is in library/ %d times", id, len(folders))
		}
		out[id] = folders[0].BuildID
	}
	return out
}
