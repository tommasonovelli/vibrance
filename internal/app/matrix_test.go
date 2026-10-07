package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"vibrance/internal/api"
	"vibrance/internal/auth"
)

// The authorization matrix of DESIGN.md §12.4 (I6): for every operation of
// the specification, the status each kind of client gets. It is the way not
// to forget the authorization of a new operation: an operation of the
// specification without a row, or a row without an operation, fails the
// test.

// access is a row of the matrix: the status of a request of the operation
//
//   - anonymous: without a session;
//   - user: with the session of a user, on a resource of their own;
//   - admin: with the session of an admin, on a resource of their own;
//   - other: on a resource that belongs to another user, by a user and by
//     an admin, who sees nothing of the others either (§7.5, §8.6). 0 for
//     an operation whose resources belong to nobody.
type access struct {
	anonymous, user, admin, other int
}

const (
	s200 = http.StatusOK
	s201 = http.StatusCreated
	s204 = http.StatusNoContent
	s202 = http.StatusAccepted
	s401 = http.StatusUnauthorized
	s403 = http.StatusForbidden
	s404 = http.StatusNotFound
)

var authorizationMatrix = map[string]access{
	"getServerInfo":       {s200, s200, s200, 0},
	"login":               {s200, s200, s200, 0},
	"createToken":         {s201, s201, s201, 0},
	"logout":              {s401, s204, s204, 0},
	"getMe":               {s401, s200, s200, 0},
	"changePassword":      {s401, s204, s204, 0},
	"listSessions":        {s401, s200, s200, 0},
	"revokeOtherSessions": {s401, s204, s204, 0},
	"revokeSession":       {s401, s204, s204, s404},
	"getSettings":         {s401, s200, s200, 0},
	"updateSettings":      {s401, s200, s200, 0},
	"listUsers":           {s401, s403, s200, 0},
	"createUser":          {s401, s403, s201, 0},
	"getUser":             {s401, s403, s200, 0},
	"updateUser":          {s401, s403, s200, 0},
	"deleteUser":          {s401, s403, s204, 0},
	"resetUserPassword":   {s401, s403, s204, 0},
	"getLibraryStatus":    {s401, s403, s200, 0},
	"scanLibrary":         {s401, s403, s202, 0},
	"listArtists":         {s401, s200, s200, 0},
	"getArtist":           {s401, s200, s200, 0},
	"listAlbums":          {s401, s200, s200, 0},
	"listTracks":          {s401, s200, s200, 0},
	"listRandomTracks":    {s401, s200, s200, 0},
	"getAlbum":            {s401, s200, s200, 0},
	"getTrack":            {s401, s200, s200, 0},
	"getCatalogSummary":   {s401, s200, s200, 0},
	"search":              {s401, s200, s200, 0},
	"getTrackAudio":       {s401, s200, s200, 0},
	"getAlbumCover":       {s401, s200, s200, 0},
	"getTrackLyrics":      {s401, s200, s200, 0},
	"listFavoriteTracks":  {s401, s200, s200, 0},
	"getFavoritesSummary": {s401, s200, s200, 0},
	"addFavoriteTrack":    {s401, s204, s204, 0},
	"addFavoriteTracks":   {s401, s204, s204, 0},
	"removeFavoriteTrack": {s401, s204, s204, 0},
	"listPlaylists":       {s401, s200, s200, 0},
	"listTrackPlaylists":  {s401, s200, s200, 0},
	"createPlaylist":      {s401, s201, s201, 0},
	"getPlaylist":         {s401, s200, s200, s404},
	"updatePlaylist":      {s401, s200, s200, s404},
	"deletePlaylist":      {s401, s204, s204, s404},
	"listPlaylistItems":   {s401, s200, s200, s404},
	"addPlaylistItems":    {s401, s200, s200, s404},
	"removePlaylistItem":  {s401, s200, s200, s404},
	"movePlaylistItem":    {s401, s200, s200, s404},
}

