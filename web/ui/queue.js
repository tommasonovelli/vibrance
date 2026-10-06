// The queue as the person sees it: a side panel beside the page that opens
// from the bar's Queue button and remembers it, and the same list as "Up
// next" on Now playing (mountQueue serves both). Now playing, then what was
// queued by hand, then the rest of the context. A song is dragged by its grip
// or moved with Alt and the arrows, removed with its ×; every removal says
// Undo. At the end of the context the music stops, and the list says so.
import * as player from './player.js';
import { api, coverUrl } from './api.js';
import { $, clone, emptyState, formatLength, formatTime, setCover, show, toast } from './ui.js';
import { newPlaylist, reload as reloadSidebar } from './sidebar.js';

const root = document.documentElement;
const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
const touch = () => matchMedia('(hover: none)').matches; // no double-click: a tap plays
const clamp = (value, length) => Math.min(Math.max(value, 0), length - 1);

// ---- The panel beside the page --------------------------------------------------------------

export const isQueueOpen = () => root.dataset.queue === 'open';

export function toggleQueue(open = !isQueueOpen()) {
  if (open) root.dataset.queue = 'open'; else delete root.dataset.queue;
  try { localStorage.setItem('vibrance.queue', open ? 'open' : 'closed'); } catch { /* not remembered */ }
  if (open) panel?.refresh();
  document.dispatchEvent(new CustomEvent('queue:panel', { detail: { open } }));
}

let panel = null;
export function initQueue() {
  panel = mountQueue($('#queue'), 'panel', { visible: isQueueOpen });
  $('#queue .q-close').addEventListener('click', () => { toggleQueue(false); $('#btn-queue').focus(); });
}

// ---- The list --------------------------------------------------------------------------------

