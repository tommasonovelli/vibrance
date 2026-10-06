// The one search: a big field, and what it found by kind, the best match
// first. It answers while the person types (about 150 ms after the last key)
// and keeps the old results in view until the new ones arrive.
import { api } from '../api.js';
import { $, announce, clone, emptyState, errorNotice, pending, setCover, setTitle, show } from '../ui.js';
import * as act from '../actions.js';
import { albumCard, cardActions, grid, personCard, trackList } from '../list.js';

const KINDS = [['all', 'All', null], ['artists', 'Artists', 'artist'], ['albums', 'Albums', 'album'], ['songs', 'Songs', 'track']];
const plain = text => text.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();

// The words of the query, as the server reads them, to underline where they
// begin a word of a name: case and accents do not count (proposal 4.6).
function highlight(element, words) {
  const text = element.textContent;
  let folded = '';
  const at = []; // where each letter of `folded` comes from in `text`
  for (let i = 0; i < text.length;) {
    const ch = String.fromCodePoint(text.codePointAt(i));
    for (const c of plain(ch)) { folded += c; at.push(i); }
    i += ch.length;
  }
  const spans = [];
  for (const word of words) {
    for (let start = folded.indexOf(word); start >= 0; start = folded.indexOf(word, start + 1)) {
      if (start === 0 || !/[\p{L}\p{N}]/u.test(folded[start - 1])) spans.push([at[start], at[start + word.length - 1] + 1]);
    }
  }
  if (!spans.length) return;
  spans.sort((a, b) => a[0] - b[0]);
  const parts = [];
  let done = 0;
  for (const [from, to] of spans) {
    if (to <= done) continue;
    const begin = Math.max(from, done);
    if (begin > done) parts.push(text.slice(done, begin));
    const mark = document.createElement('mark');
    mark.textContent = text.slice(begin, to);
    parts.push(mark);
    done = to;
  }
  if (done < text.length) parts.push(text.slice(done));
  element.replaceChildren(...parts);
}

