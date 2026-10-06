// The lists every view is made of: rows of songs, cards of albums and
// artists, and the next page that arrives when the end comes into view.
// Rows share their behaviour here: selection (click, Shift, Ctrl), the keys,
// the ⋯ menu (also on right-click), the heart, the mark of the song that is
// playing, and drag to a playlist of the sidebar. Markup lives in the
// templates of index.html; layout in app.css §10.
import { coverUrl } from './api.js';
import { $, clone, closeMenu, formatTime, openMenu, pending, setCover, setIcon, show } from './ui.js';
import * as act from './actions.js';
import * as player from './player.js';

// ---- The head every list page has ----------------------------------------------

// Title and the launcher of the one global search (proposal 7.5).
export function listHead(head) {
  const link = clone('t-searchlink');
  $('kbd', link).textContent = $('#search-hint').textContent;
  $('.head-tools', head).append(link);
  return head;
}

// ---- Rows of songs ---------------------------------------------------------------

// The selection bar lifts the toasts above it (app.css §8). A custom element
// knows when its bar comes and goes, however it goes: a list made again, a
// page left.
let bars = 0;
customElements.define('x-selbar', class extends HTMLElement {
  connectedCallback() { $('#toasts').toggleAttribute('data-lifted', ++bars > 0); }
  disconnectedCallback() { $('#toasts').toggleAttribute('data-lifted', --bars > 0); }
});

const inside = 'a, button';
const CHUNK = 20; // rows to a block that skips its rendering off screen
const touch = () => matchMedia('(hover: none)').matches; // no hover, no double-click: a tap plays

function heartOf(row) {
  const on = row.track.favorite;
  const heart = $('.heart', row);
  heart.classList.toggle('is-fav', on);
  heart.setAttribute('aria-label', on ? 'Remove from favorites' : 'Add to favorites');
}

function makeRow(track, number) {
  const row = clone('t-track');
  row.track = track;
  const same = track.artist === track.album.artist.name;
  const artist = `/artists/${track.album.artist.id}`;
  $('.n', row).textContent = number;
  $('.nm', row).textContent = track.title;
  setCover($('.th', row), track.album.cover, 256);
  for (const link of row.querySelectorAll('.ar, .c-artist')) {
    link.textContent = track.artist;
    // The artist of the track is free text: a link only when it is the album's artist (proposal 4.2).
    if (same) link.href = artist; else link.removeAttribute('href');
  }
  const album = $('.c-album', row);
  album.textContent = track.album.title;
  album.href = `/albums/${track.album.id}`;
  $('.c-genre', row).textContent = track.genre || '';
  $('.lyr', row).textContent = track.has_lyrics ? 'Lyrics' : '';
  $('.tm', row).textContent = formatTime(track.duration_ms);
  $('.dots', row).setAttribute('aria-label', `More actions, ${track.title}`);
  row.classList.toggle('is-off', !track.available);
  heartOf(row);
  return row;
}

// The song that is playing takes the accent and the bars of an equalizer
// where its number was; they stand still while it is paused.
function markPlaying() {
  const now = player.current(), playing = player.isPlaying();
  for (const row of document.querySelectorAll('.tl .tr:not(.hd)')) {
    const mine = !!now && row.track?.id === now.track.id;
    if (!mine && !row.classList.contains('is-playing')) continue; // it was not the song, and is not: nothing to change
    row.classList.toggle('is-playing', mine);
    show($('.eq', row), mine);
    $('.eq', row).classList.toggle('is-paused', !playing);
    // What the row's button does when pressed: pause the song, or play it.
    setIcon($('.pl', row), mine && playing ? 'pause' : 'play');
  }
}
document.addEventListener('player:track', markPlaying);
document.addEventListener('player:state', markPlaying);

// A heart changed anywhere: every row of that song follows, and on the list
// of favorites a song that is no longer one steps aside (Undo brings it back).
document.addEventListener('track:favorite', ({ detail: { track } }) => {
  for (const row of document.querySelectorAll('.tl .tr:not(.hd)')) {
    if (row.track.id !== track.id) continue;
    row.track.favorite = track.favorite;
    heartOf(row);
    if (row.closest('.tl').dataset.only === 'favorites') row.hidden = !track.favorite;
  }
});