// matrixRequest builds, in w, a request of an operation that succeeds when
// actor (nil: nobody) may make it on a resource of owner: the method, the
// path under /api/v1, the body (nil: none) and the If-Match header ("":
// none). It makes what the request needs, a session, an account to change
// or a playlist, as owner.
type matrixRequest func(w *world, actor, owner *account) (method, path string, body any, ifMatch string)

var matrixRequests = map[string]matrixRequest{
	"getServerInfo": func(*world, *account, *account) (string, string, any, string) { return "GET", "/server", nil, "" },
	"login": func(_ *world, _, owner *account) (string, string, any, string) {
		return "POST", "/auth/login", map[string]any{"username": owner.name, "password": owner.password}, ""
	},
	"createToken": func(_ *world, _, owner *account) (string, string, any, string) {
		return "POST", "/auth/tokens", map[string]any{"username": owner.name, "password": owner.password, "device_name": "matrix"}, ""
	},
	"logout": func(*world, *account, *account) (string, string, any, string) { return "POST", "/auth/logout", nil, "" },
	"getMe":  func(*world, *account, *account) (string, string, any, string) { return "GET", "/me", nil, "" },
	"changePassword": func(_ *world, _, owner *account) (string, string, any, string) {
		return "PUT", "/me/password", map[string]any{"current_password": owner.password, "new_password": "a new password of the matrix"}, ""
	},
	"listSessions": func(*world, *account, *account) (string, string, any, string) { return "GET", "/me/sessions", nil, "" },
	// The sessions revoked are those of the session of the request: no
	// request names another user (TestRevokeOtherSessions proves that the
	// sessions of the others stay).
	"revokeOtherSessions": func(w *world, _, owner *account) (string, string, any, string) {
		w.token(owner)
		return "DELETE", "/me/sessions", nil, ""
	},
	// The settings are those of the session of the request: no request
	// names another user (TestSettingsOverTheAPI proves that the settings of
	// one user are not those of another).
	"getSettings": func(*world, *account, *account) (string, string, any, string) { return "GET", "/me/settings", nil, "" },
	"updateSettings": func(*world, *account, *account) (string, string, any, string) {
		return "PATCH", "/me/settings", map[string]any{"theme": "light"}, ""
	},
	"revokeSession": func(w *world, _, owner *account) (string, string, any, string) {
		return "DELETE", "/me/sessions/" + w.token(owner).Session.ID, nil, ""
	},
	"listUsers": func(*world, *account, *account) (string, string, any, string) { return "GET", "/admin/users", nil, "" },
	"createUser": func(w *world, _, _ *account) (string, string, any, string) {
		w.accounts++
		return "POST", "/admin/users", map[string]any{"username": "new" + strings.Repeat("x", w.accounts), "password": "the password of the matrix", "role": "user"}, ""
	},
	"getUser": func(w *world, _, _ *account) (string, string, any, string) {
		return "GET", "/admin/users/" + w.newAccount(auth.RoleUser).id, nil, ""
	},
	"updateUser": func(w *world, _, _ *account) (string, string, any, string) {
		return "PUT", "/admin/users/" + w.newAccount(auth.RoleUser).id, map[string]any{"role": "admin", "disabled": false}, ""
	},
	"deleteUser": func(w *world, _, _ *account) (string, string, any, string) {
		return "DELETE", "/admin/users/" + w.newAccount(auth.RoleUser).id, nil, ""
	},
	"resetUserPassword": func(w *world, _, _ *account) (string, string, any, string) {
		return "PUT", "/admin/users/" + w.newAccount(auth.RoleUser).id + "/password", map[string]any{"password": "the password of the matrix"}, ""
	},
	"getLibraryStatus": func(*world, *account, *account) (string, string, any, string) {
		return "GET", "/admin/library", nil, ""
	},
	"scanLibrary": func(*world, *account, *account) (string, string, any, string) {
		return "POST", "/admin/library/scan", nil, ""
	},
	"listArtists": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/artists", nil, ""
	},
	"getArtist": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/artists/" + entryArtist, nil, ""
	},
	"listAlbums": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/albums", nil, ""
	},
	"listTracks": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/tracks", nil, ""
	},
	"listRandomTracks": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/tracks/random", nil, ""
	},
	"getAlbum": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/albums/" + entryAlbum, nil, ""
	},
	"getTrack": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/tracks/" + entryTrack, nil, ""
	},
	"getCatalogSummary": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "GET", "/catalog/summary", nil, ""
	},
	"search": func(*world, *account, *account) (string, string, any, string) {
		return "GET", "/search?q=track", nil, ""
	},
	"getTrackAudio": func(w *world, _, _ *account) (string, string, any, string) {
		return "GET", "/tracks/" + w.fixtureTrack(albumA, 1) + "/audio", nil, ""
	},
	"getAlbumCover": func(w *world, _, _ *account) (string, string, any, string) {
		w.fixtureTrack(albumA, 1)
		return "GET", "/albums/" + albumA + "/cover", nil, ""
	},
	"getTrackLyrics": func(w *world, _, _ *account) (string, string, any, string) {
		return "GET", "/tracks/" + w.fixtureTrack(albumA, 1) + "/lyrics", nil, ""
	},
	// The favorites are those of the session: no request names another user,
	// so the row has no cell for the resource of another (TestFavorites
	// proves that each user sees and changes only their own).
	"listFavoriteTracks": func(*world, *account, *account) (string, string, any, string) {
		return "GET", "/me/favorites/tracks", nil, ""
	},
	"getFavoritesSummary": func(*world, *account, *account) (string, string, any, string) {
		return "GET", "/me/favorites/summary", nil, ""
	},
	"addFavoriteTrack": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "PUT", "/me/favorites/tracks/" + entryTrack, nil, ""
	},
	"addFavoriteTracks": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "POST", "/me/favorites/tracks", map[string]any{"track_ids": []string{entryTrack}}, ""
	},
	"removeFavoriteTrack": func(w *world, _, _ *account) (string, string, any, string) {
		w.catalogEntry()
		return "DELETE", "/me/favorites/tracks/" + entryTrack, nil, ""
	},
	// A playlist is of owner, with one item: the row says what another user,
	// and an admin, get on it.
	"listPlaylists": func(*world, *account, *account) (string, string, any, string) { return "GET", "/playlists", nil, "" },
	// The track is of nobody, and the playlists listed are those of the
	// session: the playlist of owner with the track is listed to owner, and
	// to nobody else (TestTrackPlaylists).
	"listTrackPlaylists": func(w *world, _, owner *account) (string, string, any, string) {
		w.playlistWithItem(owner)
		return "GET", "/tracks/" + entryTrack + "/playlists", nil, ""
	},
	"createPlaylist": func(*world, *account, *account) (string, string, any, string) {
		return "POST", "/playlists", map[string]any{"name": "A playlist of the matrix", "description": ""}, ""
	},
	"getPlaylist": func(w *world, _, owner *account) (string, string, any, string) {
		p, _ := w.playlistWithItem(owner)
		return "GET", "/playlists/" + p.ID, nil, ""
	},
	"updatePlaylist": func(w *world, _, owner *account) (string, string, any, string) {
		p, _ := w.playlistWithItem(owner)
		return "PUT", "/playlists/" + p.ID, map[string]any{"name": "Another name", "description": "of the matrix"}, ""
	},
	"deletePlaylist": func(w *world, _, owner *account) (string, string, any, string) {
		p, _ := w.playlistWithItem(owner)
		return "DELETE", "/playlists/" + p.ID, nil, ""
	},
	"listPlaylistItems": func(w *world, _, owner *account) (string, string, any, string) {
		p, _ := w.playlistWithItem(owner)
		return "GET", "/playlists/" + p.ID + "/items", nil, ""
	},
	"addPlaylistItems": func(w *world, _, owner *account) (string, string, any, string) {
		p, _ := w.playlistWithItem(owner)
		return "POST", "/playlists/" + p.ID + "/items", map[string]any{"track_ids": []string{entryTrack}, "position": nil}, ""
	},
	"removePlaylistItem": func(w *world, _, owner *account) (string, string, any, string) {
		p, itemID := w.playlistWithItem(owner)
		return "DELETE", "/playlists/" + p.ID + "/items/" + itemID, nil, ""
	},
	// The move needs If-Match: the tag is the right one, so that a refusal
	// is about who asks and not about the revision.
	"movePlaylistItem": func(w *world, _, owner *account) (string, string, any, string) {
		p, itemID := w.playlistWithItem(owner)
		return "POST", "/playlists/" + p.ID + "/items/" + itemID + "/move", map[string]any{"position": 0}, p.ETag()
	},
}

