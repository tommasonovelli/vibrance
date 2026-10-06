// The preferences that follow the person from device to device (B5,
// GET and PUT /me/settings): volume leveling and single-key shortcuts. The
// last answer is kept in localStorage, which is also all there is while the
// server has no such endpoint. `theme` is one of them (PUT replaces the whole
// object): it is put on <html> here, by Account's choice, by the sidebar's
// button and by what the server remembers.
import { api } from './api.js';

const KEY = 'vibrance.settings';
const defaults = { volume_leveling: 'automatic', single_key_shortcuts: false, theme: 'dark' };
const root = document.documentElement;

function stored() {
  try { return JSON.parse(localStorage.getItem(KEY)) || {}; } catch { return {}; }
}

// prefs.js put the theme of this browser on <html> before the first paint.
// A browser that was never given one takes the account's theme when it comes;
// one that has a choice keeps it, and the next change tells the server.
let value = { ...defaults, ...stored(), theme: root.dataset.theme === 'light' ? 'light' : 'dark' };
let onServer = false; // the server answered with them, so they follow the person

// What is known now, at once; `loadSettings` brings the server's.
export const settings = () => value;
// Whether they are kept on the account (they are on this browser all the same).
export const synced = () => onServer;

function applyTheme(theme) {
  root.dataset.theme = theme;
  try { localStorage.setItem('vibrance.theme', theme); } catch { /* not remembered */ }
}

function keep() {
  try { localStorage.setItem(KEY, JSON.stringify(value)); } catch { /* not remembered */ }
  document.dispatchEvent(new CustomEvent('settings:changed', { detail: value }));
}

export async function loadSettings() {
  try {
    let mine = null;
    try { mine = localStorage.getItem('vibrance.theme'); } catch { /* no storage: the account's */ }
    value = { ...defaults, ...await api.get('/me/settings') };
    onServer = true;
    if (mine) value.theme = root.dataset.theme; else applyTheme(value.theme);
    keep();
  } catch { /* no endpoint, or no answer: the ones kept here stand */ }
  return value;
}

// One preference changes at once on this device, and is sent to the server
// after; a server that refuses leaves it as it is here.
export async function saveSetting(key, next) {
  value = { ...value, [key]: next };
  if (key === 'theme') applyTheme(next);
  keep();
  try {
    await api.put('/me/settings', value);
    onServer = true;
  } catch { /* kept on this device */ }
}
