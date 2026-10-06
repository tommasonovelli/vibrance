// The controls of the player, in the bar below the page and again on Now
// playing: one binding serves both, by the data-act and data-slider
// attributes of their markup. The bar itself (cover, names, links) is
// initBar(). Sliders are role="slider" elements whose value is the CSS
// custom property --p (0 to 1); the line and the knob move by transform.
import * as player from './player.js';
import * as act from './actions.js';
import { navigate } from './router.js';
import { setPlayingFrom } from './sidebar.js';
import { toggleQueue } from './queue.js';
import { $, formatTime, setCover, setIcon, show } from './ui.js';

const clamp = (value, low, high) => Math.min(Math.max(value, low), high);

// Now playing takes the whole window (app.css §17: the sidebar and the bar give
// way); Close goes back to the page that was before it.
let back = '/';
document.addEventListener('route', ({ detail }) => {
  const now = detail.path === '/now-playing';
  document.body.toggleAttribute('data-now', now);
  if (!now) back = detail.path + (detail.query.size ? `?${detail.query}` : '');
});
export const lastPage = () => back;

// "Playing from playlist Sunday morning": where the music comes from, and the page to go there.
const FROM = {
  album: ['album', c => `/albums/${c.id}`],
  artist: ['artist', c => `/artists/${c.id}`],
  playlist: ['playlist', c => `/playlists/${c.id}`],
  favorites: ['', () => '/favorites'],
  library: ['', () => '/'],
  search: ['search', c => `/search?q=${encodeURIComponent(c.name)}`],
};
export function contextLink(context) {
  const kind = FROM[context?.type];
  if (!kind) return null;
  return { label: kind[0] ? `Playing from ${kind[0]}` : 'Playing from', name: context.name, href: kind[1](context) };
}

// ---- Sliders -------------------------------------------------------------------------

// `spec` = { max(), now(), set(value, final), step, big, live, text(value), tip }.
// Pointer: dragged with the pointer captured; a seek is made when the pointer
// is released (a drag over a stream is not a hundred requests), a volume
// follows it. Keys, as an ARIA slider: arrows, Page Up and Down, Home, End.
function slider(el, spec, signal) {
  let drag = false;
  const at = event => {
    const box = el.getBoundingClientRect();
    return clamp((event.clientX - box.left) / box.width, 0, 1);
  };
  const enabled = () => el.getAttribute('aria-disabled') !== 'true';
  const move = event => {
    const value = at(event) * spec.max();
    el.style.setProperty('--p', at(event));
    spec.preview?.(value);
    if (spec.live) spec.set(value, false);
  };
  el.addEventListener('pointerdown', event => {
    if (event.button !== 0 || !enabled()) return;
    el.setPointerCapture(event.pointerId);
    drag = true;
    move(event);
  }, { signal });
  el.addEventListener('pointermove', event => {
    if (drag) move(event);
    if (spec.tip && enabled()) {
      el.style.setProperty('--at', `${at(event) * el.clientWidth}px`);
      $('.tip', el).textContent = formatTime(at(event) * spec.max());
    }
  }, { signal });
  const release = event => {
    if (!drag) return;
    drag = false;
    spec.set(at(event) * spec.max(), true);
    spec.release?.();
  };
  el.addEventListener('pointerup', release, { signal });
  el.addEventListener('pointercancel', () => { drag = false; spec.release?.(); }, { signal });
  el.addEventListener('keydown', event => {
    if (event.ctrlKey || event.metaKey || event.altKey || !enabled()) return;
    const now = spec.now(), max = spec.max();
    const to = { ArrowRight: now + spec.step, ArrowUp: now + spec.step, ArrowLeft: now - spec.step, ArrowDown: now - spec.step, PageUp: now + spec.big, PageDown: now - spec.big, Home: 0, End: max }[event.key];
    if (to === undefined) return;
    event.preventDefault();
    spec.set(clamp(to, 0, max), true);
  }, { signal });
  return { get dragging() { return drag; } };
}

// ---- The controls --------------------------------------------------------------------------

