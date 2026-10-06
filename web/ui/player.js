// The player: the audio, the queue and everything the rest of the page asks
// of them. It owns the playback state and says what changed on `document`:
//
//   player:track  { track, context } | null   the song that is current
//   player:state  { playing, ended }          play, pause, the queue ran out
//   player:queue  { queue }                   the songs that follow the current one
//   player:time   { position, duration }      milliseconds; about 4 times a second, never while the page is hidden
//   player:volume { volume, muted }
//   player:mode   { shuffle, repeat }         repeat is 'off', 'all' or 'one'
//
// `context` says where the music comes from: { type: 'album' | 'artist' |
// 'playlist' | 'favorites' | 'library' | 'search', id, name }. playNext and
// addToQueue (and every removal) return a function that puts things back,
// which is what the Undo of their toast calls.
//
// The queue is one list in the order of play, with a pointer at the song that
// plays; the songs before the pointer have played. What the person queued by
// hand (`user`) sits right after the pointer and plays before the rest of
// the context. At the end of the list the music stops: nothing is chosen for
// the listener (proposal 7.8).
//
// Two <audio> elements take turns: while one plays, the other has the start
// of the next song loaded, so the switch is instant (proposal 7.12). Levelling
// and volume go through a Web Audio GainNode per element (7.10), because
// `audio.volume` can only lower the level. Everything is kept in localStorage
// and comes back paused after a reload.
import { api, audioUrl, coverUrl } from './api.js';
import { settings } from './settings.js';
import { toast } from './ui.js';

const emit = (name, detail) => document.dispatchEvent(new CustomEvent(name, { detail }));
const clamp = (value, low, high) => Math.min(Math.max(value, low), high);

// ---- State ---------------------------------------------------------------------

let items = [];      // { track, context, user, seq }: the whole queue, in the order it plays
let index = -1;      // the song that plays; -1 when there is none
let all = [];        // the songs of the context in the order of its list, for turning shuffle off and Play again
let now = null;      // { track, context } of the current song
let playing = false;
let ended = false;   // the queue ran out: paused on the last song, which shows the end state
let shuffleOn = false;
let repeatMode = 'off';
let volume = .7;
let muted = false;
let more = null;     // how the queue goes on when it runs low (the Library): { sort, order, after }
let refilling = null;
let skipped = 0;     // songs in a row that could not be played

const entry = (track, context, user = false, seq = -1) => ({ track, context, user, seq });
const playable = e => e.track.available !== false;

export const current = () => now;
export const isPlaying = () => playing;
export const isShuffled = () => shuffleOn;
export const getRepeat = () => repeatMode;
export const getVolume = () => volume;
export const isMuted = () => muted;
export const hasEnded = () => ended;

// The songs after the current one, split the way the panel shows them:
// `user` is "Next in queue", `rest` is "Next from <context>".
export function queue() {
  const after = items.slice(index + 1);
  const user = after.findIndex(e => !e.user);
  const run = user < 0 ? after.length : user;
  return { now: items[index] || null, user: after.slice(0, run), rest: after.slice(run), ended };
}

// The next song that plays by itself: -1 at the end of the queue (repeat all
// goes back to the first). Songs that are not available are passed over.
function following(from = index) {
  for (let i = from + 1; i < items.length; i++) if (playable(items[i])) return i;
  if (repeatMode === 'all') for (let i = 0; i <= from && i < items.length; i++) if (playable(items[i])) return i;
  return -1;
}
function preceding(from = index) {
  for (let i = from - 1; i >= 0; i--) if (playable(items[i])) return i;
  return -1;
}
// The songs of the context as it was started, for "End of Sunday morning: 23 songs".
export const contextTracks = () => all.map(e => e.track);
export const canNext = () => index >= 0 && !ended && following() >= 0;

// ---- Storage -----------------------------------------------------------------------

const KEY = 'vibrance.player', POSITION = 'vibrance.position', VOLUME = 'vibrance.volume';
const KEPT = 500; // songs after the current one that are remembered

function read(key) {
  try { return JSON.parse(localStorage.getItem(key)); } catch { return null; }
}
function write(key, value) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* not remembered */ }
}

let saving = 0;
const saveSoon = () => { clearTimeout(saving); saving = setTimeout(save, 400); };
function save() {
  clearTimeout(saving);
  const from = Math.max(0, index - 20);
  write(KEY, {
    items: items.slice(from, index + 1 + KEPT), index: index - from, shuffle: shuffleOn, repeat: repeatMode, ended, more,
  });
}
function savePosition() {
  if (items[index]) write(POSITION, { id: items[index].track.id, ms: position() });
}

