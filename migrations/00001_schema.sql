-- DESIGN.md §5.2: the whole schema, except the full-text tables, which are
-- kept out of sqlc and live in 00002_search.sql. Forward only (I13): there is
-- no Down section.
--
-- Conventions (§5.1). Timestamps are integers, Unix milliseconds in UTC. Ids
-- are lowercase UUIDs, 36 characters of text. Booleans are integers 0 or 1.
-- A column is NOT NULL unless §5.2 marks it optional. Every CHECK has a name,
-- <table>_<column>_check, so that a violation says which rule failed.
--
-- artists, albums and tracks have an explicit integer key, seq, beside their
-- text id. It is the rowid, which the full-text tables point to: a rowid
-- that is not declared may be renumbered by VACUUM, and by the VACUUM INTO
-- of the backup (T6). Their rows are never deleted (I3), so the foreign keys
-- to them are ON DELETE RESTRICT; only what belongs to a user goes away with
-- the user (ON DELETE CASCADE).

-- +goose Up

CREATE TABLE meta (
    "key" text NOT NULL PRIMARY KEY,
    value text NOT NULL
);

CREATE TABLE users (
    id                  text    NOT NULL PRIMARY KEY,
    -- §7.1: ^[a-z0-9][a-z0-9._-]{2,31}$, written with GLOB because SQLite has
    -- no regular expressions of its own.
    username            text    NOT NULL UNIQUE
        CONSTRAINT users_username_lower_check CHECK (username = lower(username))
        CONSTRAINT users_username_check CHECK (
            length(username) BETWEEN 3 AND 32
            AND username GLOB '[a-z0-9]*'
            AND username NOT GLOB '*[^a-z0-9._-]*'),
    password_hash       text    NOT NULL,
    role                text    NOT NULL
        CONSTRAINT users_role_check CHECK (role IN ('admin', 'user')),
    disabled            integer NOT NULL DEFAULT 0
        CONSTRAINT users_disabled_check CHECK (disabled IN (0, 1)),
    created_at          integer NOT NULL,
    password_changed_at integer NOT NULL
);

