package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"vibrance/internal/httpx"
)

// specPath is the specification, from the directory of this package.
const specPath = "../../api/openapi.yaml"

// loadSpec reads api/openapi.yaml as the tools will.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("loading %s: %v", specPath, err)
	}
	return doc
}

// designError is one entry of the column "Errori principali" of DESIGN.md
// §8.3.
type designError struct {
	status int
	code   string
}

// designOperation is one row of the table of DESIGN.md §8.3.
type designOperation struct {
	method  string
	path    string
	id      string
	access  string // public, user or admin
	success int
	errors  []designError
}

// The codes the table of §8.3 leaves implied where it writes only a status
// for the playlists: §8.6 and §8.4 name them.
var (
	playlistNotFound     = designError{404, "playlist_not_found"}
	preconditionFailed   = designError{412, "precondition_failed"}
	preconditionRequired = designError{428, "precondition_required"}
)

// designOperations is the table of DESIGN.md §8.3, copied row by row and in
// its order. It is the list the specification is compared with: an
// operation is added to the API by adding it to the design, to this table
// and to api/openapi.yaml.
var designOperations = []designOperation{
	{"GET", "/server", "getServerInfo", "public", 200, nil},
	{"POST", "/auth/login", "login", "public", 200, []designError{{401, "invalid_credentials"}}},
	{"POST", "/auth/tokens", "createToken", "public", 201, []designError{{401, "invalid_credentials"}}},
	{"POST", "/auth/logout", "logout", "user", 204, nil},
	{"GET", "/me", "getMe", "user", 200, nil},
	{"PUT", "/me/password", "changePassword", "user", 204, []designError{{422, "current_password_invalid"}, {422, "password_invalid"}}},
	{"GET", "/me/sessions", "listSessions", "user", 200, nil},
	{"DELETE", "/me/sessions/{id}", "revokeSession", "user", 204, []designError{{404, "session_not_found"}}},
	{"GET", "/admin/users", "listUsers", "admin", 200, []designError{{403, "forbidden"}}},
	{"POST", "/admin/users", "createUser", "admin", 201, []designError{{409, "username_taken"}, {422, "username_invalid"}, {422, "password_invalid"}}},
	{"GET", "/admin/users/{id}", "getUser", "admin", 200, []designError{{404, "user_not_found"}}},
	{"PUT", "/admin/users/{id}", "updateUser", "admin", 200, []designError{{409, "last_admin"}, {409, "cannot_modify_self"}}},
	{"DELETE", "/admin/users/{id}", "deleteUser", "admin", 204, []designError{{409, "last_admin"}, {409, "cannot_modify_self"}}},
	{"PUT", "/admin/users/{id}/password", "resetUserPassword", "admin", 204, []designError{{422, "password_invalid"}}},
	{"GET", "/admin/library", "getLibraryStatus", "admin", 200, nil},
	{"POST", "/admin/library/scan", "scanLibrary", "admin", 202, nil},
	{"GET", "/artists", "listArtists", "user", 200, []designError{{400, "invalid_cursor"}}},
	{"GET", "/artists/{id}", "getArtist", "user", 200, []designError{{404, "artist_not_found"}}},
	{"GET", "/albums", "listAlbums", "user", 200, []designError{{400, "invalid_cursor"}}},
	{"GET", "/albums/{id}", "getAlbum", "user", 200, []designError{{404, "album_not_found"}}},
	{"GET", "/tracks/{id}", "getTrack", "user", 200, []designError{{404, "track_not_found"}}},
	{"GET", "/search", "search", "user", 200, []designError{{400, "invalid_request"}}},
	{"GET", "/tracks/{id}/audio", "getTrackAudio", "user", 200, []designError{{404, "track_unavailable"}, {400, "unsupported_profile"}, {503, "library_changing"}}},
	{"GET", "/albums/{id}/cover", "getAlbumCover", "user", 200, []designError{{404, "cover_not_found"}, {503, "library_changing"}}},
	{"GET", "/tracks/{id}/lyrics", "getTrackLyrics", "user", 200, []designError{{404, "lyrics_not_found"}}},
	{"GET", "/me/favorites/tracks", "listFavoriteTracks", "user", 200, []designError{{400, "invalid_cursor"}}},
	{"PUT", "/me/favorites/tracks/{id}", "addFavoriteTrack", "user", 204, []designError{{404, "track_not_found"}}},
	{"DELETE", "/me/favorites/tracks/{id}", "removeFavoriteTrack", "user", 204, []designError{{404, "track_not_found"}}},
	{"GET", "/playlists", "listPlaylists", "user", 200, nil},
	{"POST", "/playlists", "createPlaylist", "user", 201, []designError{{422, "too_many_playlists"}, {422, "invalid_request"}}},
	{"GET", "/playlists/{id}", "getPlaylist", "user", 200, []designError{playlistNotFound}},
	{"PUT", "/playlists/{id}", "updatePlaylist", "user", 200, []designError{playlistNotFound, preconditionFailed}},
	{"DELETE", "/playlists/{id}", "deletePlaylist", "user", 204, []designError{playlistNotFound, preconditionFailed}},
	{"GET", "/playlists/{id}/items", "listPlaylistItems", "user", 200, []designError{playlistNotFound, {400, "invalid_cursor"}}},
	{"POST", "/playlists/{id}/items", "addPlaylistItems", "user", 200, []designError{playlistNotFound, preconditionFailed, {422, "too_many_items"}, {422, "unknown_track"}, {422, "track_unavailable"}, {422, "invalid_position"}, preconditionRequired}},
	{"DELETE", "/playlists/{id}/items/{item_id}", "removePlaylistItem", "user", 200, []designError{{404, "item_not_found"}, preconditionFailed}},
	{"POST", "/playlists/{id}/items/{item_id}/move", "movePlaylistItem", "user", 200, []designError{playlistNotFound, preconditionFailed, {422, "invalid_position"}, preconditionRequired}},
}

