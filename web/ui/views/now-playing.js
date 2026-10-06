// Now playing: the song on its own cover, its lyrics following the music,
// and what plays next. The page lies on the blurred cover and is always dark
// (app.css §17); the sidebar and the bar are out of the way (the controls
// are here, bound like the bar's).
import * as player from '../player.js';
import * as act from '../actions.js';
import { api } from '../api.js';
import { $, clone, emptyState, openMenu, setCover, setIcon, setTitle, show } from '../ui.js';
import { navigate } from '../router.js';
import { bindTransport, contextLink, lastPage } from '../bar.js';
import { mountQueue } from '../queue.js';

const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
const remembered = key => { try { return localStorage.getItem(key) !== 'off'; } catch { return true; } };
const remember = (key, on) => { try { localStorage.setItem(key, on ? 'on' : 'off'); } catch { /* not remembered */ } };

export async function render(main, params, signal) {
  setTitle('Now playing');
  main.dataset.glow = 'now';
  await player.restored(); // a reload on this page: the song comes back from the browser first
  if (signal.aborted) return;
  if (!player.current()) {
    main.append(emptyState({ icon: 'queue', title: 'Nothing is playing', text: 'Choose a song and it shows here, with its lyrics.', link: { href: '/', label: 'Go to Library' } }));
    return;
  }
  const view = clone('t-now');
  main.append(view);
  const body = $('.now-body', view);
  const cover = $('.now-cover', view);
  const list = $('.ly-list', view), column = $('.now-lyr', view), pill = $('.now-pill', view), note = $('.ly-note', view);
  const lyricsButton = $('[data-act="lyrics"]', view), queueButton = $('[data-act="queue"]', view), screenButton = $('[data-act="fullscreen"]', view);

  // ---- What the person chose to see ----

  let showLyrics = remembered('vibrance.now.lyrics'), showQueue = remembered('vibrance.now.queue');
  let hasLyrics = false;

  function layout() {
    const lyrics = showLyrics && hasLyrics;
    show(column, lyrics);
    body.classList.toggle('no-lyrics', !lyrics);
    body.classList.toggle('no-queue', !showQueue);
    show($('.now-queue', view), showQueue);
    lyricsButton.classList.toggle('is-shown', lyrics);
    lyricsButton.setAttribute('aria-label', !hasLyrics ? 'No lyrics for this song' : showLyrics ? 'Hide lyrics' : 'Show lyrics');
    lyricsButton.title = hasLyrics ? '' : 'No lyrics for this song';
    lyricsButton.setAttribute('aria-disabled', String(!hasLyrics));
    queueButton.classList.toggle('is-shown', showQueue);
    queueButton.setAttribute('aria-label', showQueue ? 'Hide queue' : 'Show queue');
    if (lyrics) follow(true);
  }

  const toggles = {
    lyrics: () => { if (!hasLyrics) return; showLyrics = !showLyrics; remember('vibrance.now.lyrics', showLyrics); layout(); },
    queue: () => { showQueue = !showQueue; remember('vibrance.now.queue', showQueue); layout(); },
    fullscreen: () => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen?.()),
  };
  bindTransport(view, toggles, signal);
  document.addEventListener('now:toggle', event => toggles[event.detail]?.(), { signal });
  // Registered after the transport's own, so this has the last word on the lyrics button.
  document.addEventListener('player:track', layout, { signal });
  document.addEventListener('fullscreenchange', () => {
    const full = !!document.fullscreenElement;
    setIcon($('.i', screenButton), full ? 'collapse' : 'expand');
    screenButton.setAttribute('aria-label', full ? 'Leave the whole screen' : 'Use the whole screen');
  }, { signal });
  signal.addEventListener('abort', () => { if (document.fullscreenElement) document.exitFullscreen().catch(() => {}); });

  mountQueue($('.now-queue', view), 'now', { signal });

  $('.now-close', view).addEventListener('click', () => navigate(lastPage()));
  const dots = $('.now-dots', view);
  dots.addEventListener('click', () => {
    const track = player.current()?.track;
    if (track) openMenu(dots, act.trackMenu([track]));
  });

  // ---- The song ----

  let album = null;
  function showSong() {
    const now = player.current();
    if (!now) return;
    const { track, context } = now;
    setTitle(track.title);
    cover.alt = `Cover of ${track.album.title}`;
    if (album !== track.album.id) {
      album = track.album.id;
      // The cover is also the colour field: set once it has decoded, with the same URL (one download).
      setCover(cover, track.album.cover, 640);
      main.style.removeProperty('--cover');
      if (track.album.cover) cover.decode().then(() => { if (!signal.aborted && album === track.album.id) main.style.setProperty('--cover', `url("${cover.currentSrc}")`); }).catch(() => {});
    }
    $('.now-title', view).textContent = track.title;
    const artist = $('.now-artist', view);
    artist.textContent = track.artist;
    if (track.artist === track.album.artist.name) artist.href = `/artists/${track.album.artist.id}`; else artist.removeAttribute('href');
    const link = $('.now-album a', view);
    link.textContent = track.album.title;
    link.href = `/albums/${track.album.id}`;
    $('.now-year', view).textContent = track.album.year ? ` · ${track.album.year}` : '';
    const from = contextLink(context), box = $('.now-from', view);
    show(box, !!from);
    if (from) {
      $('.cap', box).textContent = from.label;
      $('a', box).textContent = from.name;
      $('a', box).href = from.href;
    }
    badges(track);
  }

  // FLAC, 24-bit / 96 kHz, the lyrics, and whether the volume is being levelled.
  function badges(track) {
    const parts = act.formatParts(track.format);
    const texts = [parts.codec, [track.format.bit_depth && `${track.format.bit_depth}-bit`, parts.rate].filter(Boolean).join(' / ')];
    if (!track.format.bit_depth) texts[1] = [parts.depth, parts.rate].filter(Boolean).join(' / ');
    if (!track.has_lyrics) texts.push('No lyrics'); else if (lyrics) texts.push(lyrics.synced ? 'Synced lyrics' : 'Lyrics');
    if (player.leveling()) texts.push('Volume leveled');
    const box = $('.now-badges', view);
    box.replaceChildren(...texts.map(text => Object.assign(document.createElement('span'), { textContent: text })));
  }

  // ---- The lyrics ----

  let lyrics = null, lines = [], times = [], current = -1, following = true, loading = 0;

  async function load() {
    const track = player.current()?.track;
    const token = ++loading;
    if (!track) return;
    lyrics = null;
    hasLyrics = !!track.has_lyrics;
    layout();
    list.replaceChildren();
    lines = [];
    times = [];
    current = -1;
    if (!hasLyrics) { badges(track); return; }
    try {
      const data = await api.get(`/tracks/${track.id}/lyrics`, null, { signal });
      if (token !== loading) return;
      lyrics = data;
    } catch (error) {
      if (token !== loading || error.name === 'AbortError') return;
      hasLyrics = false; // the song says it has lyrics and the server has none: the column goes away
    }
    paint(track);
  }

  // Synced lines are buttons (select one to jump to it), one tab stop for the
  // column; unsynced lyrics are text. A blank line is a pause or a gap.
  function paint(track) {
    badges(track);
    layout();
    if (!lyrics) return;
    const synced = lyrics.synced;
    const out = document.createDocumentFragment();
    for (const line of lyrics.lines) {
      const el = document.createElement(synced && line.text ? 'button' : 'p');
      if (!line.text) el.className = 'ly-gap'; else { el.className = 'ly'; el.textContent = line.text; }
      if (el.tagName === 'BUTTON') { el.type = 'button'; el.tabIndex = -1; }
      if (!line.text) el.setAttribute('aria-hidden', 'true');
      lines.push(el);
      times.push(line.time_ms);
      out.append(el);
    }
    list.classList.toggle('is-plain', !synced);
    list.append(out);
    note.textContent = synced ? 'Select a line to jump to it.' : 'These lyrics aren’t synced.';
    following = true;
    show(pill, false);
    if (synced) { lines.find(el => el.tagName === 'BUTTON').tabIndex = 0; lightLine(player.position()); }
  }

  // The last line that has begun (a binary search; a moment ahead so the line is lit as it is sung).
  function lineAt(ms) {
    let low = 0, high = times.length - 1, found = -1;
    while (low <= high) {
      const middle = (low + high) >> 1;
      if (times[middle] <= ms) { found = middle; low = middle + 1; } else high = middle - 1;
    }
    return found;
  }

  function lightLine(position) {
    if (!lyrics?.synced) return;
    const at = lineAt(position + 150);
    if (at === current) return;
    lines[current]?.classList.remove('is-current');
    lines[current]?.removeAttribute('aria-current');
    lines[at]?.classList.add('is-current');
    lines[at]?.setAttribute('aria-current', 'true');
    current = at;
    if (following) follow(false);
  }

  // The current line is kept a third of the way down the column.
  function follow(instant) {
    const el = lines[current];
    if (!el || column.hidden) return;
    list.scrollTo({ top: el.offsetTop - list.clientHeight * .35 + el.offsetHeight / 2, behavior: instant || reduced() ? 'instant' : 'smooth' });
  }

  // A scroll by the person (wheel, touch, keys) stops the following and
  // offers the way back; the scrolling the page does itself does not.
  const unfollow = () => {
    if (!following || current < 0 || !lyrics?.synced) return;
    following = false;
    show(pill, true);
  };
  list.addEventListener('wheel', unfollow, { passive: true, signal });
  list.addEventListener('touchmove', unfollow, { passive: true, signal });
  list.addEventListener('keydown', event => {
    const buttons = lines.filter(el => el.tagName === 'BUTTON'), at = buttons.indexOf(event.target);
    if (at < 0 || event.altKey || event.ctrlKey || event.metaKey) return;
    const go = to => { event.preventDefault(); buttons[Math.min(Math.max(to, 0), buttons.length - 1)].focus(); unfollow(); };
    if (event.key === 'ArrowDown') go(at + 1); else if (event.key === 'ArrowUp') go(at - 1);
    else if (event.key === 'Home') go(0); else if (event.key === 'End') go(buttons.length - 1);
  }, { signal });
  list.addEventListener('focusin', event => {
    const roving = lines.find(el => el.tabIndex === 0);
    if (roving && event.target.tabIndex === -1 && event.target.tagName === 'BUTTON') { roving.tabIndex = -1; event.target.tabIndex = 0; }
  }, { signal });
  list.addEventListener('click', event => {
    const at = lines.indexOf(event.target.closest('button'));
    if (at < 0 || times[at] == null) return;
    following = true;
    show(pill, false);
    player.seek(times[at]);
  }, { signal });
  pill.addEventListener('click', () => { following = true; show(pill, false); follow(false); });

  document.addEventListener('player:time', event => lightLine(event.detail.position), { signal });
  document.addEventListener('player:mode', () => { const track = player.current()?.track; if (track) badges(track); }, { signal });
  document.addEventListener('settings:changed', () => { const track = player.current()?.track; if (track) badges(track); }, { signal });

  let shown = null;
  const change = () => {
    const track = player.current()?.track;
    showSong();
    if (track && track.id !== shown) { shown = track.id; load(); }
  };
  document.addEventListener('player:track', change, { signal });
  change();
}
