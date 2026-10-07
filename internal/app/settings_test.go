package app

import (
	"encoding/json"
	"net/http"
	"testing"

	"vibrance/internal/api"
	"vibrance/internal/auth"
	"vibrance/internal/store"
)

// The preferences of step W4 (docs/proposals/web-client-api.md B5) over the
// API: the defaults, the changes of some fields, the refusals of the
// validation, and whose they are. Every answer is checked against the
// specification (I10). The rules of the service are proved in
// internal/auth.

var defaultSettings = api.Settings{VolumeLeveling: "automatic", SingleKeyShortcuts: false, Theme: "dark"}

// settings reads the preferences with a credential.
func (w *world) settings(c credential) api.Settings {
	w.t.Helper()
	rec := w.do(http.MethodGet, "/me/settings", nil, c)
	wantStatus(w.t, "GET /me/settings", rec, http.StatusOK)
	return decode[api.Settings](w.t, rec)
}

// patchSettings changes the preferences with a credential and returns the
// answer.
func (w *world) patchSettings(c credential, body any) api.Settings {
	w.t.Helper()
	rec := w.do(http.MethodPatch, "/me/settings", body, c)
	wantStatus(w.t, "PATCH /me/settings", rec, http.StatusOK)
	return decode[api.Settings](w.t, rec)
}

