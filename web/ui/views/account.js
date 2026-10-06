// Account: who is signed in, the password, the preferences that follow the
// person (settings.js) and the places they are signed in. Nothing here asks
// "are you sure": a session signed out can sign in again, and a preference
// changes back with a click.
import { api, isMissing } from '../api.js';
import { $, clone, fieldError, formatAgo, formatDate, pageHead, pending, radioGroup, sayError, setIcon, show, toast } from '../ui.js';
import { loadSettings, saveSetting, settings, synced } from '../settings.js';
import { openShortcuts } from '../shortcuts.js';

const WORDS = {
  current_password_invalid: 'That isn’t your current password.',
  password_invalid: 'Use at least 12 characters, with no control characters.',
};
const longDate = new Intl.DateTimeFormat('en', { dateStyle: 'long' });

function lastUsed(session) {
  const age = Date.now() - Date.parse(session.last_used_at);
  if (age < 120000) return 'Active now';
  return `Last used ${age < 14 * 86400000 ? formatAgo(session.last_used_at) : formatDate(session.last_used_at)}`;
}

function sessionRow(session) {
  const row = clone('t-session');
  row.dataset.id = session.id;
  setIcon($('.dev .i', row), session.kind === 'token' ? 'phone' : 'laptop');
  const kind = session.kind === 'token' ? 'App' : 'Browser';
  $('.sess-name', row).textContent = session.device_name || `Unnamed ${kind.toLowerCase()}`;
  const [a, b, c] = $('.sess-meta', row).children;
  a.textContent = kind;
  b.textContent = lastUsed(session);
  c.textContent = `Expires ${formatDate(session.expires_at)}`;
  show($('.sess-here', row), session.current);
  show($('.sess-out', row), !session.current);
  $('.sess-out', row).setAttribute('aria-label', `Sign out ${$('.sess-name', row).textContent}`);
  return row;
}

export async function render(main, params, signal) {
  pageHead(main, 'Account');
  // Three reads at once: who, where, and what the server remembers.
  const waiting = pending(main, 't-ph-rows');
  let me, list;
  try {
    [me, list] = await Promise.all([
      api.get('/me', null, { signal }),
      api.get('/me/sessions', null, { signal }),
      loadSettings(),
    ]);
  } finally {
    waiting();
  }
  if (signal.aborted) return;
  const view = clone('t-account');
  main.append(view);

  // ---- Profile
  $('.who-av', view).textContent = me.username[0].toUpperCase();
  $('.who-name', view).textContent = me.username;
  $('.badge', view).textContent = me.role === 'admin' ? 'Admin' : 'User';
  $('.who-meta .note', view).textContent = `Member since ${longDate.format(new Date(me.created_at))}`;

  // ---- Change password
  const form = $('form', view);
  const [current, next] = form.querySelectorAll('.field');
  const done = $('.pw-done', form);
  form.addEventListener('input', () => show(done, false));
  form.addEventListener('submit', async event => {
    event.preventDefault();
    fieldError(current);
    fieldError(next);
    show(done, false);
    if (!form.elements.current.value) { fieldError(current, 'Enter your current password.'); form.elements.current.focus(); return; }
    if (new TextEncoder().encode(form.elements.new.value).length < 12) { fieldError(next, WORDS.password_invalid); form.elements.new.focus(); return; }
    const button = $('button[type="submit"]', form);
    button.disabled = true;
    try {
      await api.put('/me/password', { current_password: form.elements.current.value, new_password: form.elements.new.value });
      form.reset();
      show(done, true);
      // The server signed out every other session: the list says so.
      sessions = sessions.filter(session => session.current);
      paintSessions();
    } catch (error) {
      if (error.code === 'current_password_invalid') { fieldError(current, WORDS[error.code]); form.elements.current.focus(); }
      else if (error.code === 'password_invalid') { fieldError(next, WORDS[error.code]); form.elements.new.focus(); }
      else toast({ title: 'Couldn’t change the password', sub: sayError(error), badge: 'alert', error: true });
    } finally {
      button.disabled = false;
    }
  });

  // ---- Playback and appearance
  const levelling = radioGroup($('[data-setting="volume_leveling"]', view), settings().volume_leveling, value => saveSetting('volume_leveling', value));
  const theme = radioGroup($('[data-setting="theme"]', view), settings().theme, value => saveSetting('theme', value));
  const single = $('.switch', view);
  single.addEventListener('click', () => saveSetting('single_key_shortcuts', !settings().single_key_shortcuts));
  $('.keys-link .link', view).addEventListener('click', openShortcuts);
  // The same settings change from the shortcuts sheet, the sidebar's theme
  // button and the server's answer: the controls follow.
  const showSettings = () => {
    levelling(settings().volume_leveling);
    theme(settings().theme);
    single.setAttribute('aria-checked', String(settings().single_key_shortcuts));
    $('.ac-saved', view).textContent = synced() ? 'Saved to your account' : 'Saved in this browser';
  };
  document.addEventListener('settings:changed', showSettings, { signal });
  showSettings();

  // ---- Where you're signed in
  let sessions = list.sessions;
  const host = $('.sessions', view);
  const others = $('.ac-others', view);

  function paintSessions() {
    host.replaceChildren(...sessions.map(sessionRow));
    show(others, sessions.some(session => !session.current));
  }

  // At once: the row goes, the request follows, and a refusal brings it back.
  async function revoke(row) {
    const at = sessions.findIndex(session => session.id === row.dataset.id);
    const [gone] = sessions.splice(at, 1);
    const after = row.nextElementSibling?.querySelector('.sess-out') || null;
    paintSessions();
    (after || $('#ac-sessions', view)).focus?.();
    try {
      await api.del(`/me/sessions/${gone.id}`);
    } catch (error) {
      if (error.code === 'session_not_found') return; // already gone
      sessions.splice(at, 0, gone);
      paintSessions();
      toast({ title: 'Couldn’t sign out', sub: sayError(error), badge: 'alert', error: true });
    }
  }

  // DELETE /me/sessions (B3); a server without it gets one request per session.
  async function revokeOthers() {
    const gone = sessions.filter(session => !session.current);
    sessions = sessions.filter(session => session.current);
    paintSessions();
    let failed = [], why = null;
    try {
      await api.del('/me/sessions');
    } catch (error) {
      if (!isMissing(error) && error.status !== 405) {
        failed = gone;
        why = error;
      } else {
        const results = await Promise.allSettled(gone.map(session => api.del(`/me/sessions/${session.id}`)));
        failed = gone.filter((_, i) => results[i].status === 'rejected' && results[i].reason.code !== 'session_not_found');
        why = results.find(result => result.status === 'rejected')?.reason;
      }
    }
    if (failed.length) {
      sessions = sessions.concat(failed);
      paintSessions();
      toast({ title: 'Couldn’t sign out everywhere', sub: sayError(why), badge: 'alert', error: true });
    } else if (gone.length) {
      toast({ title: 'Signed out everywhere else', sub: `${gone.length} ${gone.length === 1 ? 'session' : 'sessions'} ended`, badge: 'check' });
    }
  }

  host.addEventListener('click', event => {
    const button = event.target.closest('.sess-out');
    if (button) revoke(button.closest('.sess'));
  });
  others.addEventListener('click', revokeOthers);
  $('#ac-sessions', view).tabIndex = -1;
  paintSessions();
}
