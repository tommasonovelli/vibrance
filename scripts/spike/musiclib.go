package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

// client talks to MusicLib's documented API (docs/operations.md of
// MusicLib, "The API"), signed in with the password of the spike's .env.
type client struct {
	base string
	http *http.Client
}

// apiTimeout bounds one request; waitTimeout bounds a wait for MusicLib's
// background work (an import, a render).
const (
	apiTimeout  = 30 * time.Second
	waitTimeout = 5 * time.Minute
	pollEvery   = 200 * time.Millisecond
)

// login signs in with POST /login and keeps the session cookie.
func login(ctx context.Context) (*client, error) {
	base, password := os.Getenv("MUSICLIB_URL"), os.Getenv("MUSICLIB_PASSWORD")
	if base == "" || password == "" {
		return nil, errors.New("MUSICLIB_URL and MUSICLIB_PASSWORD must be set (scripts/spike/compose.yaml)")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	c := &client{base: base, http: &http.Client{
		Jar:     jar,
		Timeout: apiTimeout,
		// The sign-in answers 303; the redirect is not followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/login",
		strings.NewReader(url.Values{"password": {password}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /login: %w", err)
	}
	if err := drain(resp); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSeeOther {
		return nil, fmt.Errorf("POST /login: status %d, want 303", resp.StatusCode)
	}
	return c, nil
}

func drain(resp *http.Response) error {
	_, err := io.Copy(io.Discard, resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("reading the response: %w", err)
	}
	return nil
}

// call sends one API request. body is JSON (a value), raw bytes with their
// content type, or nil; out, when not nil, receives the JSON answer of a 2xx
// status. Any other status is an error with MusicLib's error body.
func (c *client) call(ctx context.Context, method, p string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case rawBody:
		rd, contentType = bytes.NewReader(b.data), b.contentType
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			return fmt.Errorf("%s %s: encoding the body: %w", method, p, err)
		}
		rd, contentType = bytes.NewReader(enc), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+p, rd)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet {
		req.Header.Set("X-Musiclib-Request", "1")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, p, err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%s %s: reading the response: %w", method, p, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s %s: status %d: %s", method, p, resp.StatusCode, bytes.TrimSpace(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decoding the response: %w", method, p, err)
		}
	}
	return nil
}

// rawBody is a request body that is not JSON (an upload).
type rawBody struct {
	data        []byte
	contentType string
}

// The API's shapes, as far as the spike reads them.
type album struct {
	ID          string  `json:"id"`
	Revision    int64   `json:"revision"`
	ETag        string  `json:"etag"`
	ArtistID    string  `json:"artist_id"`
	ArtistName  string  `json:"artist_name"`
	Title       string  `json:"title"`
	Year        *int    `json:"year"`
	Genre       *string `json:"genre"`
	Compilation bool    `json:"compilation"`
	Trashed     bool    `json:"trashed"`
	Tracks      []track `json:"tracks"`
}

type track struct {
	ID         string  `json:"id"`
	Disc       int     `json:"disc"`
	No         int     `json:"no"`
	Title      string  `json:"title"`
	Artist     *string `json:"artist"`
	Genre      *string `json:"genre"`
	DurationMS *int64  `json:"duration_ms"`
}

type artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	ETag string `json:"etag"`
}

type albumStatus struct {
	Revision          int64  `json:"revision"`
	Trashed           bool   `json:"trashed"`
	PublishedRevision int64  `json:"published_revision"`
	Renderer          string `json:"renderer"`
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

// update is the PUT body that keeps the album as it is.
func (a album) update() albumUpdate {
	id := a.ArtistID
	u := albumUpdate{ArtistID: &id, Title: a.Title, Year: a.Year, Genre: a.Genre, Compilation: a.Compilation}
	for _, t := range a.Tracks {
		u.Tracks = append(u.Tracks, trackUpdate{ID: t.ID, Disc: t.Disc, No: t.No, Title: t.Title, Artist: t.Artist, Genre: t.Genre})
	}
	return u
}

func (c *client) album(ctx context.Context, id string) (album, error) {
	var a album
	err := c.call(ctx, http.MethodGet, "/api/albums/"+id, nil, nil, &a)
	return a, err
}

func (c *client) putAlbum(ctx context.Context, a album, u albumUpdate) error {
	return c.call(ctx, http.MethodPut, "/api/albums/"+a.ID, map[string]string{"If-Match": a.ETag}, u, nil)
}

// albums lists the active albums (not in the trash), every page.
func (c *client) albums(ctx context.Context) ([]album, error) {
	var all []album
	after := ""
	for {
		var page struct {
			Albums []album `json:"albums"`
			Next   *string `json:"next"`
		}
		q := "/api/albums?limit=100"
		if after != "" {
			q += "&after=" + url.QueryEscape(after)
		}
		if err := c.call(ctx, http.MethodGet, q, nil, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Albums...)
		if page.Next == nil {
			return all, nil
		}
		after = *page.Next
	}
}

// published is an album as it is on disk after its render.
type published struct {
	Album album
	Dir   albumDir
}

// waitPublished waits until MusicLib has published the current revision of
// album id: no render job left, the published revision is the revision, and
// a folder with that album_id, that revision and a build_id different from
// prevBuild is on disk. A failed render job is an error.
func (c *client) waitPublished(ctx context.Context, id, prevBuild string) (published, error) {
	deadline := time.Now().Add(waitTimeout)
	for {
		var st albumStatus
		if err := c.call(ctx, http.MethodGet, "/api/albums/"+id+"/status", nil, nil, &st); err != nil {
			return published{}, err
		}
		if j := st.Job; j != nil && j.State == "failed" {
			return published{}, fmt.Errorf("album %s: the render failed: %s: %s", id, deref(j.ErrorCode), deref(j.ErrorMessage))
		}
		if st.Job == nil && !st.Trashed && st.PublishedRevision == st.Revision {
			dirs, err := findAlbum(id)
			if err != nil {
				return published{}, err
			}
			if len(dirs) == 1 && dirs[0].Receipt.AlbumRevision == st.Revision && dirs[0].Receipt.BuildID != prevBuild {
				a, err := c.album(ctx, id)
				if err != nil {
					return published{}, err
				}
				return published{Album: a, Dir: dirs[0]}, nil
			}
		}
		if err := sleep(ctx, deadline, "album "+id+" to be published"); err != nil {
			return published{}, err
		}
	}
}

// waitGone waits until album id is in the trash, its render job is done and
// no folder on disk carries its album_id.
func (c *client) waitGone(ctx context.Context, id string) error {
	deadline := time.Now().Add(waitTimeout)
	for {
		var st albumStatus
		if err := c.call(ctx, http.MethodGet, "/api/albums/"+id+"/status", nil, nil, &st); err != nil {
			return err
		}
		if j := st.Job; j != nil && j.State == "failed" {
			return fmt.Errorf("album %s: the job failed: %s: %s", id, deref(j.ErrorCode), deref(j.ErrorMessage))
		}
		if st.Job == nil && st.Trashed {
			dirs, err := findAlbum(id)
			if err != nil {
				return err
			}
			if len(dirs) == 0 {
				return nil
			}
		}
		if err := sleep(ctx, deadline, "album "+id+" to leave the library"); err != nil {
			return err
		}
	}
}

// findAlbum returns the folders of the library whose receipt has album_id id.
func findAlbum(id string) ([]albumDir, error) {
	all, err := scanLibrary(libraryRoot)
	if err != nil {
		return nil, err
	}
	var out []albumDir
	for _, d := range all {
		if d.Receipt.AlbumID == id {
			out = append(out, d)
		}
	}
	return out, nil
}

func sleep(ctx context.Context, deadline time.Time, what string) error {
	if time.Now().After(deadline) {
		return fmt.Errorf("timed out after %s waiting for %s", waitTimeout, what)
	}
	t := time.NewTimer(pollEvery)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// newUUID returns a random (version 4) UUID, the id of an import request.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// albumByTitle finds the album with the given title in all (a list, whose
// entries have no tracks) and reads it whole.
func (c *client) albumByTitle(ctx context.Context, all []album, title string) (album, error) {
	i := slices.IndexFunc(all, func(a album) bool { return a.Title == title })
	if i < 0 {
		return album{}, fmt.Errorf("album %q: %w", title, errNotFound)
	}
	return c.album(ctx, all[i].ID)
}
