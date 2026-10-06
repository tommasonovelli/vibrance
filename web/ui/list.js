// The lists every view is made of: rows of songs, cards of albums and
// artists, and the next page that arrives when the end comes into view.
// Rows share their behaviour here: selection (click, Shift, Ctrl), the keys,
// the ⋯ menu (also on right-click), the heart, the mark of the song that is
// playing, and drag to a playlist of the sidebar. Markup lives in the
// templates of index.html; layout in app.css §10.
import { api } from './api.js';
import { $, clone, closeMenu, formatTime, openMenu, pending, setCover, show } from './ui.js';
import * as act from './actions.js';
import { current, isPlaying } from './player.js';

// ---- The head every list page has ----------------------------------------------

// Title and the launcher of the one global search (proposal 7.5).
export function listHead(head) {
  const link = clone('t-searchlink');
  $('kbd', link).textContent = $('#search-hint').textContent;
  $('.head-tools', head).append(link);
  return head;
}

// ---- Rows of songs ---------------------------------------------------------------

const inside = 'a, button';
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
  const now = current(), playing = isPlaying();
  for (const row of document.querySelectorAll('.tl .tr:not(.hd)')) {
    const mine = !!now && row.track?.id === now.track.id;
    row.classList.toggle('is-playing', mine);
    show($('.eq', row), mine);
    $('.eq', row).classList.toggle('is-paused', !playing);
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
// 'album'); `context()` says where the music comes from; `skip` leaves out
// the links to the page the songs are on.
export function trackList(kind, label, context, { skip = [], only = null } = {}) {
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

  function onBar(event) {
    const button = event.target.closest('[data-act]');
    if (!button) return;
    const tracks = chosen();
    const run = {
      next: () => act.playNext(tracks),
      queue: () => act.addToQueue(tracks),
      playlist: () => act.addSheet(tracks),
      fav: () => act.setFavorites(tracks, !tracks.every(track => track.favorite)),
      clear,
    }[button.dataset.act];
    run();
  }

  function playRow(row) {
    const list = shown();
    act.playFrom(list.map(one => one.track), list.indexOf(row), context());
  }

  // The menu of a row works on the whole selection when the row is part of it.
  function menuFor(row, button, at = null) {
    closeMenu();
    const tracks = selected.has(row) && selected.size > 1 ? chosen() : [row.track];
    const menu = openMenu(button, act.trackMenu(tracks, skip), at);
    if (menu && tracks.length === 1) act.markHeld(menu, tracks[0]);
  }

  el.addEventListener('click', event => {
    const row = event.target.closest('.tr');
    if (!row?.track) return;
    const target = event.target;
    if (target.closest('.heart')) { act.setFavorites([row.track], !row.track.favorite); return; }
    if (target.closest('.dots')) { menuFor(row, target.closest('.dots')); return; }
    if (target.closest('a')) return; // links go where they go
    if (target.closest('.no') || touch()) { select(row); playRow(row); return; }
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
    switch (event.key) {
      case 'ArrowDown': go(at + 1); break;
      case 'ArrowUp': go(at - 1); break;
      case 'Home': go(0); break;
      case 'End': go(list.length - 1); break;
      case 'Enter': event.preventDefault(); playRow(row); break;
      case 'Escape': if (selected.size) { event.preventDefault(); clear(); } break;
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

  function add(tracks, numbered = (track, i) => i + 1) {
    const page = document.createDocumentFragment(); // one insertion for the whole page
    const first = rows.length;
    const fresh = tracks.map((track, i) => makeRow(track, numbered(track, first + i)));
    for (const row of fresh) { rows.push(row); row.querySelectorAll(inside).forEach(control => { control.tabIndex = -1; }); }
    page.append(...fresh);
    el.append(page);
    if (!active && fresh.length) activate(fresh[0]);
    markPlaying();
  }

  return { el, add, rows, tracks: () => shown().map(row => row.track), clear };
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
  return { first, say, remove: () => { watch.disconnect(); end.remove(); } };
}