// A table of songs. `kind` picks the columns ('library', 'artist', 'search',
// 'album', 'playlist'); `context()` says where the music comes from; `skip`
// leaves out the links to the page the songs are on. A playlist also gives
// `menu(rows)` (its own ⋯ menu), `remove(rows)` (Delete key, the selection's
// button), `reorder(row, before)` (the grip, Alt and the arrows: `before` is
// the row it goes in front of, null for the end) and `rest()` (the songs
// that are not loaded yet, so Enter plays to the end of the playlist).
export function trackList(kind, label, context, { skip = [], only = null, menu = null, remove = null, reorder = null, rest = null } = {}) {
  const el = document.createElement('div');
  el.className = `tl is-${kind}`;
  el.setAttribute('role', 'table');
  el.setAttribute('aria-label', label);
  if (only) el.dataset.only = only;
  const rows = [];
  const selected = new Set();
  let anchor = null, active = null, bar = null;
  const shown = () => rows.filter(row => !row.hidden);

  // One row of the list is a tab stop; the arrows move between rows, and
  // Tab goes through the buttons and links of the row that has it.
  function activate(row) {
    if (active === row) return;
    for (const [one, value] of [[active, -1], [row, null]]) {
      if (!one) continue;
      one.tabIndex = value ?? 0;
      for (const control of one.querySelectorAll(inside)) { if (value) control.tabIndex = value; else control.removeAttribute('tabindex'); }
    }
    active = row;
  }

  function showSelection() {
    for (const row of rows) {
      row.classList.toggle('is-sel', selected.has(row));
      if (selected.has(row)) row.setAttribute('aria-selected', 'true'); else row.removeAttribute('aria-selected');
    }
    if (selected.size < 2) { bar?.remove(); bar = null; return; }
    if (!bar) {
      bar = clone('t-selbar');
      show($('[data-act="remove"]', bar), !!remove);
      bar.addEventListener('click', onBar);
      el.before(bar);
    }
    $('.sel-count', bar).textContent = `${selected.size} selected`;
    const all = [...selected].every(row => row.track.favorite);
    $('.sel-fav span', bar).textContent = all ? 'Remove from favorites' : 'Add to favorites';
  }

  function select(row, { shiftKey = false, ctrlKey = false, metaKey = false } = {}) {
    if (shiftKey && anchor?.isConnected) {
      const list = shown(), [a, b] = [list.indexOf(anchor), list.indexOf(row)].sort((x, y) => x - y);
      selected.clear();
      for (const one of list.slice(a, b + 1)) selected.add(one);
    } else if (ctrlKey || metaKey) {
      if (!selected.delete(row)) selected.add(row);
      anchor = row;
    } else {
      selected.clear();
      selected.add(row);
      anchor = row;
    }
    activate(row);
    showSelection();
  }

  const clear = () => { selected.clear(); showSelection(); };
  const chosen = () => [...selected].map(row => row.track);
  const chosenRows = () => rows.filter(row => selected.has(row));

  function onBar(event) {
    const button = event.target.closest('[data-act]');
    if (!button) return;
    const tracks = chosen();
    const run = {
      next: () => act.playNext(tracks),
      queue: () => act.addToQueue(tracks),
      playlist: () => act.addSheet(tracks),
      fav: () => act.setFavorites(tracks, !tracks.every(track => track.favorite)),
      remove: () => remove(chosenRows()),
      clear,
    }[button.dataset.act];
    run();
  }

  async function playRow(row) {
    const list = shown();
    const tracks = list.map(one => one.track);
    act.playFrom(rest ? [...tracks, ...await rest()] : tracks, list.indexOf(row), context());
  }

  // The menu of a row works on the whole selection when the row is part of it.
  function menuFor(row, button, at = null) {
    closeMenu();
    const several = selected.has(row) && selected.size > 1;
    const tracks = several ? chosen() : [row.track];
    const opened = openMenu(button, menu ? menu(several ? chosenRows() : [row]) : act.trackMenu(tracks, skip), at);
    if (opened && tracks.length === 1) act.markHeld(opened, tracks[0]);
  }

  el.addEventListener('click', event => {
    const row = event.target.closest('.tr');
    if (!row?.track) return;
    const target = event.target;
    if (target.closest('.heart')) { act.setFavorites([row.track], !row.track.favorite); return; }
    if (target.closest('.dots')) { menuFor(row, target.closest('.dots')); return; }
    if (target.closest('a')) return; // links go where they go
    if (target.closest('.no') || touch()) {
      select(row);
      // The number of the song that plays is its pause button.
      if (player.current()?.track.id === row.track.id) player.toggle(); else playRow(row);
      return;
    }
    select(row, event);
  });
  el.addEventListener('dblclick', event => {
    const row = event.target.closest('.tr');
    if (row && !event.target.closest(inside)) playRow(row);
  });
  el.addEventListener('contextmenu', event => {
    const row = event.target.closest('.tr');
    if (!row?.track) return;
    event.preventDefault();
    if (!selected.has(row)) select(row);
    menuFor(row, $('.dots', row), event.button === 2 ? { x: event.clientX, y: event.clientY } : null);
  });
  el.addEventListener('keydown', event => {
    const row = event.target;
    if (!row.classList?.contains('tr')) return;
    const list = shown(), at = list.indexOf(row);
    const go = to => { event.preventDefault(); const next = list[Math.min(Math.max(to, 0), list.length - 1)]; select(next, { shiftKey: event.shiftKey }); next.focus(); };
    if (reorder && event.altKey && (event.key === 'ArrowUp' || event.key === 'ArrowDown')) {
      event.preventDefault();
      if (event.key === 'ArrowUp' && at > 0) reorder(row, list[at - 1]);
      if (event.key === 'ArrowDown' && at < list.length - 1) reorder(row, list[at + 2] ?? null);
      return;
    }
    switch (event.key) {
      case 'ArrowDown': go(at + 1); break;
      case 'ArrowUp': go(at - 1); break;
      case 'Home': go(0); break;
      case 'End': go(list.length - 1); break;
      case 'Enter': event.preventDefault(); playRow(row); break;
      case 'Escape': if (selected.size) { event.preventDefault(); clear(); } break;
      case 'Delete': case 'Backspace': if (remove) { event.preventDefault(); remove(selected.has(row) ? chosenRows() : [row]); } break;
      case 'a': case 'A': if (event.ctrlKey || event.metaKey) { event.preventDefault(); selected.clear(); for (const one of list) selected.add(one); showSelection(); } break;
      case 'ContextMenu': event.preventDefault(); menuFor(row, $('.dots', row)); break;
      case 'F10': if (event.shiftKey) { event.preventDefault(); menuFor(row, $('.dots', row)); } break;
    }
  });
  // Selected songs are dragged together onto a playlist of the sidebar.
  el.addEventListener('dragstart', event => {
    const row = event.target.closest('.tr');
    if (!row?.track) { event.preventDefault(); return; }
    if (!selected.has(row)) select(row);
    drag(event, chosen());
  });

  // Drag by the grip: the row is picked up, a line shows where it will land,
  // and the page scrolls when the pointer nears its edge.
  el.addEventListener('pointerdown', event => {
    const grip = event.target.closest('.grip');
    const row = grip?.closest('.tr');
    if (!reorder || !row?.track || event.button !== 0) return;
    event.preventDefault();
    const list = shown(), from = list.indexOf(row);
    let slot = from, y = event.clientY;
    const stop = new AbortController();
    const line = document.createElement('div');
    line.className = 'drop-line';
    line.setAttribute('aria-hidden', 'true');
    el.append(line);
    grip.setPointerCapture(event.pointerId);
    row.classList.add('is-drag');
    const place = () => {
      slot = list.findIndex(one => { const box = one.getBoundingClientRect(); return y < box.top + box.height / 2; });
      if (slot < 0) slot = list.length;
      const edge = slot < list.length ? list[slot].getBoundingClientRect().top : list[list.length - 1].getBoundingClientRect().bottom;
      line.style.setProperty('--y', `${edge - el.getBoundingClientRect().top}px`);
    };
    const scroller = setInterval(() => {
      if (y < 80) scrollBy(0, -12); else if (y > innerHeight - 150) scrollBy(0, 12);
      place();
    }, 16);
    const end = drop => {
      clearInterval(scroller);
      stop.abort();
      line.remove();
      row.classList.remove('is-drag');
      if (drop && slot !== from && slot !== from + 1) reorder(row, list[slot] ?? null);
    };
    grip.addEventListener('pointermove', e => { y = e.clientY; place(); }, { signal: stop.signal });
    grip.addEventListener('pointerup', () => end(true), { signal: stop.signal });
    grip.addEventListener('pointercancel', () => end(false), { signal: stop.signal });
    document.addEventListener('keydown', e => { if (e.key === 'Escape') end(false); }, { signal: stop.signal });
    place();
  });

  function add(tracks, numbered = (track, i) => i + 1) {
    const page = document.createDocumentFragment(); // one insertion for the whole page
    const first = rows.length;
    const fresh = tracks.map((track, i) => makeRow(track, numbered(track, first + i)));
    for (const row of fresh) { rows.push(row); row.querySelectorAll(inside).forEach(control => { control.tabIndex = -1; }); }
    // A page of rows is one block that skips its layout and paint while it is
    // far off screen (app.css §10): the browser watches one block per page,
    // not one per row, which is what keeps a list of thousands scrolling.
    for (let i = 0; i < fresh.length; i += CHUNK) {
      const chunk = document.createElement('div');
      chunk.className = 'tl-chunk';
      chunk.setAttribute('role', 'rowgroup');
      chunk.style.setProperty('--n', Math.min(CHUNK, fresh.length - i));
      chunk.append(...fresh.slice(i, i + CHUNK));
      page.append(chunk);
    }
    el.append(page);
    if (!active && fresh.length) activate(fresh[0]);
    markPlaying();
    return fresh;
  }

  // What a playlist does to its rows: a row goes in front of another (or to
  // the end), a row leaves, a row steps aside until Undo has had its say.
  function move(row, before) {
    const had = document.activeElement === row;
    rows.splice(rows.indexOf(row), 1);
    if (before) { rows.splice(rows.indexOf(before), 0, row); before.before(row); } else { rows.push(row); el.append(row); }
    if (had) row.focus({ preventScroll: true });
  }
  function hide(row, hidden) {
    row.hidden = hidden;
    if (hidden && selected.delete(row)) showSelection();
  }
  function drop(row) {
    const at = rows.indexOf(row);
    if (at >= 0) rows.splice(at, 1); // a row of a list that was made again is not here
    row.remove();
    if (selected.delete(row)) showSelection();
    if (active === row) { active = null; const [first] = shown(); if (first) activate(first); }
  }
  // The focus goes to the row beside one that left, so the keyboard is not lost.
  function focusNear(row) {
    const list = shown().filter(one => one !== row);
    const near = list.find(one => row.compareDocumentPosition(one) & Node.DOCUMENT_POSITION_FOLLOWING) ?? list.at(-1);
    if (near) { activate(near); near.focus({ preventScroll: true }); }
  }

  return { el, add, rows, tracks: () => shown().map(row => row.track), clear, move, hide, drop, focusNear, chosen: chosenRows };
}