// ---- Audio: two elements, one gain each -------------------------------------------------

const slots = [makeSlot(), makeSlot()]; // slots[0] plays; slots[1] holds the start of what is next
let ctx = null;

function makeSlot() {
  const el = new Audio();
  el.preload = 'none';
  const slot = { el, entry: null, node: null, tries: 0 };
  const mine = () => slots[0] === slot;
  el.addEventListener('timeupdate', () => mine() && tick());
  el.addEventListener('ended', () => mine() && finish());
  // A start loaded ahead that cannot be played is let go: it is tried again, and fails visibly, when its turn comes.
  el.addEventListener('error', () => { if (el.hasAttribute('src')) { if (mine()) failed(slot); else release(slot); } });
  el.addEventListener('play', () => mine() && setPlaying(true));
  el.addEventListener('pause', () => mine() && !el.ended && setPlaying(false));
  el.addEventListener('playing', () => { if (mine()) slot.tries = skipped = 0; });
  el.addEventListener('durationchange', () => mine() && emitTime());
  return slot;
}

// The levelling needs the Web Audio graph, which a browser lets start only
// after the person has touched the page; until then (a media key right after
// a reload) the element's own volume does the lowering.
function connect() {
  if (ctx || !window.AudioContext || navigator.userActivation?.hasBeenActive === false) return;
  try {
    ctx = new AudioContext();
    for (const slot of slots) {
      slot.node = ctx.createGain();
      ctx.createMediaElementSource(slot.el).connect(slot.node).connect(ctx.destination);
    }
  } catch {
    ctx = null;
    return;
  }
  applyAll();
}

// ReplayGain (proposal 7.10): the album's gain for the songs of an album that
// play in order, the song's own gain otherwise, never beyond the peak (and 1 dB
// under it for the lossy codecs). A song with no gain plays as it is.
const ordered = (a, b) => a && b && a.track.album.id === b.track.album.id
  && (b.track.disc - a.track.disc || b.track.number - a.track.number) > 0;

export function leveling(of = items[index]) {
  const gain = of?.track.replay_gain;
  if (!gain || settings().volume_leveling === 'off') return null;
  const at = items.indexOf(of);
  const album = !shuffleOn && (ordered(items[at - 1], of) || ordered(of, items[at + 1]));
  const db = album ? gain.album_gain_db ?? gain.track_gain_db : gain.track_gain_db ?? gain.album_gain_db;
  if (db == null) return null;
  const peak = album ? gain.album_peak ?? gain.track_peak : gain.track_peak ?? gain.album_peak;
  const lossy = of.track.format.codec === 'mp3' || of.track.format.codec === 'aac';
  const linear = Math.min(10 ** (db / 20), peak > 0 ? (lossy ? .89 : 1) / peak : 1);
  return { db: 20 * Math.log10(linear), linear, album };
}

// Volume follows a curve (the square), which is how loudness is heard; the
// gain eases to its new value so a drag of the slider makes no clicks.
function applyGain(slot) {
  const loud = muted ? 0 : volume ** 2 * (leveling(slot.entry)?.linear ?? 1);
  if (slot.node) {
    slot.node.gain.setTargetAtTime(loud, ctx.currentTime, .02);
    slot.el.volume = 1;
  } else {
    slot.el.volume = Math.min(1, loud);
  }
}
const applyAll = () => slots.forEach(applyGain);
document.addEventListener('settings:changed', applyAll);

function setSource(slot, of, preload = 'auto') {
  slot.entry = of;
  slot.tries = 0;
  slot.el.src = audioUrl(of.track);
  slot.el.preload = preload;
  applyGain(slot);
}

function release(slot) {
  slot.entry = null;
  slot.el.pause();
  if (slot.el.hasAttribute('src')) {
    slot.el.removeAttribute('src');
    slot.el.load();
  }
}

// ---- Time ---------------------------------------------------------------------------------

export function position() {
  return (slots[0].el.currentTime || 0) * 1000;
}
export function length() {
  const el = slots[0].el;
  return Number.isFinite(el.duration) ? el.duration * 1000 : (items[index]?.track.duration_ms ?? 0);
}

function emitTime() {
  if (!document.hidden) emit('player:time', { position: position(), duration: length() });
}

