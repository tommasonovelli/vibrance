// One playlist: its head on the colour field of its covers, the songs in
// order with the day each was added, and a search that adds songs without
// leaving the page. Its songs are moved by the grip, by Alt and the arrows or
// by the menu, and taken out with Delete or the menu; every change shows at
// once and goes to the server after (proposal 7.2).
//
// The server's side of it, from the contract: a change that depends on
// positions carries the tag of the revision the page holds (If-Match), and a
// change made on an older one answers 412; then the page reads the playlist
// again and says so. The page's requests go one after another, each with the
// tag the one before gave.
import { api, coverUrl, isStale } from '../api.js';
import { $, announce, clone, emptyState, formatAgo, formatDate, formatLength, openMenu, plural, setCover, setTitle, show, staleToast, toast } from '../ui.js';
import * as act from '../actions.js';
import { pager, playlistArt, trackList } from '../list.js';
import { navigate } from '../router.js';

const PAGE = 100;

// Songs taken out wait for the end of their toast before the server hears
// of it; one toast at a time, so at most one removal waits. If the page
// goes away first, it is sent then (proposal 7.2).
let waiting = null; // { id, ids, etag(), close(), sent }
let sending = Promise.resolve(); // the last removal sent
addEventListener('pagehide', () => {
  if (!waiting || waiting.sentAlready) return;
  const { id, ids, etag } = waiting;
  waiting.sentAlready = true;
  // Requests that leave with the page cannot wait for each other's tag: the
  // first carries the one the page holds, the rest name their item, which is
  // all a removal needs.
  ids.forEach((item, i) => api.del(`/playlists/${id}/items/${item}`, { ifMatch: i ? undefined : etag(), keepalive: true }).catch(() => {}));
});

