// What a person does with songs and albums, from a menu, a button or a drag:
// play them, queue them, make them favorites, put them in a playlist. Each
// change shows at once and ends in a toast with Undo, not in a question
// (proposal 7.2); a failure puts things back and says so in one sentence.
import { api, coverUrl, isMissing, isStale, optional, walk } from './api.js';
import { $, clone, confirmSheet, openSheet, setCover, show, staleToast, toast, whenClosed, formatTime } from './ui.js';
import * as player from './player.js';
import { getPlaylists, newPlaylist } from './sidebar.js';

const emit = (name, detail) => document.dispatchEvent(new CustomEvent(name, { detail }));
const number = new Intl.NumberFormat('en');
const plural = (n, word) => `${number.format(n)} ${word}${n === 1 ? '' : 's'}`;

// The second line and the cover of a toast: "Title · Artist", or the count.
function about(tracks) {
  const [first] = tracks;
  return {
    sub: tracks.length === 1 ? `${first.title} · ${first.artist}` : plural(tracks.length, 'song'),
    cover: coverUrl(first.album.cover, 256),
  };
}

const failed = (title, error) => toast({ title, sub: error.message, badge: 'alert', error: true });

// ---- Playing and queueing ----------------------------------------------------

// A song whose file is gone cannot be played: the others still can.
function playable(tracks) {
  const ok = tracks.filter(track => track.available);
  if (tracks.length && !ok.length) {
    toast({ title: 'This song can’t be played', sub: 'The file is missing from the library folder.', badge: 'alert', error: true });
  }
  return ok;
}

export function playFrom(tracks, index, context) {
  if (!playable([tracks[index]]).length) return;
  const ok = tracks.filter(track => track.available);
  player.play(ok, ok.indexOf(tracks[index]), context);
}

export function playAll(tracks, context) {
  const ok = playable(tracks);
  if (ok.length) player.play(ok, 0, context);
}

export function shuffleAll(tracks, context) {
  const ok = playable(tracks);
  if (ok.length) player.shuffle(ok, context);
}

export function playNext(tracks) {
  const ok = playable(tracks);
  if (ok.length) toast({ title: 'Plays next', ...about(ok), badge: 'queue', undo: player.playNext(ok) });
}

export function addToQueue(tracks) {
  const ok = playable(tracks);
  if (ok.length) toast({ title: 'Added to queue', ...about(ok), badge: 'queue', undo: player.addToQueue(ok) });
}

// ---- Favorites ---------------------------------------------------------------

// The heart changes first, on the song itself: rows, the player bar and the
// counts listen for track:favorite. The server is told after; if it
// refuses, the heart goes back.
async function keep(tracks, on) {
  const changed = tracks.filter(track => track.favorite !== on);
  const mark = value => { for (const track of changed) { track.favorite = value; emit('track:favorite', { track }); } };
  mark(on);
  try {
    if (on && changed.length > 1) {
      try {
        await api.post('/me/favorites/tracks', { track_ids: changed.map(track => track.id) });
      } catch (error) {
        if (!isMissing(error)) throw error;
        await Promise.all(changed.map(track => api.put(`/me/favorites/tracks/${track.id}`)));
      }
    } else {
      await Promise.all(changed.map(track => api[on ? 'put' : 'del'](`/me/favorites/tracks/${track.id}`)));
    }
  } catch (error) {
    mark(!on);
    failed('Couldn’t update favorites', error);
    return null;
  }
  return changed;
}

export async function setFavorites(tracks, on) {
  const changed = tracks.filter(track => track.favorite !== on);
  if (!changed.length) return;
  toast({
    title: on ? 'Added to favorites' : 'Removed from favorites', ...about(changed), badge: 'fav',
    undo: () => keep(changed, !on),
  });
  await keep(changed, on);
}

// The sidebar's count follows every heart.
document.addEventListener('track:favorite', ({ detail }) => {
  const count = $('#favorites-count');
  if (!count || count.hidden) return;
  count.textContent = number.format(Math.max(0, parseInt(count.textContent.replace(/\D/g, ''), 10) + (detail.track.favorite ? 1 : -1)));
});

// ---- Playlists ---------------------------------------------------------------

// Whatever changes a playlist says so on `document`, with the playlist as the
// server answered it: the sidebar, the open playlist and the grid follow
// (`playlist:changed` { playlist, added?: items, removed?: item ids },
// `playlist:deleted` { id }). Nobody reads a playlist again to learn this.
export const playlistContext = playlist => ({ type: 'playlist', id: playlist.id, name: playlist.name });

// Every song of a playlist, in order, the ones that are gone included.
export async function playlistTracks(id) {
  const all = [];
  for await (const page of walk(`/playlists/${id}/items`)) all.push(...page.items.map(item => item.track));
  return all;
}