// ---- Drag to the sidebar ---------------------------------------------------------

let dragged = null;

function drag(event, tracks) {
  dragged = tracks;
  event.dataTransfer.effectAllowed = 'copy';
  event.dataTransfer.setData('text/plain', tracks.map(track => track.title).join('\n'));
  const ghost = clone('t-ghost');
  $('span', ghost).textContent = tracks.length === 1 ? '1 song' : `${tracks.length} songs`;
  document.body.append(ghost);
  event.dataTransfer.setDragImage(ghost, 12, 12);
  setTimeout(() => ghost.remove());
}

// The playlists of the sidebar, and Favorites, take what is dropped on them.
const target = event => (dragged && event.target.closest?.('#side-playlists li')) || null;
let over = null;
const leave = () => { over?.firstElementChild.classList.remove('drop-target'); over = null; };
document.addEventListener('dragover', event => {
  const li = target(event);
  if (!li) { leave(); return; }
  event.preventDefault();
  if (li !== over) { leave(); over = li; li.firstElementChild.classList.add('drop-target'); }
});
document.addEventListener('dragend', () => { leave(); dragged = null; });
document.addEventListener('drop', event => {
  const li = target(event);
  if (!li) return;
  event.preventDefault();
  const tracks = dragged;
  leave();
  dragged = null;
  if (li.dataset.playlist) act.addToPlaylists(tracks, [{ id: li.dataset.playlist, name: $('.nav-label', li).textContent }]);
  else act.setFavorites(tracks, true);
});