// designErrorCodes is the table of DESIGN.md §8.4, with the `*_not_found`
// of its 404 row written out as §8.3 names them.
var designErrorCodes = []string{
	"invalid_request", "invalid_cursor", "unsupported_profile",
	"login_required", "invalid_credentials",
	"forbidden", "origin_not_allowed", "request_header_required",
	"not_found",
	"session_not_found", "user_not_found", "artist_not_found", "album_not_found",
	"track_not_found", "playlist_not_found", "item_not_found",
	"track_unavailable", "cover_not_found", "lyrics_not_found",
	"method_not_allowed",
	"username_taken", "last_admin", "cannot_modify_self",
	"precondition_failed",
	"body_too_large",
	"host_not_allowed",
	"username_invalid", "password_invalid", "current_password_invalid",
	"too_many_playlists", "too_many_items", "unknown_track", "invalid_position",
	"precondition_required",
	"internal",
	"not_ready", "shutting_down",
	"library_changing",
}

// mainSchemas are the schemas of DESIGN.md §8.2, plus the lyrics of §9.3:
// each must carry an example.
var mainSchemas = []string{
	"Error", "User", "Session", "Cover", "ArtistRef", "ArtistSummary", "AlbumRef",
	"AlbumSummary", "AlbumDetail", "Track", "Playlist", "PlaylistItem",
	"LibraryStatus", "ServerInfo", "Lyrics",
}

// specOperation is one operation of the specification.
type specOperation struct {
	method string
	path   string
	item   *openapi3.PathItem
	op     *openapi3.Operation
}

// operations lists the operations of the specification, ordered by path
// and method.
func operations(doc *openapi3.T) []specOperation {
	var ops []specOperation
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			ops = append(ops, specOperation{method: method, path: path, item: item, op: op})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].path != ops[j].path {
			return ops[i].path < ops[j].path
		}
		return ops[i].method < ops[j].method
	})
	return ops
}

// documented is every sentence a reader of one operation sees: its
// description and the descriptions of its parameters.
func (o specOperation) documented() string {
	var b strings.Builder
	b.WriteString(o.op.Description)
	for _, p := range o.op.Parameters {
		b.WriteString("\n")
		b.WriteString(p.Value.Description)
	}
	return b.String()
}

// errorStatuses are the 4xx and 5xx responses of an operation.
func (o specOperation) errorStatuses(t *testing.T) []int {
	t.Helper()
	var statuses []int
	for key := range o.op.Responses.Map() {
		status, err := strconv.Atoi(key)
		if err != nil {
			t.Errorf("%s: response %q is not a status code: every response is declared by its status", o.op.OperationID, key)
			continue
		}
		if status >= 400 {
			statuses = append(statuses, status)
		}
	}
	sort.Ints(statuses)
	return statuses
}

func (o specOperation) has(status int) bool {
	return o.op.Responses.Value(strconv.Itoa(status)) != nil
}

func TestSpecValid(t *testing.T) {
	doc := loadSpec(t)

	// D5, T23: the tools do not support 3.1 in full.
	if doc.OpenAPI != "3.0.3" {
		t.Errorf("openapi = %q, want 3.0.3", doc.OpenAPI)
	}
	// Schemas, references, formats, patterns, and every example against
	// the schema it stands for.
	err := doc.Validate(t.Context(), openapi3.EnableSchemaFormatValidation(), openapi3.EnableExamplesValidation())
	if err != nil {
		t.Errorf("the specification is not valid: %v", err)
	}
	// §8.8: the published document names the base path; the copy used to
	// validate requests removes it (T23).
	if len(doc.Servers) != 1 || doc.Servers[0].URL != BasePath {
		t.Errorf("servers = %v, want exactly one, %s", doc.Servers, BasePath)
	}
}