// matrixMismatch lists what differs between the operations of doc and the
// rows of the matrix.
func matrixMismatch(doc *openapi3.T, matrix map[string]access) []string {
	var problems []string
	inSpec := map[string]bool{}
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			inSpec[op.OperationID] = true
			if _, ok := matrix[op.OperationID]; !ok {
				problems = append(problems, op.OperationID+" has no row in the matrix")
			}
		}
	}
	for id := range matrix {
		if !inSpec[id] {
			problems = append(problems, id+" is in the matrix and not in the specification")
		}
	}
	slices.Sort(problems)
	return problems
}

// The matrix has a row for every operation of the specification and for
// nothing else; every operation has its request; and a row says what the
// access rule of its operation implies.
func TestAuthorizationMatrixCoversTheSpecification(t *testing.T) {
	doc, ops := specOperations(t)
	if problems := matrixMismatch(doc, authorizationMatrix); len(problems) != 0 {
		t.Fatalf("the matrix and the specification differ:\n%s", strings.Join(problems, "\n"))
	}
	rules := api.Access(doc)
	for _, o := range ops {
		id := o.op.OperationID
		if _, has := matrixRequests[id]; !has {
			t.Errorf("%s has no request in the matrix", id)
		}
		row := authorizationMatrix[id]
		switch rules[o.method+" "+api.BasePath+o.path] {
		case auth.Public:
			if row.anonymous == s401 || row.user == s401 || row.admin == s401 {
				t.Errorf("%s is public and its row asks for a session: %+v", id, row)
			}
		case auth.Authenticated:
			if row.anonymous != s401 || row.user == s403 || row.user == s401 || row.admin == s403 {
				t.Errorf("%s is for users and its row says %+v", id, row)
			}
		case auth.AdminOnly:
			if row.anonymous != s401 || row.user != s403 || row.admin == s403 || row.admin == s401 || row.other != 0 {
				t.Errorf("%s is for admins and its row says %+v", id, row)
			}
		default:
			t.Errorf("%s has no access rule", id)
		}
		if row.other != 0 && row.other != s404 {
			t.Errorf("%s: a resource of another user answers %d, never anything but 404 (I6)", id, row.other)
		}
	}
	for id := range matrixRequests {
		if _, ok := authorizationMatrix[id]; !ok {
			t.Errorf("a request for %s, which has no row", id)
		}
	}
}

