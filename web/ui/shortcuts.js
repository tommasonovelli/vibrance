// The keys that work everywhere (proposal 7.6). A single character as a
// shortcut must be possible to turn off (WCAG 2.1.4), so L, Q, S, R, M and F
// answer only when the setting is on, here or in Account. Space, Ctrl or Cmd
// with the arrows, ? and Esc are always on; none of them while a field has the
// focus. (Ctrl or Cmd+K and / belong to app.js, Enter to the lists.)
import * as player from './player.js';
import { saveSetting, settings } from './settings.js';
import { $, announce, openSheet } from './ui.js';
import { navigate } from './router.js';
import { lastPage } from './bar.js';
import { toggleQueue } from './queue.js';

const typing = element => element?.closest?.('input, textarea, select, [contenteditable]');
// A button or a link takes Space for itself.
const pressable = element => element?.closest?.('button, a[href], summary, [role="menuitem"], [role="switch"], [role="checkbox"], [role="option"]');
const onNow = () => location.pathname === '/now-playing';
const toggleNow = what => document.dispatchEvent(new CustomEvent('now:toggle', { detail: what }));

const single = {
  l: () => (onNow() ? toggleNow('lyrics') : navigate('/now-playing')),
  q: () => (onNow() ? toggleNow('queue') : toggleQueue()),
  s: () => { player.setShuffle(!player.isShuffled()); announce(`Shuffle ${player.isShuffled() ? 'on' : 'off'}`); },
  r: () => { player.cycleRepeat(); announce({ off: 'Repeat off', all: 'Repeat all', one: 'Repeat one' }[player.getRepeat()]); },
  m: () => { player.toggleMute(); announce(player.isMuted() ? 'Muted' : 'Sound on'); },
  f: () => (onNow() ? toggleNow('fullscreen') : navigate('/now-playing')),
};

export function initShortcuts() {
  document.addEventListener('keydown', event => {
    if (event.defaultPrevented || event.isComposing) return;
    const field = typing(event.target);
    const command = event.ctrlKey || event.metaKey;
    if (document.querySelector('dialog[open]')) return; // a sheet has its own keys
    if (command && !event.altKey && !event.shiftKey && !field && (event.key === 'ArrowRight' || event.key === 'ArrowLeft')) {
      event.preventDefault();
      if (event.key === 'ArrowRight') player.next(); else player.previous();
      return;
    }
    if (command || event.altKey || field) return;
    if (event.key === ' ') {
      if (pressable(event.target) || !player.current()) return;
      event.preventDefault(); // the page does not scroll
      player.toggle();
    } else if (event.key === '?') {
      event.preventDefault();
      openShortcuts();
    } else if (event.key === 'Escape') {
      if (onNow() && !document.querySelector('.menu[popover]')) navigate(lastPage());
    } else if (!event.repeat && settings().single_key_shortcuts) {
      single[event.key.toLowerCase()]?.();
    }
  });
}

// The sheet that lists them; its switch is the same setting as Account's.
export function openShortcuts() {
  openSheet('t-sheet-keys', dialog => {
    const toggle = $('.switch', dialog), group = $('.kcols-single', dialog);
    const show = () => {
      const on = settings().single_key_shortcuts;
      toggle.setAttribute('aria-checked', String(on));
      group.classList.toggle('off', !on);
    };
    toggle.addEventListener('click', () => { saveSetting('single_key_shortcuts', !settings().single_key_shortcuts); show(); });
    show();
  });
}
