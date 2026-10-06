// One artist: a circle split between two covers (there are no pictures of
// artists), its albums, and its songs.
import { api, optional } from '../api.js';
import { $, clone, emptyState, formatLength, openMenu, setCover, setTitle, show } from '../ui.js';
import * as act from '../actions.js';
import { albumCard, cardActions, grid, trackList } from '../list.js';

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
const SHOWN = 6; // songs before "Show all"

export async function render(main, params, signal) {
  const id = params.id;
  let artist, tracks;
  const songs = act.artistTracks(id).catch(error => { if (error.status === 404) return []; throw error; });
  try {
    [artist, tracks] = await Promise.all([api.get(`/artists/${id}`, null, { signal }), songs]);
  } catch (error) {
    songs.catch(() => {});
    if (error.status !== 404) throw error;
    setTitle('Artist not found');
    main.append(emptyState({ icon: 'artist', title: 'Artist not found.', link: { href: '/artists', label: 'Go to Artists' } }));
    return;
  }
  setTitle(artist.name);
  const view = clone('t-artist');
  main.append(view);
  const { albums } = artist;
  const context = { type: 'artist', id, name: artist.name };

  // The field and the circle come from the first two albums.
  const covers = albums.filter(album => album.cover).slice(0, 2);
  const avatar = $('.avatar', view);
  for (const album of covers) {
    const img = document.createElement('img');
    img.alt = '';
    img.width = img.height = 100;
    img.decoding = 'async';
    setCover(img, album.cover, 256);
    avatar.append(img);
  }
  Promise.all([...avatar.children].map(img => img.decode())).then(() => {
    if (signal.aborted || !covers.length) return;
    main.style.setProperty('--cover', `url("${avatar.children[0].currentSrc}")`);
    main.style.setProperty('--cover-b', `url("${avatar.children[avatar.children.length - 1].currentSrc}")`);
    main.dataset.glow = 'artist';
  }).catch(() => {});

  $('.artist-name', view).textContent = artist.name;
  const songCount = albums.reduce((sum, album) => sum + album.track_count, 0);
  $('.summary', view).textContent = [plural(albums.length, 'album'), plural(songCount, 'song'), formatLength(albums.reduce((sum, album) => sum + album.duration_ms, 0))].join(' · ');

  $('.play-xl', view).setAttribute('aria-label', `Play ${artist.name}`);
  $('.play-xl', view).addEventListener('click', () => act.playAll(tracks, context));
  $('.shuffle', view).addEventListener('click', async () => {
    const random = await optional(api.get('/tracks/random', { artist: id, limit: 50 }, { signal })).catch(() => null);
    act.shuffleAll(random ? random.tracks : tracks, context);
  });
  const dots = $('.head-dots', view);
  dots.addEventListener('click', () => openMenu(dots, act.collectionMenu(() => tracks, context, { play: false })));

  const gridEl = grid(`Albums by ${artist.name}`);
  cardActions(gridEl);
  const cards = document.createDocumentFragment();
  for (const album of albums) cards.append(albumCard(album, false));
  gridEl.append(cards);
  $('.list', $('section', view)).append(gridEl);

  if (tracks.length) {
    const section = $('.songs', view);
    const list = trackList('artist', `Songs by ${artist.name}`, () => context, { skip: ['artist'] });
    $('.list', section).append(list.el);
    list.add(tracks.slice(0, SHOWN));
    show(section, true);
    const toggle = $('.show-all', section);
    if (tracks.length > SHOWN) {
      let all = false;
      const label = () => { toggle.textContent = all ? 'Show fewer' : `Show all ${tracks.length}`; };
      label();
      toggle.addEventListener('click', () => {
        if (list.rows.length < tracks.length) list.add(tracks.slice(list.rows.length));
        all = !all;
        list.rows.forEach((row, i) => { row.hidden = !all && i >= SHOWN; });
        label();
      });
    } else {
      show(toggle, false);
    }
  }
}
