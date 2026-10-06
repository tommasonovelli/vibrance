# Proposal: API additions for the web and mobile player

Status: **approved by the owner on 2026-10-06**, after a review against the contract of release 0.1.0. The owner approved every addition of sections 2 and 3 (A1–A4, B1–B6) and the serving of the web UI (C1, section 3), as a change of scope (I16). The errata of DESIGN.md of the same day turns them into the plan steps W1–W6 (section 6). Section 5 stays out of scope.

This document compares the player mockups (the "Vibrance Player" design canvas: Library, Albums with the queue panel, Album, Artist, Playlists, Playlist, Search, Now playing, Sign in, Account, Administration, sheets and menus, empty, loading and error states, keyboard shortcuts) with `api/openapi.yaml` as of commit `6890c24`; the contract of release 0.1.0 is the same, apart from line breaks. It lists every action the mockups show, the endpoint each one uses, and the endpoints and fields that are missing. Each missing piece is written as the OpenAPI fragment we expect, so that a backend engineer can implement it without guessing.

## How to use this document

- Every addition is compatible inside `/api/v1` (DESIGN.md §8.1): new operations, new response fields, new error codes. No existing operation changes its behavior.
- Each addition is a change of scope. Under I16 and DESIGN.md §0.8 the owner approves it, and the orchestrator records it as an errata entry and as a plan step before an engineer implements it. Schema changes are new migrations (I13): release 0.1.0 closed `00001` and `00002`.
- Every operation other than GET and HEAD declares the parameter `XVibranceRequest`, as every such operation of the contract does (I4).
- Items are ranked:
  - **P1**: a screen of the mockups cannot be built without it.
  - **P2**: the screen can be built today, but only with many requests or a worse experience.
  - **P3**: optional. The UI works without it.
- Section 4 lists what the client does by itself, with no API change. Section 5 lists what the mockups deliberately leave out because DESIGN.md §1.3 puts it out of scope, and the decisions left to the owner.
- Section 7 lists the behavior rules for the client that come from UX and psychology research, with the strength of the evidence. Every source in section 9 was opened and checked against what it is cited for. Section 8 lists the questions for the design system.

## 1. Coverage of the mockups

Legend: **ok** = covered by an existing operation; **new** = needs an addition from section 2 or 3; **client** = handled by the client, section 4.

### Sidebar, player bar and every page

| Element | Uses | Status |
|---|---|---|
| Lockup, nav, theme, collapse | none | client |
| "Your playlists" list with counts | `listPlaylists` (`name`, `item_count`) | ok |
| Playlist thumbnails in the sidebar, menus and cards | `Playlist.covers` | **new (A2)** |
| Favorites count in the sidebar | `GET /me/favorites/summary` | **new (A4)** |
| "Administration" shown only to admins | `getMe` (`role`) | ok |
| Problem badge on "Administration" | `getLibraryStatus` (`problems`) | ok |
| Sign out | `logout` | ok |
| Player: cover, title, artist, favorite toggle | `Track`, `album.cover.url`, `addFavoriteTrack` / `removeFavoriteTrack` | ok |
| Player: play, seek, previous, next, shuffle, repeat, volume | `getTrackAudio` with `Range`; queue in the client | ok / client |
| Volume normalization | `Track.replay_gain` | ok |

### Library (all songs)

| Element | Uses | Status |
|---|---|---|
| Paginated list of every available song | `GET /tracks` | **new (A1)** |
| Row "Artist" column and sort | `Track.artist` (the artist of the song); "Go to artist" opens the album artist (4.2) | **new (A1)** / client rule |
| Sort: Title, Artist, Album, Recently added | `GET /tracks?sort=` | **new (A1)** |
| "5,120 songs" | `GET /catalog/summary` | **new (A3)** |
| Tab "Favorites" | `listFavoriteTracks` | ok |
| "86 favorites" | `GET /me/favorites/summary` | **new (A4)** |
| Play all | `GET /tracks` paged, one page at a time as the queue needs it (4.1) | **new (A1)** |
| Shuffle the whole library | `GET /tracks/random` | **new (B1)** |
| Search launcher in the head, Ctrl/Cmd+K (one global search, see 7.5) | `search` | ok |
| Row thumbnail | `Track.album.cover.url` + `size=256` | ok |
| Heart on the row | `Track.favorite`, `addFavoriteTrack` / `removeFavoriteTrack` | ok |

### The ⋯ menu of a song (Library, Album, Artist, Search, Playlist)

| Action | Uses | Status |
|---|---|---|
| Play next, Add to queue | none | client |
| Add to playlist › (list of playlists) | `listPlaylists` | ok |
| Add to playlist › a playlist | `addPlaylistItems` with `position: null` | ok |
| Add to playlist › New playlist… | `createPlaylist`, then `addPlaylistItems` | ok |
| Check marks on the playlists that already hold the song | `GET /tracks/{id}/playlists` | **new (B4, P3)** |
| Add to / Remove from favorites | `addFavoriteTrack` / `removeFavoriteTrack` | ok |
| Go to album | `Track.album.id` | ok |
| Go to artist | `Track.album.artist.id` (see 4.2) | client rule |
| Lyrics (only for the song playing: player bar and Now playing; no longer in row menus) | `Track.has_lyrics`, `getTrackLyrics` | ok |
| Double-click or Enter on a row: play from this song, in the order of the list | the list already loaded | client |
| Select several rows (Shift/Ctrl+click) and drag them onto a playlist of the sidebar | `addPlaylistItems` with every `track_id` | ok |
| Song details (disc, number, duration, genre, format, size, ReplayGain) | `getTrack` | ok |
| Download file | `getTrackAudio` (see 4.3) | client rule |
| Playlist only: Move to top / Move to bottom, drag to reorder | `movePlaylistItem` with `If-Match` | ok |
| Playlist only: Remove from this playlist | `removePlaylistItem` | ok |
| Playlist only: Add to another playlist | `addPlaylistItems` | ok |

### Albums

| Element | Uses | Status |
|---|---|---|
| Grid, sort Title / Artist / Year / Recently added, ascending or descending | `listAlbums` | ok |
| "412 albums · 37 artists" | `GET /catalog/summary` | **new (A3)** |
| Search albums | `search?types=album` | ok |
| Card ⋯: Play, Shuffle, Play next, Add to queue | `getAlbum` (`tracks`) | ok |
| Card ⋯: Add to playlist | `addPlaylistItems` with the album's track ids | ok |
| Card ⋯: Go to artist, Album details | `AlbumSummary.artist.id`, `getAlbum` | ok |

### Album

| Element | Uses | Status |
|---|---|---|
| Head: cover, title, artist, year, genre, song count, length | `getAlbum` | ok |
| Blurred cover behind the head | `cover.url` with `size=640` | ok / client |
| Badge "FLAC · 24-bit / 96 kHz" | derived from `tracks[].format` (see 4.4) | client rule |
| Play, Shuffle, Add to queue | `getAlbum` | ok / client |
| ⋯: Play next, Add to playlist, Go to artist, Download cover | `getAlbum`, `addPlaylistItems`, `getAlbumCover?size=original` | ok |
| ⋯: Add all songs to favorites | one `addFavoriteTrack` per track, or `POST /me/favorites/tracks` | ok / **new (B2)** |
| Song rows: number, title, artist, genre, "Lyrics", duration, heart, ⋯ | `AlbumDetail.tracks` | ok |
| Disc headings | `disc_count`, `Track.disc` | ok |
| "More by …" | `getArtist` or `listAlbums?artist=` | ok |

### Artist

| Element | Uses | Status |
|---|---|---|
| Artists A–Z (not drawn yet) | `listArtists` | ok |
| Name, albums | `getArtist` | ok |
| "2 albums · 21 songs · 1 h 24 min" | sum of `AlbumSummary.track_count` and `duration_ms` | ok / client |
| Picture | none (out of scope, §1.3): a circle split between the covers of two albums | client |
| Songs of the artist, "Show all 21" | `GET /tracks?artist=` | **new (A1)** |
| Play, Shuffle the artist | `GET /tracks?artist=`, `GET /tracks/random?artist=` | **new (A1, B1)** |

### Playlists