export async function render(main, params, signal) {
  const id = params.id;
  // A removal still waiting for this playlist is sent first, or the list would show the song again.
  if (waiting?.id === id) waiting.close();
  await sending;

  let playlist, etag, revision;
  const itemsUrl = `/playlists/${id}/items`;
  let prefetch = api.get(itemsUrl, { limit: PAGE }, { signal }); // the first songs are asked for with the playlist itself
  prefetch.catch(() => {});
  try {
    playlist = await api.get(`/playlists/${id}`, null, { signal });
  } catch (error) {
    if (error.status !== 404) throw error;
    setTitle('Playlist not found');
    main.append(emptyState({ icon: 'playlists', title: 'Playlist not found.', link: { href: '/playlists', label: 'Go to Playlists' } }));
    return;
  }
  etag = playlist.etag;
  revision = playlist.revision;

  const view = clone('t-playlist');
  main.append(view);
  const context = () => act.playlistContext(playlist);
  let list, pg;
  let generation = 0;         // a reload makes every move still waiting meaningless: its positions are of the old list
  let inflight = 0;
  let chain = Promise.resolve();
  const removed = new Set();  // items taken out, until the server has said it (a reload leaves them out)
  const serial = task => {
    inflight++;
    const run = chain.then(task).finally(() => { inflight--; });
    chain = run.catch(() => {});
    return run;
  };

  // ---- Head ----

  const title = $('.pl-title', view), desc = $('.desc', view), meta = $('.meta', view), length = $('.length', view);
  const play = $('.play-xl', view), shuffle = $('.shuffle', view), cover = $('.cover-box', view);
  const songs = $('.songs', view), empty = $('.pl-empty', view);
  empty.append(emptyState({ icon: 'playlists', title: 'This playlist is empty', text: 'Search below for songs to add to it.' }));
  const hiddenRows = () => list.rows.filter(row => row.hidden);

  // The count and the length are the server's, less what waits to be taken out.
  const totals = () => {
    const out = hiddenRows();
    return { count: playlist.item_count - out.length, duration: playlist.duration_ms - out.reduce((sum, row) => sum + (row.track.available ? row.track.duration_ms : 0), 0) };
  };

  let artKey = null;
  function showArt() {
    const key = playlist.covers.map(c => c.url).join('|');
    if (key === artKey) return;
    artKey = key;
    const art = playlistArt(playlist, 640, true);
    cover.replaceChildren(art);
    // The head lies on its covers, blurred (app.css §11), once they have decoded: same URLs, no new download.
    const imgs = art.matches('img') ? [art] : [...art.querySelectorAll('img')];
    main.removeAttribute('data-glow');
    if (!imgs.length) return;
    Promise.all(imgs.map(img => img.decode())).then(() => {
      if (signal.aborted || key !== artKey) return;
      imgs.forEach((img, i) => main.style.setProperty(['--cover', '--cover-b', '--cover-c', '--cover-d'][i], `url("${img.currentSrc}")`));
      main.dataset.glow = imgs.length > 1 ? 'playlist' : 'album';
    }).catch(() => {});
  }

  function showHead() {
    const { count, duration } = totals();
    setTitle(playlist.name);
    title.textContent = playlist.name;
    desc.textContent = playlist.description;
    show(desc, !!playlist.description);
    meta.textContent = `${count ? `${plural(count, 'song')}, ${formatLength(duration)}` : 'No songs'} · Updated ${formatAgo(playlist.updated_at)}`;
    play.disabled = shuffle.disabled = !count;
    play.setAttribute('aria-label', `Play ${playlist.name}`);
    showArt();
  }

  // The numbers of the rows, the empty state and the line under the list.
  function sync() {
    let n = 0;
    for (const row of list.rows) if (!row.hidden) $('.n', row).textContent = ++n;
    const { count, duration } = totals();
    show(list.el, n > 0);
    show(empty, n === 0 && !pg.next());
    const gone = list.rows.filter(row => !row.hidden && row.track.available === false).length;
    length.textContent = count ? [
      pg.next() ? `Showing ${n} of ${plural(count, 'song')}` : plural(count, 'song'),
      formatLength(duration),
      gone ? (gone === 1 ? 'One song is not in the library right now' : `${gone} songs are not in the library right now`) : '',
    ].filter(Boolean).join(' · ') : '';
    show(length, !!length.textContent);
    markFound();
  }

  // ---- The songs ----

  // What a row says of its place in the playlist: the day it was added; a
  // song that is gone keeps its place and says so.
  function decorate(fresh, items) {
    fresh.forEach((row, i) => {
      const item = items[i];
      row.item = item;
      $('.c-added', row).textContent = formatDate(item.added_at);
      if (item.track.available === false) {
        const album = $('.c-album', row);
        album.textContent = 'Not in the library right now';
        album.removeAttribute('href');
        $('.tm', row).textContent = '–';
      }
    });
  }

  function appendItems(items) {
    const have = new Set(list.rows.map(row => row.item.id));
    const fresh = items.filter(item => !have.has(item.id) && !removed.has(item.id));
    if (fresh.length) decorate(list.add(fresh.map(item => item.track)), fresh);
  }

  // The songs after the ones that are loaded, for Play and Enter on a long playlist.
  async function rest() {
    const tracks = [];
    try {
      for (let after = pg.next(); after;) {
        const page = await api.get(itemsUrl, { limit: 200, after }, { signal });
        tracks.push(...page.items.filter(item => !removed.has(item.id)).map(item => item.track));
        after = page.next;
      }
    } catch (error) {
      if (error.name !== 'AbortError') toast({ title: 'Couldn’t read the whole playlist', sub: error.message, badge: 'alert', error: true });
    }
    return tracks;
  }

  // Builds the list and its pages again, as far as it went before (a reload
  // must not lose the person's place), and puts the page back where it was.
  async function build(until = 0) {
    pg?.remove();
    list = trackList('playlist', `Songs in ${playlist.name}`, context, { menu: rowMenu, remove: removeRows, reorder, rest });
    list.el.append(clone('t-track-head'));
    songs.replaceChildren(list.el);
    pg = pager(list.el, {
      signal, blocks: 't-ph-rows',
      read: async after => {
        const page = await (after === undefined && prefetch ? prefetch : api.get(itemsUrl, { limit: PAGE, after }, { signal }));
        prefetch = null;
        // Another page of an older or newer revision than the one held: someone changed the playlist in between.
        if (page.etag && page.etag !== etag && !inflight && after !== undefined) {
          reload(true);
          throw new DOMException('Changed elsewhere', 'AbortError');
        }
        return { items: page.items, next: page.next };
      },
      add: items => { appendItems(items); showHead(); sync(); },
    });
    await pg.first;
    for (let size = -1; list.rows.length < until && pg.next() && size !== list.rows.length;) {
      size = list.rows.length;
      await pg.more();
    }
    sync();
  }

  // Someone else changed the playlist (412, or a page with another tag): read it again.
  async function reload(say) {
    generation++;
    if (say) staleToast();
    const y = scrollY, until = list.rows.length;
    prefetch = api.get(itemsUrl, { limit: PAGE }, { signal });
    prefetch.catch(() => {});
    try {
      const next = await api.get(`/playlists/${id}`, null, { signal });
      playlist = next;
      etag = next.etag;
      revision = next.revision;
      await build(until);
    } catch (error) {
      if (error.name === 'AbortError') return;
      if (error.status === 404) { navigate('/playlists', { replace: true }); return; }
      toast({ title: 'Couldn’t read the playlist', sub: error.message, badge: 'alert', error: true });
      return;
    }
    showHead();
    outward();
    scrollTo(0, y);
  }

  // An answer about this playlist: its revision gives the tag the next
  // request carries. The others hear it too (the sidebar), with the count
  // less what waits to be taken out.
  function accept(next) {
    if (next.revision >= revision) {
      if (next.revision > revision) { revision = next.revision; etag = next.etag; }
      playlist = next;
    }
    showHead();
    outward();
  }
  const outward = () => {
    const { count, duration } = totals();
    document.dispatchEvent(new CustomEvent('playlist:changed', { detail: { playlist: { ...playlist, item_count: count, duration_ms: duration }, own: true } }));
  };

  // ---- Moving ----

  // `before` is the row the song goes in front of; null is the end. Its place
  // changes here first; the server is told in turn, with the tag the page holds.
  function reorder(row, before) {
    const from = list.rows.indexOf(row);
    list.move(row, before);
    const position = list.rows.indexOf(row);
    if (position === from) return;
    row.scrollIntoView({ block: 'nearest' });
    sync();
    const asked = generation;
    serial(async () => {
      if (asked !== generation) return;
      try {
        accept(await api.post(`${itemsUrl}/${row.item.id}/move`, { position }, { ifMatch: etag }));
      } catch (error) {
        if (error.name === 'AbortError') return;
        if (isStale(error)) await reload(true);
        else {
          toast({ title: 'Couldn’t move the song', sub: error.message, badge: 'alert', error: true });
          await reload(false);
        }
      }
    });
  }

  // "Move to bottom" of a playlist that is not all loaded: the end is read first.
  async function toBottom(row) {
    for (let size = -1; pg.next() && size !== list.rows.length;) {
      size = list.rows.length;
      await pg.more();
    }
    reorder(row, null);
  }

  // ---- Removing ----

  function removeRows(rows) {
    rows = rows.filter(row => !row.hidden);
    if (!rows.length) return;
    const first = rows[0].track;
    const hadFocus = list.el.contains(document.activeElement);
    for (const row of rows) { list.hide(row, true); removed.add(row.item.id); }
    if (hadFocus) list.focusNear(rows.at(-1));
    sync();
    showHead();
    outward();

    const entry = { id, ids: rows.map(row => row.item.id), etag: () => etag, sent: null, sentAlready: false, close: () => {} };
    const shown = toast({
      title: rows.length === 1 ? `Removed from ${playlist.name}` : `Removed ${plural(rows.length, 'song')} from ${playlist.name}`,
      sub: rows.length === 1 ? `${first.title} · ${first.artist}` : '',
      cover: coverUrl(first.album.cover, 256), badge: 'remove',
      undo: () => {
        for (const row of rows) removed.delete(row.item.id);
        // After a reload these rows are not in the list any more: it is made again.
        if (!rows.every(row => row.isConnected)) { reload(false); return; }
        for (const row of rows) list.hide(row, false);
        sync();
        showHead();
        outward();
      },
      done: undone => {
        if (waiting === entry) waiting = null;
        if (!undone && !entry.sentAlready) sending = entry.sent = send(rows);
        entry.sentAlready = true;
      },
    });
    entry.close = shown.close;
    waiting = entry;
  }

  // The rows leave the list at the moment their request is queued, so the
  // positions of later moves count without them, as the server will.
  function send(rows) {
    const ids = rows.map(row => row.item.id);
    for (const row of rows) list.drop(row);
    return Promise.all(ids.map(item => serial(async () => {
      try {
        const next = await api.del(`${itemsUrl}/${item}`, { ifMatch: etag });
        removed.delete(item);
        accept(next);
      } catch (error) {
        removed.delete(item);
        if (error.name === 'AbortError' || error.code === 'item_not_found') return;
        if (isStale(error)) await reload(true);
        else {
          toast({ title: 'Couldn’t remove the song', sub: error.message, badge: 'alert', error: true });
          await reload(false);
        }
      }
    }))).then(() => { sync(); showHead(); });
  }

  // ---- Menus ----

  function rowMenu(rows) {
    const tracks = rows.map(row => row.track);
    const one = rows.length === 1 ? rows[0] : null;
    const gone = tracks.every(track => !track.available);
    const all = tracks.every(track => track.favorite);
    const shown = list.rows.filter(row => !row.hidden);
    return [
      { label: 'Play next', icon: 'play-next', disabled: gone, run: () => act.playNext(tracks) },
      { label: 'Add to queue', icon: 'queue', disabled: gone, run: () => act.addToQueue(tracks) },
      '-',
      act.playlistMenu(tracks, { except: id, label: 'Add to another playlist' }),
      { label: all ? 'Remove from favorites' : 'Add to favorites', icon: 'heart', run: () => act.setFavorites(tracks, !all) },
      ...(one ? [
        '-',
        { label: 'Move to top', icon: 'to-top', disabled: one === shown[0], run: () => reorder(one, shown[0]) },
        { label: 'Move to bottom', icon: 'to-bottom', disabled: one === shown.at(-1) && !pg.next(), run: () => toBottom(one) },
        '-',
        { label: 'Go to album', icon: 'album', href: `/albums/${one.track.album.id}` },
        { label: act.artistLabel(one.track), icon: 'artist', href: `/artists/${one.track.album.artist.id}` },
      ] : []),
      '-',
      { label: 'Remove from this playlist', icon: 'remove', danger: true, run: () => removeRows(rows) },
    ];
  }

  const everything = async () => [...list.tracks(), ...await rest()];
  const dots = $('.head-dots', view);
  dots.addEventListener('click', () => openMenu(dots, [
    ...act.collectionMenu(act.lazy(everything, 'read the playlist'), context(), { play: false, playlist: false }),
    { label: 'Find songs', icon: 'search', run: () => input.focus() },
    { label: 'Edit details', icon: 'edit', run: edit },
    '-',
    { label: 'Delete playlist', icon: 'trash', danger: true, run: () => act.deletePlaylist({ ...playlist, etag }) },
  ]));
  const edit = () => act.editPlaylist({ ...playlist, etag });
  $('.edit', view).addEventListener('click', edit);
  play.addEventListener('click', async () => act.playAll(await everything(), context()));
  shuffle.addEventListener('click', async () => act.shuffleAll(await everything(), context()));

  // ---- Find songs ----

  const input = $('.find input', view), found = $('.res-list', view);
  const mine = new Set(); // songs added from here, until the list shows them
  let timer, request;

  function markFound() {
    if (!found.children.length) return;
    const have = new Set(list.rows.filter(row => !row.hidden).map(row => row.track.id));
    for (const row of found.querySelectorAll('.res')) {
      const there = have.has(row.track.id) || mine.has(row.track.id);
      show($('.add', row), !there);
      show($('.added', row), there);
    }
  }

  function showFound(tracks, q) {
    if (!tracks.length) {
      const none = document.createElement('p');
      none.className = 'note';
      none.textContent = `No songs found for “${q}”.`;
      found.replaceChildren(none);
      announce(`No songs found for ${q}`);
      return;
    }
    found.replaceChildren(...tracks.map(track => {
      const row = clone('t-find-row');
      row.track = track;
      setCover($('.th', row), track.album.cover, 256);
      $('.nm', row).textContent = track.title;
      $('.ar', row).textContent = track.artist;
      $('.c', row).textContent = track.album.title;
      $('.add', row).setAttribute('aria-label', `Add ${track.title} to ${playlist.name}`);
      return row;
    }));
    markFound();
    announce(`${plural(tracks.length, 'song')} found`);
  }

  async function search() {
    const q = input.value.trim();
    request?.abort();
    if (!q) { found.replaceChildren(); return; }
    request = new AbortController();
    const stop = AbortSignal.any([signal, request.signal]);
    try {
      showFound((await api.get('/search', { q, types: 'track', limit: 20 }, { signal: stop })).tracks, q);
    } catch (error) {
      if (stop.aborted) return;
      const note = document.createElement('p');
      note.className = 'note';
      note.textContent = 'The search didn’t work. Try again.';
      found.replaceChildren(note);
    }
  }
  input.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(search, 150); });
  found.addEventListener('click', async event => {
    const row = event.target.closest('.res');
    if (!row || !event.target.closest('.add')) return;
    mine.add(row.track.id);
    markFound(); // "Added" at once
    if (!await act.addToPlaylists([row.track], [{ ...playlist, etag }])) { mine.delete(row.track.id); markFound(); }
  });
  signal.addEventListener('abort', () => { clearTimeout(timer); request?.abort(); });

  await build();
  showHead();
  sync();

  // ---- What changes the playlist elsewhere ----

  // Answers about this playlist, from this page or from anywhere (a song
  // dropped on the sidebar, an Undo), come here. A newer revision gives the
  // tag the next request carries; the same revision (a change made here, not
  // answered yet) only changes what is shown.
  document.addEventListener('playlist:changed', ({ detail }) => {
    const next = detail.playlist;
    if (detail.own || next.id !== id || next.revision < revision) return;
    if (next.revision > revision) { revision = next.revision; etag = next.etag; }
    playlist = next;
    // Everything that was added is at the end; if the end is not loaded yet, it comes with its page.
    if (detail.added && !pg.next()) appendItems(detail.added);
    for (const item of detail.removed ?? []) {
      const row = list.rows.find(one => one.item.id === item);
      if (row) { mine.delete(row.track.id); list.drop(row); }
    }
    showHead();
    sync();
  }, { signal });
  document.addEventListener('playlist:deleted', ({ detail }) => {
    if (detail.id === id) navigate('/playlists', { replace: true });
  }, { signal });
  // A playlist with nothing in it leads straight to adding songs (proposal 7.1).
  if (!playlist.item_count) input.focus();
}
