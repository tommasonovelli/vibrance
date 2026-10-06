// The sidebar: the playlists and counts it lists, the current page, and the
// buttons at its foot (sign out, theme, collapse). The theme and the
// collapsed state were put on <html> by prefs.js before the first paint;
// this module keeps them in step with the buttons.
import { api, coverUrl, optional } from './api.js';
import { $, clone, openSheet, setIcon, show, toast } from './ui.js';
import { navigate } from './router.js';
import { saveSetting } from './settings.js';

const root = document.documentElement;
const number = new Intl.NumberFormat('en');
let playlists = [];
let playingFrom = null; // the id of the playlist the music comes from

// The playlists as the sidebar last read them, for menus and sheets.
export const getPlaylists = () => playlists;

function remember(key, value) {
  try { localStorage.setItem(key, value); } catch { /* not remembered */ }
}

// ---- Current page and the playing mark -------------------------------------

export function setCurrent(path) {
  for (const link of document.querySelectorAll('.sidebar a[aria-current]')) link.removeAttribute('aria-current');
  let link = null;
  const playlist = /^\/playlists\/([^/]+)/.exec(path);
  if (playlist) {
    link = document.querySelector(`.sidebar [data-playlist="${CSS.escape(playlist[1])}"] a`);
  } else {
    const name = path === '/' ? 'library' : path.split('/')[1];
    link = document.querySelector(`.sidebar [data-nav="${CSS.escape(name)}"]`);
  }
  link?.setAttribute('aria-current', 'page');
}

// The speaker beside a playlist whose songs are playing (null: none).
export function setPlayingFrom(id) {
  playingFrom = id;
  for (const li of document.querySelectorAll('#side-playlists [data-playlist]')) {
    show($('.ctx', li), li.dataset.playlist === id);
  }
}

// ---- Content ---------------------------------------------------------------

// One row of the list: name, count, the first cover (or the quiet icon).
function fillPlaylist(li, playlist) {
  li.dataset.playlist = playlist.id;
  $('a', li).href = `/playlists/${playlist.id}`;
  $('.nav-label', li).textContent = playlist.name;
  $('.nav-count', li).textContent = playlist.item_count;
  const art = coverUrl(playlist.covers?.[0], 256);
  const img = $('.side-cover', li);
  if (art && img.getAttribute('src') !== art) img.src = art;
  show(img, !!art);
  show($('.fav-ic', li), !art);
}

function renderPlaylists() {
  const list = $('#side-playlists');
  for (const li of list.querySelectorAll('[data-playlist]')) li.remove();
  const rows = document.createDocumentFragment();
  for (const playlist of playlists) {
    const li = clone('t-side-playlist');
    fillPlaylist(li, playlist);
    rows.append(li);
  }
  list.append(rows);
  setPlayingFrom(playingFrom);
  setCurrent(location.pathname);
}

const changed = () => document.dispatchEvent(new CustomEvent('playlists:changed'));

// A playlist was made or changed (a rename, songs added or taken out): its
// row follows, and nothing else is read again. Every answer that changes a
// playlist carries it whole, so this is all the sidebar ever needs.
function update(playlist) {
  const at = playlists.findIndex(p => p.id === playlist.id);
  if (at >= 0) playlists[at] = playlist; else playlists.push(playlist);
  let li = document.querySelector(`#side-playlists [data-playlist="${CSS.escape(playlist.id)}"]`);
  if (!li) {
    li = clone('t-side-playlist');
    $('#side-playlists').append(li);
  }
  fillPlaylist(li, playlist);
  setPlayingFrom(playingFrom);
  setCurrent(location.pathname);
  changed();
}

function drop(id) {
  playlists = playlists.filter(p => p.id !== id);
  document.querySelector(`#side-playlists [data-playlist="${CSS.escape(id)}"]`)?.remove();
  changed();
}

// Playlists change from many places; they say so, and the sidebar listens.
document.addEventListener('playlist:changed', ({ detail }) => update(detail.playlist));
document.addEventListener('playlist:deleted', ({ detail }) => drop(detail.id));

