// The sidebar: the playlists and counts it lists, the current page, and the
// buttons at its foot (sign out, theme, collapse). The theme and the
// collapsed state were put on <html> by prefs.js before the first paint;
// this module keeps them in step with the buttons.
import { api, coverUrl, optional } from './api.js';
import { $, clone, openSheet, setIcon, show, toast } from './ui.js';
import { navigate } from './router.js';

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

function renderPlaylists() {
  const list = $('#side-playlists');
  for (const li of list.querySelectorAll('[data-playlist]')) li.remove();
  for (const playlist of playlists) {
    const li = clone('t-side-playlist');
    li.dataset.playlist = playlist.id;
    $('a', li).href = `/playlists/${playlist.id}`;
    $('.nav-label', li).textContent = playlist.name;
    $('.nav-count', li).textContent = playlist.item_count;
    const art = coverUrl(playlist.covers?.[0], 256);
    if (art) {
      const img = $('.side-cover', li);
      img.src = art;
      img.hidden = false;
    } else {
      $('.fav-ic', li).hidden = false;
    }
    list.append(li);
  }
  setPlayingFrom(playingFrom);
  setCurrent(location.pathname);
}

// Reads the playlists, the number of favorites and, for an admin, the number
// of problems of the library again. A part the server does not have yet, or
// cannot answer, is left out: the sidebar is never the reason a page fails.
export async function reload(isAdmin = !$('#nav-admin').hidden) {
  const [lists, favorites, library] = await Promise.all([
    api.get('/playlists').catch(() => null),
    optional(api.get('/me/favorites/summary')).catch(() => null),
    isAdmin ? optional(api.get('/admin/library')).catch(() => null) : null,
  ]);
  if (lists) {
    playlists = lists.playlists;
    renderPlaylists();
    document.dispatchEvent(new CustomEvent('playlists:changed'));
  }
  const count = $('#favorites-count');
  count.hidden = !favorites;
  if (favorites) count.textContent = number.format(favorites.track_count);
  const problems = $('#admin-count');
  problems.hidden = !library?.problems.length;
  if (library) problems.textContent = library.problems.length;
}

// ---- New playlist ----------------------------------------------------------

// Opens the sheet and creates the playlist; a new playlist opens on its page.
export function newPlaylist() {
  const dialog = openSheet('t-sheet-new-playlist');
  const form = $('form', dialog);
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
      await reload();
      navigate(`/playlists/${playlist.id}`);
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

  $('#new-playlist').addEventListener('click', newPlaylist);
  $('#theme-toggle').addEventListener('click', () => {
    root.dataset.theme = root.dataset.theme === 'light' ? 'dark' : 'light';
    remember('vibrance.theme', root.dataset.theme);
    showTheme();
  });
  $('#sidebar-toggle').addEventListener('click', () => {
    if (root.dataset.sidebar === 'collapsed') delete root.dataset.sidebar; else root.dataset.sidebar = 'collapsed';
    remember('vibrance.sidebar', root.dataset.sidebar || 'expanded');
    showCollapsed();
  });
  $('#sign-out').addEventListener('click', async () => {
    try { await api.post('/auth/logout'); } catch { /* signed out already, or the server is away: leave all the same */ }
    location.assign('/login');
  });
  await reload(me.role === 'admin');
}
