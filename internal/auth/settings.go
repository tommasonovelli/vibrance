package auth

import (
	"context"
	"fmt"

	"vibrance/internal/store"
)

// The values of the preferences (docs/proposals/web-client-api.md B5). The
// database refuses any other (migrations/00005_settings.sql).
const (
	VolumeLevelingAutomatic = "automatic"
	VolumeLevelingOff       = "off"
	ThemeDark               = "dark"
	ThemeLight              = "light"
)

// Settings are the preferences of a user, the same on every device.
type Settings struct {
	// VolumeLeveling is VolumeLevelingAutomatic or VolumeLevelingOff.
	VolumeLeveling     string
	SingleKeyShortcuts bool
	// Theme is ThemeDark or ThemeLight.
	Theme string
}

// DefaultSettings are the preferences of a user who never saved any. They
// are the only copy of the defaults: the database keeps no default of its
// own, and a row always holds every value.
var DefaultSettings = Settings{VolumeLeveling: VolumeLevelingAutomatic, SingleKeyShortcuts: false, Theme: ThemeDark}

// SettingsUpdate is a change of some preferences: a nil field keeps the
// value the user has.
type SettingsUpdate struct {
	VolumeLeveling     *string
	SingleKeyShortcuts *bool
	Theme              *string
}

// apply returns the preferences current with the fields of u that are set.
func (u SettingsUpdate) apply(current Settings) Settings {
	if u.VolumeLeveling != nil {
		current.VolumeLeveling = *u.VolumeLeveling
	}
	if u.SingleKeyShortcuts != nil {
		current.SingleKeyShortcuts = *u.SingleKeyShortcuts
	}
	if u.Theme != nil {
		current.Theme = *u.Theme
	}
	return current
}

// Settings returns the preferences of the user of the request, or
// DefaultSettings when the user never saved any.
func (s *Service) Settings(ctx context.Context, p Principal) (Settings, error) {
	var settings Settings
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		settings, err = settingsOf(ctx, q, p.UserID)
		return err
	})
	if err != nil {
		return Settings{}, fmt.Errorf("auth: reading the settings: %w", err)
	}
	return settings, nil
}

// UpdateSettings changes the preferences of the user of the request that u
// sets and keeps the others, and returns every preference as saved. The
// reading of the current values and the writing of the new ones are one
// transaction: two changes of different fields at once both stay.
func (s *Service) UpdateSettings(ctx context.Context, p Principal, u SettingsUpdate) (Settings, error) {
	var saved Settings
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		current, err := settingsOf(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		saved = u.apply(current)
		return q.PutSettings(ctx, store.PutSettingsParams{UserID: p.UserID, VolumeLeveling: saved.VolumeLeveling,
			SingleKeyShortcuts: flag(saved.SingleKeyShortcuts), Theme: saved.Theme})
	})
	if err != nil {
		return Settings{}, fmt.Errorf("auth: changing the settings: %w", err)
	}
	return saved, nil
}

// settingsOf reads the preferences of the user id in q: the defaults when
// there is no row.
func settingsOf(ctx context.Context, q *store.Queries, userID string) (Settings, error) {
	row, err := q.GetSettings(ctx, userID)
	switch {
	case noRows(ctx, err):
		return DefaultSettings, nil
	case err != nil:
		return Settings{}, err
	}
	return Settings{VolumeLeveling: row.VolumeLeveling, SingleKeyShortcuts: row.SingleKeyShortcuts == 1, Theme: row.Theme}, nil
}