func TestSpecMatchesDesign(t *testing.T) {
	doc := loadSpec(t)

	found := map[string]specOperation{}
	for _, o := range operations(doc) {
		found[o.op.OperationID] = o
	}
	want := map[string]designOperation{}
	for _, d := range designOperations {
		want[d.id] = d
	}
	if len(want) != len(designOperations) {
		t.Fatalf("the table of the test repeats an operationId")
	}

	for id := range found {
		if _, ok := want[id]; !ok {
			t.Errorf("operation %q is in the specification and not in the table of DESIGN.md §8.3", id)
		}
	}
	for _, d := range designOperations {
		o, ok := found[d.id]
		if !ok {
			t.Errorf("operation %q of DESIGN.md §8.3 is not in the specification", d.id)
			continue
		}
		if o.method != d.method || o.path != d.path {
			t.Errorf("%s is %s %s, want %s %s", d.id, o.method, o.path, d.method, d.path)
		}

		// Access: public operations say `security: []`; the others inherit
		// the two schemes and answer 401; admin operations answer 403
		// forbidden.
		switch d.access {
		case "public":
			if o.op.Security == nil || len(*o.op.Security) != 0 {
				t.Errorf("%s is public: want `security: []`", d.id)
			}
			if o.has(401) && !slices.Contains(d.errors, designError{401, "invalid_credentials"}) {
				t.Errorf("%s is public and declares 401", d.id)
			}
		case "user", "admin":
			if o.op.Security != nil {
				t.Errorf("%s overrides the security of the document: %v", d.id, *o.op.Security)
			}
			if ref := o.op.Responses.Value("401"); ref == nil || ref.Ref != "#/components/responses/Unauthorized" {
				t.Errorf("%s needs a session: want the response 401 Unauthorized", d.id)
			}
		default:
			t.Fatalf("%s: unknown access %q in the table of the test", d.id, d.access)
		}
		if d.access == "admin" && !strings.Contains(o.documented(), "`403 forbidden`") {
			t.Errorf("%s is for admins: its description must name `403 forbidden`", d.id)
		}
		if d.access != "admin" && strings.Contains(o.documented(), "`403 forbidden`") {
			t.Errorf("%s is not for admins and names `403 forbidden`", d.id)
		}

		// Success: exactly the status of the table, plus the partial and
		// the not-modified answers of the media.
		var successes []int
		for key := range o.op.Responses.Map() {
			if status, err := strconv.Atoi(key); err == nil && status < 400 {
				successes = append(successes, status)
			}
		}
		sort.Ints(successes)
		wantSuccesses := []int{d.success}
		switch d.id {
		case "getTrackAudio":
			wantSuccesses = []int{200, 206, 304}
		case "getAlbumCover":
			wantSuccesses = []int{200, 206, 304}
		}
		if !slices.Equal(successes, wantSuccesses) {
			t.Errorf("%s: success statuses %v, want %v", d.id, successes, wantSuccesses)
		}

		// Errors: the status is declared and the description names the
		// code next to it.
		for _, e := range d.errors {
			if !o.has(e.status) {
				t.Errorf("%s: response %d (%s) is not declared", d.id, e.status, e.code)
			}
			if mention := fmt.Sprintf("`%d %s`", e.status, e.code); !strings.Contains(o.documented(), mention) {
				t.Errorf("%s: the description does not name %s", d.id, mention)
			}
		}
	}
}

