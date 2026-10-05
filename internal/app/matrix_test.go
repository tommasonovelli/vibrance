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
	"revokeSession":       {s401, s204, s204, s404},
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
	"getAlbum":            {s401, s200, s200, 0},
	"getTrack":            {s401, s200, s200, 0},
	"search":              {s401, s200, s200, 0},
	"getTrackAudio":       {s401, s200, s200, 0},
	"getAlbumCover":       {s401, s200, s200, 0},
	"getTrackLyrics":      {s401, s200, s200, 0},
	"listFavoriteTracks":  {s401, s200, s200, 0},
	"addFavoriteTrack":    {s401, s204, s204, 0},
	"removeFavoriteTrack": {s401, s204, s204, 0},
	"listPlaylists":       {s401, s200, s200, 0},
	"createPlaylist":      {s401, s201, s201, 0},
	"getPlaylist":         {s401, s200, s200, s404},
	"updatePlaylist":      {s401, s200, s200, s404},
	"deletePlaylist":      {s401, s204, s204, s404},
	"listPlaylistItems":   {s401, s200, s200, s404},
	"addPlaylistItems":    {s401, s200, s200, s404},
	"removePlaylistItem":  {s401, s200, s200, s404},
	"movePlaylistItem":    {s401, s200, s200, s404},
}

// pendingOperations answer 501 not_implemented until their step implements
// them; the step then removes them from here and gives them a request in
// matrixRequests. Until then the matrix checks their refusals (401, 403)
// exactly, and expects 501 where it says success or 404.
var pendingOperations = []string{
	"getLibraryStatus", "scanLibrary",
	"listFavoriteTracks", "addFavoriteTrack", "removeFavoriteTrack",
	"listPlaylists", "createPlaylist", "getPlaylist", "updatePlaylist", "deletePlaylist", "listPlaylistItems",
	"addPlaylistItems", "removePlaylistItem", "movePlaylistItem",
}

// matrixRequest builds, in w, a request of an operation that succeeds when
// actor (nil: nobody) may make it on a resource of owner: the method, the
// path under /api/v1 and the body (nil: none). It makes what the request
// needs, a session or an account to change, as owner.
type matrixRequest func(w *world, actor, owner *account) (method, path string, body any)