// Reads the playlists and the number of favorites again. A part the server
// does not have yet, or cannot answer, is left out: the sidebar is never the
// reason a page fails.
export async function reload() {
  const [lists, favorites] = await Promise.all([
    api.get('/playlists').catch(() => null),
    optional(api.get('/me/favorites/summary')).catch(() => null),
  ]);
  if (lists) {
    playlists = lists.playlists;
    renderPlaylists();
    changed();
  }
  const count = $('#favorites-count');
  count.hidden = !favorites;
  if (favorites) count.textContent = number.format(favorites.track_count);
}

// The badge of Administration counts what needs attention; status.js reads it.
document.addEventListener('library:status', ({ detail }) => {
  const badge = $('#admin-count');
  badge.hidden = !detail.problems.length;
  badge.textContent = detail.problems.length;
});

// ---- New playlist ----------------------------------------------------------

// Opens the sheet and creates the playlist; a new playlist opens on its page,
// unless the caller says what to do with it (a menu adding songs to it).
// `name` fills the field in advance (saving the queue).
export function newPlaylist(onCreated = playlist => navigate(`/playlists/${playlist.id}`), { name = '' } = {}) {
  const dialog = openSheet('t-sheet-new-playlist', d => {
    $('input[name="name"]', d).value = name;
  });
  const form = $('form', dialog);
  if (name) form.elements.name.select();
  form.addEventListener('submit', async event => {
    if (event.submitter?.value !== 'create') return;
    event.preventDefault();
    const name = form.elements.name.value.trim();
    if (!name) {
      form.elements.name.focus();
      return;
    }
    try {
      const playlist = await api.post('/playlists', { name, description: form.elements.description.value.trim() });
      dialog.close('create');
      document.dispatchEvent(new CustomEvent('playlist:changed', { detail: { playlist } }));
      onCreated(playlist);
    } catch (error) {
      toast({ title: 'Couldn’t create the playlist', sub: error.message, badge: 'alert', error: true });
    }
  });
}

// ---- Foot: theme, collapse, sign out ---------------------------------------

function showTheme() {
  const dark = root.dataset.theme !== 'light';
  const button = $('#theme-toggle');
  setIcon(button, dark ? 'moon' : 'sun');
  $('.nav-label', button).textContent = `Theme: ${dark ? 'Dark' : 'Light'}`;
}

function showCollapsed() {
  const collapsed = root.dataset.sidebar === 'collapsed';
  const button = $('#sidebar-toggle');
  button.setAttribute('aria-expanded', String(!collapsed));
  $('.nav-label', button).textContent = `${collapsed ? 'Expand' : 'Collapse'} sidebar`;
}

export async function init(me) {
  $('#account-name').textContent = me.username;
  $('#nav-admin').hidden = me.role !== 'admin';
  if (/Mac|iPhone|iPad/.test(navigator.platform)) $('#search-hint').textContent = '⌘ K';
  showTheme();
  showCollapsed();
  document.addEventListener('route', event => setCurrent(event.detail.path));

  $('#new-playlist').addEventListener('click', () => newPlaylist());
  // Account's Theme row and the server's answer change it too: the label follows.
  $('#theme-toggle').addEventListener('click', () => saveSetting('theme', root.dataset.theme === 'light' ? 'dark' : 'light'));
  document.addEventListener('settings:changed', showTheme);
  $('#sidebar-toggle').addEventListener('click', () => {
    if (root.dataset.sidebar === 'collapsed') delete root.dataset.sidebar; else root.dataset.sidebar = 'collapsed';
    remember('vibrance.sidebar', root.dataset.sidebar || 'expanded');
    showCollapsed();
  });
  $('#sign-out').addEventListener('click', async () => {
    try { await api.post('/auth/logout'); } catch { /* signed out already, or the server is away: leave all the same */ }
    location.assign('/login');
  });
  await reload();
}