func TestSpecCompleteness(t *testing.T) {
	doc := loadSpec(t)
	ops := operations(doc)

	// The two schemes, on every operation that does not say otherwise.
	wantSecurity := openapi3.SecurityRequirements{{"bearerAuth": {}}, {"cookieAuth": {}}}
	if got, want := fmt.Sprint(doc.Security), fmt.Sprint(wantSecurity); got != want {
		t.Errorf("security of the document = %s, want %s", got, want)
	}
	cookie := doc.Components.SecuritySchemes["cookieAuth"]
	if cookie == nil || cookie.Value.Type != "apiKey" || cookie.Value.In != "cookie" || cookie.Value.Name != "vibrance_session" {
		t.Errorf("cookieAuth must be the cookie vibrance_session")
	}
	bearer := doc.Components.SecuritySchemes["bearerAuth"]
	if bearer == nil || bearer.Value.Type != "http" || bearer.Value.Scheme != "bearer" {
		t.Errorf("bearerAuth must be an HTTP bearer scheme")
	}

	tags := map[string]bool{}
	for _, tag := range doc.Tags {
		if tag.Description == "" {
			t.Errorf("tag %q has no description", tag.Name)
		}
		tags[tag.Name] = false
	}

	seen := map[string]bool{}
	for _, o := range ops {
		id := o.op.OperationID
		if id == "" {
			t.Errorf("%s %s has no operationId", o.method, o.path)
			continue
		}
		if seen[id] {
			t.Errorf("operationId %q is used twice", id)
		}
		seen[id] = true

		if o.op.Summary == "" || o.op.Description == "" {
			t.Errorf("%s: summary and description are required", id)
		}
		if len(o.op.Tags) != 1 {
			t.Errorf("%s: want exactly one tag, got %v", id, o.op.Tags)
		}
		for _, tag := range o.op.Tags {
			if _, ok := tags[tag]; !ok {
				t.Errorf("%s: tag %q is not declared in the document", id, tag)
			}
			tags[tag] = true
		}
		// Parameters are declared on the operation, where the generated
		// code and the tests read them.
		if len(o.item.Parameters) != 0 {
			t.Errorf("%s: parameters declared on the path item", id)
		}

		// Errors every request can meet at the boundary (§7.6, §8.4).
		errs := o.errorStatuses(t)
		if len(errs) == 0 {
			t.Errorf("%s declares no error response", id)
		}
		for _, status := range []int{403, 421, 500, 503} {
			if !o.has(status) {
				t.Errorf("%s: response %d is not declared", id, status)
			}
		}
		// X-Vibrance-Request does not count: the boundary answers 403 for
		// it before the validator sees the request, never 400.
		hasBody := o.op.RequestBody != nil
		validated := 0
		for _, p := range o.op.Parameters {
			if p.Ref != requestHeaderRef {
				validated++
			}
		}
		if (validated > 0 || hasBody) != o.has(400) {
			t.Errorf("%s: response 400 goes with a body or a parameter the validator checks, and only with one", id)
		}
		if hasBody != o.has(413) {
			t.Errorf("%s: response 413 goes with a request body, and only with one", id)
		}

		for key, ref := range o.op.Responses.Map() {
			checkResponse(t, id, key, ref)
		}
		for _, p := range o.op.Parameters {
			if p.Value.Description == "" {
				t.Errorf("%s: parameter %q has no description", id, p.Value.Name)
			}
			// A header parameter is never `required`: a missing header
			// has its own answer (428), not the 400 of the validator.
			// X-Vibrance-Request is the one that is, and its own answer
			// is the 403 of the boundary (TestSpecRequestHeader).
			if p.Value.In == "header" && p.Value.Required != (p.Ref == requestHeaderRef) {
				t.Errorf("%s: header parameter %q: required = %t", id, p.Value.Name, p.Value.Required)
			}
			if p.Value.In == "path" && (p.Value.Schema.Value.Format != "uuid" || !p.Value.Required) {
				t.Errorf("%s: path parameter %q must be a required UUID (I2)", id, p.Value.Name)
			}
		}
		if hasBody {
			checkRequestBody(t, id, o.op.RequestBody)
		}
	}
	for tag, used := range tags {
		if !used {
			t.Errorf("tag %q is declared and no operation uses it", tag)
		}
	}

	// Examples: one for each main schema of §8.2, and one for each request.
	for _, name := range mainSchemas {
		ref := doc.Components.Schemas[name]
		if ref == nil {
			t.Errorf("schema %q of DESIGN.md §8.2 is not in the specification", name)
			continue
		}
		if ref.Value.Example == nil {
			t.Errorf("schema %q has no example", name)
		}
	}
	for name, ref := range doc.Components.Schemas {
		if ref.Value.Example == nil {
			continue
		}
		err := ref.Value.VisitJSON(ref.Value.Example, openapi3.EnableFormatValidation(), openapi3.MultiErrors())
		if err != nil {
			t.Errorf("the example of schema %q does not match it: %v", name, err)
		}
	}

	// Every error response is the one Error component.
	for name, ref := range doc.Components.Responses {
		if name == "NotModified" {
			continue
		}
		media := ref.Value.Content.Get("application/json")
		if media == nil || media.Schema.Ref != "#/components/schemas/Error" {
			t.Errorf("response %q must carry the Error schema", name)
			continue
		}
		if media.Example == nil {
			t.Errorf("response %q has no example", name)
		}
	}
}

// checkResponse checks what every response declares: the request id, and
// for errors the shared Error model.
func checkResponse(t *testing.T, id, key string, ref *openapi3.ResponseRef) {
	t.Helper()
	status, err := strconv.Atoi(key)
	if err != nil {
		return // reported by errorStatuses
	}
	resp := ref.Value
	if h := resp.Headers["X-Request-Id"]; h == nil || h.Ref != "#/components/headers/X-Request-Id" {
		t.Errorf("%s %d: the header X-Request-Id is not declared", id, status)
	}
	if status < 400 {
		if ref.Ref != "" && ref.Ref != "#/components/responses/NotModified" {
			t.Errorf("%s %d: a success is declared in its operation, not shared: %s", id, status, ref.Ref)
		}
		noBody := status == 204 || status == 304
		if noBody != (len(resp.Content) == 0) {
			t.Errorf("%s %d: content declared = %t", id, status, len(resp.Content) != 0)
		}
		return
	}
	// 416, and the 412 of a file, are written by http.ServeContent for a
	// range outside the file and for an If-Match of another file: their body
	// is not the error model.
	if status == 416 || (status == 412 && servesFiles(id)) {
		if len(resp.Content) != 0 {
			t.Errorf("%s %d must not declare a body", id, status)
		}
		return
	}
	if !strings.HasPrefix(ref.Ref, "#/components/responses/") {
		t.Errorf("%s %d: an error response must be one of the shared responses", id, status)
		return
	}
	if len(resp.Content) != 1 || resp.Content.Get("application/json") == nil ||
		resp.Content.Get("application/json").Schema.Ref != "#/components/schemas/Error" {
		t.Errorf("%s %d: an error response is JSON with the Error schema", id, status)
	}
}