| Element | Uses | Status |
|---|---|---|
| Cards with name, "23 songs · 1 h 32 min" | `listPlaylists` | ok |
| 2×2 mosaic of covers | `Playlist.covers` | **new (A2)** |
| Favorites tile with "86 songs · 5 h 41 min" | `GET /me/favorites/summary` | **new (A4)** |
| Sort: Recently updated / Name | `listPlaylists` returns them all; sorted in the client | client |
| New playlist | `createPlaylist` | ok |
| Card ⋯: Play, Shuffle, Play next, Add to queue | `listPlaylistItems` | ok |
| Card ⋯: Edit details, Delete playlist | `updatePlaylist`, `deletePlaylist` | ok |
| Empty playlist | `item_count = 0`, `covers = []` | ok / **new (A2)** |

### Playlist

| Element | Uses | Status |
|---|---|---|
| Head: mosaic, name, description, "23 songs, 1 h 32 min", "Updated 2 days ago" | `getPlaylist` (+ `covers`) | ok / **new (A2)** |
| Blurred mosaic behind the head | `covers[].url` | **new (A2)** |
| Play, Shuffle, Edit details, ⋯ Delete | `listPlaylistItems`, `updatePlaylist`, `deletePlaylist` | ok |
| Rows with "Added" date | `PlaylistItem.added_at` | ok |
| Unavailable song in grey | `Track.available = false` | ok |
| Reorder by dragging, Move to top / bottom | `movePlaylistItem` + `If-Match` | ok |
| "Find songs for this playlist" + Add | `search?types=track`, `addPlaylistItems` | ok |
| "Playlist changed elsewhere" | `412 precondition_failed`, then `getPlaylist` | ok |
| Undo after "Added to …" | `removePlaylistItem` with `added[].item_id` | ok |
| Undo after "Removed from Sunday morning" | the `DELETE` is sent only when the toast ends (see 7.2) | client rule |

### Search

| Element | Uses | Status |
|---|---|---|
| Field, chips All / Artists / Albums / Songs | `search?types=` | ok |
| Top result | chosen by the client (see 4.6) | client rule |
| Songs, Artists ("2 albums"), Albums | `SearchResult` | ok |
| "Show all 9" | no total and no cursor in `search`: "Show all" shows up to 50 (see 4.6) | client rule |

### Now playing

| Element | Uses | Status |
|---|---|---|
| Cover, title, artist, album, year, format badges | `Track` | ok |
| Synced lyrics, jump to a line | `getTrackLyrics` (`synced`, `time_ms`), `Range` | ok |
| Up next, Next in queue, Clear queue, remove from queue | none | client |
| "Lantern Bay is skipped: it is not in the library" | `Track.available` | ok |
| "Playing from playlist Sunday morning" | none | client |

### Sign in

| Element | Uses | Status |
|---|---|---|
| Username, password, show password | `login` with `device_name` from the browser | ok |
| Error "Wrong username or password" | `401 invalid_credentials` | ok |
| "Vibrance 0.1.0" | `getServerInfo` (public) | ok |

### Account

| Element | Uses | Status |
|---|---|---|
| Username, role, "Member since" | `getMe` | ok |
| Change password | `changePassword` | ok |
| "Where you're signed in": device, Browser or App, last use, expiry, "This device" | `listSessions` | ok |
| Sign out one session | `revokeSession` | ok |
| Sign out everywhere else | one `revokeSession` per session, or `DELETE /me/sessions` | ok / **new (B3)** |
| Playback: Volume leveling (Automatic / Off), Single-key shortcuts | stored in the browser; shared with the mobile app only with `GET`/`PATCH /me/settings` | client / **new (B5, P3)** |

### Queue panel (Albums board) and Now playing queue

| Element | Uses | Status |
|---|---|---|
| Panel opened only from the Queue button, remembered | none | client |
| Now playing, Next in queue (Play next / Add to queue), Next from the context | none | client |
| Reorder by dragging, remove, Clear | none | client |
| "Lantern Bay is skipped: it isn't in the library right now" | `Track.available` | ok |
| "After Isobars, the music stops." End state with Play again / Shuffle | none | client |
| Save queue as playlist | `createPlaylist`, then `addPlaylistItems` in blocks of at most 1,000 ids | ok |

### Empty, loading and error states (States board)

| Element | Uses | Status |
|---|---|---|
| Static placeholders after the first second | none | client |
| No favorites / No playlists / The library is empty | `listFavoriteTracks`, `listPlaylists`, `listAlbums` empty; `getLibraryStatus` for admins | ok |
| No results, with how search matches | `search` | ok |
| Can't reach Vibrance, retry and resume at the same position | `getTrackAudio` with `Range` | ok |
| Keyboard shortcuts sheet | none | client |

### Administration

| Element | Uses | Status |
|---|---|---|
| State Updating / Up to date / Waiting / Needs attention | `getLibraryStatus.state` | ok |
| "295 of 415 albums" | `progress.indexed`, `progress.discovered` | ok |
| Albums and songs, available and not | `albums`, `tracks` | ok |
| Last scan and its length | `last_scan.started_at`, `finished_at` | ok |
| MusicLib maintenance | `musiclib_maintenance` | ok |
| Needs attention (path, message, code) | `problems` | ok |
| Scan library | `scanLibrary` | ok |
| Users: name, role, status, created | `listUsers` | ok |
| "You" | `getMe.id` | ok |
| New user | `createUser` | ok |
| Make admin / Make user, Disable / Enable | `updateUser` (`role` and `disabled` are both required) | ok |
| Set a new password | `resetUserPassword` | ok |
| Delete account | `deleteUser` | ok |
| "Vibrance 0.1.0 · API v1" | `getServerInfo` | ok |

## 2. P1 additions

### A1. `GET /tracks`: every available track, sorted and paginated

**Why.** The Library page lists songs, not albums. The Artist page lists the songs of an artist. Today the only way is `getAlbum` for every album.

**Behavior.**

- Only available tracks, like `listAlbums`.
- `sort`:
  - `title`: by title, then id;
  - `artist`: by the track's artist, then album title, album id, disc, number, id;
  - `album`: by album title, then album id, disc, number, id (the order of the album page, album after album);
  - `added`: by the moment Vibrance first saw the audio of the track, then id. A track that MusicLib moves to another album gets a new id (its playlist items and favorites follow it, DESIGN.md errata "i riferimenti seguono l'audio"), but it keeps the moment of the track it replaces: a move does not make it recently added.
- `order=desc` reverses the whole order, as for albums.
- Names and titles sort as in `listArtists` (`x/text/collate`, numeric, §5.5).
- `artist=<id>` keeps the tracks of the albums of that artist (the album artist, as `listAlbums?artist=`). An id that is no artist's gives an empty list, not an error.
- **Two meanings of "artist", on purpose.** `sort=artist` orders by `Track.artist`, the artist of the song that the row shows: in a compilation each song sorts under its own artist. `artist=<id>` filters by the album artist, the only artist that is an entity (§5.3), the one "Go to artist" opens. So the Artist page of a guest who sings one song of a compilation does not list that song; that is the limit of §5.3, not of this endpoint.
- The cursor belongs to the `sort` and the `order` it was read with: with others, `400 invalid_cursor` (§8.5).
- `favorite` is computed for the user of the request, as everywhere.

```yaml
  /tracks:
    get:
      operationId: listTracks
      tags: [Catalog]
      summary: The available tracks, in a chosen order
      description: |
        The available tracks. Paginated. `sort` chooses the order:

        - `title`: by title;
        - `artist`: by the artist of the track, then album title, album, disc
          and number;
        - `album`: by album title, then album, disc and number;
        - `added`: by the moment Vibrance first saw the audio of the
          track; a track that MusicLib moves to another album keeps the
          moment of the track it replaces.

        `sort=artist` orders by the artist of each track (`artist` of the
        track), while the parameter `artist` filters by the artist of the
        album. Titles and names are ordered as for `GET /artists`, and tracks
        that tie on every key by id, so the order is total. `order=desc`
        reverses the whole order. `artist` keeps only the tracks of the
        albums of one artist; an id that is no artist's gives an empty list. A cursor
        belongs to the `sort` and the `order` it was read with: with others
        it answers `400 invalid_cursor`.
      parameters:
        - name: sort
          in: query
          description: The order of the list.
          schema:
            type: string
            enum: [title, artist, album, added]
            default: title
        - name: order
          in: query
          description: The direction of the order.
          schema:
            type: string
            enum: [asc, desc]
            default: asc
        - name: artist
          in: query
          description: The id of an artist, to list only the tracks of its albums. An id that no artist has gives an empty list, not an error.
          schema:
            type: string
            format: uuid
            pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        - $ref: "#/components/parameters/Limit"
        - $ref: "#/components/parameters/After"
      responses:
        "200":
          description: One page of tracks.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/TrackList"
        "400":
          $ref: "#/components/responses/BadRequest"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    TrackList:
      type: object
      description: "One page of tracks."
      required: [tracks, next]
      properties:
        tracks:
          type: array
          items:
            $ref: "#/components/schemas/Track"
        next:
          $ref: "#/components/schemas/Cursor"
```