var matrixRequests = map[string]matrixRequest{
	"getServerInfo": func(*world, *account, *account) (string, string, any) { return "GET", "/server", nil },
	"login": func(_ *world, _, owner *account) (string, string, any) {
		return "POST", "/auth/login", map[string]any{"username": owner.name, "password": owner.password}
	},
	"createToken": func(_ *world, _, owner *account) (string, string, any) {
		return "POST", "/auth/tokens", map[string]any{"username": owner.name, "password": owner.password, "device_name": "matrix"}
	},
	"logout": func(*world, *account, *account) (string, string, any) { return "POST", "/auth/logout", nil },
	"getMe":  func(*world, *account, *account) (string, string, any) { return "GET", "/me", nil },
	"changePassword": func(_ *world, _, owner *account) (string, string, any) {
		return "PUT", "/me/password", map[string]any{"current_password": owner.password, "new_password": "a new password of the matrix"}
	},
	"listSessions": func(*world, *account, *account) (string, string, any) { return "GET", "/me/sessions", nil },
	"revokeSession": func(w *world, _, owner *account) (string, string, any) {
		return "DELETE", "/me/sessions/" + w.token(owner).Session.ID, nil
	},
	"listUsers": func(*world, *account, *account) (string, string, any) { return "GET", "/admin/users", nil },
	"createUser": func(w *world, _, _ *account) (string, string, any) {
		w.accounts++
		return "POST", "/admin/users", map[string]any{"username": "new" + strings.Repeat("x", w.accounts), "password": "the password of the matrix", "role": "user"}
	},
	"getUser": func(w *world, _, _ *account) (string, string, any) {
		return "GET", "/admin/users/" + w.newAccount(auth.RoleUser).id, nil
	},
	"updateUser": func(w *world, _, _ *account) (string, string, any) {
		return "PUT", "/admin/users/" + w.newAccount(auth.RoleUser).id, map[string]any{"role": "admin", "disabled": false}
	},
	"deleteUser": func(w *world, _, _ *account) (string, string, any) {
		return "DELETE", "/admin/users/" + w.newAccount(auth.RoleUser).id, nil
	},
	"resetUserPassword": func(w *world, _, _ *account) (string, string, any) {
		return "PUT", "/admin/users/" + w.newAccount(auth.RoleUser).id + "/password", map[string]any{"password": "the password of the matrix"}
	},
	"listArtists": func(w *world, _, _ *account) (string, string, any) {
		w.catalogEntry()
		return "GET", "/artists", nil
	},
	"getArtist": func(w *world, _, _ *account) (string, string, any) {
		w.catalogEntry()
		return "GET", "/artists/" + entryArtist, nil
	},
	"listAlbums": func(w *world, _, _ *account) (string, string, any) {
		w.catalogEntry()
		return "GET", "/albums", nil
	},
	"getAlbum": func(w *world, _, _ *account) (string, string, any) {
		w.catalogEntry()
		return "GET", "/albums/" + entryAlbum, nil
	},
	"getTrack": func(w *world, _, _ *account) (string, string, any) {
		w.catalogEntry()
		return "GET", "/tracks/" + entryTrack, nil
	},
	"search": func(*world, *account, *account) (string, string, any) { return "GET", "/search?q=track", nil },
	"getTrackAudio": func(w *world, _, _ *account) (string, string, any) {
		return "GET", "/tracks/" + w.fixtureTrack(albumA, 1) + "/audio", nil
	},
	"getAlbumCover": func(w *world, _, _ *account) (string, string, any) {
		w.fixtureTrack(albumA, 1)
		return "GET", "/albums/" + albumA + "/cover", nil
	},
	"getTrackLyrics": func(w *world, _, _ *account) (string, string, any) {
		return "GET", "/tracks/" + w.fixtureTrack(albumA, 1) + "/lyrics", nil
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
// nothing else; every operation is either pending or has its request; and
// a row says what the access rule of its operation implies.
func TestAuthorizationMatrixCoversTheSpecification(t *testing.T) {
	doc, ops := specOperations(t)
	if problems := matrixMismatch(doc, authorizationMatrix); len(problems) != 0 {
		t.Fatalf("the matrix and the specification differ:\n%s", strings.Join(problems, "\n"))
	}
	rules := api.Access(doc)
	for _, o := range ops {
		id := o.op.OperationID
		_, has := matrixRequests[id]
		if pending := slices.Contains(pendingOperations, id); pending == has {
			t.Errorf("%s: pending %v, with a request %v: it must be one of the two", id, pending, has)
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
		pending := slices.Contains(pendingOperations, id)
		for _, c := range cases {
			where := id + " as " + c.who
			var req = o.example(t)
			if !pending {
				method, path, body := matrixRequests[id](w, c.actor, c.owner)
				req = w.request(method, path, body)
			}
			// The example asks for its Host; the world has the same.
			req.Host = w.host
			w.as(c.actor)(req)
			rec := send(w.s.http.Handler, req)
			assertConforms(t, where, req, rec)
			want := c.want
			if pending && want != s401 && want != s403 {
				want = http.StatusNotImplemented
			}
			if rec.Code != want {
				t.Errorf("%s: status %d, want %d (%s)", where, rec.Code, want, redacted(rec))
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
// need no session, and answer the same with one and without. step is the
// step that serves a route not served yet: it answers 404 until then.
func TestInfrastructureRoutes(t *testing.T) {
	w := newWorld(t, apiOrigin)
	for _, r := range []struct {
		path   string
		status int
		step   string
	}{
		{livePath, s200, ""},
		{readyPath, s200, ""},
		{"/", http.StatusFound, ""},
		{"/api/openapi.yaml", s200, "S20"},
		{"/api/docs", s200, "S20"},
	} {
		want := r.status
		if r.step != "" {
			want = s404
		}
		for who, c := range map[string]credential{"anonymous": nobody, "a user": w.as(w.anna), "an admin": w.as(w.admin)} {
			req := w.request("GET", "", nil)
			req.URL.Path = r.path
			c(req)
			rec := send(w.s.http.Handler, req)
			if rec.Code != want {
				t.Errorf("GET %s as %s: status %d, want %d", r.path, who, rec.Code, want)
			}
			wantHeaders(t, "GET "+r.path, rec.Header())
			if rec.Header().Get("Content-Type") == "application/json" && !json.Valid(rec.Body.Bytes()) {
				t.Errorf("GET %s: the body is not JSON", r.path)
			}
		}
	}
}
