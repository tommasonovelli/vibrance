// The albums as a grid of covers, in the order the person picks (the URL
// keeps it, so Back and a reload come back to the same view).
import { api, optional } from '../api.js';
import { $, clone, emptyState, errorNotice, pageHead, show } from '../ui.js';
import { adminLine } from '../status.js';
import { albumCard, cardActions, grid, listHead, pager } from '../list.js';

const SORTS = [['title', 'Title'], ['artist', 'Artist'], ['year', 'Year'], ['added', 'Recently added']];
const number = new Intl.NumberFormat('en');
const firstOrder = sort => (sort === 'added' ? 'desc' : 'asc');

export async function render(main, params, signal) {
  listHead(pageHead(main, 'Albums'));
  const view = clone('t-albums');
  main.append(view);
  const query = params.query;
  let sort = SORTS.some(([key]) => key === query.get('sort')) ? query.get('sort') : 'title';
  let order = ['asc', 'desc'].includes(query.get('order')) ? query.get('order') : firstOrder(sort);
  const host = $('.list', view);
  let run;

  const count = $('.count', view);
  optional(api.get('/catalog/summary', null, { signal })).then(summary => {
    if (!summary) return;
    count.textContent = `${number.format(summary.albums)} ${summary.albums === 1 ? 'album' : 'albums'} · ${number.format(summary.artists)} ${summary.artists === 1 ? 'artist' : 'artists'}`;
    show(count, true);
  }).catch(() => {});

  const seg = $('.seg', view), flip = $('.order', view);
  for (const [key, label] of SORTS) {
    const button = document.createElement('button');
    button.type = 'button';
    button.setAttribute('role', 'radio');
    button.dataset.sort = key;
    button.textContent = label;
    seg.append(button);
  }
  const showChoice = () => {
    for (const button of seg.children) button.setAttribute('aria-checked', String(button.dataset.sort === sort));
    flip.setAttribute('aria-label', order === 'asc' ? 'Ascending order' : 'Descending order');
  };

  async function fill(first) {
    run?.abort();
    run = new AbortController();
    const stop = AbortSignal.any([signal, run.signal]);
    const albums = grid('Albums');
    cardActions(albums);
    host.replaceChildren(albums);
    const pg = pager(albums, {
      key: `/albums?${sort}&${order}`, signal: stop, blocks: 't-ph-grid',
      read: async after => {
        const page = await api.get('/albums', { sort, order, limit: 60, after }, { signal: stop });
        return { items: page.albums, next: page.next };
      },
      add: items => {
        const page = document.createDocumentFragment();
        for (const album of items) page.append(albumCard(album, true));
        albums.append(page);
      },
    });
    try {
      await pg.first;
    } catch (error) {
      if (first) throw error;
      if (!stop.aborted) host.replaceChildren(errorNotice(error, () => fill()));
      return;
    }
    if (!stop.aborted && !albums.children.length) {
      const state = emptyState({ icon: 'album', title: 'No albums yet', text: 'Ask the person who runs this server to add music with MusicLib.' });
      adminLine(state);
      host.replaceChildren(state);
    }
  }

  function choose(key, direction) {
    sort = key;
    order = direction;
    const url = new URL(location.href);
    url.searchParams.delete('sort');
    url.searchParams.delete('order');
    if (sort !== 'title') url.searchParams.set('sort', sort);
    if (order !== firstOrder(sort)) url.searchParams.set('order', order);
    history.replaceState(history.state, '', url);
    showChoice();
    fill();
  }
  seg.addEventListener('click', event => {
    const key = event.target.closest('[data-sort]')?.dataset.sort;
    if (key && key !== sort) choose(key, firstOrder(key));
  });
  flip.addEventListener('click', () => choose(sort, order === 'asc' ? 'desc' : 'asc'));

  showChoice();
  await fill(true);
}