// A playlist that changed elsewhere (412): read it again, or take it away if
// it is gone.
export async function resync(id) {
  try {
    emit('playlist:changed', { playlist: await api.get(`/playlists/${id}`) });
  } catch (error) {
    if (error.status === 404) emit('playlist:deleted', { id });
  }
}

// Puts songs at the end of a playlist, at most 1,000 to a request. Each
// block is announced as soon as the server has it.
export async function appendTracks(playlist, tracks) {
  const added = [];
  let last = null;
  for (let i = 0; i < tracks.length; i += 1000) {
    const block = tracks.slice(i, i + 1000);
    last = await api.post(`/playlists/${playlist.id}/items`, { track_ids: block.map(track => track.id), position: null });
    const items = block.map((track, n) => ({ id: last.added[n].item_id, position: last.added[n].position, added_at: last.playlist.updated_at, track }));
    added.push(...items);
    emit('playlist:changed', { playlist: last.playlist, added: items });
  }
  return { playlist: last.playlist, added };
}

// Adds the songs at the end of each playlist; answers what was done, or null
// when something failed (a toast says so). Undo takes each new item out
// again by its id, on the revision the answer gave (an item put back would
// be a new item with a new date).
export async function addToPlaylists(tracks, playlists) {
  const results = [];
  try {
    for (const playlist of playlists) results.push([playlist, await appendTracks(playlist, tracks)]);
  } catch (error) {
    failed(`Couldn’t add to ${playlists[results.length].name}`, error);
    return null;
  }
  toast({
    title: playlists.length === 1 ? `Added to ${playlists[0].name}` : `Added to ${plural(playlists.length, 'playlist')}`,
    ...about(tracks), badge: 'add',
    undo: async () => {
      for (const [playlist, { playlist: now, added }] of results) {
        let etag = now.etag, latest = now;
        try {
          for (const { id } of added) {
            latest = await api.del(`/playlists/${playlist.id}/items/${id}`, { ifMatch: etag });
            etag = latest.etag;
          }
        } catch (error) {
          if (isStale(error)) { staleToast(); resync(playlist.id); } else failed('Couldn’t undo', error);
          continue;
        }
        emit('playlist:changed', { playlist: latest, removed: added.map(item => item.id) });
      }
    },
  });
  return results;
}

// What a menu item does with songs that may have to be read first (an
// album's): if the reading fails, the reader has said so already.
const withTracks = (read, fn) => async () => {
  try { fn(typeof read === 'function' ? await read() : read); } catch { /* said by lazy() */ }
};

// "Add to playlist": a submenu, one song in one click. `tracks` is a list or
// a function that reads it. On a playlist's own page the playlist is left
// out, and the item says "another".
export function playlistMenu(tracks, { except = null, label = 'Add to playlist' } = {}) {
  return {
    label, icon: 'plus',
    items: [
      { label: 'New playlist…', icon: 'plus', run: withTracks(tracks, list => newPlaylist(playlist => addToPlaylists(list, [playlist]))) },
      ...(getPlaylists().length ? ['-'] : []),
      ...getPlaylists().filter(playlist => playlist.id !== except).map(playlist => ({
        label: playlist.name, icon: 'playlists', cover: coverUrl(playlist.covers?.[0], 256), key: playlist.id,
        run: withTracks(tracks, list => addToPlaylists(list, [playlist])),
      })),
    ],
  };
}

// A check mark on the playlists that hold the song, if the server can say.
export async function markHeld(menu, track) {
  const held = await optional(api.get(`/tracks/${track.id}/playlists`)).catch(() => null);
  if (!held) return;
  const ids = new Set(held.playlists.map(playlist => playlist.id));
  for (const item of menu.querySelectorAll('.sub .menu .mi[data-key]')) show($('.end', item), ids.has(item.dataset.key));
}

// The Edit details sheet: name and description. The change shows at once
// (it is announced before the server answers) and goes back if it fails.
export function editPlaylist(playlist) {
  const dialog = openSheet('t-sheet-edit-playlist', d => {
    $('input[name="name"]', d).value = playlist.name;
    $('textarea', d).value = playlist.description;
  });
  const form = $('form', dialog);
  form.elements.name.select();
  form.addEventListener('submit', async event => {
    if (event.submitter?.value !== 'save') return;
    event.preventDefault();
    const name = form.elements.name.value.trim(), description = form.elements.description.value.trim();
    if (!name) { form.elements.name.focus(); return; }
    dialog.close('save');
    if (name === playlist.name && description === playlist.description) return;
    emit('playlist:changed', { playlist: { ...playlist, name, description } });
    try {
      emit('playlist:changed', { playlist: await api.put(`/playlists/${playlist.id}`, { name, description }, { ifMatch: playlist.etag }) });
    } catch (error) {
      if (isStale(error)) { staleToast(); resync(playlist.id); return; }
      emit('playlist:changed', { playlist });
      failed('Couldn’t save the details', error);
    }
  });
}