// settingsRows counts the rows of the settings of the account with that id.
func (w *world) settingsRows(userID string) int {
	w.t.Helper()
	var n int
	err := w.store.Read(w.t.Context(), func(q *store.Queries) error {
		return q.Conn().QueryRowContext(w.t.Context(), `SELECT count(*) FROM settings WHERE user_id = ?`, userID).Scan(&n)
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

func TestSettingsOverTheAPI(t *testing.T) {
	w := newWorld(t, apiOrigin)
	cookie := sessionCookie(w.cookie(w.anna).Token)
	token := bearer(w.token(w.anna).Token)

	// The defaults, for a user who never saved any; reading writes nothing.
	if got := w.settings(cookie); got != defaultSettings {
		t.Fatalf("the defaults: %+v, want %+v", got, defaultSettings)
	}
	if n := w.settingsRows(w.anna.id); n != 0 {
		t.Fatalf("reading the defaults wrote %d rows", n)
	}

	// A change sets what it sends, keeps the rest, and answers every field;
	// the other session of the user (another device) reads the same.
	for _, step := range []struct {
		body any
		want api.Settings
	}{
		{map[string]any{"theme": "light"}, api.Settings{VolumeLeveling: "automatic", SingleKeyShortcuts: false, Theme: "light"}},
		{map[string]any{"volume_leveling": "off"}, api.Settings{VolumeLeveling: "off", SingleKeyShortcuts: false, Theme: "light"}},
		{map[string]any{"single_key_shortcuts": true}, api.Settings{VolumeLeveling: "off", SingleKeyShortcuts: true, Theme: "light"}},
		{map[string]any{}, api.Settings{VolumeLeveling: "off", SingleKeyShortcuts: true, Theme: "light"}},
		{map[string]any{"single_key_shortcuts": false, "volume_leveling": "automatic"}, api.Settings{VolumeLeveling: "automatic", SingleKeyShortcuts: false, Theme: "light"}},
		{map[string]any{"volume_leveling": "off", "single_key_shortcuts": true, "theme": "dark"}, api.Settings{VolumeLeveling: "off", SingleKeyShortcuts: true, Theme: "dark"}},
	} {
		if got := w.patchSettings(cookie, step.body); got != step.want {
			t.Fatalf("PATCH %v: %+v, want %+v", step.body, got, step.want)
		}
		if got := w.settings(token); got != step.want {
			t.Fatalf("after PATCH %v, the other session reads %+v, want %+v", step.body, got, step.want)
		}
	}
	saved := w.settings(cookie)

	// What the validation refuses, before the operation runs: nothing
	// changes.
	for _, bad := range []struct {
		name string
		body json.RawMessage
	}{
		{"an unknown key", json.RawMessage(`{"theme":"light","volume":"loud"}`)},
		{"an unknown key alone", json.RawMessage(`{"language":"it"}`)},
		{"a theme outside the enum", json.RawMessage(`{"theme":"auto"}`)},
		{"a leveling outside the enum", json.RawMessage(`{"volume_leveling":"track"}`)},
		{"a leveling in upper case", json.RawMessage(`{"volume_leveling":"Off"}`)},
		{"the boolean false for off (an unquoted YAML off)", json.RawMessage(`{"volume_leveling":false}`)},
		{"a string for a boolean", json.RawMessage(`{"single_key_shortcuts":"true"}`)},
		{"a number for a boolean", json.RawMessage(`{"single_key_shortcuts":1}`)},
		{"null", json.RawMessage(`{"theme":null}`)},
		{"a duplicate key", json.RawMessage(`{"theme":"light","theme":"light"}`)},
		{"an array", json.RawMessage(`[{"theme":"light"}]`)},
		{"a JSON null body", json.RawMessage(`null`)},
	} {
		rec := w.do(http.MethodPatch, "/me/settings", bad.body, cookie)
		wantCode(t, bad.name, rec, http.StatusBadRequest, "invalid_request")
	}
	// Without X-Vibrance-Request (I4).
	req := w.request(http.MethodPatch, "/me/settings", map[string]any{"theme": "light"})
	req.Header.Del("X-Vibrance-Request")
	cookie(req)
	wantCode(t, "no X-Vibrance-Request", send(w.s.http.Handler, req), http.StatusForbidden, "request_header_required")
	if got := w.settings(cookie); got != saved {
		t.Fatalf("after the refused changes: %+v, want %+v", got, saved)
	}
}

// The preferences are of the user of the request: another user, an admin
// included, has their own, and deleting an account deletes its row and no
// other (§7.5). A new account with the same name starts from the defaults.
func TestSettingsBelongToTheirUser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna, bob, admin := w.as(w.anna), w.as(w.bob), w.as(w.admin)

	annaWants := w.patchSettings(anna, map[string]any{"theme": "light", "single_key_shortcuts": true})
	if got := w.settings(bob); got != defaultSettings {
		t.Fatalf("bob after anna changed hers: %+v", got)
	}
	if got := w.settings(admin); got != defaultSettings {
		t.Fatalf("the admin after anna changed hers: %+v", got)
	}
	bobWants := w.patchSettings(bob, map[string]any{"volume_leveling": "off"})
	if want := (api.Settings{VolumeLeveling: "off", SingleKeyShortcuts: false, Theme: "dark"}); bobWants != want {
		t.Fatalf("bob: %+v, want %+v", bobWants, want)
	}
	if got := w.settings(anna); got != annaWants {
		t.Fatalf("anna after bob changed his: %+v, want %+v", got, annaWants)
	}

	wantStatus(t, "deleteUser", w.do(http.MethodDelete, "/admin/users/"+w.anna.id, nil, admin), http.StatusNoContent)
	if a, b := w.settingsRows(w.anna.id), w.settingsRows(w.bob.id); a != 0 || b != 1 {
		t.Fatalf("%d rows of the deleted account and %d of the other, want 0 and 1", a, b)
	}
	if got := w.settings(bob); got != bobWants {
		t.Fatalf("bob after the deletion of anna: %+v, want %+v", got, bobWants)
	}
	rec := w.do(http.MethodPost, "/admin/users", map[string]any{"username": "anna", "password": w.anna.password, "role": auth.RoleUser}, admin)
	wantStatus(t, "anna again", rec, http.StatusCreated)
	w.anna.id = decode[api.User](t, rec).Id
	if got := w.settings(w.as(w.anna)); got != defaultSettings {
		t.Fatalf("a new account with the name of a deleted one: %+v, want the defaults", got)
	}
}