let lastSaved = 0;
function tick() {
  emitTime();
  const at = Date.now();
  if (at - lastSaved > 5000) { lastSaved = at; savePosition(); }
  if (!slots[1].entry && length() - position() < 20000) preload();
}
document.addEventListener('visibilitychange', () => { if (document.hidden) savePosition(); else emitTime(); });
addEventListener('pagehide', () => { savePosition(); save(); });

export function seek(ms) {
  const slot = slots[0];
  if (!slot.entry) return;
  slot.el.currentTime = clamp(ms, 0, length()) / 1000;
  if (ended) { ended = false; emit('player:state', { playing, ended }); }
  emitTime();
  positionState();
  savePosition();
}

// ---- Going to a song ----------------------------------------------------------------------------

// Makes items[i] the song that plays: the start that was loaded ahead is used
// if it is the right one.
function goTo(i, { play = true, position: from = 0 } = {}) {
  index = i;
  ended = false;
  const of = items[i];
  const [, spare] = slots;
  if (spare.entry !== of) setSource(spare, of, play ? 'auto' : 'none');
  slots.reverse();
  release(slots[1]);
  const slot = slots[0];
  if (from) slot.el.currentTime = from / 1000;
  now = { track: of.track, context: of.context };
  applyAll();
  emit('player:track', now);
  emit('player:state', { playing, ended });
  changed();
  sessionTrack();
  if (play) start(); else setPlaying(false);
}

function start() {
  connect();
  ctx?.resume().catch(() => {});
  const slot = slots[0];
  setPlaying(true);
  // Refused (no touch on the page yet) is a pause; a missing file arrives as an `error` event.
  slot.el.play().catch(error => { if (error.name === 'NotAllowedError' && slots[0] === slot) setPlaying(false); });
}

function setPlaying(value) {
  if (playing === value) return;
  playing = value;
  emit('player:state', { playing, ended });
  if ('mediaSession' in navigator) navigator.mediaSession.playbackState = playing ? 'playing' : 'paused';
  if (!playing) savePosition();
  positionState();
}

// What follows has its start loaded into the idle element; a queue that
// changed under it makes that start the wrong one.
function preload() {
  const at = repeatMode === 'one' ? -1 : following();
  const next = at < 0 ? null : items[at];
  const [, spare] = slots;
  if (spare.entry && spare.entry !== next) release(spare);
  if (next && !spare.entry && next !== items[index] && length() - position() < 20000) setSource(spare, next);
}

// The end of a song: the next one, the same one again, or the end of the queue.
async function finish() {
  if (repeatMode === 'one') {
    slots[0].el.currentTime = 0;
    start();
    return;
  }
  let at = following();
  if (at < 0 && refilling) { await refilling; at = following(); }
  if (at < 0) stopAtEnd(); else goTo(at);
}

function stopAtEnd() {
  ended = true;
  slots[0].el.currentTime = 0;
  setPlaying(false);
  emit('player:state', { playing, ended });
  emitTime();
  sessionActions();
  saveSoon();
}

// A song that cannot be played. If its file is gone it is skipped, with a
// word about it; if the server could not be reached it is tried again a
// moment later, and then the music pauses. Never a loop: three songs in a
// row that fail stop it too.
async function failed(slot) {
  if (!slot.entry) return;
  const { track } = slot.entry;
  let gone = track.available === false;
  if (!gone) {
    try { gone = !(await api.get(`/tracks/${track.id}`)).available; } catch (error) { gone = error.status === 404; }
  }
  if (slots[0] !== slot) return; // the person moved on meanwhile
  if (!gone) {
    if (slot.tries++ < 2) {
      const at = slot.el.currentTime;
      setTimeout(() => {
        if (slots[0] !== slot || !playing) return;
        slot.el.src = audioUrl(track);
        slot.el.currentTime = at;
        start();
      }, 1500 * slot.tries);
      return;
    }
    setPlaying(false);
    toast({ title: 'Can’t play this song right now', sub: 'Can’t reach Vibrance. Check your connection and try again.', badge: 'alert', error: true });
    return;
  }
  track.available = false;
  const at = following();
  if (++skipped >= 3) {
    setPlaying(false);
    toast({ title: 'Playback stopped', sub: 'Several songs in a row can’t be played.', badge: 'alert', error: true });
    changed();
    return;
  }
  toast({
    title: 'This song can’t be played',
    sub: `The file is missing from the library folder.${at < 0 ? '' : ' Playing the next one.'}`,
    badge: 'alert', error: true, undoLabel: 'Details',
    undo: () => import('./actions.js').then(actions => actions.songDetails(track)),
  });
  if (at < 0) { changed(); stopAtEnd(); } else goTo(at);
}