// checkRequestBody checks T23: a request is JSON, required, a named schema
// with an example, and no object in it accepts unknown keys.
func checkRequestBody(t *testing.T, id string, body *openapi3.RequestBodyRef) {
	t.Helper()
	if !body.Value.Required {
		t.Errorf("%s: the request body must be required", id)
	}
	media := body.Value.Content.Get("application/json")
	if media == nil || len(body.Value.Content) != 1 {
		t.Errorf("%s: the request body must be application/json and nothing else", id)
		return
	}
	if !strings.HasPrefix(media.Schema.Ref, "#/components/schemas/") {
		t.Errorf("%s: the request body must be a named schema", id)
	}
	if media.Schema.Value.Example == nil {
		t.Errorf("%s: the request schema has no example", id)
	}
	checkClosed(t, id+" request", media.Schema.Value)
}

// checkClosed fails for every object under schema that does not say
// `additionalProperties: false`.
func checkClosed(t *testing.T, where string, schema *openapi3.Schema) {
	t.Helper()
	if schema.Type.Is("object") || len(schema.Properties) > 0 {
		if has := schema.AdditionalProperties.Has; has == nil || *has || schema.AdditionalProperties.Schema != nil {
			t.Errorf("%s: an object without `additionalProperties: false` accepts unknown keys", where)
		}
	}
	for name, p := range schema.Properties {
		checkClosed(t, where+"."+name, p.Value)
	}
	if schema.Items != nil {
		checkClosed(t, where+"[]", schema.Items.Value)
	}
	for _, list := range [][]*openapi3.SchemaRef{schema.AllOf, schema.AnyOf, schema.OneOf} {
		for i, s := range list {
			checkClosed(t, fmt.Sprintf("%s(%d)", where, i), s.Value)
		}
	}
}

// requestSchemas are the names of the schemas that are request bodies.
func requestSchemas(doc *openapi3.T) map[string]bool {
	names := map[string]bool{}
	for _, o := range operations(doc) {
		if o.op.RequestBody == nil {
			continue
		}
		if media := o.op.RequestBody.Value.Content.Get("application/json"); media != nil {
			names[strings.TrimPrefix(media.Schema.Ref, "#/components/schemas/")] = true
		}
	}
	return names
}

// In a response every field is always present: what has no value is null,
// never missing (§8.2).
func TestSpecResponseFieldsAreRequired(t *testing.T) {
	doc := loadSpec(t)
	requests := requestSchemas(doc)
	if len(requests) == 0 {
		t.Fatal("no request schema found")
	}

	var check func(where string, schema *openapi3.Schema)
	check = func(where string, schema *openapi3.Schema) {
		names := slices.Sorted(maps.Keys(schema.Properties))
		required := slices.Clone(schema.Required)
		sort.Strings(required)
		if !slices.Equal(names, required) {
			t.Errorf("%s: properties %v, required %v: a response field is always present", where, names, required)
		}
		for name, p := range schema.Properties {
			if p.Ref == "" {
				check(where+"."+name, p.Value)
			}
			// A date is the one Timestamp, in the one format of T25.
			if strings.HasSuffix(name, "_at") && !refersTo(p, "Timestamp") {
				t.Errorf("%s.%s: a date must be the Timestamp schema", where, name)
			}
			// An id is the one Id.
			if (name == "id" || strings.HasSuffix(name, "_id")) && !refersTo(p, "Id") {
				t.Errorf("%s.%s: an id must be the Id schema", where, name)
			}
		}
		if schema.Items != nil && schema.Items.Ref == "" {
			check(where+"[]", schema.Items.Value)
		}
		for i, s := range schema.AllOf {
			if s.Ref == "" {
				check(fmt.Sprintf("%s(%d)", where, i), s.Value)
			}
		}
	}
	for name, ref := range doc.Components.Schemas {
		if requests[name] {
			continue
		}
		check(name, ref.Value)
	}
}

// refersTo reports whether a property is the named schema, or the named
// schema made nullable.
func refersTo(p *openapi3.SchemaRef, name string) bool {
	ref := "#/components/schemas/" + name
	if p.Ref == ref {
		return true
	}
	return p.Value.Nullable && len(p.Value.AllOf) == 1 && p.Value.AllOf[0].Ref == ref
}