// Fills `host` with the queue and keeps it up to date (redrawn once a frame
// at most, and only while it can be seen). `variant` is 'panel' or 'now'.
export function mountQueue(host, variant, { signal, visible = () => true } = {}) {
  host.replaceChildren(clone('t-queue'));
  const q = host.firstElementChild;
  const body = $('.q-body', q), drop = $('.q-drop', q);
  $('.q-title', q).textContent = variant === 'now' ? 'Up next' : 'Queue';
  show($('.q-close', q), variant === 'panel');
  show($('.q-save', q), variant === 'now');
  $('.q-save', q).addEventListener('click', saveQueue);

  let dirty = true, frame = 0, focusEntry = null, active = null, shownEnded = false;

  function row(e, kind) {
    const li = clone('t-qi');
    const { track } = e;
    li.entry = e;
    setCover($('.qcov', li), track.album.cover, 256);
    $('.nm', li).textContent = track.title;
    $('.ar', li).textContent = track.artist;
    li.classList.toggle('is-now', kind === 'now');
    li.classList.toggle('is-off', track.available === false);
    const x = $('.x', li);
    if (kind === 'now') {
      $('.tm', li).textContent = formatTime(track.duration_ms);
      x.remove();
    } else {
      $('.tm', li).remove();
      li.dataset.move = '';
      x.setAttribute('aria-label', `Remove ${track.title} from the queue`);
    }
    li.tabIndex = -1;
    if (x.isConnected) x.tabIndex = -1;
    return li;
  }

  const caption = (text, clear) => {
    const el = clone('t-qcap');
    $('span', el).textContent = text;
    const button = $('.link', el);
    show(button, !!clear);
    if (clear) button.addEventListener('click', clearNext);
    return el;
  };
  const list = rows => {
    const el = document.createElement('div');
    el.className = 'q-list';
    el.setAttribute('role', 'list');
    el.append(...rows);
    return el;
  };

  // What the queue says about its end and about what it skips.
  function notes({ now, user, rest }) {
    const el = clone('t-q-end');
    const lines = [];
    const gone = [...user, ...rest].filter(e => e.track.available === false);
    if (gone.length === 1) lines.push(`${gone[0].track.title} is skipped: it isn’t in the library right now.`);
    else if (gone.length) lines.push(`${gone.length} songs are skipped: they aren’t in the library right now.`);
    const last = [now, ...user, ...rest].findLast(e => e.track.available !== false) ?? now;
    const repeat = player.getRepeat();
    lines.push(repeat === 'one' ? `${now.track.title} repeats.`
      : repeat === 'all' ? `After ${last.track.title}, the queue starts again.`
      : `After ${last.track.title}, the music stops.`);
    for (const text of lines) {
      const p = document.createElement('p');
      p.className = 'note';
      p.textContent = text;
      $('.q-notes', el).append(p);
    }
    show($('.q-save', el), variant === 'panel');
    $('.q-save', el).addEventListener('click', saveQueue);
    return el;
  }

  function ended(state) {
    const el = clone('t-q-ended');
    const context = state.now.context, tracks = player.contextTracks();
    $('h3', el).textContent = context?.name ? `End of ${context.name}` : 'End of the queue';
    const length = tracks.reduce((sum, track) => sum + (track.duration_ms || 0), 0);
    $('.q-end-text', el).textContent = `${plural(tracks.length, 'song')}, ${formatLength(length)}. The music stops here.`;
    $('.q-again', el).addEventListener('click', () => player.replay(false));
    const shuffle = $('.q-shuffle', el);
    $('span', shuffle).textContent = context?.name ? `Shuffle ${context.name}` : 'Shuffle';
    shuffle.addEventListener('click', () => player.replay(true));
    return el;
  }

  function render() {
    dirty = false;
    const state = player.queue();
    const { now, user, rest } = state;
    const out = document.createDocumentFragment();
    if (!now) {
      out.append(emptyState({ icon: 'queue', title: 'The queue is empty', text: 'Songs you play, play next or add to the queue show here.' }));
    } else if (state.ended) {
      out.append(ended(state));
    } else {
      out.append(caption('Now playing'), list([row(now, 'now')]));
      if (user.length) out.append(caption('Next in queue', true), list(user.map(e => row(e, 'next'))));
      if (rest.length) out.append(caption(`Next from ${rest[0].context?.name ?? now.context?.name ?? 'the queue'}`), list(rest.map(e => row(e, 'next'))));
      out.append(notes(state));
    }
    body.replaceChildren(out);
    const rows = [...body.querySelectorAll('.qi')];
    // One row is the tab stop; the arrows go from row to row.
    active = rows.find(li => li.entry === focusEntry) ?? rows[0] ?? null;
    if (active) { active.tabIndex = 0; $('.x', active)?.removeAttribute('tabindex'); }
    if (focusEntry && active?.entry === focusEntry) active.focus();
    focusEntry = null;
    shownEnded = state.ended;
  }

  function schedule() {
    dirty = true;
    if (!frame && visible()) frame = requestAnimationFrame(() => { frame = 0; render(); });
  }
  for (const name of ['player:track', 'player:queue', 'player:mode']) document.addEventListener(name, schedule, { signal });
  // Play and pause do not change the list; only the end of the queue does.
  document.addEventListener('player:state', () => { if (player.hasEnded() !== shownEnded) schedule(); }, { signal });
  if (visible()) render();

  // ---- Doing things ----

  function remove(e, li) {
    const rows = [...body.querySelectorAll('.qi[data-move]')];
    const near = rows[rows.indexOf(li) + 1] ?? rows[rows.indexOf(li) - 1];
    focusEntry = near?.entry ?? null;
    const undo = player.removeFromQueue(e);
    toast({ title: 'Removed from queue', sub: `${e.track.title} · ${e.track.artist}`, cover: coverUrl(e.track.album.cover, 256), badge: 'queue', undo });
  }

  function clearNext() {
    const n = player.queue().user.length;
    toast({ title: 'Cleared Next in queue', sub: plural(n, 'song'), badge: 'queue', undo: player.clearQueue() });
  }

  const jump = li => { if (li?.entry && li.dataset.move !== undefined) player.jumpTo(li.entry); };

  body.addEventListener('click', event => {
    const li = event.target.closest('.qi');
    if (!li) return;
    if (event.target.closest('.x')) remove(li.entry, li);
    else if (touch() && !event.target.closest('.grip')) jump(li);
  });
  body.addEventListener('dblclick', event => { if (!event.target.closest('button')) jump(event.target.closest('.qi')); });
  body.addEventListener('focusin', event => {
    const li = event.target.closest('.qi');
    if (!li || li === active) return;
    if (active) { active.tabIndex = -1; $('.x', active)?.setAttribute('tabindex', '-1'); }
    active = li;
    li.tabIndex = 0;
    $('.x', li)?.removeAttribute('tabindex');
  });
  body.addEventListener('keydown', event => {
    const li = event.target.closest('.qi');
    if (!li || event.target.closest('.x')) return;
    const rows = [...body.querySelectorAll('.qi')], at = rows.indexOf(li);
    const movable = [...body.querySelectorAll('.qi[data-move]')], from = movable.indexOf(li);
    const go = to => { event.preventDefault(); rows[clamp(to, rows.length)]?.focus(); };
    if (event.altKey && (event.key === 'ArrowUp' || event.key === 'ArrowDown') && from >= 0) {
      event.preventDefault();
      focusEntry = li.entry;
      player.moveInQueue(li.entry, event.key === 'ArrowUp' ? from - 1 : from + 2);
      return;
    }
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    switch (event.key) {
      case 'ArrowDown': go(at + 1); break;
      case 'ArrowUp': go(at - 1); break;
      case 'Home': go(0); break;
      case 'End': go(rows.length - 1); break;
      case 'Enter': event.preventDefault(); jump(li); break;
      case 'Delete': case 'Backspace': if (from >= 0) { event.preventDefault(); remove(li.entry, li); } break;
    }
  });

  // Drag by the grip: the row is picked up, a line shows where it will land,
  // and the list scrolls when the pointer nears its edge.
  body.addEventListener('pointerdown', event => {
    const grip = event.target.closest('.grip');
    const li = grip?.closest('.qi');
    if (!li || event.button !== 0 || li.dataset.move === undefined) return;
    event.preventDefault();
    const rows = [...body.querySelectorAll('.qi[data-move]')];
    const from = rows.indexOf(li), top = () => q.getBoundingClientRect().top;
    let slot = from, y = event.clientY;
    const stop = new AbortController();
    grip.setPointerCapture(event.pointerId);
    li.classList.add('is-drag');
    show(drop, true);
    const place = () => {
      slot = rows.findIndex(r => { const box = r.getBoundingClientRect(); return y < box.top + box.height / 2; });
      if (slot < 0) slot = rows.length;
      const edge = slot < rows.length ? rows[slot].getBoundingClientRect().top : rows[rows.length - 1].getBoundingClientRect().bottom;
      drop.style.setProperty('--y', `${edge - top()}px`);
    };
    const scroller = setInterval(() => {
      const box = host.getBoundingClientRect();
      if (y < box.top + 40) host.scrollTop -= 10; else if (y > box.bottom - 40) host.scrollTop += 10;
      place();
    }, 16);
    const end = move => {
      clearInterval(scroller);
      stop.abort();
      li.classList.remove('is-drag');
      show(drop, false);
      if (move && slot !== from && slot !== from + 1) { focusEntry = li.entry; player.moveInQueue(li.entry, slot); }
    };
    grip.addEventListener('pointermove', e => { y = e.clientY; place(); }, { signal: stop.signal });
    grip.addEventListener('pointerup', () => end(true), { signal: stop.signal });
    grip.addEventListener('pointercancel', () => end(false), { signal: stop.signal });
    document.addEventListener('keydown', e => { if (e.key === 'Escape') end(false); }, { signal: stop.signal });
    place();
  });

  return { refresh: () => dirty && schedule() };
}

// ---- Save queue as playlist ---------------------------------------------------------------------

// The New playlist sheet, with a name already in it; then the songs go in
// blocks of at most 1,000 (proposal 7.8). Songs that are not available are
// left out: the server would refuse them.
function saveQueue() {
  const { now, user, rest } = player.queue();
  if (!now) return;
  const tracks = [now, ...user, ...rest].map(e => e.track).filter(track => track.available !== false);
  const context = now.context;
  newPlaylist(async playlist => {
    try {
      for (let i = 0; i < tracks.length; i += 1000) {
        await api.post(`/playlists/${playlist.id}/items`, { track_ids: tracks.slice(i, i + 1000).map(track => track.id), position: null });
      }
      reloadSidebar();
      toast({ title: `Saved as ${playlist.name}`, sub: plural(tracks.length, 'song'), badge: 'add' });
    } catch (error) {
      toast({ title: `Couldn’t add the songs to ${playlist.name}`, sub: error.message, badge: 'alert', error: true });
    }
  }, { name: context?.name ? `${context.name} queue` : 'Queue' });
}
