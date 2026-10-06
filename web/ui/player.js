// The player's interface. Round 2 only needs the buttons to work and the
// events to fire, so this body keeps a queue and a current song and says so
// on `document`; there is no audio yet. Round 3 replaces the body (the audio
// element, the queue panel, the bar) and keeps these signatures.
//
//   player:track  { track, context } | null   the song that is current
//   player:state  { playing }                 play or pause
//   player:queue  { queue }                   the songs that follow the current one
//
// `context` says where the music comes from: { type: 'album' | 'artist' |
// 'playlist' | 'favorites' | 'library' | 'search', id, name }. playNext and
// addToQueue return a function that takes those songs out again, which is
// what the Undo of their toast calls.
let queue = [];
let now = null; // { track, context }
let playing = false;

const emit = (name, detail) => document.dispatchEvent(new CustomEvent(name, { detail }));

export const current = () => now;
export const isPlaying = () => playing;

export function play(tracks, index = 0, context = null) {
  if (!tracks[index]) return;
  queue = tracks.slice(index + 1);
  now = { track: tracks[index], context };
  playing = true;
  emit('player:track', now);
  emit('player:state', { playing });
  emit('player:queue', { queue });
}

// A fresh order for the songs: Fisher-Yates here, until the player spreads
// the artists apart (proposal 7.9).
export function shuffle(tracks, context = null) {
  const order = tracks.slice();
  for (let i = order.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [order[i], order[j]] = [order[j], order[i]];
  }
  play(order, 0, context);
}

function insert(tracks, at) {
  const added = tracks.slice();
  queue = [...queue.slice(0, at), ...added, ...queue.slice(at)];
  emit('player:queue', { queue });
  return () => {
    queue = queue.filter(track => !added.includes(track));
    emit('player:queue', { queue });
  };
}

export const playNext = tracks => insert(tracks, 0);
export const addToQueue = tracks => insert(tracks, queue.length);