// Delete playlist: the one thing here that asks first, as it cannot be undone.
export async function deletePlaylist(playlist) {
  const ok = await confirmSheet({
    title: `Delete “${playlist.name}”?`,
    text: `The playlist is deleted. Its ${plural(playlist.item_count, 'song')} stay in your library. This can’t be undone.`,
    action: 'Delete playlist', danger: true,
  });
  if (!ok) return false;
  try {
    await api.del(`/playlists/${playlist.id}`, { ifMatch: playlist.etag });
  } catch (error) {
    if (isStale(error)) { staleToast(); resync(playlist.id); } else failed('Couldn’t delete the playlist', error);
    return false;
  }
  emit('playlist:deleted', { id: playlist.id });
  toast({ title: `Deleted “${playlist.name}”`, badge: 'trash' });
  return true;
}

// Several songs, several playlists: the sheet with the check boxes.
export async function addSheet(tracks) {
  const picked = new Set();
  const dialog = openSheet('t-sheet-add', d => {
    $('.sheet-title', d).textContent = `Add ${plural(tracks.length, 'song')}`;
    const list = $('.pick-list', d), submit = $('.btn-primary', d);
    const label = () => { submit.textContent = picked.size ? `Add to ${plural(picked.size, 'playlist')}` : 'Add'; submit.disabled = !picked.size; };
    for (const playlist of getPlaylists()) {
      const pick = clone('t-pick');
      pick.dataset.name = playlist.name.toLowerCase();
      $('.pick-name', pick).textContent = playlist.name;
      $('.sub', pick).textContent = plural(playlist.item_count, 'song');
      setCover($('.pick-cover', pick), playlist.covers?.[0]);
      pick.addEventListener('click', () => {
        const on = !picked.has(playlist);
        if (on) picked.add(playlist); else picked.delete(playlist);
        pick.setAttribute('aria-pressed', String(on));
        show($('.box .i', pick), on);
        $('.box', pick).classList.toggle('on', on);
        label();
      });
      list.append(pick);
    }
    $('input', d).addEventListener('input', event => {
      const text = event.target.value.trim().toLowerCase();
      for (const pick of list.children) show(pick, pick.dataset.name.includes(text));
    });
    label();
  });
  const result = await whenClosed(dialog);
  if (result === 'add') addToPlaylists(tracks, [...picked]);
  if (result === 'new') newPlaylist(playlist => addToPlaylists(tracks, [playlist]));
}

// ---- Details and files -------------------------------------------------------

const CODECS = { flac: 'FLAC', mp3: 'MP3', aac: 'AAC', alac: 'ALAC' };

// "FLAC", "24-bit", "96 kHz": the parts of a format, for the album's badges
// and the details of a song.
export function formatParts(format) {
  return {
    codec: CODECS[format.codec] || format.codec,
    depth: format.bit_depth ? `${format.bit_depth}-bit` : (format.bitrate ? `${Math.round(format.bitrate / 1000)} kbps` : ''),
    rate: `${+(format.sample_rate / 1000).toFixed(1)} kHz`,
  };
}

const decibels = value => `${value < 0 ? '−' : '+'}${Math.abs(value).toFixed(1)} dB`;
const megabytes = size => (size >= 1e9 ? `${(size / 1e9).toFixed(1)} GB` : `${(size / 1e6).toFixed(1)} MB`);

export function songDetails(track) {
  const { format: f, replay_gain: gain } = track;
  const parts = formatParts(f);
  const gains = gain && [gain.track_gain_db != null && `Track ${decibels(gain.track_gain_db)}`, gain.album_gain_db != null && `Album ${decibels(gain.album_gain_db)}`].filter(Boolean);
  const facts = [
    ['Disc, track', `${track.disc}, ${track.number}`],
    ['Duration', formatTime(track.duration_ms)],
    ['Genre', track.genre || '–'],
    ['Format', [parts.codec, parts.depth, parts.rate, f.channels === 2 ? 'stereo' : f.channels === 1 ? 'mono' : `${f.channels} channels`].filter(Boolean).join(' · ')],
    ['Size', megabytes(f.size)],
    ['ReplayGain', gains?.length ? gains.join(' · ') : 'None'],
    ['Lyrics', track.has_lyrics ? 'Available' : 'None'],
  ];
  let lyrics;
  const dialog = openSheet('t-sheet-song', d => {
    setCover($('.sd-cover', d), track.album.cover, 256);
    $('.sheet-title', d).textContent = track.title;
    $('.sd-sub', d).textContent = `${track.artist} · ${track.album.title}`;
    for (const [name, value] of facts) {
      const dt = document.createElement('dt'), dd = document.createElement('dd');
      dt.textContent = name;
      dd.textContent = value;
      if (name === 'Lyrics') lyrics = dd;
      $('.facts', d).append(dt, dd);
    }
    $('.dl', d).addEventListener('click', () => downloadTrack(track));
  });
  if (track.has_lyrics) {
    optional(api.get(`/tracks/${track.id}/lyrics`)).then(text => {
      if (text) lyrics.textContent = text.synced ? 'Synced' : 'Not synced';
    }).catch(() => { /* "Available" says enough */ });
  }
}