// ---- Cards -------------------------------------------------------------------------

// A grid of covers: albums, or round artists.
export function grid(label) {
  const el = document.createElement('div');
  el.className = 'grid';
  el.setAttribute('role', 'list');
  el.setAttribute('aria-label', label);
  return el;
}

export const albumContext = album => ({ type: 'album', id: album.id, name: album.title });

function cardMenu(album) {
  return [
    ...act.collectionMenu(act.lazy(() => act.albumTracks(album.id), 'read the album'), albumContext(album)),
    '-',
    { label: 'Go to artist', icon: 'artist', href: `/artists/${album.artist.id}` },
  ];
}

// `byArtist` shows the artist under the title (the Albums page); without it
// the line says how many songs the album has (the pages of an artist).
export function albumCard(album, byArtist) {
  const card = clone('t-album-card');
  card.album = album;
  const href = `/albums/${album.id}`;
  for (const link of [$('.art a', card), $('.ct', card)]) link.href = href;
  $('.ct', card).textContent = album.title;
  setCover($('.cover-img', card), album.cover, 256);
  $('.cplay', card).setAttribute('aria-label', `Play ${album.title}`);
  $('.dots', card).setAttribute('aria-label', `More actions, ${album.title}`);
  const by = $('.cm-a', card);
  if (byArtist) {
    by.textContent = album.artist.name;
    by.href = `/artists/${album.artist.id}`;
  } else {
    by.textContent = album.track_count === 1 ? '1 song' : `${album.track_count} songs`;
    by.removeAttribute('href');
  }
  $('.cm-b', card).textContent = album.year ?? '';
  return card;
}

