// Small helpers every view shares: templates, durations, toasts, menus and
// sheets. The markup of each piece is a <template> in index.html; this file
// clones it and sets text, href and src, never innerHTML.
import { coverUrl } from './api.js';

export const $ = (selector, root = document) => root.querySelector(selector);

// A fresh copy of the one element a <template> holds.
export const clone = id => document.getElementById(id).content.firstElementChild.cloneNode(true);

// Shows or hides an element. `hidden` as a property exists on HTML elements
// only; the attribute works on the SVG icons too.
export const show = (element, visible) => element.toggleAttribute('hidden', !visible);

// Points the <use> of an icon element at another symbol of the sprite.
export const setIcon = (svg, name) => svg.querySelector('use').setAttribute('href', `#i-${name}`);

// ---- Durations -------------------------------------------------------------

const two = n => String(n).padStart(2, '0');

// A track time: 3:48, 1:02:03; an unknown one is a dash.
export function formatTime(ms) {
  if (ms == null || !Number.isFinite(ms)) return '–';
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60);
  return h ? `${h}:${two(m)}:${two(s % 60)}` : `${m}:${two(s % 60)}`;
}

// The length of an album or a playlist: 49 min, 1 h 32 min, 4 h 05 min.
export function formatLength(ms) {
  const min = Math.round((ms || 0) / 60000);
  if (min < 1) return ms > 0 ? '< 1 min' : '0 min';
  const h = Math.floor(min / 60), m = min % 60;
  if (!h) return `${m} min`;
  return m ? `${h} h ${two(m)} min` : `${h} h`;
}

// A day, as a column of "Added" shows it: Sep 28, with the year only when it is not this one.
const monthDay = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' });
const withYear = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' });
export function formatDate(iso) {
  const date = new Date(iso);
  return (date.getFullYear() === new Date().getFullYear() ? monthDay : withYear).format(date);
}

// How long ago: just now, 5 minutes ago, yesterday, 2 days ago, last month.
const relative = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
const UNITS = [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]];
export function formatAgo(iso) {
  const seconds = (Date.parse(iso) - Date.now()) / 1000;
  const unit = UNITS.find(([, size]) => -seconds >= size);
  return unit ? relative.format(Math.trunc(seconds / unit[1]), unit[0]) : 'just now';
}

// ---- Announcements ---------------------------------------------------------

// Said by screen readers, politely: a new page, a result. Emptied first so
// the same sentence twice is still read.
export function announce(text) {
  const live = $('#live');
  live.textContent = '';
  setTimeout(() => { live.textContent = text; }, 50);
}

// ---- Covers and waiting ----------------------------------------------------

// An album's cover in an <img>. Without one, a quiet disc on the block's
// own background (an image with no source shows the browser's broken icon).
export function setCover(img, cover, size = 256) {
  img.src = coverUrl(cover, size) || '/no-cover.svg';
}

// What stands in for a list that is loading: nothing during the first
// second, a quick answer needs no sign; then still blocks, no spinner and no
// shimmer (proposal 7.3). Returns what removes them.
export function pending(host, template) {
  let blocks;
  const timer = setTimeout(() => host.append(blocks = clone(template)), 1000);
  return () => { clearTimeout(timer); blocks?.remove(); };
}

// ---- Page head and states --------------------------------------------------

export function setTitle(title) {
  document.title = `${title} — Vibrance`;
}

// The head of a view: its title, and a place for the tools at its right.
export function pageHead(main, title) {
  const head = clone('t-page-head');
  $('.title', head).textContent = title;
  setTitle(title);
  main.append(head);
  return head;
}

// A centred state: "No favorites yet", "Page not found.".
// `link` goes elsewhere ({href, label}); `action` is the one filled button that
// fills the page ({label, run}).
export function emptyState({ icon = 'info', title, text = '', link = null, action = null }) {
  const el = clone('t-empty');
  setIcon($('.mark', el), icon);
  $('.empty-title', el).textContent = title;
  const note = $('.empty-text', el);
  note.textContent = text;
  note.hidden = !text;
  const a = $('.empty-link', el);
  if (link) {
    a.textContent = link.label;
    a.href = link.href;
    a.hidden = false;
  }
  if (action) {
    const button = $('.empty-action', el);
    button.textContent = action.label;
    button.addEventListener('click', action.run);
    button.hidden = false;
  }
  return el;
}

