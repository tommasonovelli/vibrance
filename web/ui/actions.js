// What a person does with songs and albums, from a menu, a button or a drag:
// play them, queue them, make them favorites, put them in a playlist. Each
// change shows at once and ends in a toast with Undo, not in a question
// (proposal 7.2); a failure puts things back and says so in one sentence.
import { api, coverUrl, isMissing, isStale, optional, walk } from './api.js';
import { $, clone, openSheet, setCover, setIcon, show, staleToast, toast, whenClosed, formatTime } from './ui.js';
import * as player from './player.js';
import { getPlaylists, newPlaylist, reload as reloadSidebar } from './sidebar.js';

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

// Adds the songs at the end of each playlist. Undo takes each new item out
// again by its id, on the revision the answer gave (an item put back would
// be a new item with a new date).
export async function addToPlaylists(tracks, playlists) {
  const results = [];
  try {
    for (const playlist of playlists) {
      results.push([playlist, await api.post(`/playlists/${playlist.id}/items`, { track_ids: tracks.map(track => track.id), position: null })]);
    }
  } catch (error) {
    const name = playlists[results.length].name;
    if (results.length) reloadSidebar();
    return failed(`Couldn’t add to ${name}`, error);
  }
  reloadSidebar();
  toast({
    title: playlists.length === 1 ? `Added to ${playlists[0].name}` : `Added to ${plural(playlists.length, 'playlist')}`,
    ...about(tracks), badge: 'add',
    undo: async () => {
      try {
        for (const [playlist, { playlist: now, added }] of results) {
          let etag = now.etag;
          for (const { item_id } of added) etag = (await api.del(`/playlists/${playlist.id}/items/${item_id}`, { ifMatch: etag })).etag;
        }
      } catch (error) {
        if (isStale(error)) staleToast(); else failed('Couldn’t undo', error);
      }
      reloadSidebar();
    },
  });
}

// What a menu item does with songs that may have to be read first (an
// album's): if the reading fails, the reader has said so already.
const withTracks = (read, fn) => async () => {
  try { fn(typeof read === 'function' ? await read() : read); } catch { /* said by lazy() */ }
};

// "Add to playlist": a submenu, one song in one click. `tracks` is a list or
// a function that reads it.
export function playlistMenu(tracks) {
  return {
    label: 'Add to playlist', icon: 'plus',
    items: [
      { label: 'New playlist…', icon: 'plus', run: withTracks(tracks, list => newPlaylist(playlist => addToPlaylists(list, [playlist]))) },
      ...(getPlaylists().length ? ['-'] : []),
      ...getPlaylists().map(playlist => ({
        label: playlist.name, icon: 'playlists', cover: coverUrl(playlist.covers?.[0], 256),
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
  const items = menu.querySelectorAll('.sub .menu .mi'); // the first is "New playlist…"
  getPlaylists().forEach((playlist, i) => items[i + 1] && show($('.end', items[i + 1]), ids.has(playlist.id)));
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

const artistLabel = track => (track.artist === track.album.artist.name ? 'Go to artist' : `Go to ${track.album.artist.name}`);

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
export function collectionMenu(read, context, { play = true, queue = true, favorites = false } = {}) {
  const run = fn => withTracks(read, fn);
  return [
    ...(play ? [
      { label: 'Play', icon: 'play', filled: true, run: run(tracks => playAll(tracks, context)) },
      { label: 'Shuffle', icon: 'shuffle', run: run(tracks => shuffleAll(tracks, context)) },
    ] : []),
    { label: 'Play next', icon: 'play-next', run: run(playNext) },
    ...(queue ? [{ label: 'Add to queue', icon: 'queue', run: run(addToQueue) }] : []),
    playlistMenu(read),
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
