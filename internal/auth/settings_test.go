package auth

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
)

// settingsRows counts the rows of the settings of the account with that id.
func (f *fixture) settingsRows(t *testing.T, userID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	scanErr := db.QueryRowContext(t.Context(), `SELECT count(*) FROM settings WHERE user_id = ?`, userID).Scan(&n)
	if err := errors.Join(scanErr, db.Close()); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) settings(t *testing.T, p Principal) Settings {
	t.Helper()
	got, err := f.Settings(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f *fixture) updateSettings(t *testing.T, p Principal, u SettingsUpdate) Settings {
	t.Helper()
	got, err := f.UpdateSettings(t.Context(), p, u)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ptr[T any](v T) *T { return &v }

// docs/proposals/web-client-api.md B5: a user who never saved a preference
// has the defaults, and reading them writes nothing; a change sets the
// fields it sends and keeps the others, and the answer is every
// preference as saved.
func TestSettingsDefaultsAndPartialChanges(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	p := f.principal(t, f.login(t, "alice", alicePassword))

	want := Settings{VolumeLeveling: "automatic", SingleKeyShortcuts: false, Theme: "dark"}
	if DefaultSettings != want {
		t.Fatalf("the defaults are %+v, want %+v", DefaultSettings, want)
	}
	if got := f.settings(t, p); got != want {
		t.Fatalf("no preference saved: %+v, want the defaults %+v", got, want)
	}
	if n := f.settingsRows(t, alice.ID); n != 0 {
		t.Fatalf("reading the defaults wrote %d rows", n)
	}

	steps := []struct {
		name string
		u    SettingsUpdate
		want Settings
	}{
		{"the theme alone", SettingsUpdate{Theme: ptr(ThemeLight)}, Settings{VolumeLevelingAutomatic, false, ThemeLight}},
		{"the leveling alone", SettingsUpdate{VolumeLeveling: ptr(VolumeLevelingOff)}, Settings{VolumeLevelingOff, false, ThemeLight}},
		{"the shortcuts alone", SettingsUpdate{SingleKeyShortcuts: ptr(true)}, Settings{VolumeLevelingOff, true, ThemeLight}},
		{"nothing", SettingsUpdate{}, Settings{VolumeLevelingOff, true, ThemeLight}},
		{"the same value again", SettingsUpdate{Theme: ptr(ThemeLight)}, Settings{VolumeLevelingOff, true, ThemeLight}},
		{"false is a value, not an absence", SettingsUpdate{SingleKeyShortcuts: ptr(false)}, Settings{VolumeLevelingOff, false, ThemeLight}},
		{"two fields", SettingsUpdate{VolumeLeveling: ptr(VolumeLevelingAutomatic), Theme: ptr(ThemeDark)}, Settings{VolumeLevelingAutomatic, false, ThemeDark}},
		{"every field", SettingsUpdate{ptr(VolumeLevelingOff), ptr(true), ptr(ThemeLight)}, Settings{VolumeLevelingOff, true, ThemeLight}},
	}
	for _, s := range steps {
		if got := f.updateSettings(t, p, s.u); got != s.want {
			t.Fatalf("%s: answered %+v, want %+v", s.name, got, s.want)
		}
		if got := f.settings(t, p); got != s.want {
			t.Fatalf("%s: read back %+v, want %+v", s.name, got, s.want)
		}
	}
	if n := f.settingsRows(t, alice.ID); n != 1 {
		t.Fatalf("%d rows of settings, want 1", n)
	}

	// Every session of the user, cookie or token, has the same preferences.
	other := f.principal(t, f.token(t, "alice", alicePassword))
	if got, want := f.settings(t, other), steps[len(steps)-1].want; got != want {
		t.Fatalf("another session of the user: %+v, want %+v", got, want)
	}
}

// The first change of a user without a row starts from the defaults:
// the fields it does not send get them.
func TestSettingsFirstChangeKeepsTheDefaults(t *testing.T) {
	for _, tc := range []struct {
		u    SettingsUpdate
		want Settings
	}{
		{SettingsUpdate{Theme: ptr(ThemeLight)}, Settings{VolumeLevelingAutomatic, false, ThemeLight}},
		{SettingsUpdate{VolumeLeveling: ptr(VolumeLevelingOff)}, Settings{VolumeLevelingOff, false, ThemeDark}},
		{SettingsUpdate{SingleKeyShortcuts: ptr(true)}, Settings{VolumeLevelingAutomatic, true, ThemeDark}},
		{SettingsUpdate{}, DefaultSettings},
	} {
		f := newFixture(t)
		f.user(t, "alice", alicePassword, RoleUser)
		p := f.principal(t, f.login(t, "alice", alicePassword))
		if got := f.updateSettings(t, p, tc.u); got != tc.want {
			t.Fatalf("the first change %+v: %+v, want %+v", tc.u, got, tc.want)
		}
		if got := f.settings(t, p); got != tc.want {
			t.Fatalf("after the first change %+v: %+v, want %+v", tc.u, got, tc.want)
		}
	}
}

// The preferences of one user are not those of another, and they go away
// with the account (§7.5), and nobody else's with it.
func TestSettingsBelongToTheirUser(t *testing.T) {
	f := newFixture(t)
	f.user(t, "admin", otherPassword, RoleAdmin)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	bob := f.user(t, "bob", bobPassword, RoleUser)
	admin := f.principal(t, f.login(t, "admin", otherPassword))
	pa := f.principal(t, f.login(t, "alice", alicePassword))
	pb := f.principal(t, f.login(t, "bob", bobPassword))

	aliceWants := f.updateSettings(t, pa, SettingsUpdate{Theme: ptr(ThemeLight), SingleKeyShortcuts: ptr(true)})
	if got := f.settings(t, pb); got != DefaultSettings {
		t.Fatalf("bob after alice changed hers: %+v, want the defaults", got)
	}
	bobWants := f.updateSettings(t, pb, SettingsUpdate{VolumeLeveling: ptr(VolumeLevelingOff)})
	if want := (Settings{VolumeLevelingOff, false, ThemeDark}); bobWants != want {
		t.Fatalf("bob: %+v, want %+v", bobWants, want)
	}
	if got := f.settings(t, pa); got != aliceWants {
		t.Fatalf("alice after bob changed his: %+v, want %+v", got, aliceWants)
	}

	if err := f.DeleteUser(t.Context(), admin, alice.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.settingsRows(t, alice.ID); n != 0 {
		t.Fatalf("the account is deleted and %d rows of its settings are left", n)
	}
	if n := f.settingsRows(t, bob.ID); n != 1 {
		t.Fatalf("bob has %d rows of settings after the deletion of alice, want 1", n)
	}
	if got := f.settings(t, pb); got != bobWants {
		t.Fatalf("bob after the deletion of alice: %+v, want %+v", got, bobWants)
	}
}

// A value outside the enums never reaches the service through the API (the
// validation of the specification refuses it): if it did, the database
// would refuse it, and nothing would change.
func TestSettingsOutsideTheDomainAreRefusedByTheDatabase(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", alicePassword, RoleUser)
	p := f.principal(t, f.login(t, "alice", alicePassword))
	saved := f.updateSettings(t, p, SettingsUpdate{Theme: ptr(ThemeLight)})
	for _, u := range []SettingsUpdate{{Theme: ptr("auto")}, {VolumeLeveling: ptr("Off")}, {Theme: ptr("")}} {
		got, err := f.UpdateSettings(t.Context(), p, u)
		if err == nil || refusalCode(err) != "" {
			t.Fatalf("the change %+v: %+v, %v; want an error of the database", u, got, err)
		}
	}
	if got := f.settings(t, p); got != saved {
		t.Fatalf("after refused changes: %+v, want %+v", got, saved)
	}
	if n := f.settingsRows(t, alice.ID); n != 1 {
		t.Fatalf("%d rows of settings, want 1", n)
	}
}

// Changes of different fields at once, from different devices, all stay:
// each reads the row and writes it in one transaction, so none writes back
// a value it read before another changed it.
func TestSettingsConcurrentChangesOfDifferentFields(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice", alicePassword, RoleUser)
	p := f.principal(t, f.login(t, "alice", alicePassword))
	for round := range 10 {
		// Each round flips every field, from three goroutines at once.
		want := Settings{VolumeLevelingOff, true, ThemeLight}
		if round%2 == 1 {
			want = DefaultSettings
		}
		updates := []SettingsUpdate{
			{VolumeLeveling: ptr(want.VolumeLeveling)},
			{SingleKeyShortcuts: ptr(want.SingleKeyShortcuts)},
			{Theme: ptr(want.Theme)},
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(updates)*5)
		for _, u := range updates {
			for range 5 {
				wg.Go(func() {
					if _, err := f.UpdateSettings(t.Context(), p, u); err != nil {
						errs <- err
					}
				})
			}
		}
		wg.Go(func() {
			// A reader at the same time sees a whole row, never an error.
			if _, err := f.Settings(t.Context(), p); err != nil {
				errs <- err
			}
		})
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		if got := f.settings(t, p); got != want {
			t.Fatalf("round %d: %+v, want %+v: a change was lost", round, got, want)
		}
	}
}