// Wires every [data-act] button, [data-slider] and [data-time] inside `root`
// to the player, and keeps them in step with it. `on` gives what only the
// owner knows how to do: lyrics, queue, fullscreen.
export function bindTransport(root, on = {}, signal) {
  const control = name => $(`[data-act="${name}"]`, root);
  const seek = $('[data-slider="seek"]', root), vol = $('[data-slider="volume"]', root);
  const labels = { now: $('[data-time="now"]', root), total: $('[data-time="total"]', root) };

  const heart = () => {
    const button = control('heart');
    const track = player.current()?.track;
    if (!button || !track) return;
    button.classList.toggle('is-fav', track.favorite);
    button.setAttribute('aria-pressed', String(track.favorite));
    button.setAttribute('aria-label', track.favorite ? 'Remove from favorites' : 'Add to favorites');
  };

  const clicks = {
    shuffle: () => player.setShuffle(!player.isShuffled()),
    previous: () => player.previous(),
    play: () => player.toggle(),
    next: () => player.next(),
    repeat: () => player.cycleRepeat(),
    mute: () => player.toggleMute(),
    heart: () => { const track = player.current()?.track; if (track) act.setFavorites([track], !track.favorite); },
    lyrics: () => on.lyrics?.(),
    queue: () => on.queue?.(),
    fullscreen: () => on.fullscreen?.(),
  };
  root.addEventListener('click', event => {
    const button = event.target.closest('[data-act]');
    if (button && !button.disabled && button.getAttribute('aria-disabled') !== 'true') clicks[button.dataset.act]?.();
  }, { signal });

  // The seek slider's value is the time; a drag shows the time it would seek to, and time events wait until it is let go.
  let seeking = null;
  const length = () => player.length();
  const showTime = ({ position, duration } = { position: player.position(), duration: player.length() }) => {
    if (seeking !== null) return;
    const p = duration > 0 ? clamp(position / duration, 0, 1) : 0;
    root.style.setProperty('--p', p);
    seek.style.setProperty('--p', p);
    labels.now.textContent = formatTime(position);
    const total = formatTime(duration);
    if (labels.total.textContent !== total) { // the file may last a little otherwise than the tag says
      labels.total.textContent = total;
      seek.setAttribute('aria-valuemax', Math.round(duration / 1000));
    }
    seek.setAttribute('aria-valuenow', Math.round(position / 1000));
    seek.setAttribute('aria-valuetext', `${formatTime(position)} of ${formatTime(duration)}`);
  };
  if (seek) {
    slider(seek, {
      max: length, now: player.position, step: 5000, big: 30000, tip: true,
      set: (value, final) => { if (final) { seeking = null; player.seek(value); showTime(); } },
      preview: value => { seeking = value; labels.now.textContent = formatTime(value); },
      release: () => { seeking = null; },
    }, signal);
  }

  const showVolume = () => {
    const level = player.isMuted() ? 0 : player.getVolume();
    vol.style.setProperty('--p', level);
    vol.setAttribute('aria-valuenow', Math.round(level * 100));
    vol.setAttribute('aria-valuetext', `${Math.round(level * 100)}%`);
    const mute = control('mute');
    setIcon($('.i', mute), level === 0 ? 'volume-off' : 'volume');
    mute.setAttribute('aria-label', level === 0 ? 'Unmute' : 'Mute');
  };
  if (vol) {
    slider(vol, {
      max: () => 1, now: () => (player.isMuted() ? 0 : player.getVolume()), step: .05, big: .1, live: true,
      set: value => player.setVolume(value),
    }, signal);
    document.addEventListener('player:volume', showVolume, { signal });
    showVolume();
  }

  const showTrack = () => {
    const now = player.current();
    const track = now?.track;
    for (const name of ['previous', 'play']) control(name).disabled = !track;
    seek.setAttribute('aria-disabled', String(!track));
    seek.tabIndex = track ? 0 : -1;
    const lyrics = control('lyrics');
    if (lyrics) {
      lyrics.disabled = !track;
      lyrics.setAttribute('aria-disabled', String(!!track && !track.has_lyrics));
      lyrics.title = track && !track.has_lyrics ? 'No lyrics for this song' : '';
    }
    heart();
    showState();
    showTime();
  };

  const showState = () => {
    const track = player.current()?.track;
    root.dataset.state = !track ? 'idle' : player.isPlaying() ? 'playing' : 'paused';
    control('play').setAttribute('aria-label', player.isPlaying() ? 'Pause' : 'Play');
    const next = control('next');
    next.disabled = !track;
    next.setAttribute('aria-disabled', String(!!track && !player.canNext()));
  };

  const showMode = () => {
    control('shuffle').setAttribute('aria-pressed', String(player.isShuffled()));
    const mode = player.getRepeat(), repeat = control('repeat');
    repeat.setAttribute('aria-pressed', String(mode !== 'off'));
    repeat.setAttribute('aria-label', { off: 'Repeat off', all: 'Repeat all', one: 'Repeat one' }[mode]);
    show($('.one', repeat), mode === 'one');
  };

  const events = { 'player:track': showTrack, 'player:state': showState, 'player:time': event => showTime(event.detail), 'player:mode': showMode, 'player:queue': showState, 'track:favorite': heart };
  for (const [name, run] of Object.entries(events)) document.addEventListener(name, run, { signal });
  showMode();
  showTrack();
}

// ---- The bar ---------------------------------------------------------------------------------

// The cover, the title and the artist of the current song, each a way to its page.
function showSong() {
  const now = player.current();
  const cover = $('#np-cover'), title = $('#np-title'), artist = $('#np-artist');
  if (!now) {
    cover.hidden = true;
    title.textContent = 'Nothing playing';
    title.removeAttribute('href');
    artist.hidden = true;
    $('#np-heart').hidden = true;
    return;
  }
  const { track, context } = now;
  cover.hidden = false;
  if (cover.dataset.id !== track.album.id) { setCover(cover, track.album.cover, 256); cover.dataset.id = track.album.id; }
  title.textContent = track.title;
  title.href = `/albums/${track.album.id}`;
  artist.hidden = false;
  artist.textContent = track.artist;
  // The artist of a song is free text: a link only when it is the album's artist (proposal 4.2).
  if (track.artist === track.album.artist.name) artist.href = `/artists/${track.album.artist.id}`; else artist.removeAttribute('href');
  $('#np-heart').hidden = false;
  setPlayingFrom(context?.type === 'playlist' ? context.id : null);
}

export function initBar() {
  const bar = $('#player');
  const queueButton = $('#btn-queue');
  bindTransport(bar, {
    lyrics: () => navigate('/now-playing'),
    queue: toggleQueue,
  });
  document.addEventListener('player:track', showSong);
  document.addEventListener('queue:panel', ({ detail }) => queueButton.setAttribute('aria-pressed', String(detail.open)));
  queueButton.setAttribute('aria-pressed', String(document.documentElement.dataset.queue === 'open'));
  showSong();
}
