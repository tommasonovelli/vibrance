// One album: its cover as a colour field behind the head, the songs by disc,
// and what else the artist has.
import { api, coverUrl } from '../api.js';
import { $, clone, emptyState, formatLength, openMenu, setCover, setTitle, show } from '../ui.js';
import * as act from '../actions.js';
import { albumCard, albumContext, cardActions, grid, trackList } from '../list.js';

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
const minutes = ms => {
  const min = Math.round(ms / 60000);
  return min < 60 ? plural(min, 'minute') : formatLength(ms);
};

// The format badges: one codec and one sample format for every song, or
// "Mixed formats" (proposal 4.4).
function badges(tracks) {
  const key = ({ format: f }) => `${f.codec}/${f.bit_depth}/${f.sample_rate}`;
  if (new Set(tracks.map(key)).size !== 1) return ['Mixed formats'];
  const f = tracks[0].format, parts = act.formatParts(f);
  return [parts.codec, [f.bit_depth && `${f.bit_depth}-bit`, parts.rate].filter(Boolean).join(' / ')];
}

// The head lies on its own cover, blurred (app.css §11): once the image has
// decoded, so nothing flashes. The same URL as the head's cover: one download.
function field(main, img, signal) {
  img.decode().then(() => {
    if (signal.aborted) return;
    main.style.setProperty('--cover', `url("${img.currentSrc}")`);
    main.dataset.glow = 'album';
  }).catch(() => {});
}

export async function render(main, params, signal) {
  let album;
  try {
    album = await api.get(`/albums/${params.id}`, null, { signal });
  } catch (error) {
    if (error.status !== 404) throw error;
    setTitle('Album not found');
    main.append(emptyState({ icon: 'album', title: 'Album not found.', link: { href: '/albums', label: 'Go to Albums' } }));
    return;
  }
  setTitle(album.title);
  const { tracks } = album;
  const view = clone('t-album');
  main.append(view);
  const context = albumContext(album);

  const cover = $('.cover', view);
  cover.alt = `Cover of ${album.title}`;
  setCover(cover, album.cover, 640);
  if (album.cover) field(main, cover, signal);
  $('.album-title', view).textContent = album.title;
  const artist = $('.artist', view);
  artist.textContent = album.artist.name;
  artist.href = `/artists/${album.artist.id}`;
  $('.f-year', view).textContent = album.year ?? '';
  $('.f-genre', view).textContent = album.genre ?? '';
  $('.f-size', view).textContent = `${plural(tracks.length, 'song')}, ${formatLength(album.duration_ms)}`;
  if (tracks.length) {
    const fmt = $('.fmt', view);
    for (const text of badges(tracks)) {
      const span = document.createElement('span');
      span.textContent = text;
      fmt.append(span);
    }
  }
  $('.play-xl', view).setAttribute('aria-label', `Play ${album.title}`);
  $('.play-xl', view).addEventListener('click', () => act.playAll(tracks, context));
  $('.shuffle', view).addEventListener('click', () => act.shuffleAll(tracks, context));
  $('.queue-btn', view).addEventListener('click', () => act.addToQueue(tracks));
  const dots = $('.head-dots', view);
  dots.addEventListener('click', () => openMenu(dots, [
    ...act.collectionMenu(() => tracks, context, { play: false, queue: false, favorites: true }),
    '-',
    { label: 'Go to artist', icon: 'artist', href: `/artists/${album.artist.id}` },
    ...(album.cover ? [{ label: 'Download cover', icon: 'download', run: () => act.downloadCover(album) }] : []),
  ]));

  // The songs, with a heading for each disc when there are several.
  const list = trackList('album', `Songs of ${album.title}`, () => context, { skip: ['album'] });
  $('.discs', view).append(list.el);
  const discs = Map.groupBy(tracks, track => track.disc);
  for (const [disc, group] of discs) {
    if (discs.size > 1) {
      const heading = document.createElement('h3');
      heading.className = 'disc';
      heading.textContent = `Disc ${disc}`;
      list.el.append(heading);
    }
    list.add(group, track => track.number);
  }
  const lyrics = tracks.filter(track => track.has_lyrics).length;
  const note = $('.lyrics-note', view);
  note.textContent = `Lyrics on ${plural(lyrics, 'song')}`;
  show(note, lyrics > 0);
  $('.length', view).textContent = `${plural(tracks.length, 'song')}, ${minutes(album.duration_ms)}${album.year ? ` · Released ${album.year}` : ''}`;
  if (!tracks.length) $('section', view).replaceChildren(emptyState({ icon: 'album', title: 'No songs available', text: 'The files of this album are not in the library folder.' }));

  moreBy(view, album, signal);
}

// The artist's other albums, after the page has painted.
async function moreBy(view, album, signal) {
  try {
    const page = await api.get('/albums', { artist: album.artist.id, sort: 'year', limit: 13 }, { signal });
    const others = page.albums.filter(other => other.id !== album.id).slice(0, 12);
    if (!others.length || signal.aborted) return;
    const section = $('.more-by', view);
    $('h2', section).textContent = `More by ${album.artist.name}`;
    $('a', section).href = `/artists/${album.artist.id}`;
    const albums = grid(`More by ${album.artist.name}`);
    cardActions(albums);
    const cards = document.createDocumentFragment();
    for (const other of others) cards.append(albumCard(other, false));
    albums.append(cards);
    $('.list', section).append(albums);
    show(section, true);
  } catch { /* the page is whole without it */ }
}
