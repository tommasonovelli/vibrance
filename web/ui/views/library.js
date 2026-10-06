// The Library: every song (GET /tracks, sorted), or the favorites of the
// person (the tab beside it). The list loads page by page as it is scrolled.
import { api, optional, walk } from '../api.js';
import { $, clone, emptyState, errorNotice, openMenu, pageHead, setTitle, show } from '../ui.js';
import { adminLine } from '../status.js';
import * as act from '../actions.js';
import { listHead, pager, trackList } from '../list.js';

const SORTS = [['title', 'Title'], ['artist', 'Artist'], ['album', 'Album'], ['added', 'Recently added']];
const number = new Intl.NumberFormat('en');
// "Recently added" reads newest first; the others, A to Z.
const firstOrder = sort => (sort === 'added' ? 'desc' : 'asc');

export async function render(main, params, signal) {
  const favorites = params.path === '/favorites';
  listHead(pageHead(main, 'Library'));
  if (favorites) setTitle('Favorites');
  const view = clone('t-library');
  main.append(view);
  $('.seg a:nth-child(' + (favorites ? 2 : 1) + ')', view).setAttribute('aria-current', 'page');

  const query = params.query;
  let sort = SORTS.some(([key]) => key === query.get('sort')) ? query.get('sort') : 'title';
  let order = ['asc', 'desc'].includes(query.get('order')) ? query.get('order') : firstOrder(sort);
  let total = null, emptied = false;
  let list, run;
  const host = $('.list', view);
  // `after` is where the pages loaded so far end: the player asks for the songs that follow when its queue runs low.
  const context = () => (favorites ? { type: 'favorites', id: null, name: 'Favorites' } : { type: 'library', id: null, name: 'Library', sort, order, after: pg?.next() ?? null });
  const noun = n => (favorites ? (n === 1 ? 'favorite' : 'favorites') : (n === 1 ? 'song' : 'songs'));

  // The count: the server's summary if it has one, otherwise nothing.
  const count = $('.count', view);
  const showCount = () => { show(count, total !== null && !emptied); if (total !== null) count.textContent = `${number.format(total)} ${noun(total)}`; };
  optional(api.get(favorites ? '/me/favorites/summary' : '/catalog/summary', null, { signal }))
    .then(summary => { if (summary) { total = favorites ? summary.track_count : summary.tracks; showCount(); pg?.say(); } })
    .catch(() => {});
  if (favorites) {
    document.addEventListener('track:favorite', ({ detail }) => {
      if (total === null) return;
      total = Math.max(0, total + (detail.track.favorite ? 1 : -1));
      showCount();
    }, { signal });
  }

  const sortButton = $('.sort', view);
  show(sortButton, !favorites);
  const labelSort = () => { $('span', sortButton).textContent = SORTS.find(([key]) => key === sort)[1]; };
  labelSort();

  let pg = null;
  async function fill(first) {
    run?.abort();
    run = new AbortController();
    const stop = AbortSignal.any([signal, run.signal]);
    host.replaceChildren();
    list = trackList('library', favorites ? 'Favorite songs' : 'Songs', context, { only: favorites ? 'favorites' : null });
    list.el.append(clone('t-track-head'));
    host.append(list.el);
    pg = pager(list.el, {
      key: favorites ? '/favorites' : `/tracks?${sort}&${order}`,
      signal: stop,
      add: tracks => list.add(tracks),
      note: loaded => (total ? `Showing ${number.format(loaded)} of ${number.format(total)} ${noun(total)}. ` : '') + 'More load as you scroll.',
      read: async after => {
        if (favorites) {
          const page = await api.get('/me/favorites/tracks', { limit: 100, after }, { signal: stop });
          return { items: page.favorites.map(favorite => favorite.track), next: page.next };
        }
        const page = await api.get('/tracks', { sort, order, limit: 50, after }, { signal: stop });
        return { items: page.tracks, next: page.next };
      },
    });
    try {
      await pg.first;
    } catch (error) {
      if (first) throw error;
      if (!stop.aborted) host.replaceChildren(errorNotice(error, () => fill()));
      return;
    }
    if (stop.aborted) return;
    if (!list.rows.length) empty();
  }

  function empty() {
    emptied = true;
    const state = favorites
      ? emptyState({ icon: 'heart', title: 'No favorites yet', text: 'Select the heart next to a song to keep it here.', link: { href: '/', label: 'Go to Library' } })
      : emptyState({ icon: 'library', title: 'The library is empty', text: 'Ask the person who runs this server to add music with MusicLib.' });
    if (!favorites) adminLine(state); // admins also see the scanner's state
    host.replaceChildren(state);
    for (const control of view.querySelectorAll('.play-xl, .shuffle, .sort, .count')) show(control, false);
  }

  // Play and Shuffle. The Library plays the songs it shows; its shuffle asks
  // the server for random ones (GET /tracks/random), as 50,000 songs are not
  // all loaded. Favorites are few: all of them are read.
  const allFavorites = async () => {
    const all = [];
    for await (const page of walk('/me/favorites/tracks', {}, { signal })) all.push(...page.favorites.map(favorite => favorite.track));
    return all;
  };
  $('.play-xl', view).addEventListener('click', async () => {
    act.playAll(favorites ? await allFavorites().catch(() => list.tracks()) : list.tracks(), context());
  });
  $('.shuffle', view).addEventListener('click', async () => {
    if (favorites) { act.shuffleAll(await allFavorites().catch(() => list.tracks()), context()); return; }
    const random = await optional(api.get('/tracks/random', { limit: 50 }, { signal })).catch(() => null);
    act.shuffleAll(random ? random.tracks : list.tracks(), context());
  });

  function choose(key, direction) {
    sort = key;
    order = direction;
    const url = new URL(location.href);
    url.searchParams.delete('sort');
    url.searchParams.delete('order');
    if (sort !== 'title') url.searchParams.set('sort', sort);
    if (order !== firstOrder(sort)) url.searchParams.set('order', order);
    history.replaceState(history.state, '', url);
    labelSort();
    fill();
  }
  sortButton.addEventListener('click', () => openMenu(sortButton, [
    ...SORTS.map(([key, label]) => ({ label, checked: key === sort, run: () => choose(key, key === sort ? order : firstOrder(key)) })),
    '-',
    { label: 'Ascending', checked: order === 'asc', run: () => choose(sort, 'asc') },
    { label: 'Descending', checked: order === 'desc', run: () => choose(sort, 'desc') },
  ]));

  await fill(true);
}