// One sentence where it happened, the answer of the server in a closed Details.
const sentences = {
  network: 'Can’t reach Vibrance. Check your connection and try again.',
  library_changing: 'The library is being updated. Try again in a moment.',
  not_ready: 'Vibrance is starting. Try again in a moment.',
};
// What to tell a person about an error: the sentence a view has for that code
// (`table`, by `code`), else the general one for it. Never the server's own
// words, which are for the Details.
export function sayError(error, table = {}) {
  return table[error.code] || sentences[error.code] || (error.status >= 500 ? 'Vibrance had a problem. Try again in a moment.' : 'This didn’t work.');
}

export function errorNotice(error, retry) {
  const el = clone('t-error');
  $('.notice-text', el).textContent = sayError(error);
  const lines = [error.request, error.status ? `${error.status} ${error.code}: ${error.message}` : 'Network error, no answer from the server'];
  $('pre', el).textContent = lines.filter(Boolean).join('\n');
  const button = $('.notice-retry', el);
  if (retry) button.addEventListener('click', retry); else button.hidden = true;
  return el;
}

// ---- Forms -----------------------------------------------------------------

// Says what is wrong beside the field it is about, or takes it away
// (`text` empty). `where` is the .field, or the error line itself.
export function fieldError(where, text = '') {
  const note = where.matches('.field-error') ? where : $('.field-error', where);
  note.textContent = text;
  show(note, !!text);
  const input = $('input', where);
  if (text) input?.setAttribute('aria-invalid', 'true'); else input?.removeAttribute('aria-invalid');
}

// A group of buttons with role="radio" (data-value): the checked one is in the
// tab order, the arrows move and check, as for a radio group. Returns what
// sets the value from outside; `change(value)` runs on a choice made here.
export function radioGroup(group, value, change) {
  const buttons = [...group.querySelectorAll('[role="radio"]')];
  const set = (next, focus = false) => {
    for (const button of buttons) {
      const on = button.dataset.value === next;
      button.setAttribute('aria-checked', String(on));
      button.tabIndex = on ? 0 : -1;
      if (on && focus) button.focus();
    }
  };
  group.addEventListener('click', event => {
    const button = event.target.closest('[role="radio"]');
    if (button && button.getAttribute('aria-checked') !== 'true') { set(button.dataset.value); change(button.dataset.value); }
  });
  group.addEventListener('keydown', event => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key];
    if (!step) return;
    event.preventDefault();
    const at = buttons.findIndex(button => button.getAttribute('aria-checked') === 'true');
    const next = buttons[(at + step + buttons.length) % buttons.length].dataset.value;
    set(next, true);
    change(next);
  });
  set(value);
  return set;
}

// ---- Toasts ----------------------------------------------------------------

// One at a time, above the player bar, for about 5 seconds, paused while the
// pointer or the focus is on it. `done(undone)` runs once when it ends, for
// whatever reason: a change that Undo cancels is made, or kept, there.
const badges = { add: 'plus', fav: 'heart', queue: 'lines' };
let shown = null;

export function toast({ title, sub = '', cover = null, badge = 'add', undo = null, undoLabel = 'Undo', error = false, done = null, ms = 5000 }) {
  if (shown) shown('replaced');
  const el = clone('t-toast');
  const icon = badges[badge] || badge;
  el.setAttribute('role', error ? 'alert' : 'status');
  el.classList.toggle('is-error', error);
  $('.toast-title', el).textContent = title;
  $('.toast-sub', el).textContent = sub;
  const art = $('.tcov-img', el), mark = $('.ticon', el), tag = $('.tbadge', el);
  if (cover) {
    art.src = cover;
    art.hidden = false;
    tag.hidden = false;
    tag.classList.toggle('is-fav', badge === 'fav');
    $('.i', tag).classList.toggle('f', badge === 'fav');
    setIcon($('.i', tag), icon);
  } else {
    mark.hidden = false;
    setIcon($('.i', mark), icon);
  }
  const undoButton = $('.undo', el);
  undoButton.hidden = !undo;
  undoButton.textContent = undoLabel;

  let timer, ended = false;
  const wait = () => { clearTimeout(timer); timer = setTimeout(() => end('timeout'), ms); };
  function end(reason) {
    if (ended) return;
    ended = true;
    clearTimeout(timer);
    el.remove();
    if (shown === end) shown = null;
    done?.(reason === 'undo');
  }
  undoButton.addEventListener('click', () => { undo(); end('undo'); });
  $('.toast-close', el).addEventListener('click', () => end('dismiss'));
  el.addEventListener('pointerenter', () => clearTimeout(timer));
  el.addEventListener('pointerleave', wait);
  el.addEventListener('focusin', () => clearTimeout(timer));
  el.addEventListener('focusout', event => { if (!el.contains(event.relatedTarget)) wait(); });
  shown = end;
  $('#toasts').append(el);
  wait();
  return { close: () => end('dismiss') };
}

