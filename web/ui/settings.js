// The preferences that follow the person from device to device (B5,
// GET and PUT /me/settings): volume leveling and single-key shortcuts. The
// last answer is kept in localStorage, which is also all there is while the
// server has no such endpoint. `theme` rides along because PUT replaces the
// whole object; the sidebar's button still owns it.
import { api } from './api.js';

const KEY = 'vibrance.settings';
const defaults = { volume_leveling: 'automatic', single_key_shortcuts: false, theme: 'dark' };

function stored() {
  try { return JSON.parse(localStorage.getItem(KEY)) || {}; } catch { return {}; }
}

let value = { ...defaults, ...stored() };

// What is known now, at once; `loadSettings` brings the server's.
export const settings = () => value;

function keep() {
  try { localStorage.setItem(KEY, JSON.stringify(value)); } catch { /* not remembered */ }
  document.dispatchEvent(new CustomEvent('settings:changed', { detail: value }));
}

export async function loadSettings() {
  try {
    value = { ...defaults, ...await api.get('/me/settings') };
    keep();
  } catch { /* no endpoint, or no answer: the ones kept here stand */ }
  return value;
}

// One preference changes at once on this device, and is sent to the server
// after; a server that refuses leaves it as it is here.
export async function saveSetting(key, next) {
  value = { ...value, [key]: next, theme: document.documentElement.dataset.theme || value.theme };
  keep();
  try { await api.put('/me/settings', value); } catch { /* kept on this device */ }
}
