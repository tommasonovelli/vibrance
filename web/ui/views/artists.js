// The artists, A to Z: a round monogram and the name, as many as there are.
import { api, optional } from '../api.js';
import { $, clone, emptyState, pageHead, show } from '../ui.js';
import { grid, listHead, pager, personCard } from '../list.js';

const number = new Intl.NumberFormat('en');

export async function render(main, params, signal) {
  listHead(pageHead(main, 'Artists'));
  const view = clone('t-artists');
  main.append(view);
  const count = $('.count', view);
  optional(api.get('/catalog/summary', null, { signal })).then(summary => {
    if (!summary) return;
    count.textContent = `${number.format(summary.artists)} ${summary.artists === 1 ? 'artist' : 'artists'}`;
    show(count, true);
  }).catch(() => {});

  const people = grid('Artists');
  $('.list', view).append(people);
  const pg = pager(people, {
    key: '/artists', signal, blocks: 't-ph-grid',
    read: async after => {
      const page = await api.get('/artists', { limit: 100, after }, { signal });
      return { items: page.artists, next: page.next };
    },
    add: artists => {
      const page = document.createDocumentFragment();
      for (const artist of artists) page.append(personCard(artist));
      people.append(page);
    },
  });
  await pg.first;
  if (!people.children.length) {
    $('.list', view).replaceChildren(emptyState({ icon: 'artist', title: 'No artists yet', text: 'Ask the person who runs this server to add music with MusicLib.' }));
  }
}