**Implementation notes.** These are for the engineer to confirm; DESIGN.md §5.2 has the final word.

- `tracks` has no sort keys today. A new migration adds:
  - `title_key BLOB` and `artist_key BLOB`, computed from `tracks.title` and `tracks.artist` with the same collation as `albums.title_key`;
  - `album_key BLOB`, a copy of `albums.title_key` so that keyset pages need no join, as `albums.artist_key` does;
  - `first_seen_at INTEGER`. For existing rows it is backfilled with the album's `first_seen_at`. A new row takes the `first_seen_at` of the unavailable row with the same fingerprint whose references it takes over (the same pairing the scanner already makes when references follow the audio), and the moment of the cycle when there is none.
- The scanner keeps these columns up to date in the same transaction as the rest of the row. The collation rebuild of T26 recomputes them too.
- Indexes, checked with `EXPLAIN QUERY PLAN` as in §8.5:
  - `tracks(available, title_key, id)`;
  - `tracks(available, artist_key, album_key, album_id, disc, no, id)`;
  - `tracks(available, album_key, album_id, disc, no, id)`;
  - `tracks(available, first_seen_at, id)`.
- The `artist` filter reads `albums(artist_id, available)`. If that cannot use an index together with the order, denormalize the album artist id onto `tracks` as well.
- Tests:
  - the property test of §8.5 (pages joined equal the full sorted list) for each `sort` and `order`;
  - `invalid_cursor` across sorts;
  - unavailable tracks never listed;
  - a moved track keeps its place in `sort=added` (the scenario of S23 with a real move);
  - authorization matrix entry (I6) and OpenAPI conformance (I10).

### A2. `Playlist.covers`: the covers that make the mosaic

**Why.** The mockups show every playlist with a 2×2 mosaic: in the sidebar, the Playlists grid, the "Add to playlist" menu and the head of the playlist. `Playlist` has no picture, and reading the items of 500 playlists to find their covers is not reasonable.

**Behavior.** A new field, always present, on every `Playlist` (`listPlaylists`, `getPlaylist`, `createPlaylist`, `updatePlaylist`, and `playlist` inside `AddPlaylistItemsResult`):

- up to 4 covers of **distinct albums**;
- taken from the items with an available track, in position order;
- albums without a cover are skipped;
- an empty list when there are none.

The client draws one cover when there are 1 to 3, a 2×2 mosaic when there are 4, and a placeholder when the list is empty.

```yaml
# components/schemas/Playlist: add "covers" to required and to properties
        covers:
          type: array
          maxItems: 4
          description: |
            Up to four covers of distinct albums, taken from the items whose
            track is available, in the order of the items; albums without a
            cover are skipped. Empty when there is none. Clients draw one
            cover with fewer than four, a 2×2 mosaic with four.
          items:
            $ref: "#/components/schemas/Cover"
```

**Notes.**

- Cost: one indexed query per playlist on `playlist_items(playlist_id, position, id)` joined with `tracks` and `albums`, stopping at 4 distinct albums. `listPlaylists` returns up to 500 playlists, so S24's budget must be measured again with 500 playlists of 10,000 items.
- `covers` changes when items change; `revision` already grows then. It also changes when MusicLib changes a cover, without a new `revision`. That is acceptable: it is read data, and `If-Match` protects only writes.

### A3. `GET /catalog/summary`: how much music there is

**Why.** "5,120 songs" on Library and "412 albums · 37 artists" on Albums. `getLibraryStatus` has the numbers but is for admins only and says much more than a listener needs. Keyset lists have no totals, on purpose (§8.5).

```yaml
  /catalog/summary:
    get:
      operationId: getCatalogSummary
      tags: [Catalog]
      summary: How many artists, albums and tracks are available
      description: |
        The counts of what is available to listen to, and the sum of the
        known durations of the available tracks. Unavailable rows are not
        counted.
      responses:
        "200":
          description: The counts.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/CatalogSummary"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    CatalogSummary:
      type: object
      description: "How much music is available."
      required: [artists, albums, tracks, duration_ms]
      properties:
        artists:
          type: integer
          minimum: 0
        albums:
          type: integer
          minimum: 0
        tracks:
          type: integer
          minimum: 0
        duration_ms:
          type: integer
          format: int64
          minimum: 0
      example:
        artists: 37
        albums: 412
        tracks: 5120
        duration_ms: 1296000000
```

### A4. `GET /me/favorites/summary`: how many favorites

**Why.** The sidebar shows "Favorites 86", and the Favorites tile shows "86 songs · 5 h 41 min". The favorites list is paginated and has no total.

**Behavior.** Same meaning as the counts of a playlist (§8.6): `track_count` counts every favorite, unavailable ones included; `duration_ms` counts only the available ones.

```yaml
  /me/favorites/summary:
    get:
      operationId: getFavoritesSummary
      tags: [Favorites]
      summary: How many favorite tracks the user has
      description: |
        `track_count` counts every favorite of the user of the request, the
        ones whose track is not available included, as `item_count` does for
        a playlist. `duration_ms` is the sum of the durations of the
        favorites whose track is available.
      responses:
        "200":
          description: The counts.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/FavoritesSummary"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    FavoritesSummary:
      type: object
      description: "How many favorite tracks the user has."
      required: [track_count, duration_ms]
      properties:
        track_count:
          type: integer
          minimum: 0
        duration_ms:
          type: integer
          format: int64
          minimum: 0
      example:
        track_count: 86
        duration_ms: 20460000
```

The route `/me/favorites/summary` sits beside `/me/favorites/tracks` and does not collide with `/me/favorites/tracks/{id}`.

## 3. P2 and P3 additions

### B1 (P2). `GET /tracks/random`: shuffle the library or an artist

**Why.** "Shuffle" on Library (5,000 to 50,000 songs) and on an artist. Without it the client must read every page of `GET /tracks` before playing.

**Behavior.**

- Up to `limit` available tracks chosen at random, with no repetition inside one answer.
- No cursor: when the queue runs low the client asks again, and a later answer may repeat songs.
- `artist` filters as in A1.

```yaml
  /tracks/random:
    get:
      operationId: listRandomTracks
      tags: [Catalog]
      summary: Available tracks chosen at random
      description: |
        Up to `limit` available tracks, chosen at random, none twice in the
        same answer. Not paginated: ask again for more, and a later answer may
        repeat a track. `artist` keeps only the tracks of the albums of one
        artist; an id that is no artist's gives an empty list.
      parameters:
        - name: artist
          in: query
          description: The id of an artist, to choose only among the tracks of its albums.
          schema:
            type: string
            format: uuid
            pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        - name: limit
          in: query
          description: The most tracks in the answer.
          schema:
            type: integer
            minimum: 1
            maximum: 200
            default: 50
      responses:
        "200":
          description: The tracks.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/RandomTrackList"
        "400":
          $ref: "#/components/responses/BadRequest"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    RandomTrackList:
      type: object
      required: [tracks]
      properties:
        tracks:
          type: array
          maxItems: 200
          items:
            $ref: "#/components/schemas/Track"
```

**Notes.**

- The router is `net/http`'s `ServeMux` (`std-http-server`), which always prefers the more specific pattern: `GET /api/v1/tracks/random` wins over `GET /api/v1/tracks/{id}` whatever the order of registration. A test still checks that `/tracks/random` answers the list and not `400 invalid_request`.
- The client spreads the songs of each artist and album inside each answer (7.9); it cannot spread across answers, and a later answer may repeat a song.
- The method of choosing (for example random `seq` values, or `ORDER BY random()` on the available ids) is chosen by measurement (§2.1), with the 50,000-track library of S24.

### B2 (P2). `POST /me/favorites/tracks`: several favorites at once

**Why.** "Add all songs to favorites" in the album menu. Without it that is one `PUT` per track.

**Behavior.**

- Idempotent: tracks that are already favorites keep their date.
- All or nothing.
- Unknown ids answer `422 unknown_track` with the ids in `details`, as `addPlaylistItems` does.
- Unavailable tracks are accepted, as in `addFavoriteTrack`.
- The favorites list orders by `favorited_at`, then by track id (`sql/favorites.sql`), and `favorited_at` is in milliseconds: one timestamp for the whole request would leave the songs of an album in the order of their random ids. So the new favorites get `favorited_at` one millisecond apart: the first id `now`, the second `now − 1`, and so on. The list, the most recent first, then shows them in the order of the request, as the album shows them. An id repeated in the request counts once, at its first place.