// T25: a date is RFC 3339 in UTC with exactly three digits of milliseconds,
// in the schema and in every example of the document.
func TestSpecDates(t *testing.T) {
	doc := loadSpec(t)

	ts := doc.Components.Schemas["Timestamp"]
	if ts == nil {
		t.Fatal("no Timestamp schema")
	}
	if ts.Value.Format != "date-time" || ts.Value.Pattern == "" {
		t.Fatalf("Timestamp must be a date-time with the pattern of the one format")
	}
	pattern := regexp.MustCompile(ts.Value.Pattern)
	for value, want := range map[string]bool{
		"2026-09-30T12:34:56.000Z":      true,
		"2026-09-30T12:34:56.789Z":      true,
		"2026-09-30T12:34:56Z":          false,
		"2026-09-30T12:34:56.7Z":        false,
		"2026-09-30T12:34:56.789123Z":   false,
		"2026-09-30T12:34:56.000+00:00": false,
		"2026-09-30T14:34:56.000+02:00": false,
		"2026-09-30 12:34:56.000Z":      false,
		"2026-09-30T12:34:56.000z":      false,
		"1759235696000":                 false,
	} {
		if got := pattern.MatchString(value); got != want {
			t.Errorf("Timestamp pattern matches %q = %t, want %t", value, got, want)
		}
		err := ts.Value.VisitJSON(value, openapi3.EnableFormatValidation())
		if (err == nil) != want {
			t.Errorf("Timestamp accepts %q = %t, want %t (%v)", value, err == nil, want, err)
		}
	}

	// Every string of the document that begins as a date is one in that
	// format: examples of schemas, of responses, of parameters.
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the specification: %v", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("reading the specification back: %v", err)
	}
	looksLikeDate := regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[T ]`)
	dates := 0
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		case string:
			if looksLikeDate.MatchString(v) {
				dates++
				if !pattern.MatchString(v) {
					t.Errorf("the date %q is not in the format of T25 (three digits of milliseconds, Z)", v)
				}
			}
		}
	}
	walk(tree)
	if dates == 0 {
		t.Error("no example with a date was found: the walk does not see the examples")
	}
}

// §8.4: every stable code is in the specification, as a value of Error.code
// and in the description of a response or of an operation.
func TestSpecErrorCodes(t *testing.T) {
	doc := loadSpec(t)

	code := doc.Components.Schemas["Error"].Value.Properties["code"].Value
	var enum []string
	for _, v := range code.Enum {
		enum = append(enum, fmt.Sprint(v))
	}
	want := slices.Clone(designErrorCodes)
	got := slices.Clone(enum)
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("Error.code enum differs from DESIGN.md §8.4:\n got  %v\n want %v", got, want)
	}

	var text strings.Builder
	text.WriteString(doc.Info.Description)
	for _, ref := range doc.Components.Responses {
		text.WriteString("\n" + *ref.Value.Description)
	}
	for _, o := range operations(doc) {
		text.WriteString("\n" + o.documented())
	}
	for _, c := range designErrorCodes {
		if !strings.Contains(text.String(), "`"+c+"`") && !regexp.MustCompile("`[0-9]{3} "+c+"`").MatchString(text.String()) {
			t.Errorf("code %q is explained by no response and no operation", c)
		}
		// The description of the field lists every code under its status.
		if !strings.Contains(code.Description, "`"+c+"`") {
			t.Errorf("code %q is not in the description of Error.code", c)
		}
	}

	// The example of every shared error response is an Error with a code
	// of the enum, under a status that response is used for.
	errorSchema := doc.Components.Schemas["Error"].Value
	for name, ref := range doc.Components.Responses {
		media := ref.Value.Content.Get("application/json")
		if media == nil {
			continue
		}
		if err := errorSchema.VisitJSON(media.Example, openapi3.MultiErrors()); err != nil {
			t.Errorf("the example of response %q is not an Error: %v", name, err)
		}
	}
}

// §8.1, §8.6: where If-Match is taken, where it is required, and where the
// ETag is given.
func TestSpecPlaylistPreconditions(t *testing.T) {
	doc := loadSpec(t)

	type rule struct {
		ifMatch  bool // takes If-Match, answers 412
		required bool // answers 428 without it
		etag     bool // gives the ETag header on success
	}
	rules := map[string]rule{
		"createPlaylist":     {etag: true},
		"getPlaylist":        {etag: true},
		"listPlaylistItems":  {etag: true},
		"updatePlaylist":     {ifMatch: true, etag: true},
		"deletePlaylist":     {ifMatch: true},
		"removePlaylistItem": {ifMatch: true, etag: true},
		"addPlaylistItems":   {ifMatch: true, required: true, etag: true},
		"movePlaylistItem":   {ifMatch: true, required: true, etag: true},
	}
	for _, o := range operations(doc) {
		id := o.op.OperationID
		r := rules[id]
		takes := false
		for _, p := range o.op.Parameters {
			if p.Value.In == "header" && p.Value.Name == "If-Match" {
				takes = true
			}
		}
		if takes != r.ifMatch {
			t.Errorf("%s: takes If-Match = %t, want %t", id, takes, r.ifMatch)
		}
		if want := r.ifMatch || servesFiles(id); o.has(412) != want {
			t.Errorf("%s: declares 412 = %t, want %t", id, o.has(412), want)
		}
		if o.has(428) != r.required {
			t.Errorf("%s: declares 428 = %t, want %t", id, o.has(428), r.required)
		}
		if !strings.HasPrefix(o.path, "/playlists") {
			continue
		}
		for key, ref := range o.op.Responses.Map() {
			if status, err := strconv.Atoi(key); err != nil || status >= 300 {
				continue
			}
			_, gives := ref.Value.Headers["ETag"]
			if gives != r.etag {
				t.Errorf("%s %s: gives the ETag header = %t, want %t", id, key, gives, r.etag)
			}
		}
	}

	// The body carries the tag too: a proxy may weaken the header.
	if !slices.Contains(doc.Components.Schemas["Playlist"].Value.Required, "etag") {
		t.Error("Playlist must carry its etag in the body")
	}
}

// requestHeaderRef is how an operation declares X-Vibrance-Request.
const requestHeaderRef = "#/components/parameters/XVibranceRequest"

// I4, §7.6: X-Vibrance-Request is a parameter of the specification, one
// component that every operation other than a GET refers to, and no GET
// does: a generated client and the documentation page send it without
// being told. It is required, with "1" as its only value, and it is not a
// security scheme, because it is not a credential.
func TestSpecRequestHeader(t *testing.T) {
	doc := loadSpec(t)

	ref := doc.Components.Parameters["XVibranceRequest"]
	if ref == nil {
		t.Fatal("the parameter XVibranceRequest is not a component of the specification")
	}
	p := ref.Value
	if p.Name != httpx.RequestHeader || p.In != "header" || !p.Required || p.Description == "" {
		t.Errorf("the parameter is %q in %q, required = %t: want the required header %s, with a description",
			p.Name, p.In, p.Required, httpx.RequestHeader)
	}
	if s := p.Schema.Value; !s.Type.Is("string") || !reflect.DeepEqual(s.Enum, []any{"1"}) {
		t.Errorf("the schema of the parameter is %v with the values %v: want a string that is \"1\"", s.Type, s.Enum)
	}
	if !strings.Contains(p.Description, "`403 request_header_required`") {
		t.Error("the description of the parameter must name `403 request_header_required`")
	}

	writes := 0
	for _, o := range operations(doc) {
		declared := 0
		for _, p := range o.op.Parameters {
			switch {
			case p.Ref == requestHeaderRef:
				declared++
			case p.Value.In == "header" && strings.EqualFold(p.Value.Name, httpx.RequestHeader):
				t.Errorf("%s declares %s by itself: it must refer to the component", o.op.OperationID, httpx.RequestHeader)
			}
		}
		want := 1
		if o.method == http.MethodGet {
			want = 0
		} else {
			writes++
		}
		if declared != want {
			t.Errorf("%s (%s) declares %s %d times, want %d", o.op.OperationID, o.method, httpx.RequestHeader, declared, want)
		}
		// The answer to a request without it is declared too.
		if ref := o.op.Responses.Value("403"); ref == nil || ref.Ref != "#/components/responses/Forbidden" {
			t.Errorf("%s: want the response 403 Forbidden", o.op.OperationID)
		}
	}
	// The operations of DESIGN.md §8.3 that are not a GET.
	if writes != 18 {
		t.Errorf("%d operations other than GET, want 18", writes)
	}
	for name, scheme := range doc.Components.SecuritySchemes {
		if name != "cookieAuth" && name != "bearerAuth" || strings.EqualFold(scheme.Value.Name, httpx.RequestHeader) {
			t.Errorf("the security scheme %q: the header is not a credential, and the schemes are the cookie and the bearer token", name)
		}
	}
}

// §8.1, §8.5: the paginated lists and their limits; §10.2 for the search.
func TestSpecLists(t *testing.T) {
	doc := loadSpec(t)

	paginated := map[string]string{
		"listArtists":        "artists",
		"listAlbums":         "albums",
		"listFavoriteTracks": "favorites",
		"listPlaylistItems":  "items",
	}
	for _, o := range operations(doc) {
		id := o.op.OperationID
		params := map[string]*openapi3.Parameter{}
		for _, p := range o.op.Parameters {
			if p.Value.In == "query" {
				params[p.Value.Name] = p.Value
			}
		}
		list, isPaginated := paginated[id]
		if _, has := params["after"]; has != isPaginated {
			t.Errorf("%s: takes `after` = %t, want %t", id, has, isPaginated)
		}
		limit, hasLimit := params["limit"]
		if hasLimit != (isPaginated || id == "search") {
			t.Errorf("%s: takes `limit` = %t", id, hasLimit)
		}
		if !isPaginated {
			continue
		}
		checkLimit(t, id, limit, 50, 200)

		body := o.op.Responses.Value("200").Value.Content.Get("application/json").Schema.Value
		if got := slices.Sorted(maps.Keys(body.Properties)); !slices.Equal(got, slices.Sorted(slices.Values([]string{list, "next"}))) {
			t.Errorf("%s: the page has %v, want %s and next", id, got, list)
			continue
		}
		if !body.Properties[list].Value.Type.Is("array") {
			t.Errorf("%s: %s must be an array", id, list)
		}
		next := body.Properties["next"].Value
		if !next.Type.Is("string") || !next.Nullable {
			t.Errorf("%s: next must be a string or null", id)
		}
	}

	for _, o := range operations(doc) {
		if o.op.OperationID != "search" {
			continue
		}
		for _, p := range o.op.Parameters {
			switch p.Value.Name {
			case "limit":
				checkLimit(t, "search", p.Value, 10, 50)
			case "q":
				s := p.Value.Schema.Value
				if !p.Value.Required || s.MinLength != 1 || s.MaxLength == nil || *s.MaxLength != 100 {
					t.Errorf("search: q must be required, 1 to 100 characters")
				}
			case "types":
				s := p.Value.Schema.Value
				if p.Value.Explode == nil || *p.Value.Explode || !s.Type.Is("array") ||
					fmt.Sprint(s.Items.Value.Enum) != "[artist album track]" {
					t.Errorf("search: types must be a comma-separated list of artist, album, track")
				}
			}
		}
	}
}

func checkLimit(t *testing.T, id string, p *openapi3.Parameter, def, max float64) {
	t.Helper()
	s := p.Schema.Value
	if !s.Type.Is("integer") || s.Min == nil || *s.Min != 1 || s.Max == nil || *s.Max != max || fmt.Sprint(s.Default) != fmt.Sprint(def) {
		t.Errorf("%s: limit must be an integer from 1 to %v, default %v", id, max, def)
	}
}

// §9: what the media operations declare beyond JSON.
func TestSpecMedia(t *testing.T) {
	doc := loadSpec(t)

	want := map[string][]string{
		"getTrackAudio":  {"audio/flac", "audio/mp4", "audio/mpeg"},
		"getAlbumCover":  {"image/jpeg", "image/png"},
		"getTrackLyrics": {"application/json"},
	}
	for _, o := range operations(doc) {
		id := o.op.OperationID
		types, isMedia := want[id]

		// 503 library_changing comes with Retry-After, and only the media
		// answer it.
		unavailable := o.op.Responses.Value("503")
		if unavailable == nil {
			continue // reported by TestSpecCompleteness
		}
		_, retry := unavailable.Value.Headers["Retry-After"]
		if retry != isMedia {
			t.Errorf("%s: 503 declares Retry-After = %t, want %t", id, retry, isMedia)
		}
		if !isMedia {
			continue
		}
		ok := o.op.Responses.Value("200").Value
		if got := slices.Sorted(maps.Keys(ok.Content)); !slices.Equal(got, types) {
			t.Errorf("%s: content types %v, want %v", id, got, types)
		}
		if _, has := ok.Headers["ETag"]; !has {
			t.Errorf("%s: 200 must declare the ETag header", id)
		}
		// HEAD is answered wherever GET is (§8.1); the router of net/http
		// does it for a GET pattern, and the description says so.
		if !strings.Contains(o.op.Description, "HEAD") {
			t.Errorf("%s: the description must say that HEAD is answered", id)
		}
	}

	// D10: `profile` is reserved. An enum would turn the answer of the
	// handler, 400 unsupported_profile, into the validator's invalid_request.
	for _, o := range operations(doc) {
		for _, p := range o.op.Parameters {
			if p.Value.Name == "profile" && (len(p.Value.Schema.Value.Enum) != 0 || p.Value.Required) {
				t.Error("getTrackAudio: profile must be an optional string without an enum")
			}
		}
	}

	// The session cookie is set by login and dropped by logout.
	for id, status := range map[string]string{"login": "200", "logout": "204"} {
		for _, o := range operations(doc) {
			if o.op.OperationID != id {
				continue
			}
			if _, has := o.op.Responses.Value(status).Value.Headers["Set-Cookie"]; !has {
				t.Errorf("%s %s must declare Set-Cookie", id, status)
			}
		}
	}
	// I5: a token is given once, by createToken, and no schema but its
	// answer has a field for a secret.
	for name, ref := range doc.Components.Schemas {
		for field := range ref.Value.Properties {
			secret := strings.Contains(field, "password") || strings.Contains(field, "token") || strings.Contains(field, "hash")
			allowed := requestSchemas(doc)[name] || (name == "TokenResult" && field == "token") || (name == "Cover" && field == "hash")
			if secret && !allowed {
				t.Errorf("schema %s has the field %q: responses never carry passwords, tokens or hashes (I5)", name, field)
			}
		}
	}
}

// The statuses of the table of the test are real ones: a typo there would
// make TestSpecMatchesDesign ask the specification for a wrong response.
func TestDesignTableIsWellFormed(t *testing.T) {
	codes := map[string]bool{}
	for _, c := range designErrorCodes {
		if codes[c] {
			t.Errorf("code %q is listed twice", c)
		}
		codes[c] = true
	}
	for _, d := range designOperations {
		if http.StatusText(d.success) == "" || d.success >= 300 {
			t.Errorf("%s: success status %d", d.id, d.success)
		}
		for _, e := range d.errors {
			if http.StatusText(e.status) == "" || e.status < 400 {
				t.Errorf("%s: error status %d", d.id, e.status)
			}
			if !codes[e.code] {
				t.Errorf("%s: code %q is not in the list of DESIGN.md §8.4", d.id, e.code)
			}
		}
	}
}

// servesFiles tells whether the operation serves a file with
// http.ServeContent (§9.1, §9.2), which answers ranges and conditions
// itself.
func servesFiles(id string) bool { return id == "getTrackAudio" || id == "getAlbumCover" }
