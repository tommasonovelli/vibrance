# Web interface

The listening interface of Vibrance: plain HTML, CSS and JavaScript (ES modules), with no framework, no build step, no package and no external URL. It talks to the JSON API under `/api/v1` and to nothing else.

The server embeds this folder (`web/embed.go`) and serves it at its root (`internal/api/ui.go`): every file at its path, `login.html` at `/login`, and `index.html` at every other path outside `/api` and `/health`, with the `Content-Security-Policy` below. Only `.html`, `.js`, `.css`, `.svg` and `.woff2` files are embedded: a file of another type added here is not served.

## Files

| File | What it does |
|---|---|
| `index.html` | The app shell: the SVG sprite (brand and icons), sidebar, page, queue panel, player bar, toast region, and a `<template>` for every piece a view repeats |
| `login.html`, `login.js` | The sign-in page, with its own small script |
| `app.css` | All the styles, in numbered sections; tokens on `:root` as `light-dark()` |
| `app.js` | Boot: session check, the parts below, the global search keys |
| `prefs.js` | Classic script in `<head>`: theme, sidebar and queue state on `<html>` before the first paint |
| `api.js` | The only module that calls `fetch`: errors, 401 redirect, pages, cover and audio URLs |
| `router.js` | History API routing, link interception, view transitions, scroll restore |
| `ui.js` | Shared helpers: templates, durations, counts, toasts, menus, sheets, states |
| `list.js` | Rows of songs, album and artist cards, selection, drag, the pager for the next page |
| `actions.js` | What a person does with songs: play, queue, favorites, playlists, details, downloads |
| `player.js` | Audio engine (two elements), queue, shuffle, repeat, volume leveling, Media Session |
| `bar.js` | The player bar and the transport controls shared with Now playing |
| `queue.js` | The queue panel and the "Up next" list |
| `shortcuts.js` | Keyboard shortcuts and their sheet |
| `settings.js` | Preferences that follow the person (`/me/settings`, with a local fallback) |
| `sidebar.js` | Playlists, counts, the current page, new playlist, theme, collapse, sign out |
| `status.js` | State of the library for admins, the one banner (offline, no answer, library) |
| `views/*.js` | One module per screen: `library`, `albums`, `album`, `artists`, `artist`, `playlists`, `playlist`, `search`, `now-playing`, `account`, `admin`, `not-found` |
| `fonts/`, `grain.svg`, `favicon.svg`, `no-cover.svg` | Static assets; see [VENDOR.md](VENDOR.md) |

## How a view works

`app.js` maps each path to a module that loads when first needed. A view exports `render(main, params, signal)`: `params` holds the parts of the path (`id`), `path` and `query`; `signal` aborts when the person has gone elsewhere. The view clones templates with `clone('t-...')`, fills them with `textContent`, `src` and `href`, and appends them to `main`; it puts its head first and awaits its data after. It passes `signal` to every request and stops writing when it is aborted. Listeners on `document` are added with `{ signal }` so they leave with the page. The router keeps the old page for at most one second while the new one loads, and shows still blocks after that.

There is no store. The player owns the playback state; other modules read it through its functions and listen to events.

## Events

All on `document`, as `CustomEvent`s.

| Event | Detail | Sent by |
|---|---|---|
| `player:track` | `{track, context}` or `null` | the song that is current changed |
| `player:state` | `{playing, ended}` | play, pause, the queue ran out |
| `player:queue` | `{queue}` | the songs after the current one changed |
| `player:time` | `{position, duration}` (ms) | about 4 times a second, not while the page is hidden |
| `player:volume` | `{volume, muted}` | volume or mute |
| `player:mode` | `{shuffle, repeat}` | shuffle or repeat |
| `playlist:changed` | `{playlist, added?, removed?, own?}` | any answer that changes a playlist; sidebar, grid and open page follow |
| `playlist:deleted` | `{id}` | a playlist was deleted |
| `track:favorite` | `{track}` | a song's heart changed (and again if the server refused) |
| `route` | `{path, query}` | a page is about to be shown |
| `queue:panel`, `now:toggle`, `settings:changed`, `library:status`, `net:down`, `net:up` | | the queue panel, Now playing's toggles, preferences, the library of an admin, reachability |

## Content-Security-Policy

The page is written for `default-src 'self'`. The code keeps these rules:

- no inline `<script>`, no `on*=` attributes, no `eval`;
- no `style="..."` attribute and no `setAttribute('style', ...)`; dynamic values go through `el.style.setProperty('--x', value)` or properties (CSSOM);
- no `innerHTML` with data: markup lives in `<template>`s and data goes in with `textContent`, `src` and `href`;
- no `data:` URI (the grain is a file) and no URL of another host.

## API operations

Every operation of `api/openapi.yaml` the screens need, plus the additions of [docs/proposals/web-client-api.md](../../docs/proposals/web-client-api.md). Those that come from the proposal are marked; a `404 not_found` on one of them is not an error: what it would show is hidden.

| Operation | Used for | If missing |
|---|---|---|
| `GET /tracks` (**A1**) | the Library, the songs of an artist, the queue after the loaded rows | the Library is an error page; an artist's songs come from its albums |
| `GET /catalog/summary` (**A3**) | counts in the Library, Albums, Artists and Administration | the counts are left out |
| `GET /me/favorites/summary` (**A4**) | the count of Favorites in the sidebar and its tile | the count is left out |
| `Playlist.covers` (**A2**) | the covers of playlists | a placeholder |
| `GET /tracks/random` (**B1**) | Shuffle of the Library or an artist, the queue when shuffled | the loaded songs are shuffled |
| `POST /me/favorites/tracks` (**B2**) | several favorites at once | one `PUT` per song |
| `DELETE /me/sessions` (**B3**) | "Sign out everywhere else" | one `DELETE` per session |
| `GET /tracks/{id}/playlists` (**B4**) | check marks in "Add to playlist" | no check marks |
| `GET`, `PUT /me/settings` (**B5**) | leveling, single-key shortcuts and theme on every device | kept in this browser |
| `B6` (gapless data) | not used | |
| `/server`, `/me`, `/me/password`, `/me/sessions`, `/auth/login`, `/auth/logout` | sign in, version, account | |
| `/albums`, `/albums/{id}`, `/artists`, `/artists/{id}`, `/search`, `/tracks/{id}`, `/tracks/{id}/lyrics`, `/tracks/{id}/audio`, `/albums/{id}/cover` | the catalog and the player | |
| `/playlists`, `/playlists/{id}`, `/playlists/{id}/items`, `.../items/{item}`, `.../items/{item}/move`, `/me/favorites/tracks`, `/me/favorites/tracks/{id}` | playlists (`ETag` and `If-Match`; `412` reads the playlist again) and favorites | |
| `/admin/library`, `/admin/library/scan`, `/admin/users`, `/admin/users/{id}`, `/admin/users/{id}/password` | Administration, admins only | |

A cover is always asked for with `coverUrl()`: the URL carries the cover hash, only the sizes 256 (rows, grids, bar, queue) and 640 (heads, Now playing, Media Session) are used, and `original` only to download, so the browser downloads each cover once.

## Checking it

There is no test suite in this folder yet. The interface was checked against a throwaway mock of the API, in a real browser, outside the repository.