// The message of a playlist change that met a newer revision (412).
export const staleToast = () => toast({ title: 'Playlist changed elsewhere', sub: 'Showing the latest version.', badge: 'refresh' });

// ---- Menus -----------------------------------------------------------------

// openMenu(button, items, at). `at` = {x, y} opens it at the pointer (a
// right-click) instead of under the button. An item is {label, icon, href, run, danger, checked,
// cover, disabled, filled, key}, or {label, icon, items: [...]} for a submenu (one level),
// or '-' for a line. Keys: ↑ ↓ Home End move, → opens a submenu, ← and Esc
// close it, Esc closes the menu, Tab leaves. A submenu stays open for 300 ms
// after the pointer leaves it, so a diagonal move does not lose it.
let open = null; // { menu, button, timer }

function itemElement(spec) {
  let el = clone('t-menu-item');
  $('.mi-label', el).textContent = spec.label;
  if (spec.icon) setIcon($('.mi-icon', el), spec.icon);
  $('.mi-icon', el).classList.toggle('f', !!spec.filled);
  if (spec.cover) {
    const img = $('.mi-cover', el);
    img.src = spec.cover;
    img.hidden = false;
    show($('.mi-icon', el), false);
  } else if (!spec.icon) {
    show($('.mi-icon', el), false);
  }
  show($('.end', el), !!spec.checked);
  if (spec.href) {
    const link = document.createElement('a');
    link.className = el.className;
    link.setAttribute('role', 'menuitem');
    link.href = spec.href;
    link.append(...el.childNodes);
    el = link;
  }
  if (spec.danger) el.classList.add('danger');
  if (spec.key) el.dataset.key = spec.key; // what a later change to the menu finds the item by
  if (spec.disabled) {
    el.setAttribute('aria-disabled', 'true');
  } else {
    el.addEventListener('click', () => { closeMenu(); spec.run?.(); });
  }
  return el;
}

function listElements(specs, nested) {
  return specs.map(spec => {
    if (spec === '-') return document.createElement('hr');
    if (!spec.items || nested) return itemElement(spec);
    const sub = clone('t-submenu');
    const trigger = $('.mi', sub);
    $('.mi-label', trigger).textContent = spec.label;
    if (spec.icon) setIcon($('.mi-icon', trigger), spec.icon); else show($('.mi-icon', trigger), false);
    $('.menu', sub).append(...listElements(spec.items, true));
    return sub;
  });
}

function openSub(sub, focus) {
  for (const other of open.menu.querySelectorAll('.sub')) if (other !== sub) closeSub(other);
  clearTimeout(open.timer);
  const panel = $('.menu', sub);
  $('.mi', sub).setAttribute('aria-expanded', 'true');
  panel.hidden = false;
  // Beside the trigger, or on its other side and higher when the window ends.
  const box = panel.getBoundingClientRect();
  sub.classList.toggle('flip', box.right > innerWidth - 8);
  const over = box.bottom - (innerHeight - 8);
  panel.style.setProperty('--shift', over > 0 ? `${-over}px` : '0px');
  if (focus) enabled(panel)[0]?.focus();
}

function closeSub(sub) {
  $('.mi', sub).setAttribute('aria-expanded', 'false');
  $('.menu', sub).hidden = true;
}

const enabled = list => [...list.querySelectorAll(':scope > .mi:not([aria-disabled]), :scope > .sub > .mi')];

// Under the button, its right edge on the menu's; or, for a right-click,
// with its corner at the pointer (`at` = {x, y}).
function place(menu, button, at) {
  const b = at ? { left: at.x, right: at.x + menu.offsetWidth, top: at.y - 4, bottom: at.y - 4 } : button.getBoundingClientRect();
  const m = menu.getBoundingClientRect();
  const left = Math.min(Math.max(8, at ? b.left : b.right - m.width), innerWidth - m.width - 8);
  let top = b.bottom + 4;
  if (top + m.height > innerHeight - 8) top = Math.max(8, b.top - m.height - 4);
  menu.style.setProperty('--x', `${left}px`);
  menu.style.setProperty('--y', `${top}px`);
}

