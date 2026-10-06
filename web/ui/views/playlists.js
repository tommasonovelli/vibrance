// The playlists as a grid of covers, after the tile of Favorites. The order is
// the person's (Recently updated, or by name) and stays in the URL. The list
// is read whole (a person has at most 500), so sorting is done here.
import { api, optional } from '../api.js';
import { $, clone, closeMenu, emptyState, formatLength, openMenu, pageHead, show } from '../ui.js';
import * as act from '../actions.js';
import { grid, playlistArt } from '../list.js';
import { newPlaylist } from '../sidebar.js';

const number = new Intl.NumberFormat('en');
const plural = (n, word) => `${number.format(n)} ${word}${n === 1 ? '' : 's'}`;
const names = new Intl.Collator('en', { sensitivity: 'base', numeric: true });
const SORTS = {
  updated: (a, b) => (a.updated_at < b.updated_at ? 1 : a.updated_at > b.updated_at ? -1 : 0), // the stamps have a fixed width
  name: (a, b) => names.compare(a.name, b.name),
};

const meta = playlist => (playlist.item_count ? `${plural(playlist.item_count, 'song')} · ${formatLength(playlist.duration_ms)}` : 'Empty');

function card(playlist) {
  const el = clone('t-playlist-card');
  el.playlist = playlist;
  const href = `/playlists/${playlist.id}`;
  const link = $('.art a', el);
  link.href = href;
  link.append(playlistArt(playlist, 256));
  $('.ct', el).href = href;
  $('.ct', el).textContent = playlist.name;
  show($('.cplay', el), playlist.item_count > 0); // nothing to play in an empty one
  $('.cplay', el).setAttribute('aria-label', `Play ${playlist.name}`);
  $('.dots', el).setAttribute('aria-label', `More actions, ${playlist.name}`);
  $('.cm', el).textContent = meta(playlist);
  return el;
}

function menu(playlist) {
  const read = act.lazy(() => act.playlistTracks(playlist.id), 'read the playlist');
  return [
    ...act.collectionMenu(read, act.playlistContext(playlist), { playlist: false }),
    '-',
    { label: 'Edit details', icon: 'edit', run: () => act.editPlaylist(playlist) },
    { label: 'Delete playlist', icon: 'trash', danger: true, run: () => act.deletePlaylist(playlist) },
  ];
}

export async function render(main, params, signal) {
  const head = pageHead(main, 'Playlists');
  const create = clone('t-new-playlist');
  create.addEventListener('click', () => newPlaylist());
  $('.head-tools', head).append(create);

  // Both are read at once; Favorites' numbers are a nicety, never a failure.
  const [lists, favorites] = await Promise.all([
    api.get('/playlists', null, { signal }),
    optional(api.get('/me/favorites/summary', null, { signal })).catch(() => null),
  ]);
  let playlists = lists.playlists;
  let sort = params.query.get('sort') in SORTS ? params.query.get('sort') : 'updated';

  const view = clone('t-playlists');
  main.append(view);
  const host = $('.list', view), seg = $('.seg', view), count = $('.count', view);
  const cards = grid('Playlists');
  cards.classList.add('grid-pl');
  host.append(cards);
  const tile = clone('t-fav-tile');
  if (favorites) $('.cm', tile).textContent = `${plural(favorites.track_count, 'song')} · ${formatLength(favorites.duration_ms)}`;

  let note = null;
  function draw() {
    const page = document.createDocumentFragment();
    page.append(tile);
    for (const playlist of [...playlists].sort(SORTS[sort])) page.append(card(playlist));
    cards.replaceChildren(page);
    count.textContent = `${plural(playlists.length, 'playlist')} · only you can see them`;
    show(count, true);
    note?.remove();
    note = playlists.length ? null : emptyState({ icon: 'playlists', title: 'No playlists yet', text: 'Make one with New playlist, or add a song to a new playlist from any menu.' });
    if (note) host.append(note);
    for (const button of seg.children) button.setAttribute('aria-checked', String(button.dataset.sort === sort));
  }

  seg.addEventListener('click', event => {
    const key = event.target.closest('[data-sort]')?.dataset.sort;
    if (!key || key === sort) return;
    sort = key;
    const url = new URL(location.href);
    url.searchParams.delete('sort');
    if (sort !== 'updated') url.searchParams.set('sort', sort);
    history.replaceState(history.state, '', url);
    draw();
  });

  // Play, the ⋯ menu and right-click of every card: one listener.
  const of = event => event.target.closest('.card')?.playlist;
  const open = (event, at) => {
    const playlist = of(event);
    if (!playlist) return;
    closeMenu();
    openMenu($('.dots', event.target.closest('.card')), menu(playlist), at);
  };
  cards.addEventListener('click', async event => {
    const playlist = of(event);
    if (event.target.closest('.dots')) open(event, null);
    else if (playlist && event.target.closest('.cplay')) {
      try {
        act.playAll(await act.lazy(() => act.playlistTracks(playlist.id), 'play the playlist')(), act.playlistContext(playlist));
      } catch { /* lazy() has said what failed */ }
    }
  });
  cards.addEventListener('contextmenu', event => {
    if (!of(event)) return;
    event.preventDefault();
    open(event, { x: event.clientX, y: event.clientY });
  });

  // What changes a playlist anywhere (a rename here, a song dropped on the
  // sidebar) changes its card; the order is made again.
  document.addEventListener('playlist:changed', ({ detail }) => {
    const at = playlists.findIndex(playlist => playlist.id === detail.playlist.id);
    if (at >= 0) playlists[at] = detail.playlist; else playlists.push(detail.playlist);
    draw();
  }, { signal });
  document.addEventListener('playlist:deleted', ({ detail }) => {
    playlists = playlists.filter(playlist => playlist.id !== detail.id);
    draw();
  }, { signal });

  draw();
}