// The browser saves the file under a name made from the tags: the server
// never sends one (proposal 4.3).
function save(href, name) {
  const link = document.createElement('a');
  link.href = href;
  link.download = name.replace(/[\\/:*?"<>|]/g, '_');
  document.body.append(link);
  link.click();
  link.remove();
}

export function downloadTrack(track) {
  save(`/api/v1/tracks/${track.id}/audio`, `${track.artist} - ${track.title}.${{ aac: 'm4a', alac: 'm4a' }[track.format.codec] || track.format.codec}`);
}

export const downloadCover = album => save(`${album.cover.url}${album.cover.url.includes('?') ? '&' : '?'}size=original`, `${album.artist.name} - ${album.title}`);

// ---- Menus -------------------------------------------------------------------

export const artistLabel = track => (track.artist === track.album.artist.name ? 'Go to artist' : `Go to ${track.album.artist.name}`);

// The ⋯ menu of one song or of a selection. `skip` leaves out the link to
// the page the songs are on already ('album' or 'artist'). Several songs get
// what makes sense for all of them.
export function trackMenu(tracks, skip = []) {
  const one = tracks.length === 1 ? tracks[0] : null;
  const gone = tracks.every(track => !track.available);
  const allFavorite = tracks.every(track => track.favorite);
  const items = [
    { label: 'Play next', icon: 'play-next', disabled: gone, run: () => playNext(tracks) },
    { label: 'Add to queue', icon: 'queue', disabled: gone, run: () => addToQueue(tracks) },
    playlistMenu(tracks),
    { label: allFavorite ? 'Remove from favorites' : 'Add to favorites', icon: 'heart', run: () => setFavorites(tracks, !allFavorite) },
  ];
  if (one) {
    const go = [];
    if (!skip.includes('album')) go.push({ label: 'Go to album', icon: 'album', href: `/albums/${one.album.id}` });
    if (!skip.includes('artist')) go.push({ label: artistLabel(one), icon: 'artist', href: `/artists/${one.album.artist.id}` });
    if (go.length) items.push('-', ...go);
    items.push('-', { label: 'Song details', icon: 'info', run: () => songDetails(one) }, { label: 'Download file', icon: 'download', disabled: !one.available, run: () => downloadTrack(one) });
  }
  return items;
}

export const albumTracks = async id => (await api.get(`/albums/${id}`)).tracks;

// The songs of an album or an artist, read when a menu item needs them; a
// failure is one sentence, never a silent button.
export function lazy(read, what) {
  return async () => {
    try { return await read(); } catch (error) { failed(`Couldn’t ${what}`, error); throw error; }
  };
}

// The items of a whole album or artist: `read` gets the songs, `context`
// is where the music comes from. A page that has buttons for some of them
// leaves those out.
export function collectionMenu(read, context, { play = true, queue = true, favorites = false, playlist = true } = {}) {
  const run = fn => withTracks(read, fn);
  return [
    ...(play ? [
      { label: 'Play', icon: 'play', filled: true, run: run(tracks => playAll(tracks, context)) },
      { label: 'Shuffle', icon: 'shuffle', run: run(tracks => shuffleAll(tracks, context)) },
    ] : []),
    { label: 'Play next', icon: 'play-next', run: run(playNext) },
    ...(queue ? [{ label: 'Add to queue', icon: 'queue', run: run(addToQueue) }] : []),
    ...(playlist ? [playlistMenu(read)] : []),
    ...(favorites ? [{ label: 'Add all songs to favorites', icon: 'heart', run: run(tracks => setFavorites(tracks, true)) }] : []),
  ];
}

// Every available song of an artist, in the order of its albums. Without
// GET /tracks (it may not exist yet) the albums are read one by one.
export async function artistTracks(id) {
  try {
    const all = [];
    for await (const page of walk('/tracks', { artist: id, sort: 'album' })) all.push(...page.tracks);
    return all;
  } catch (error) {
    if (!isMissing(error)) throw error;
    const { albums } = await api.get(`/artists/${id}`);
    return (await Promise.all(albums.map(album => albumTracks(album.id)))).flat();
  }
}