export function openMenu(button, items, at = null) {
  if (open?.button === button) { closeMenu(); return null; }
  closeMenu();
  const menu = clone('t-menu');
  menu.append(...listElements(items, false));
  document.body.append(menu);
  menu.showPopover();
  place(menu, button, at);
  button.setAttribute('aria-haspopup', 'menu');
  button.setAttribute('aria-expanded', 'true');
  button.classList.add('is-open');
  open = { menu, button, timer: 0 };
  menu.addEventListener('pointerover', onPointer);
  menu.addEventListener('pointerout', onPointer);
  document.addEventListener('keydown', onKey, true);
  document.addEventListener('pointerdown', onOutside, true);
  addEventListener('scroll', onScroll, true);
  addEventListener('resize', closeMenu);
  enabled(menu)[0]?.focus();
  return menu;
}

export function closeMenu() {
  if (!open) return;
  const { menu, button } = open;
  open = null;
  document.removeEventListener('keydown', onKey, true);
  document.removeEventListener('pointerdown', onOutside, true);
  removeEventListener('scroll', onScroll, true);
  removeEventListener('resize', closeMenu);
  const hadFocus = menu.contains(document.activeElement);
  button.setAttribute('aria-expanded', 'false');
  button.classList.remove('is-open');
  menu.remove();
  if (hadFocus && button.isConnected) button.focus({ preventScroll: true });
}

function onOutside(event) {
  // The button toggles by itself, in its click.
  if (!open.menu.contains(event.target) && !open.button.contains(event.target)) closeMenu();
}

function onScroll(event) {
  if (!open.menu.contains(event.target)) closeMenu();
}

// Pointer over a submenu's trigger or panel opens it; leaving it closes it
// after 300 ms, unless the pointer comes back.
function onPointer(event) {
  const sub = event.target.closest?.('.sub');
  if (!sub) return;
  if (event.type === 'pointerover') {
    clearTimeout(open.timer);
    if ($('.menu', sub).hidden) openSub(sub, false);
  } else if (!sub.contains(event.relatedTarget)) {
    clearTimeout(open.timer);
    open.timer = setTimeout(() => closeSub(sub), 300);
  }
}

function onKey(event) {
  const focused = document.activeElement;
  const list = focused?.closest('.menu') || open.menu;
  const sub = list.closest('.sub');
  const items = enabled(list);
  const at = items.indexOf(focused);
  const go = i => { event.preventDefault(); items[(i + items.length) % items.length]?.focus(); };
  switch (event.key) {
    case 'ArrowDown': go(at + 1); break;
    case 'ArrowUp': go(at < 0 ? -1 : at - 1); break;
    case 'Home': go(0); break;
    case 'End': go(-1); break;
    case 'ArrowRight': {
      const own = focused?.closest('.sub');
      if (own && focused === $('.mi', own)) { event.preventDefault(); openSub(own, true); }
      break;
    }
    case 'ArrowLeft':
    case 'Escape':
      event.preventDefault();
      event.stopPropagation(); // a sheet or the page behind does not see this Esc
      if (sub) { closeSub(sub); $('.mi', sub).focus(); } else if (event.key === 'Escape') closeMenu();
      break;
    case 'Tab': closeMenu(); break;
    case ' ': if (focused?.tagName === 'A') { event.preventDefault(); focused.click(); } break;
  }
}

// ---- Sheets ----------------------------------------------------------------

// Opens a <dialog> template as a modal sheet. Esc closes it (the browser's),
// and so does a button with data-close; it is removed when it closes, and
// `returnValue` is the value of the button that closed it. `setup(dialog)`
// fills it before it shows.
export function openSheet(templateId, setup) {
  const dialog = clone(templateId);
  const title = $('.sheet-title', dialog);
  if (title) {
    title.id = `sheet-title-${++sheets}`;
    dialog.setAttribute('aria-labelledby', title.id);
  }
  setup?.(dialog);
  document.body.append(dialog);
  dialog.addEventListener('click', event => {
    const button = event.target.closest('[data-close]');
    if (button) dialog.close(button.value);
  });
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  dialog.showModal();
  return dialog;
}
let sheets = 0;

export const whenClosed = dialog => new Promise(resolve => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));

// Asks before something that cannot be undone. Resolves true on the action.
export async function confirmSheet({ title, text, action, danger = false }) {
  const dialog = openSheet('t-sheet-confirm', d => {
    $('.sheet-title', d).textContent = title;
    $('.sheet-text', d).textContent = text;
    const button = $('.btn-action', d);
    button.textContent = action;
    button.classList.add(danger ? 'btn-danger' : 'btn-primary');
  });
  return (await whenClosed(dialog)) === 'confirm';
}