```yaml
  /me/favorites/tracks:
    post:
      operationId: addFavoriteTracks
      tags: [Favorites]
      summary: Make several tracks favorites
      description: |
        Makes every track of `track_ids` a favorite, in one transaction: all
        or nothing. Idempotent: a track that is a favorite already stays one,
        with the date it had. The new favorites get dates one millisecond
        apart, the first id the most recent, so the list of favorites shows
        them in the order of the request; a repeated id counts once, at its
        first place. `422 unknown_track` when an id is no track's;
        `details.track_ids` lists those ids, each once, in the order of the
        request. A track that is not available can be made a favorite.
      parameters:
        - $ref: "#/components/parameters/XVibranceRequest"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/FavoriteTracksRequest"
      responses:
        "204":
          description: The tracks are favorites.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
        "400":
          $ref: "#/components/responses/BadRequest"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "413":
          $ref: "#/components/responses/PayloadTooLarge"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "422":
          $ref: "#/components/responses/Unprocessable"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    FavoriteTracksRequest:
      type: object
      description: "The tracks to make favorites."
      additionalProperties: false
      required: [track_ids]
      properties:
        track_ids:
          type: array
          minItems: 1
          maxItems: 1000
          items:
            type: string
            format: uuid
            pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
```

### B3 (P2). `DELETE /me/sessions`: sign out everywhere else

**Why.** The Account page has "Sign out everywhere else". Without it that is one `DELETE /me/sessions/{id}` per session.

```yaml
  /me/sessions:
    delete:
      operationId: revokeOtherSessions
      tags: [Account]
      summary: Sign out every other session
      description: |
        Revokes every session of the user of the request except the one of
        this request. Idempotent: with no other session the answer is `204`
        all the same.
      parameters:
        - $ref: "#/components/parameters/XVibranceRequest"
      responses:
        "204":
          description: The other sessions are revoked.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"
```

### B4 (P3). `GET /tracks/{id}/playlists`: which playlists hold a track

**Why.** Optional: check marks in the "Add to playlist" sheet, like the music apps people know. The UI works without it.

```yaml
  /tracks/{id}/playlists:
    get:
      operationId: listTrackPlaylists
      tags: [Playlists]
      summary: The playlists of the user that hold a track
      description: |
        The playlists of the user of the request that have at least one item
        with this track, the oldest first, as `GET /playlists`. Never the
        playlists of another user. `404 track_not_found` when there is no
        such track.
      parameters:
        - $ref: "#/components/parameters/PathId"
      responses:
        "200":
          description: The playlists.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/PlaylistRefList"
        "400":
          $ref: "#/components/responses/BadRequest"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "404":
          $ref: "#/components/responses/NotFound"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    PlaylistRef:
      type: object
      required: [id, name]
      properties:
        id:
          $ref: "#/components/schemas/Id"
        name:
          type: string
    PlaylistRefList:
      type: object
      required: [playlists]
      properties:
        playlists:
          type: array
          maxItems: 500
          items:
            $ref: "#/components/schemas/PlaylistRef"
```

It needs an index on `playlist_items(track_id, playlist_id)`, which does not exist today.

### B5 (P3). `GET` and `PATCH /me/settings`: the same preferences on every device

**Why.** Account has a "Playback" section, volume leveling and single-key shortcuts, and the sidebar has the theme. In the browser they live in `localStorage`, so they differ between the laptop and the future mobile app. The UI works without this endpoint.

**Behavior.**

- `GET` answers the settings of the user of the request. A user who never saved any gets the defaults.
- `PATCH` changes only the fields it sends; the others keep their value. Unknown keys are refused (§8.1). The answer is the whole object.
- **Why not `PUT` of the whole object:** with every field required, a later version that adds a setting would refuse with `400` every save of an app that does not know it yet, which is not compatible inside `/api/v1`. With `PATCH` an older client sends only what it knows.
- A future setting is a new optional field of `SettingsUpdate` and a new required field of `Settings`, with its default for the users who never set it.
- The settings are not secret, and they are deleted with the account.

```yaml
  /me/settings:
    get:
      operationId: getSettings
      tags: [Account]
      summary: The preferences of the user
      description: The preferences of the user of the request; the defaults when none were saved.
      responses:
        "200":
          description: The preferences.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Settings"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"
    patch:
      operationId: updateSettings
      tags: [Account]
      summary: Change some preferences of the user
      description: The fields sent take their new value; the others keep theirs. The answer is every preference.
      parameters:
        - $ref: "#/components/parameters/XVibranceRequest"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/SettingsUpdate"
      responses:
        "200":
          description: The preferences as saved.
          headers:
            X-Request-Id:
              $ref: "#/components/headers/X-Request-Id"
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Settings"
        "400":
          $ref: "#/components/responses/BadRequest"
        "401":
          $ref: "#/components/responses/Unauthorized"
        "403":
          $ref: "#/components/responses/Forbidden"
        "413":
          $ref: "#/components/responses/PayloadTooLarge"
        "421":
          $ref: "#/components/responses/MisdirectedRequest"
        "500":
          $ref: "#/components/responses/Internal"
        "503":
          $ref: "#/components/responses/Unavailable"

# components/schemas
    Settings:
      type: object
      description: "The preferences of a user, the same on every device."
      required: [volume_leveling, single_key_shortcuts, theme]
      properties:
        volume_leveling:
          type: string
          enum: [automatic, "off"]
          description: "`automatic`: the album gain for the songs of an album played in order, the track gain otherwise (section 7.10)."
        single_key_shortcuts:
          type: boolean
          description: Whether L, Q, S, R, M, F, `/` and `?` work without a modifier (WCAG 2.1.4).
        theme:
          type: string
          enum: [dark, light]
      example:
        volume_leveling: automatic
        single_key_shortcuts: false
        theme: dark
    SettingsUpdate:
      type: object
      description: "The preferences to change; the ones left out keep their value."
      additionalProperties: false
      properties:
        volume_leveling:
          type: string
          enum: [automatic, "off"]
        single_key_shortcuts:
          type: boolean
        theme:
          type: string
          enum: [dark, light]
      example:
        theme: light
```

`"off"` is quoted: a YAML 1.1 parser reads a bare `off` as the boolean `false`.

The defaults are `automatic`, `false` and `dark`. The settings live in a new table keyed by the user, deleted with the account (`ON DELETE CASCADE`); a user without a row gets the defaults, and the first `PATCH` creates the row.

### B6 (P3). Gapless playback data on `Track.format`

**Why.** Albums that run one song into the next (live, classical, concept albums) need no gap. DESIGN.md D.3 lists true gapless as a future idea: it needs the encoder delay and padding of each file.

