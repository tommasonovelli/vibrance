-- The preferences of a user, GET and PATCH /me/settings
-- (docs/proposals/web-client-api.md B5, step W4 of DESIGN.md). Forward only
-- (I13): there is no Down section.
--
-- One row per user who saved a preference at least once; a user without a
-- row has the defaults, which live in one place, internal/auth, and the
-- first change writes the row with every value. The row belongs to the
-- user and goes away with the account (ON DELETE CASCADE). The settings are
-- not secret.

-- +goose Up

CREATE TABLE settings (
    user_id              text    NOT NULL PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    volume_leveling      text    NOT NULL
        CONSTRAINT settings_volume_leveling_check CHECK (volume_leveling IN ('automatic', 'off')),
    single_key_shortcuts integer NOT NULL
        CONSTRAINT settings_single_key_shortcuts_check CHECK (single_key_shortcuts IN (0, 1)),
    theme                text    NOT NULL
        CONSTRAINT settings_theme_check CHECK (theme IN ('dark', 'light'))
);
