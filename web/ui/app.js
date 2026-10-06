// Boot: who is signed in, the sidebar, the player bar and its queue, the page
// of the address, the keys that work everywhere. Nothing else lives here.
import { api } from './api.js';
import { $, errorNotice } from './ui.js';
import * as router from './router.js';
import * as sidebar from './sidebar.js';
import * as player from './player.js';
import { initBar } from './bar.js';
import { initQueue } from './queue.js';
import { initShortcuts } from './shortcuts.js';
import { loadSettings } from './settings.js';
import * as status from './status.js';

// Each path and the module of its view, loaded when first needed.
const routes = {
  '/': () => import('./views/library.js'),
  '/favorites': () => import('./views/library.js'),
  '/albums': () => import('./views/albums.js'),
  '/albums/:id': () => import('./views/album.js'),
  '/artists': () => import('./views/artists.js'),
  '/artists/:id': () => import('./views/artist.js'),
  '/playlists': () => import('./views/playlists.js'),
  '/playlists/:id': () => import('./views/playlist.js'),
  '/search': () => import('./views/search.js'),
  '/now-playing': () => import('./views/now-playing.js'),
  '/account': () => import('./views/account.js'),
  '/admin': () => import('./views/admin.js'),
};

const typing = element => element?.closest?.('input, textarea, select, [contenteditable]');

function goSearch() {
  if (location.pathname === '/search') $('#q')?.focus(); else router.navigate('/search');
}

// Ctrl or Cmd+K, and /, open the one global search. Esc closes a menu or a
// sheet by itself: ui.js and <dialog> see to it.
function keys() {
  document.addEventListener('keydown', event => {
    if (document.querySelector('dialog[open]')) return;
    const command = event.ctrlKey || event.metaKey;
    if (command && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      goSearch();
    } else if (event.key === '/' && !command && !event.altKey && !typing(event.target)) {
      event.preventDefault();
      goSearch();
    }
  });
}

async function boot() {
  const main = $('#main');
  router.warm(routes, location.pathname);
  let me;
  try {
    me = await api.get('/me'); // a 401 sends the browser to /login by itself
  } catch (error) {
    main.append(errorNotice(error, () => location.reload()));
    return;
  }
  sidebar.init(me);
  status.init(me); // the library of an admin, and the banner for everyone
  initBar();
  initQueue();
  keys();
  initShortcuts();
  // What was playing comes back paused, and the preferences of this person
  // arrive; neither holds up the page (only Now playing waits for the song).
  player.restore();
  loadSettings();
  router.start(routes, main);
}

boot();
