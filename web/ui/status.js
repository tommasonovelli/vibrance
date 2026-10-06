// What the whole app knows about the server and the library, and the one
// banner that says it. For an admin: the state of the library (GET
// /admin/library), read again only while it can change (a scan running,
// MusicLib in maintenance, the folder away) and only while the page is
// visible. For everyone: whether Vibrance can be reached (api.js says when it
// could not; the browser says when it is offline). Other views listen to
// `library:status` instead of asking: there is one reader.
import { api } from './api.js';
import { $, announce, clone, show } from './ui.js';

let admin = false;
let status = null; // the last answer of GET /admin/library
let readAt = 0;
let timer = 0;
let due = 0; // reads still owed after a scan was asked for: the cycle may not have started yet
let onAdmin = false; // the Administration page shows the library itself
let unreachable = false;
let probe = 0;

// The words of each state, here and in Administration: [dot, state, note, what the empty Library tells an admin].
export const STATES = {
  idle: ['ok', 'Up to date', 'The last scan went through every album folder.', 'Nothing was found by the last scan.'],
  scanning: ['busy', 'Updating', 'New and changed albums appear as they are indexed. You can keep listening.', 'Scanning now.'],
  maintenance: ['busy', 'Waiting', 'MusicLib is rebuilding its library. Vibrance keeps playing what it already knows and looks again when it ends.', 'Waiting for MusicLib.'],
  unavailable: ['err', 'Needs attention', 'The library folder can’t be read. Check that the MusicLib library is mounted.', 'The library folder can’t be read.'],
};

function emit() {
  readAt = Date.now();
  document.dispatchEvent(new CustomEvent('library:status', { detail: status }));
  paint();
}

// A cycle that runs is read every 2 s; maintenance or a missing folder every
// 30 s, to notice the end; a quiet library not at all.
function schedule() {
  clearTimeout(timer);
  if (!admin || document.hidden) return;
  const state = status?.state;
  const wait = state === 'scanning' || due > 0 ? 2000 : state === 'maintenance' || state === 'unavailable' ? 30000 : 0;
  if (wait) timer = setTimeout(() => refresh().catch(() => schedule()), wait);
}

export async function refresh() {
  status = await api.get('/admin/library');
  if (status.state === 'scanning') due = 0; else if (due > 0) due--;
  emit();
  schedule();
  return status;
}

// Asks for a scan. The answer may still say idle: the next reads follow the cycle.
export async function scan() {
  status = await api.post('/admin/library/scan');
  due = 3;
  emit();
  schedule();
  return status;
}

// ---- The banner ------------------------------------------------------------

let shown = '';

function paint() {
  const banner = $('#banner');
  let kind = '', text = '', link = '';
  if (!navigator.onLine) {
    kind = 'offline';
    text = 'You’re offline. What is playing keeps playing; the rest waits for the connection.';
  } else if (unreachable) {
    kind = 'down';
    text = 'Can’t reach Vibrance. Trying again in a few seconds.';
  } else if (admin && status && !onAdmin && status.state !== 'idle') {
    kind = status.state;
    text = STATES[kind][2];
    link = 'Administration';
  }
  banner.dataset.kind = kind;
  $('.dot', banner).className = `dot ${kind === 'scanning' || kind === 'maintenance' ? 'busy' : kind ? 'err' : ''}`;
  $('.banner-text', banner).textContent = text;
  show($('.banner-link', banner), !!link);
  show($('.banner-retry', banner), kind === 'down');
  show(banner, !!kind);
  if (kind && kind !== shown) announce(text);
  shown = kind;
}

// While Vibrance does not answer, one small request every 5 s finds out when
// it does again (api.js sees the answer and says `net:up`).
function watch() {
  clearTimeout(probe);
  if (!unreachable) return;
  probe = setTimeout(() => api.get('/server').catch(() => {}).finally(watch), 5000);
}

// ---- The line of an empty Library, for an admin ----------------------------

// "● Waiting for MusicLib. Scan library", added to an empty page, kept in step
// with the state; nobody but an admin gets it.
export function adminLine(host) {
  if (!admin) return;
  const line = clone('t-admin-line');
  const text = $('.al-text', line), button = $('.link', line);
  const paintLine = () => {
    if (!status) return;
    $('.dot', line).className = `dot ${STATES[status.state][0]}`;
    text.textContent = STATES[status.state][3];
  };
  // A page that left takes its line away: the listener goes with it.
  const follow = () => (line.isConnected ? paintLine() : document.removeEventListener('library:status', follow));
  button.addEventListener('click', async () => {
    button.disabled = true;
    try { await scan(); } catch { text.textContent = 'The scan could not be asked for. Try again.'; } finally { button.disabled = false; }
  });
  document.addEventListener('library:status', follow);
  paintLine();
  host.append(line);
  if (!status) refresh().catch(() => {});
}

// ---- Start -----------------------------------------------------------------

// Called once by app.js, with who is signed in.
export async function init(me) {
  admin = me.role === 'admin';
  document.addEventListener('route', event => { onAdmin = event.detail.path === '/admin'; paint(); });
  document.addEventListener('net:down', () => { unreachable = true; paint(); watch(); });
  document.addEventListener('net:up', () => { unreachable = false; paint(); if (admin) refresh().catch(() => {}); });
  addEventListener('online', () => { paint(); api.get('/server').catch(() => {}); }); // back on the network: the answer to this tells whether Vibrance is there
  addEventListener('offline', paint);
  $('.banner-retry', $('#banner')).addEventListener('click', () => api.get('/server').catch(() => {}));
  document.addEventListener('visibilitychange', () => {
    if (!admin) return;
    if (document.hidden) clearTimeout(timer);
    else if (!status || Date.now() - readAt > 15000) refresh().catch(() => schedule());
    else schedule();
  });
  paint();
  if (admin) await refresh().catch(() => {});
}