// ---- Playing and pausing ------------------------------------------------------------------

export function toggle() {
  if (playing) pause(); else resume();
}
export function pause() {
  setPlaying(false);
  slots[0].el.pause();
}
export function resume() {
  if (index < 0) return;
  if (ended) goTo(index); else start();
}

export function next() {
  const at = following();
  if (index >= 0 && !ended && at >= 0) goTo(at);
}
// Previous restarts the song once it has played for 3 seconds, as everywhere.
export function previous() {
  if (index < 0) return;
  const at = preceding();
  if (position() > 3000 || at < 0) seek(0); else goTo(at);
}

export function jumpTo(of) {
  const at = items.indexOf(of);
  if (at >= 0 && playable(of)) goTo(at);
}

// ---- Starting a queue ------------------------------------------------------------------------

// What the person queued by hand stays when a new context starts.
const handQueued = () => (index < 0 ? [] : queue().user);

// The Library goes on past the songs that are loaded: after them in its own
// order, or at random when it is shuffled (B1).
const moreFor = (context, viaShuffle) => (context?.type === 'library'
  ? { sort: context.sort, order: context.order, after: viaShuffle ? null : context.after ?? null }
  : null);

export function play(tracks, at = 0, context = null) {
  if (!tracks[at]) return;
  all = tracks.map((track, seq) => entry(track, context, false, seq));
  more = moreFor(context, false);
  const kept = handQueued();
  if (shuffleOn) {
    items = [all[at], ...kept, ...spreadOrder(all.filter((_, i) => i !== at))];
    index = 0;
  } else {
    items = [...all.slice(0, at + 1), ...kept, ...all.slice(at + 1)];
    index = at;
  }
  goTo(index);
}

// The Shuffle button: shuffle on, a random song first.
export function shuffle(tracks, context = null) {
  if (!tracks.length) return;
  all = tracks.map((track, seq) => entry(track, context, false, seq));
  more = moreFor(context, true);
  const kept = handQueued();
  const [first, ...rest] = spreadOrder(all);
  items = [first, ...kept, ...rest];
  shuffleOn = true;
  emit('player:mode', { shuffle: shuffleOn, repeat: repeatMode });
  goTo(0);
}

// Play again and Shuffle, in the end state of the queue.
export function replay(shuffled) {
  const tracks = all.map(e => e.track), context = items[index]?.context ?? null;
  if (shuffled) { shuffle(tracks, context); return; }
  setShuffle(false);
  play(tracks, 0, context);
}

// ---- Shuffle that feels random (proposal 7.9) ------------------------------------------------------
// True random makes clusters that people read as non-random. The songs of
// each artist are spread evenly along the list, with a random offset, and
// within an artist the songs of each album the same way.

function shuffled(list) {
  const order = list.slice();
  for (let i = order.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [order[i], order[j]] = [order[j], order[i]];
  }
  return order;
}

// Merges groups: item j of a group of n lands near (j + offset) / n.
function spread(groups) {
  const keyed = [];
  for (const group of groups) {
    const offset = Math.random();
    group.forEach((item, j) => keyed.push([(j + offset + (Math.random() - .5) * .3) / group.length, item]));
  }
  return keyed.sort((a, b) => a[0] - b[0]).map(pair => pair[1]);
}

const artistOf = e => e.track.album.artist.id;

function spreadOrder(entries) {
  const artists = [...Map.groupBy(entries, artistOf).values()];
  const merged = spread(artists.map(songs => spread([...Map.groupBy(shuffled(songs), e => e.track.album.id).values()])));
  // Two songs of one artist can still touch where the spreading collided:
  // each is swapped with a later song that fits there.
  const clash = i => i > 0 && i < merged.length && artistOf(merged[i]) === artistOf(merged[i - 1]);
  for (let i = 1; i < merged.length; i++) {
    if (!clash(i)) continue;
    for (let j = i + 1; j < merged.length; j++) {
      [merged[i], merged[j]] = [merged[j], merged[i]];
      if (![i, i + 1, j, j + 1].some(clash)) break;
      [merged[i], merged[j]] = [merged[j], merged[i]];
    }
  }
  return merged;
}