The client can already shorten the gap without this, by fetching the start of the next song during the last 15–20 seconds (section 7.12). The data is needed only to remove the gap completely. MP3 and AAC add silent samples at the start of each file (the encoder delay: 576 samples for LAME, plus the decoder's 529) and at the end (the padding, which fills the last frame and differs from file to file). The client removes them only if it knows how many there are. FLAC and ALAC are sample-exact and need nothing.

The data alone does not make playback gapless: an `<audio>` element always leaves a gap. The client must decode and join the songs itself, with Media Source Extensions or Web Audio (section 7.12), and that is a client step of its own.

```yaml
# components/schemas/AudioFormat: a new optional-valued field, always present
        gapless:
          type: object
          nullable: true
          description: |
            The silent samples the encoder added, from the LAME/Xing header of
            an MP3 or the iTunSMPB tag or edit list of an MP4. `null` when the
            file declares none, which is the case of FLAC and ALAC.
          required: [encoder_delay_samples, padding_samples]
          properties:
            encoder_delay_samples:
              type: integer
              minimum: 0
            padding_samples:
              type: integer
              minimum: 0
```

The scanner would read these values with the pinned `ffprobe` while it indexes the album, and store them in two new columns of `tracks`. Whether the pinned `ffprobe` exposes them for every codec must be checked first, with a spike like S1.

### C1. Serving the web UI

**Why.** The UI of `web/ui` is plain HTML, CSS and ES modules, with no build step, and it uses only the public API (DESIGN.md D.3). Today `/` redirects to `/api/docs` (D20) and nothing serves the UI. These are not operations of the API, so they are not in `api/openapi.yaml`: they are infrastructure routes like `/api/docs` (§8.3), listed by name in the authorization matrix (§12.4).

**Behavior.**

- The files of `web/ui` are embedded in the binary (`go:embed`, package `web`) and served at the root with their path: `/app.js`, `/app.css`, `/views/album.js`, `/fonts/…`. GET and HEAD only; any other method answers `405`.
- `GET /login` serves `login.html`. Every other `GET` of a path that is not a file of `web/ui` and not under `/api/` or `/health/` serves `index.html` with `200`: the client's router shows the page, or its own "not found". `/` serves `index.html` and no longer redirects to `/api/docs`; the documentation stays at `/api/docs`.
- No session is needed: the pages hold no data, and the client goes to `/login` when the API answers `401`. The rules of I4 on `Host` and `Origin` apply as on every route.
- `Content-Type` by extension (`text/html; charset=utf-8`, `text/javascript; charset=utf-8`, `text/css; charset=utf-8`, `image/svg+xml`, `font/woff2`), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, an `ETag` that is the SHA-256 of the file with `304` on `If-None-Match`, and `Cache-Control: no-cache`: the file names carry no hash, so a new release must be fetched again.
- The HTML pages carry `Content-Security-Policy: default-src 'self'; img-src 'self' data:; media-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'` and `X-Frame-Options: DENY`. The UI was written for `default-src 'self'`: no inline script, no inline style attribute, nothing from another host.
- `THIRD_PARTY_NOTICES.md` and `licenses/` list the vendored Hanken Grotesk font (SIL OFL 1.1), as for every vendored file.

**Tests.** Every file of `web/ui` is served with its type and its `ETag`; a client route (`/albums/<id>`) gives `index.html`; `/api/v1/nothing` stays a JSON `404` of the API, never `index.html`; `/login` gives `login.html`; the CSP header is on the pages; a `POST /` answers `405`; a path with `..` or a percent-encoded slash serves nothing outside `web/ui` (I2: the path chooses only among the embedded files).

## 4. Client rules (no API change)

### 4.1 The queue

Play, Play next, Add to queue, Up next, Clear queue, shuffle, repeat and "Playing from …" live in the client. The server streams one track at a time with `Range` (§9.1). Section 7.8 says how the queue behaves.

A context can be the whole library: 50,000 songs in `GET /tracks` are about 50 MB of JSON. So the queue keeps the context as a request (the endpoint, its `sort`, `order` and filter) and the cursor of its next page, and reads the next page when fewer than 20 songs are left. Shuffle of the library or of an artist reads `GET /tracks/random` the same way (B1).

### 4.2 "Go to artist" from a track

`Track.artist` is the free text of the track's tag, not an entity: artists are the album artists (§5.3). "Go to artist" opens `Track.album.artist.id`.

When `Track.artist` differs from `Track.album.artist.name` (compilations, guests), the row shows the track artist as plain text. The menu item still points to the album artist, with that name in its label.

### 4.3 "Download file"

On the web: `<a href="/api/v1/tracks/{id}/audio" download="Artist - Title.flac">`, with the name built by the client from the metadata and the extension from `format.codec`. The server never sends `Content-Disposition` (§9.1).

On mobile: the app fetches the same URL with the bearer token. "Download cover" uses `getAlbumCover?size=original`.

### 4.4 Album format badge

The badge is derived from `AlbumDetail.tracks[].format`:

- one codec and one sample format shared by every track: "FLAC · 24-bit / 96 kHz";
- otherwise "Mixed formats".

### 4.5 Counts of an artist

"2 albums · 21 songs · 1 h 24 min" is the sum of `track_count` and `duration_ms` over `ArtistDetail.albums`.

### 4.6 Search

- `SearchResult` has no relevance score shared across kinds. The client picks the top result:
  1. an artist whose name equals the query, ignoring case and accents;
  2. otherwise an album whose title starts with the query;
  3. otherwise the first track.
- There are no totals and no cursor, and `limit` is at most 50. "Show all 9" becomes "Show all", which asks `types=track&limit=50` and shows up to 50 songs. When 50 come back, the list ends with "Showing the first 50. Type more words to narrow the search." A paginated search is not part of this proposal.
- Highlighting of the matched words is done in the client.

### 4.7 Other details

- "Recently updated / Name" on Playlists: `listPlaylists` returns every playlist (at most 500); the client sorts by `updated_at` or `name`.
- `device_name` at sign-in: built from the user agent ("Firefox on macOS"). Sessions show "Browser" for `cookie` and "App" for `token`.
- "You" in the users table: `User.id == getMe.id`. The actions that would answer `409 cannot_modify_self` are hidden on that row.
- "Make admin" sends `updateUser` with the current `disabled`, and "Disable" with the current `role`: both fields are required.
- Covers: `Cover.url` plus `&size=256` for rows and the sidebar, `&size=640` for grids and heads, `size=original` only for download.
- `503 library_changing` on audio: retry after `Retry-After`. `404 track_unavailable`: skip to the next song and say so.
- `412 precondition_failed` on a playlist: read it again and show "Playlist changed elsewhere. Showing the latest version."

## 5. Out of scope on purpose (DESIGN.md §1.3)

The owner's approval of 2026-10-06 covers sections 2 and 3 only. The mockups do not show these, and they still need an owner decision before any work:

- favorites of albums and artists (the album menu has "Add all songs to favorites" instead);
- listening history, "Recently played", a home page built from history, scrobbling;
- artist pictures (the mockups split a circle between two album covers);
- shared or smart playlists;
- transcoding (`profile` is reserved, D10).

Two items are not in §1.3 but are close to it, for the future mobile app:

- a server-side play queue (`GET`/`PUT /me/play-queue`) to resume on another device;
- offline downloads.

Both store new personal data. Appendix D.3 asks to decide privacy and retention first.

The research of section 7 raises three more decisions for the owner. All three are client-only, but they are close to "no listening history":

1. **Keep the queue across reloads.** The queue, the current song and its position are kept in the browser's `localStorage`, as Apple Music keeps "Playing Next" when the app quits. It is the current state, not a history. Proposed: yes, per browser.
2. **Recent searches** on the Search page. NN/g counts recent items as an aid to recognition, but they are a small history. Proposed: no, until the owner decides.
3. **"Stop after this song" and "Stop after this album"** for evening listening. Proposed: later; not in the mockups.

## 6. Suggested order

The errata of DESIGN.md of 2026-10-06 makes these the steps W1–W6 of the plan, on the branch `dev-web-ui`:

1. **W1: A1 `GET /tracks`.** A migration with the new sort keys and `first_seen_at`, and the scanner's upkeep. It is the largest piece and unlocks Library and Artist.
2. **W2: A2 `Playlist.covers`, A3 and A4,** the summaries. Measure again with 500 playlists.
3. **W3: B1, B2, B3 and B4,** with the index on `playlist_items(track_id, playlist_id)`.
4. **W4: B5,** the settings, with their table.
5. **W5: B6,** after a spike on what the pinned `ffprobe` reports. If the spike shows that it cannot give the values for every codec, the step stops with a `BLOCCO:` and `gapless` is not added.
6. **W6: C1,** the serving of the web UI.

Each step updates `api/openapi.yaml`, regenerates `internal/api` with `scripts/generate.sh`, and adds:

- its rows to the authorization matrix (§12.4);
- OpenAPI conformance tests (I10);
- error path and mutation checks (§2.5).

## 7. Client behavior from UX research

These rules are for the web client, and later the mobile app. None of them changes the server, except where it says so.

Strength of the evidence:

- **strong**: peer-reviewed studies or a standard;
- **medium**: one study, or a recognized practitioner source;
- **weak**: vendor practice or user reports.

Numbers in brackets point to section 9.

### 7.1 Why people open a music player

People listen mainly to regulate mood and arousal:

- Schäfer et al. surveyed 834 people on 129 functions of music. Three dimensions came out, and arousal and mood regulation came first (mean 3.78), just ahead of self-awareness (3.59), with social relatedness far behind (2.01) [1].
- Lonsdale & North found the same in four studies [2]. **Strong.**

People also organize music by the occasion they will play it at:

- "driving", "programming" music in Cunningham et al. 2004 [29];
- in Cunningham et al. 2006, 25.2% of mixes were organized by event or activity [31].

The album is the unit people buy, but it did not emerge as a way of organizing [29]. **Medium.**

Consequences:

- The time from intent to sound must be short: Play on every card and head, double-click on rows.
- Playlists are the main personal tool. Make them quick to create and fill: add from any menu, drag to the sidebar, and a new playlist opens on "Find songs".
- The IKEA effect holds only when the task is completed [33], so an empty playlist should lead straight to adding songs.

### 7.2 Respond at once, undo instead of confirming

Response time limits [10] (**strong**):

- 0.1 s feels instantaneous;
- 1 s keeps the flow of thought;
- 10 s is the limit of attention.

The often-quoted 400 ms "Doherty threshold" is **not** in the 1982 IBM report it is attributed to [11]: do not cite it.

- **Optimistic updates.** Heart, Add to playlist, Remove, Play next, Add to queue and Clear queue change the screen at once. A toast confirms with the same verb ("Added to Late night drive"). If the server fails, the change is reverted and the toast says so in one sentence. `PUT` and `DELETE` on favorites are idempotent, so a retry is safe.
- **Toasts:**
  - above the player bar;
  - one at a time;
  - about 5 s, paused while hovered or focused;
  - `role="status"`;
  - Undo reachable by keyboard.
- **Undo, not confirmation,** for every reversible action [26]. NN/g: confirm only actions with serious consequences, because "if you cry wolf too many times, people will stop paying attention" [26]. In Vibrance the confirmations are:
  - Delete playlist;
  - Delete account;
  - Set a new password (admin).
- **Undo of "Remove from this playlist".** Adding the item back would create a new item, with a new `item_id` and a new `added_at`, so it is not used. Instead the client:
  1. hides the row at once;
  2. sends `DELETE /playlists/{id}/items/{item_id}` only when the toast ends;
  3. on page hide (`visibilitychange` to hidden, and `pagehide`), sends it with `fetch(..., {keepalive: true})` and `X-Vibrance-Request: 1`.

  Undo then simply shows the row again.

  The deferred `DELETE` is sent **without** `If-Match`. It names the item by its id, not by its position, so a change made in between does not make it wrong; and the client's tag may be old by then (a second removal, a move), which would make it fail with `412` at page hide, where nobody sees the answer, and the song would come back.

  While a removal waits, the client's positions are not the server's: positions count every item (§8.6), the hidden one included. So before any change that sends a position (a move, an insertion at a position) the client first sends the waiting `DELETE`s and waits for their answers; the toasts of those removals then end without Undo.

### 7.3 Loading

NN/g [14]:

- no indicator under 1 s;
- a spinner for 2–10 s waits;
- skeleton screens under 10 s;
- a progress bar above 10 s.

Viget's test (136 people) found skeleton screens perceived as the *slowest*: 2.82 s, against 2.41 s for a spinner and 2.29 s for a blank page [13]. **Medium, mixed.**

Rules:

- Reserve the space of every cover and row (`width`, `height`, `aspect-ratio`), so the layout never jumps.
- Show static `fill-strong` blocks only after the first second. No shimmer: it is decorative motion, and the design system forbids it.
- Never a full-page spinner. Play shows `aria-busy` on its button if the audio has not started within a second.
- Covers:
  - `size=256` for rows, the sidebar, toasts and the queue;
  - `size=640` for grids and heads;
  - `original` only for download.
  - The blurred field behind a head can use 256.

### 7.4 Targets

WCAG 2.2 SC 2.5.8 [18] (**strong**): a pointer target is at least 24×24 CSS px. A smaller one passes only if a 24 px circle centered on it touches no other target. Bigger and closer targets are faster to hit (Fitts, [17]).

The 4 px progress and volume bars of the first mockups failed.

- **Hit area.** The bars stay 4–5 px tall to the eye, but their hit area is 24 px tall (`::before` with `inset: -10px 0`).
- **Feedback.** A knob appears on hover and focus, and the time under the pointer shows in a small label.
- **Keyboard.** Each bar has `role="slider"`, `aria-valuetext="1:42 of 5:03"`, ←/→ for 5 s and Page Up/Page Down for 30 s.
- **Hover-only controls** (heart, drag handle, ⋯ on covers) also appear on `:focus-within`, so keyboard users never reach an invisible control.

### 7.5 Menus and search

- Choice time grows with the number of options. That holds for ordered and familiar lists; unordered lists are scanned in linear time [19].
- Cascading menus cost time when the pointer moves diagonally toward the submenu (corner steering). The documented cost is time, not more errors [20]. **Medium.**

Rules:

- **One level of submenu at most.** The song menu now has 8 items in this order: Play next, Add to queue, Add to playlist ›, Add to favorites | Go to album, Go to artist | Song details, Download file.
  - "Show lyrics" left the row menus; lyrics are for the song playing.
  - The submenu stays open for about 300 ms after the pointer leaves, or has a safe triangle [21].
  - → opens it and Esc closes it.
- **Add to playlist.** The submenu adds one song in one click and closes. The sheet with check boxes is only for several selected songs.
- **One global search** (sidebar Search, Ctrl/Cmd+K, and `/` when single-key shortcuts are on, 7.6). The fields that were in the heads of Library and Albums became launchers of it.
  - Results appear while typing, about 200 ms after the last key. The previous results stay until the new ones arrive.
  - The empty state says how search matches: names, from the first letters of each word.

### 7.6 Keyboard

WCAG 2.1.4 (Level A) [28]: a shortcut made of a single character key must be possible to turn off, to remap, or be active only on focus.

- **Always on:**
  - Space: play/pause, when no field has the focus;
  - Ctrl/Cmd+K: search;
  - Ctrl/Cmd+←/→: previous and next song;
  - Esc: close menus, sheets and Now playing;
  - Enter: play from the selected row;
  - Ctrl/Cmd+/: the shortcuts sheet.
- **Single-key shortcuts,** off by default and turned on in Account › Playback: L lyrics, Q queue, S shuffle, R repeat, M mute, F Now playing on the whole screen, `/` search and `?` the shortcuts sheet. `/` and `?` are single character keys too (a symbol, with or without Shift), so 2.1.4 applies to them as to the letters.
- Space and Enter act only when no field has the focus, and on the element that has it, as the platform does.
- Spotify's official list [53] is the reference users know: Ctrl/Cmd+K search, Space play/pause, and a help sheet on Ctrl/Cmd+/.

### 7.7 Control outside the page

The Media Session API [58] (Chrome 73, Edge 79, Firefox 82, Safari 15; not Baseline) puts Vibrance on the media keys of the keyboard, on headphones and in the operating system's media controls.

- `navigator.mediaSession.metadata` holds title, artist, album and artwork (`size=256` and `640`).
- Handlers: play, pause, previoustrack, nexttrack, seekto, seekbackward, seekforward.
- `setPositionState({duration, playbackRate, position})` is called on every seek and every change of song.
- At the end of the queue, `setActionHandler('nexttrack', null)`, so the system hides Next.

### 7.8 The queue and the end of playback

**Strong:** we judge an experience mostly by its peak and its end. In Kahneman et al. 1993, 22 of 32 people preferred to repeat the longer trial, because it ended better [15]. Applying this to a listening session is reasonable but untested.

**Weak, user reports:**

- Spotify users complain about a Now Playing panel that opens by itself [44].
- They complain about autoplay after an album ends [48].
- They have asked since 2017 to save the queue as a playlist (852 likes, not implemented) [46].
- Apple Music keeps "Playing Next" when the app quits [47].

Rules:

- **The queue is a side panel** beside the page and above the player bar. It opens only from the Queue button and remembers whether it was open.
- **Sections:**
  - Now playing;
  - Next in queue (Play next and Add to queue; Play next goes first);
  - Next from ⟨context⟩.
- Each item can be dragged to reorder and has Remove. Clear empties only "Next in queue", with Undo.
- **Save queue as playlist** opens the New playlist sheet already filled. It then calls `createPlaylist` and `addPlaylistItems`, in blocks of 1,000.
- **At the end of the context the music stops.** There is no autoplay and nothing is chosen for the listener. The panel says it in advance ("After Isobars, the music stops."). At the end it shows "End of Sunday morning" with Play again and Shuffle Sunday morning.
- **Repeat** has three visible states: off, all, one. One shows a small "1".
- **Unavailable songs** are skipped, and the queue says why.
- **Double-click or Enter** on a row plays from that row in the order of the list:
  - the rest of the album;
  - the rest of the playlist;
  - the Library list in its current sort.
  - From search results: the song, then the following results.

### 7.9 Shuffle

True random produces clusters that people read as non-random.

- Spotify dropped pure Fisher-Yates in 2014 and spreads each artist's songs evenly along the list, with a random offset [36]. Spreading by album was only mentioned as a possibility, not built.
- Spotify's 2025 "Fewer Repeats" uses listening history [37], so it does not fit Vibrance.
- Apple's iPod story and the line "less random to feel more random" are reported, not primary [38]. **Medium.**

Rules:

- Shuffle a context by spreading the songs of each artist evenly, then those of each album. Spreading by album is our own extension.
- Turning shuffle on keeps the current song and shuffles only the ones after it. Turning it off returns to the context's order after the current song.
- Nothing is remembered between sessions.

### 7.10 Volume leveling

Spotify [54] (**official documentation**):

- normalizes a whole album with one gain, so its dynamics stay;
- adjusts each track when shuffling or playing a playlist;
- leaves 1 dB of headroom for lossy files;
- never lifts a track beyond its true peak.

It does not normalize in its web player, so leveling is a small advantage for Vibrance on the web.

The API already gives `replay_gain` with the peaks.

- **Automatic** (default):
  - songs of an album played in order use `album_gain_db`;
  - everything else uses `track_gain_db`;
  - the gain is limited so that `peak × 10^(gain/20)` stays at or below 1.0, or 0.89 (−1 dB) for MP3 and AAC.
  - Songs without `replay_gain` play unchanged.
- **Off.**
- Apply it with a Web Audio `GainNode`, because `audio.volume` can only lower the level. The setting lives in Account › Playback.
- **To check on the devices.** Sending the `<audio>` element through an `AudioContext` changes how some mobile browsers treat it: Safari on iOS may suspend the context when the page goes to the background or the screen locks. Before the UI is called ready for phones, check background playback and the lock screen with leveling on. If it fails there, that browser plays without leveling, with only the attenuation that `audio.volume` can give, and Account says so.

### 7.11 Lyrics

Lyrics are among the metadata people want most:

- 81.0% in Lee & Downie's survey of 427 people, after the title (90.1%) [30];
- the most wanted extra in Cunningham et al. 2004 [29];
- "one of the most requested features" for Spotify [40].

**Strong for demand.** The emotional effect of lyrics is contested [5].

Rules:

- **Following.** The current line is kept in view, and the scroll answers the music.
- **Manual scroll.** When the listener scrolls, following stops and a "Back to current line" pill appears.
- **Line colors.** Past and coming lines share one grey, and the current line is in `ink`. A dark scrim under the column keeps the text readable on light covers.
- **Selecting a line** jumps to its time.
- **Unsynced lyrics** (`synced: false`) are static text, with the note "These lyrics aren't synced."
- **No lyrics:** the column disappears, and the Lyrics button is disabled with the tooltip "No lyrics for this song".

### 7.12 Gaps between songs

Fetch the start of the next song during the last 15–20 seconds, so it starts without a wait. Removing the gap entirely needs the encoder delay and padding of MP3 and AAC [59], which is B6, and a player that decodes and joins the songs itself (Media Source Extensions or Web Audio): an `<audio>` element alone always leaves a gap.

Crossfade stays off: it would spoil live and continuous albums, and there is no setting for it.

### 7.13 Beauty and friction

- Kurosu & Kashimura found a correlation of 0.589 between aesthetics and *perceived* usability (252 raters, 26 ATM layouts) [6].
- Tractinsky et al. found r = 0.66 before use and 0.71 after (124 people analyzed), with no effect of the real usability on what people perceived [8].
- But Tuch et al. (80 people) found the reverse effect: poor usability lowered the perceived beauty, while beauty did not raise the perceived usability [9]. **Medium.**

Consequence: the blurred cover fields and the grain make a good first impression, but any friction erases it. Sections 7.2–7.6 come before more visual polish.

Music–color associations go through emotion: fast music in a major key brings saturated, light, yellow colors; slow music in a minor key brings dark, desaturated, blue ones [4]. **Strong for the effect; untested in a UI.** It supports taking the page's color from the cover.

### 7.14 Light theme

For people with normal vision, light mode reads better, and the more so the smaller the text [23]. **Strong.**

Dark stays the reference theme for a player, but the light theme must be complete. Long pages such as Account and Administration must read well in it. The theme is chosen by the person (`Settings.theme`, B5); the browser keeps a copy in `localStorage`, so the first paint has the right theme before the API answers.

### 7.15 Empty states

Empty states should say:

- what the state is;
- what the area is for;
- the one action that fills it [27].

Rules:

- **Favorites, empty:** "No favorites yet. Select the heart next to a song to keep it here."
- **Playlists, empty:** "No playlists yet." with New playlist as the only filled button.
- **A new playlist:** opens on "Find songs for …", with the focus in the field.
- **The library is empty:** "Ask the person who runs this server to add music with MusicLib." Admins also see the scanner state and "Scan library".
- **Unreachable server:** "Can't reach Vibrance." The client retries by itself and resumes the song where it stopped (`Range`).

## 8. Questions for the design system

The mockups use the accent in four places the design system does not list:

- the filled favorite heart;
- active toggles (shuffle, repeat, switches);
- the drop line when reordering;
- the "playing from here" mark in the sidebar.

Proposal: add "the active state of toggles and favorites, the drop indicator, the current playback context" to the list. The drop target in the sidebar uses a 1 px `ink` outline, not the accent.

Three other questions:

- **"At most one filled button per view."** The player bar has an `ink` Play button, and a page can have a Magenta Play. Proposal: the rule does not count the player bar.
- **Focus ring.** On a blurred cover field (Now playing) it is `ink`, because the Magenta does not keep its contrast on every cover. Proposal: write this down.
- **Color field and automatic motion.** When the next song starts by itself, the color field of Now playing changes without an action of the user. The design system allows only motion that answers the user. Proposal: the field changes at once, with no fade, unless the owner allows a 400 ms cross-fade as an exception.

## 9. Sources

All the sources below were opened and checked for what they are cited for. Where the cited URL no longer works, the URL actually read is given.

1. Schäfer, Sedlmeier, Städtler, Huron (2013). *The psychological functions of music listening.* Frontiers in Psychology. https://www.ncbi.nlm.nih.gov/pmc/articles/PMC3741536/
2. Lonsdale & North (2011). *Why do we listen to music? A uses and gratifications analysis.* British Journal of Psychology 102:108–134. https://researchportal.hw.ac.uk/en/publications/why-do-we-listen-to-music-a-uses-and-gratifications-analysis/ (abstract; four studies, mood management first)
3. Saarikallio & Erkkilä (2007). *The role of music in adolescents' mood regulation.* Psychology of Music 35(1). https://journals.sagepub.com/doi/10.1177/0305735607068889 (8 adolescents, 120 follow-up forms; seven strategies)
4. Palmer, Schloss, Xu, Prado-León (2013). *Music–color associations are mediated by emotion.* PNAS. https://pmc.ncbi.nlm.nih.gov/articles/PMC3670360
5. Ma, Baker, Vukovics, Davis, Elliott (2024). *Lyrics and melodies: Do both affect emotions equally? A replication and extension of Ali and Peynircioğlu (2006).* Musicae Scientiae 28(1). https://repository.lsu.edu/psychology_pubs/2339 (abstract only; contradicts the original "for several variables")
6. Kurosu & Kashimura (1995). *Apparent usability vs. inherent usability.* CHI '95. https://chi1995.chistatic.hosting.acm.org/proceedings/shortppr/mk_bdy.htm
7. NN/g, *The Aesthetic-Usability Effect* (Moran, 2024). https://www.nngroup.com/articles/aesthetic-usability-effect/
8. Tractinsky, Katz, Ikar (2000). *What is beautiful is usable.* Interacting with Computers 13(2). Author's PDF: http://www.ise.bgu.ac.il/faculty/noam/papers/00_nt_ask_di_iwc.pdf
9. Tuch, Roth, Hornbæk, Opwis, Bargas-Avila (2012). *Is beautiful really usable?* Computers in Human Behavior 28(5). Author's PDF: https://cpb-us-e1.wpmucdn.com/wp.wwu.edu/dist/8/2868/files/2018/04/Tuch-et-al-2012-Is-Beautiful-Usable-2don1em.pdf
10. Nielsen, *Response Times: The 3 Important Limits.* https://www.nngroup.com/articles/response-times-3-important-limits/
11. Doherty & Thadani (1982). *The Economic Value of Rapid Response Time*, an IBM report, not a journal article. Transcript: https://jlelliotton.blogspot.com/p/the-economic-value-of-rapid-response.html. It does not contain the 400 ms threshold that https://lawsofux.com/doherty-threshold/ attributes to it.
12. Kreitz & Niemelä (2010). *Spotify – Large Scale, Low Latency, P2P Music-on-Demand Streaming.* IEEE P2P'10. https://web.archive.org/web/20240520124158/https://www.csc.kth.se/~gkreitz/spotify-p2p10/ (median start latency 265 ms; under 1% of playbacks stuttered)
13. Viget (2017). *A Bone to Pick with Skeleton Screens.* https://www.viget.com/articles/a-bone-to-pick-with-skeleton-screens
14. NN/g, *Skeleton Screens 101* (Tankala, 2023). https://www.nngroup.com/articles/skeleton-screens/
15. Kahneman, Fredrickson, Schreiber, Redelmeier (1993). *When More Pain Is Preferred to Less.* Psychological Science 4(6). https://ius.uzh.ch/dam/jcr:5ae9adc9-61ec-4174-b37c-4b752f36c23b/Kahnemann%20et%20al.%20-%20When%20More%20Pain%20is%20Preferred%20to%20Less%20(1993).pdf
16. NN/g, *Peak–End Rule* (video). https://www.nngroup.com/videos/peak-end-rule/
17. NN/g, *Fitts's Law and Its Applications in UX* (Budiu, 2022). https://www.nngroup.com/articles/fitts-law/
18. W3C, *Understanding SC 2.5.8 Target Size (Minimum).* https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html
19. Hick–Hyman law. https://en.wikipedia.org/wiki/Hick%27s_law
20. Tanvir, Bunt, Cockburn, Irani (2011). *Improving cascading menu selections with adaptive activation areas.* International Journal of Human-Computer Studies 69(11). https://hci.cs.umanitoba.ca/publications/details/improving-cascading-menu-selections-with-adaptive-activation-areas
21. Framer, *Cursor trajectory* (safe triangle). https://www.framer.com/blog/cursor-trajectory/
22. NN/g, *Memory Recognition and Recall in User Interfaces* (Budiu, 2024). https://www.nngroup.com/articles/recognition-and-recall/
23. NN/g, *Dark Mode vs. Light Mode* (Budiu, 2020; Piepenbrock et al. 2013). https://www.nngroup.com/articles/dark-mode/
24. Saffer (2013). *Microinteractions.* O'Reilly: triggers, rules, feedback, loops and modes.
25. Apple Human Interface Guidelines, *Motion* (and *Accessibility* for Reduce Motion). https://developer.apple.com/design/human-interface-guidelines/motion
26. Chisnall (2007). *Bad UI of the Week: Ask Forgiveness, Not Permission.* https://web.archive.org/web/20210304012226/https://www.informit.com/articles/article.aspx?p=1091575. Also Nielsen (2018), *Confirmation Dialogs Can Prevent User Errors — If Not Overused*: https://www.nngroup.com/articles/confirmation-dialog/
27. NN/g, *Designing Empty States in Complex Applications* (Kaplan, 2021). https://www.nngroup.com/articles/empty-state-interface-design/
28. W3C, *Understanding SC 2.1.4 Character Key Shortcuts* (Level A). https://www.w3.org/WAI/WCAG21/Understanding/character-key-shortcuts.html
29. Cunningham, Jones, Jones (2004). *Organizing digital music for use.* ISMIR. https://researchcommons.waikato.ac.nz/handle/10289/66
30. Lee & Downie (2004). *Survey of music information needs, uses, and seeking behaviours.* ISMIR. https://archives.ismir.net/ismir2004/paper/000232.pdf
31. Cunningham, Bainbridge, Falconer (2006). *"More of an art than a science": supporting the creation of playlists and mixes.* ISMIR. https://researchcommons.waikato.ac.nz/handle/10289/77
32. Hagen (2015). *The Playlist Experience.* Popular Music & Society 38(5). doi:10.1080/03007766.2015.1021174
33. Norton, Mochon, Ariely (2012). *The IKEA effect.* Journal of Consumer Psychology 22(3). https://dash.harvard.edu/handle/1/12136084
34. Sinclair & Tinson (2017). *Psychological ownership and music streaming consumption.* Journal of Business Research 71. https://dspace.stir.ac.uk/handle/1893/24523
35. Not used: https://ideas.repec.org/a/taf/tbitxx/v40y2021i16p1806-1827.html is Rushan (2021), on swiping and album art and the adoption of an app, not on how a cover changes the rating of a song.
36. Poláček (2014). *How to shuffle songs?* Spotify Engineering. https://web.archive.org/web/2022/https://engineering.atspotify.com/2014/02/how-to-shuffle-songs
37. Borgne & Sahiner (2025). *Shuffle: Making Random Feel More Human.* Spotify Engineering. https://engineering.atspotify.com/2025/11/shuffle-making-random-feel-more-human
38. Yates (2023). *Why Randomness Doesn't Feel Random.* Behavioral Scientist. https://behavioralscientist.org/?p=42092
39. MIDiA Research (2022). *Music consumer survey Q3 2021: a new generation leans forward* (paywalled; no figures). https://midiaresearch.com/reports/music-consumer-survey-q3-2021-a-new-generation-leans-forward
40. Spotify Newsroom (2021), lyrics: "one of the most requested features from listeners across the globe". https://newsroom.spotify.com/tag/musixmatch/
41. Engadget (20 June 2023), Spotify's desktop redesign. https://www.engadget.com/spotify-desktop-app-gets-a-new-look-and-upgraded-library-features-184540624.html
42. Spotify Newsroom (2023), compact library view. https://newsroom.spotify.com/tag/compact-view/
43. MacStories (2020), Apple Music for the web. https://www.macstories.net/news/apple-music-for-web-debuts-new-beta-version-with-fresh-design-and-listen-now/
44. Spotify Community (2023), the Now Playing panel opening on play. https://community.spotify.com/t5/Desktop-Windows/Desktop-New-quot-Now-Playing-View-quot-sidebar/m-p/5604632
45. 9to5Google (2025, 2026), YouTube Music Now Playing redesign. https://9to5google.com/2026/04/23/youtube-music-split-now-playing-redesign/
46. Spotify Community, "[Queue] Playlist from current Queue" (2017, "Not Right Now", 852 likes). https://community.spotify.com/t5/Live-Ideas/Queue-Playlist-from-current-Queue/idi-p/1586088
47. Apple Support, the Playing Next queue in Music on Mac. https://support.apple.com/guide/music/musb1e6d1c76/1.5/mac/15.0
48. Spotify Community (2022), stopping at the end of an album. https://community.spotify.com/t5/Desktop-Windows/How-to-disable-autoplay-after-album-ends/m-p/5508449
49. Android Authority, disabling Smart Shuffle (Wayback, April 2025). https://www.androidauthority.com/spotify-disable-smart-shuffle-completely-3548689
50. Spotify Community (2016), selecting several songs. https://community.spotify.com/t5/Desktop-Windows/Selecting-Multiple-Songs-at-Once/m-p/1530879
51. Android Police (2023), the "+" replacing the heart. https://www.androidpolice.com/spotify-plus-button/. How-To Geek on Yahoo (2024), the confusion it causes: https://tech.yahoo.com/general/articles/spotify-needs-fix-confusing-system-181516927.html
52. Spotify Community, lyrics on full screen (implemented 20 January 2023). https://community.spotify.com/t5/Implemented-Ideas/Lyrics-on-Fullscreen/idi-p/5229221
53. Spotify Support, keyboard shortcuts. https://support.spotify.com/us/article/Keyboard-shortcuts/
54. Spotify for Artists, loudness normalization. https://support.spotify.com/au/artists/article/loudness-normalization/
55. Spotify Community (2025), recent searches. https://community.spotify.com/t5/Desktop-Windows/Why-are-my-recent-searches-only-showing-up-in-a-drop-down-on/td-p/6630043
56. Plexamp on the App Store (UltraBlur, loudness leveling, true gapless). https://apple.co/3R0OiUl
57. Feishin. https://github.com/jeffvli/feishin
58. MDN, *Media Session API.* https://developer.mozilla.org/en-US/docs/Web/API/Media_Session_API. Also web.dev: https://web.dev/articles/media-session
59. web.dev, *Media Source Extensions for Audio* (gapless). https://web.dev/mse-seamless-playback. Also Gapless-5: https://github.com/regosen/Gapless-5
60. Apple Support, Sound Check and AutoMix/Crossfade in Music on Mac. YouTube Music has no official page on autoplay at the end of the queue or on keyboard shortcuts (not verifiable).