CREATE TABLE sessions (
    id           text    NOT NULL PRIMARY KEY,
    token_hash   text    NOT NULL UNIQUE,
    user_id      text    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         text    NOT NULL
        CONSTRAINT sessions_kind_check CHECK (kind IN ('cookie', 'token')),
    device_name  text
        CONSTRAINT sessions_device_name_check CHECK (length(device_name) <= 100),
    created_at   integer NOT NULL,
    last_used_at integer NOT NULL,
    expires_at   integer NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE artists (
    seq      integer PRIMARY KEY,
    id       text    NOT NULL UNIQUE,
    name     text    NOT NULL,
    sort_key blob    NOT NULL
);

CREATE TABLE albums (
    seq            integer PRIMARY KEY,
    id             text    NOT NULL UNIQUE,
    artist_id      text    NOT NULL REFERENCES artists (id) ON DELETE RESTRICT,
    artist_key     blob    NOT NULL,
    title          text    NOT NULL,
    title_key      blob    NOT NULL,
    year           integer
        CONSTRAINT albums_year_check CHECK (year BETWEEN 1 AND 9999),
    -- NULL sorts differently in the two directions (T9): an album without a
    -- year has the key 10000, after every real year.
    year_key       integer NOT NULL,
    genre          text,
    compilation    integer NOT NULL
        CONSTRAINT albums_compilation_check CHECK (compilation IN (0, 1)),
    rel_path       text    NOT NULL,
    album_revision integer NOT NULL,
    render_version text    NOT NULL,
    receipt_hash   text    NOT NULL,
    cover_rel      text
        CONSTRAINT albums_cover_rel_check CHECK (cover_rel IN ('cover.jpg', 'cover.png')),
    cover_sha256   text,
    cover_mime     text
        CONSTRAINT albums_cover_mime_check CHECK (cover_mime IN ('image/jpeg', 'image/png')),
    cover_size     integer,
    cover_mtime_ns integer,
    track_count    integer NOT NULL,
    duration_ms    integer NOT NULL,
    available      integer NOT NULL
        CONSTRAINT albums_available_check CHECK (available IN (0, 1)),
    first_seen_at  integer NOT NULL,
    updated_at     integer NOT NULL,
    CONSTRAINT albums_year_key_check CHECK (year_key = coalesce(year, 10000))
);

-- The orders of the album lists (§8.5), each led by available, and the
-- albums of one artist.
CREATE INDEX albums_title_idx ON albums (available, title_key, id);
CREATE INDEX albums_artist_idx ON albums (available, artist_key, year_key, title_key, id);
CREATE INDEX albums_year_idx ON albums (available, year_key, title_key, id);
CREATE INDEX albums_first_seen_idx ON albums (available, first_seen_at, id);
CREATE INDEX albums_artist_id_idx ON albums (artist_id, available);

CREATE TABLE tracks (
    seq           integer PRIMARY KEY,
    id            text    NOT NULL UNIQUE,
    album_id      text    NOT NULL REFERENCES albums (id) ON DELETE RESTRICT,
    fingerprint   text    NOT NULL,
    fp_version    text    NOT NULL,
    occurrence    integer NOT NULL DEFAULT 1
        CONSTRAINT tracks_occurrence_check CHECK (occurrence >= 1),
    disc          integer NOT NULL
        CONSTRAINT tracks_disc_check CHECK (disc BETWEEN 1 AND 99),
    "no"          integer NOT NULL
        CONSTRAINT tracks_no_check CHECK ("no" BETWEEN 1 AND 999),
    title         text    NOT NULL,
    artist        text    NOT NULL,
    genre         text,
    rel_path      text    NOT NULL,
    file_size     integer NOT NULL,
    file_mtime_ns integer NOT NULL,
    file_sha256   text    NOT NULL,
    codec         text    NOT NULL
        CONSTRAINT tracks_codec_check CHECK (codec IN ('flac', 'mp3', 'aac', 'alac')),
    sample_rate   integer NOT NULL,
    channels      integer NOT NULL,
    bit_depth     integer,
    bitrate       integer,
    duration_ms   integer,
    lyrics_rel    text,
    lyrics_sha256 text,
    rg_track_gain real,
    rg_track_peak real,
    rg_album_gain real,
    rg_album_peak real,
    available     integer NOT NULL
        CONSTRAINT tracks_available_check CHECK (available IN (0, 1)),
    updated_at    integer NOT NULL
);

CREATE INDEX tracks_album_disc_no_idx ON tracks (album_id, disc, "no");
-- The natural key of a track (§5.4), and the only uniqueness rule on tracks:
-- the same audio may appear twice in an album, told apart by occurrence.
CREATE UNIQUE INDEX tracks_album_fingerprint_occurrence_idx ON tracks (album_id, fingerprint, occurrence);

CREATE TABLE favorites (
    user_id    text    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    track_id   text    NOT NULL REFERENCES tracks (id) ON DELETE RESTRICT,
    created_at integer NOT NULL,
    PRIMARY KEY (user_id, track_id)
);

CREATE INDEX favorites_user_created_idx ON favorites (user_id, created_at, track_id);

CREATE TABLE playlists (
    id          text    NOT NULL PRIMARY KEY,
    user_id     text    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text    NOT NULL
        CONSTRAINT playlists_name_check CHECK (length(name) BETWEEN 1 AND 200),
    description text    NOT NULL DEFAULT ''
        CONSTRAINT playlists_description_check CHECK (length(description) <= 2000),
    revision    integer NOT NULL
        CONSTRAINT playlists_revision_check CHECK (revision > 0),
    created_at  integer NOT NULL,
    updated_at  integer NOT NULL
);

CREATE TABLE playlist_items (
    id          text    NOT NULL PRIMARY KEY,
    playlist_id text    NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    track_id    text    NOT NULL REFERENCES tracks (id) ON DELETE RESTRICT,
    -- Not unique: SQLite cannot defer a UNIQUE constraint, and a block of
    -- positions is renumbered within one transaction (T5).
    position    integer NOT NULL
        CONSTRAINT playlist_items_position_check CHECK (position >= 0),
    added_at    integer NOT NULL
);

CREATE INDEX playlist_items_position_idx ON playlist_items (playlist_id, position, id);