// Turning shuffle on keeps the current song and shuffles what follows it;
// turning it off returns to the order of the context, from the current song on.
export function setShuffle(on) {
  if (on === shuffleOn) return;
  shuffleOn = on;
  if (items.length) {
    const kept = handQueued(), from = all.indexOf(items[index]);
    const rest = on
      ? spreadOrder(items.slice(index + 1).filter(e => !e.user))
      : (from >= 0 ? all.slice(from + 1) : items.slice(index + 1).filter(e => !e.user));
    items = [...items.slice(0, index + 1), ...kept, ...rest];
  }
  emit('player:mode', { shuffle: shuffleOn, repeat: repeatMode });
  applyAll();
  changed();
}

export function setRepeat(mode) {
  repeatMode = mode;
  emit('player:mode', { shuffle: shuffleOn, repeat: repeatMode });
  changed();
}
export const cycleRepeat = () => setRepeat({ off: 'all', all: 'one', one: 'off' }[repeatMode]);

// ---- Editing the queue ------------------------------------------------------------------------

// Every change of the list ends here.
function changed(refilled = false) {
  let run = true; // a hand-queued song that is no longer in the run right after the current one is part of the context
  for (let i = index + 1; i < items.length; i++) {
    if (!items[i].user) run = false; else if (!run) items[i].user = false;
  }
  emit('player:queue', { queue: items.slice(index + 1).map(e => e.track) });
  preload();
  sessionActions();
  saveSoon();
  if (!refilled) refill();
}

function reset() {
  items = [];
  all = [];
  index = -1;
  now = null;
  ended = false;
  slots.forEach(release);
  setPlaying(false);
  emit('player:track', null);
  emit('player:queue', { queue: [] });
  sessionTrack();
  save();
}

// Takes songs out of the queue; the current one, if it is among them, gives
// way to the next.
function drop(gone) {
  const set = new Set(gone);
  const before = items.slice(0, index).filter(e => set.has(e)).length;
  const removedCurrent = set.has(items[index]);
  items = items.filter(e => !set.has(e));
  all = all.filter(e => !set.has(e));
  index -= before;
  if (!removedCurrent) { changed(); return; }
  if (!items.length) { reset(); return; }
  goTo(Math.min(index, items.length - 1), { play: playing });
}

function insert(tracks, at) {
  const context = items[index]?.context ?? null;
  const added = tracks.map(track => entry(track, context, true));
  if (index < 0) {
    items = added;
    goTo(0, { play: false });
  } else {
    items.splice(index + 1 + at, 0, ...added);
    changed();
  }
  return () => drop(added);
}

export const playNext = tracks => insert(tracks, 0);
export const addToQueue = tracks => insert(tracks, queue().user.length);

// Puts a removed song back where it was, counted from the current one.
function restoreEntries(removed, offset) {
  const at = Math.max(index + 1, index + offset);
  items.splice(at, 0, ...removed);
  for (const e of removed) if (!e.user) all.push(e);
  all.sort((a, b) => a.seq - b.seq);
  changed();
}

export function removeFromQueue(of) {
  const offset = items.indexOf(of) - index;
  if (offset <= 0) return () => {};
  drop([of]);
  return () => restoreEntries([of], offset);
}

// Clear empties "Next in queue" only (proposal 7.8).
export function clearQueue() {
  const run = queue().user;
  if (!run.length) return () => {};
  drop(run);
  return () => restoreEntries(run, 1);
}

// Moves a song of the queue before the one now at `to` (counted among the
// songs after the current one; `to` = their number puts it last). Dropped
// inside the hand-queued run it is part of it.
export function moveInQueue(of, to) {
  const after = items.slice(index + 1);
  const from = after.indexOf(of);
  if (from < 0) return;
  const run = queue().user.length - (of.user ? 1 : 0);
  after.splice(from, 1);
  const slot = clamp(to > from ? to - 1 : to, 0, after.length);
  of.user = slot < run || (slot === run && of.user);
  after.splice(slot, 0, of);
  items = [...items.slice(0, index + 1), ...after];
  changed();
}