export async function render(main, params, signal) {
  setTitle('Search');
  const view = clone('t-search');
  main.append(view);
  const input = $('#q', view), clear = $('.clear', view), results = $('.results', view), chips = $('.chips', view);
  input.value = (params.query.get('q') || '').slice(0, 100);
  let kind = KINDS.some(([key]) => key === params.query.get('kind')) ? params.query.get('kind') : 'all';
  let timer, request;

  for (const [key, label] of KINDS) {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'chip';
    chip.setAttribute('role', 'radio');
    chip.dataset.kind = key;
    chip.textContent = label;
    chips.append(chip);
  }
  const showKind = () => { for (const chip of chips.children) chip.setAttribute('aria-checked', String(chip.dataset.kind === kind)); };

  function section(title, { link = null, note = '' } = {}) {
    const el = clone('t-section');
    $('h2', el).textContent = title;
    if (link) { const button = $('button', el); button.textContent = link.label; button.addEventListener('click', link.run); show(button, true); }
    if (note) { const n = $('.note', el); n.textContent = note; show(n, true); }
    return el;
  }
  const songsOf = (tracks, q, words) => {
    const list = trackList('search', 'Songs', () => ({ type: 'search', id: q, name: `“${q}”` }));
    list.add(tracks);
    for (const row of list.rows) highlight($('.nm', row), words);
    return list.el;
  };
  const albumsOf = (albums, words) => {
    const el = grid('Albums');
    cardActions(el);
    const page = document.createDocumentFragment();
    for (const album of albums) {
      const card = albumCard(album, true);
      highlight($('.ct', card), words);
      page.append(card);
    }
    el.append(page);
    return el;
  };
  const artistsOf = (artists, words) => {
    const el = document.createElement('div');
    el.className = 'people';
    el.setAttribute('role', 'list');
    for (const artist of artists) {
      const card = personCard(artist);
      highlight($('.pn', card), words);
      el.append(card);
    }
    return el;
  };
  const add = (host, title, content, options) => {
    const el = section(title, options);
    $('.list', el).append(content);
    host.append(el);
    return el;
  };

  // The best match: an artist named as asked, then an album that begins as
  // asked, then the first song (proposal 4.6).
  function topCard(found, q, words) {
    const asked = plain(q.trim());
    const artist = found.artists.find(a => plain(a.name) === asked);
    const album = !artist && found.albums.find(a => plain(a.title).startsWith(asked));
    const track = !artist && !album && found.tracks[0];
    const card = clone('t-topcard');
    const link = $('.top-link', card), img = $('.cover-img', card), av = $('.av', card);
    const head = $('h3', card);
    let play;
    if (artist) {
      link.href = `/artists/${artist.id}`;
      show(img, false); show(av, true);
      av.textContent = artist.name.split(/\s+/).map(w => w[0]).slice(0, 2).join('').toUpperCase();
      head.textContent = artist.name;
      $('.pill', card).textContent = 'Artist';
      $('.top-sub', card).textContent = artist.album_count === 1 ? '1 album' : `${artist.album_count} albums`;
      play = async () => act.playAll(await act.artistTracks(artist.id), { type: 'artist', id: artist.id, name: artist.name });
    } else if (album) {
      link.href = `/albums/${album.id}`;
      setCover(img, album.cover, 256);
      head.textContent = album.title;
      $('.pill', card).textContent = 'Album';
      $('.top-sub', card).textContent = [album.artist.name, album.year].filter(Boolean).join(' · ');
      play = async () => act.playAll(await act.albumTracks(album.id), { type: 'album', id: album.id, name: album.title });
    } else if (track) {
      link.href = `/albums/${track.album.id}`;
      setCover(img, track.album.cover, 256);
      head.textContent = track.title;
      $('.pill', card).textContent = 'Song';
      $('.top-sub', card).textContent = `${track.artist} · ${track.album.title}`;
      play = async () => act.playFrom(found.tracks, 0, { type: 'search', id: q, name: `“${q}”` });
    } else {
      return null;
    }
    highlight(head, words);
    $('.topplay', card).setAttribute('aria-label', `Play ${head.textContent}`);
    $('.topplay', card).addEventListener('click', () => play().catch(() => {}));
    return card;
  }

  function draw(found, q) {
    const words = plain(q).split(/[^\p{L}\p{N}]+/u).filter(Boolean).slice(0, 8);
    const out = document.createDocumentFragment();
    const total = found.artists.length + found.albums.length + found.tracks.length;
    if (!total) {
      results.replaceChildren(emptyState({ icon: 'search', title: `No results for “${q}”`, text: 'Search looks at the names of songs, albums and artists, from the first letters of each word. Check the spelling, or try fewer words.' }));
      announce(`No results for ${q}`);
      return;
    }
    const go = key => () => { kind = key; showKind(); run(); };
    if (kind === 'all') {
      const top = topCard(found, q, words);
      if (top || found.tracks.length) {
        const row = document.createElement('div');
        row.className = 'top';
        if (top) {
          const left = section('Top result');
          $('.list', left).append(top);
          row.append(left);
        }
        if (found.tracks.length) {
          add(row, 'Songs', songsOf(found.tracks.slice(0, 4), q, words), { link: { label: 'Show all', run: go('songs') } });
        }
        out.append(row);
      }
      if (found.artists.length) add(out, 'Artists', artistsOf(found.artists, words));
      if (found.albums.length) add(out, 'Albums', albumsOf(found.albums, words));
    } else if (kind === 'songs') {
      add(out, 'Songs', songsOf(found.tracks, q, words), { note: found.tracks.length === 1 ? '1 song' : `${found.tracks.length} songs` });
    } else if (kind === 'artists') {
      add(out, 'Artists', artistsOf(found.artists, words));
    } else {
      add(out, 'Albums', albumsOf(found.albums, words));
    }
    results.replaceChildren(out);
    announce(`${total} results`);
  }

  async function run() {
    clearTimeout(timer);
    const q = input.value.trim();
    show(clear, !!input.value);
    const url = new URL(location.href);
    for (const key of ['q', 'kind']) url.searchParams.delete(key);
    if (q) url.searchParams.set('q', q);
    if (kind !== 'all') url.searchParams.set('kind', kind);
    history.replaceState(history.state, '', url);
    showKind();
    request?.abort();
    if (!q) { results.replaceChildren(emptyState({ icon: 'search', title: 'Search your music', text: 'Search looks at the names of songs, albums and artists, from the first letters of each word.' })); return; }
    request = new AbortController();
    const stop = AbortSignal.any([signal, request.signal]);
    // Slow for a second, and nothing to look at: still blocks. Old results stay as they are.
    const done = results.children.length ? () => {} : pending(results, 't-ph-rows');
    try {
      const types = KINDS.find(([key]) => key === kind)[2];
      draw(await api.get('/search', { q, types, limit: kind === 'songs' ? 50 : 10 }, { signal: stop }), q);
    } catch (error) {
      if (stop.aborted) return;
      results.replaceChildren(errorNotice(error, run));
    } finally {
      done();
    }
  }

  input.addEventListener('input', () => { clearTimeout(timer); show(clear, !!input.value); timer = setTimeout(run, 150); });
  clear.addEventListener('click', () => { input.value = ''; input.focus(); run(); });
  chips.addEventListener('click', event => {
    const key = event.target.closest('[data-kind]')?.dataset.kind;
    if (key && key !== kind) { kind = key; run(); }
  });
  signal.addEventListener('abort', () => { clearTimeout(timer); request?.abort(); });

  input.focus({ preventScroll: true });
  await run();
}