// Play and the ⋯ of every card of a grid, and right-click: one listener.
export function cardActions(el) {
  const album = event => event.target.closest('.card')?.album;
  const open = (event, at) => {
    const one = album(event);
    if (!one) return;
    closeMenu();
    openMenu($('.dots', event.target.closest('.card')), cardMenu(one), at);
  };
  el.addEventListener('click', async event => {
    const one = album(event);
    if (event.target.closest('.dots')) open(event, null);
    else if (one && event.target.closest('.cplay')) {
      try {
        act.playAll(await act.lazy(() => act.albumTracks(one.id), 'play the album')(), albumContext(one));
      } catch { /* lazy() has said what failed */ }
    }
  });
  el.addEventListener('contextmenu', event => {
    if (!album(event)) return;
    event.preventDefault();
    open(event, { x: event.clientX, y: event.clientY });
  });
}

// An artist is a circle with initials: there are no pictures of artists.
export function initials(name) {
  const words = name.replace(/^the\s+/i, '').split(/\s+/).filter(Boolean);
  return ((words[0]?.[0] || '') + (words.length > 1 ? words[words.length - 1][0] : '')).toUpperCase();
}

export function personCard(artist) {
  const card = clone('t-person');
  card.href = `/artists/${artist.id}`;
  $('.av', card).textContent = initials(artist.name);
  $('.pn', card).textContent = artist.name;
  $('.note', card).textContent = `Artist · ${artist.album_count === 1 ? '1 album' : `${artist.album_count} albums`}`;
  return card;
}