// The check above fails when the specification gains an operation without
// a row, or loses one that has a row.
func TestAuthorizationMatrixNoticesANewOperation(t *testing.T) {
	doc, err := api.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	doc.Paths.Set("/new", &openapi3.PathItem{Get: &openapi3.Operation{OperationID: "newOperation"}})
	if problems := matrixMismatch(doc, authorizationMatrix); !slices.Equal(problems, []string{"newOperation has no row in the matrix"}) {
		t.Fatalf("an operation without a row: %v", problems)
	}
	doc.Paths.Delete("/new")
	doc.Paths.Delete("/server")
	if problems := matrixMismatch(doc, authorizationMatrix); !slices.Equal(problems, []string{"getServerInfo is in the matrix and not in the specification"}) {
		t.Fatalf("a row without an operation: %v", problems)
	}
}

// §12.4: every cell of the matrix, on the server. Each operation runs in a
// world of its own, and each request has a new session: an operation may
// end the session it is asked with, or change the account.
func TestAuthorizationMatrix(t *testing.T) {
	_, ops := specOperations(t)
	cells := 0
	for _, o := range ops {
		id := o.op.OperationID
		row, ok := authorizationMatrix[id]
		if !ok {
			t.Errorf("%s has no row", id)
			continue
		}
		w := newWorld(t, apiOrigin)
		type cell struct {
			who          string
			actor, owner *account
			want         int
		}
		cases := []cell{
			{"anonymous", nil, w.anna, row.anonymous},
			{"user", w.anna, w.anna, row.user},
			{"admin", w.admin, w.admin, row.admin},
		}
		if row.other != 0 {
			cases = append(cases, cell{"another user", w.bob, w.anna, row.other}, cell{"an admin, on a resource of a user", w.admin, w.anna, row.other})
		}
		build, ok := matrixRequests[id]
		if !ok {
			t.Errorf("%s has no request", id)
			continue
		}
		for _, c := range cases {
			where := id + " as " + c.who
			method, path, body, ifMatch := build(w, c.actor, c.owner)
			req := w.request(method, path, body)
			if ifMatch != "" {
				req.Header.Set("If-Match", ifMatch)
			}
			w.as(c.actor)(req)
			rec := send(w.s.http.Handler, req)
			assertConforms(t, where, req, rec)
			if rec.Code != c.want {
				t.Errorf("%s: status %d, want %d (%s)", where, rec.Code, c.want, redacted(rec))
			}
			switch rec.Code {
			case s401:
				wantCode(t, where, rec, s401, auth.CodeLoginRequired)
			case s403:
				wantCode(t, where, rec, s403, auth.CodeForbidden)
			case s404:
				if code, _ := errorBody(t, where, rec); !strings.HasSuffix(code, "_not_found") || code == "not_found" {
					t.Errorf("%s: 404 %s, want the code of a resource", where, code)
				}
			}
			cells++
		}
	}
	if cells < 3*len(ops) {
		t.Fatalf("%d cells checked for %d operations", cells, len(ops))
	}
}