// The Library has more songs than are loaded: when the queue runs low it
// asks for the next ones, in order or at random.
async function refill() {
  if (!more || refilling || queue().rest.length >= 10 || !(shuffleOn || more.after)) return;
  const { sort, order, after } = more;
  refilling = (async () => {
    try {
      let tracks;
      if (shuffleOn) {
        tracks = (await api.get('/tracks/random', { limit: 50 })).tracks;
      } else {
        const page = await api.get('/tracks', { sort, order, limit: 50, after });
        more.after = page.next;
        tracks = page.tracks;
      }
      const have = new Set(items.map(e => e.track.id));
      const added = tracks.filter(track => !have.has(track.id)).map((track, i) => entry(track, items[index]?.context ?? null, false, all.length + i));
      all.push(...added);
      items.push(...(shuffleOn ? spreadOrder(added) : added));
      changed(true);
    } catch { /* the queue goes on with what it has; the next change asks again */ }
  })().finally(() => { refilling = null; });
}

// ---- Volume ------------------------------------------------------------------------------------

let savingVolume = 0;
function volumeChanged() {
  applyAll();
  emit('player:volume', { volume, muted });
  clearTimeout(savingVolume);
  savingVolume = setTimeout(() => write(VOLUME, { volume, muted }), 300);
}
export function setVolume(value) {
  volume = clamp(value, 0, 1);
  muted = false;
  volumeChanged();
}
export function setMuted(value) {
  muted = value;
  volumeChanged();
}
// With the slider at 0 the button gives some sound back.
export function toggleMute() {
  if (volume === 0) setVolume(.3); else setMuted(!muted);
}

// ---- The rest of the world: Media Session (proposal 7.7) -------------------------------------------

const media = 'mediaSession' in navigator ? navigator.mediaSession : null;

function handler(action, run) {
  try { media.setActionHandler(action, run); } catch { /* this browser has no such action */ }
}

// Title, artist, album and the cover at the two sizes the page already uses.
function sessionTrack() {
  if (!media) return;
  const track = items[index]?.track;
  media.metadata = track ? new MediaMetadata({
    title: track.title,
    artist: track.artist,
    album: track.album.title,
    artwork: track.album.cover ? [256, 640].map(size => ({ src: coverUrl(track.album.cover, size), sizes: `${size}x${size}`, type: 'image/jpeg' })) : [],
  }) : null;
  positionState();
}

// At the end of the queue the system's Next goes away.
function sessionActions() {
  if (!media) return;
  handler('nexttrack', canNext() ? next : null);
}

function positionState() {
  if (!media || index < 0) return;
  try { media.setPositionState({ duration: length() / 1000, playbackRate: 1, position: Math.min(position(), length()) / 1000 }); } catch { /* the duration is not known yet */ }
}

function setupSession() {
  if (!media) return;
  handler('play', resume);
  handler('pause', pause);
  handler('previoustrack', previous);
  handler('stop', () => { pause(); seek(0); });
  handler('seekto', details => seek(details.seekTime * 1000));
  handler('seekbackward', details => seek(position() - (details.seekOffset || 10) * 1000));
  handler('seekforward', details => seek(position() + (details.seekOffset || 10) * 1000));
  sessionActions();
}

// ---- A reload brings everything back, paused ----------------------------------------------------------

// The queue, the song and the second it was at, the modes and the volume.
// The current song is asked for once more: it may be gone, and its heart may
// have changed on another device.
export async function restore() {
  const level = read(VOLUME);
  if (level) { volume = clamp(+level.volume || 0, 0, 1); muted = !!level.muted; }
  emit('player:volume', { volume, muted });
  setupSession();
  const saved = read(KEY);
  if (!saved?.items?.length || !saved.items[saved.index]) return;
  items = saved.items;
  index = clamp(saved.index, 0, items.length - 1);
  shuffleOn = !!saved.shuffle;
  repeatMode = ['all', 'one'].includes(saved.repeat) ? saved.repeat : 'off';
  more = saved.more ?? null;
  all = items.filter(e => !e.user).sort((a, b) => a.seq - b.seq);
  const track = items[index].track;
  try {
    Object.assign(track, await api.get(`/tracks/${track.id}`));
  } catch (error) {
    if (error.status === 404) { items = []; index = -1; return; }
  }
  const at = read(POSITION);
  emit('player:mode', { shuffle: shuffleOn, repeat: repeatMode });
  goTo(index, { play: false, position: at?.id === track.id ? at.ms : 0 });
  if (saved.ended) { ended = true; emit('player:state', { playing, ended }); }
}

// A heart pressed anywhere: the copies the queue holds follow.
document.addEventListener('track:favorite', ({ detail: { track } }) => {
  for (const e of items) if (e.track.id === track.id) e.track.favorite = track.favorite;
});