// A playlist's picture (proposal A2): the first cover with one to three, a
// 2 by 2 mosaic with four, a quiet placeholder with none. Grids use the 256
// covers the rows and the sidebar already have; a head with one cover asks
// for the 640, like an album's.
export function playlistArt(playlist, size = 256, eager = false) {
  const covers = playlist.covers || [];
  const kind = covers.length >= 4 ? '.mosaic' : covers.length ? '.pl-cover' : '.empty-art';
  const art = $(kind, clone('t-pl-art'));
  if (covers.length >= 4) {
    art.querySelectorAll('img').forEach((img, i) => { img.src = coverUrl(covers[i], 256); if (eager) img.loading = 'eager'; });
  } else if (covers.length) {
    art.src = coverUrl(covers[0], size);
    if (eager) art.loading = 'eager';
  }
  return art;
}

// ---- The next page -----------------------------------------------------------------

// The pages of the last lists read, by address, for a minute: Back to a list
// shows it at once, and the next page goes on from where it was. A heart
// changes the songs the pages hold, so any change empties it.
const cache = new Map();
const KEEP = 60_000;
document.addEventListener('track:favorite', () => cache.clear());

// Reads a list a page at a time: the first now, the next ones when the end
// comes into view (an IntersectionObserver, no scroll handler). A page that
// takes more than a second shows still blocks where it will land; one that
// fails leaves a "Try again" instead of an error page. `read(after)` answers
// {items, next}; `add(items)` puts them in the list; `note(loaded)` is the
// sentence under the list while there is more.
export function pager(host, { key = null, read, add, note, blocks = 't-ph-rows', signal }) {
  const end = clone('t-more');
  host.after(end);
  let next, busy = false;
  const watch = new IntersectionObserver(entries => { if (entries[0].isIntersecting) more().catch(() => {}); }, { rootMargin: '0px 0px 800px' });
  signal?.addEventListener('abort', () => watch.disconnect());
  let loaded = 0;
  let kept = cache.get(key);
  if (kept && Date.now() - kept.at > KEEP) kept = null;
  if (key && !kept) { kept = { at: Date.now(), pages: [] }; cache.delete(key); cache.set(key, kept); if (cache.size > 8) cache.delete(cache.keys().next().value); }

  function say() {
    $('.more-text', end).textContent = next && note ? note(loaded) : '';
    show($('.more-text', end), !!next && !!note);
  }

  async function more() {
    if (busy || next === null || signal?.aborted) return;
    busy = true;
    show($('.link', end), false);
    const done = pending(end, blocks);
    try {
      const page = await read(next);
      if (signal?.aborted) return;
      next = page.next;
      loaded += page.items.length;
      kept?.pages.push(page);
      add(page.items);
    } catch (error) {
      if (error.name === 'AbortError') return;
      if (loaded === 0 && next === undefined) throw error; // the first page: the view shows the error page
      show($('.link', end), true);
    } finally {
      done();
      busy = false;
    }
    say();
    if (next === null) watch.disconnect(); else { watch.unobserve(end); watch.observe(end); }
  }
  $('.link', end).addEventListener('click', () => more().catch(() => {}));
  show($('.more-text', end), false);
  show($('.link', end), false);
  // The end is watched once the first page is in: until then it is the
  // view that waits for the answer, and shows its error.
  // A list read a moment ago comes back whole, at once.
  let first;
  if (kept?.pages.length) {
    for (const page of kept.pages) { loaded += page.items.length; add(page.items); next = page.next; }
    say();
    if (next) watch.observe(end);
    first = Promise.resolve();
  } else {
    first = more();
  }
  return { first, more, say, next: () => next ?? null, remove: () => { watch.disconnect(); end.remove(); } };
}