// §8.3, §12.4: the routes outside the specification are listed apart. They
// need no session, and answer the same with one and without. The script of
// the page of the documentation is one of them: the page is not whole
// without it. So is every file of the web interface, its sign-in page, and
// a path of its router, which is the interface's page.
func TestInfrastructureRoutes(t *testing.T) {
	w := newWorld(t, apiOrigin)
	type route struct {
		path   string
		status int
	}
	routes := []route{
		{livePath, s200},
		{readyPath, s200},
		{"/", s200},
		{"/login", s200},
		{"/albums/" + someID, s200},
		{"/api/openapi.yaml", s200},
		{"/api/docs", s200},
		{"/api/docs/scalar.js", s200},
	}
	for _, p := range uiPaths(t) {
		routes = append(routes, route{p, s200})
	}
	for _, r := range routes {
		for who, c := range map[string]credential{"anonymous": nobody, "a user": w.as(w.anna), "an admin": w.as(w.admin)} {
			req := w.request("GET", "", nil)
			req.URL.Path = r.path
			c(req)
			rec := send(w.s.http.Handler, req)
			if rec.Code != r.status {
				t.Errorf("GET %s as %s: status %d, want %d", r.path, who, rec.Code, r.status)
			}
			wantHeaders(t, "GET "+r.path, rec.Header())
			if rec.Header().Get("Content-Type") == "application/json" && !json.Valid(rec.Body.Bytes()) {
				t.Errorf("GET %s: the body is not JSON", r.path)
			}
		}
	}
}
