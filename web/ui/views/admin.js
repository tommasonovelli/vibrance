// Administration, for admins: the state of the library and a scan on request,
// then the accounts. A `user` is told "Page not found", as the API answers
// them with 403 (or 404 on a server without it). What cannot be taken back (a
// deletion) asks first; everything else happens at once and comes back if the
// server says no.
import { api, optional } from '../api.js';
import * as status from '../status.js';
import { $, announce, clone, confirmSheet, fieldError, formatDate, formatLength, number, openMenu, openSheet, pageHead, plural, radioGroup, sayError, show, toast } from '../ui.js';

const clock = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });

const WORDS = {
  username_taken: 'That username is taken.',
  username_invalid: 'Use 3 to 32 characters: a–z, 0–9, dot, dash, underscore, beginning with a letter or a digit.',
  password_invalid: 'Use at least 12 characters, with no control characters.',
  last_admin: 'There must be one enabled admin left. Make someone else an admin first.',
  cannot_modify_self: 'You can’t do that to your own account.',
  user_not_found: 'That account no longer exists.',
  forbidden: 'Only admins can do that.',
};
// What MusicLib is doing, by the library's state: [word, note].
const MUSICLIB = { idle: ['Ready', 'Not in maintenance'], scanning: ['Ready', 'Not in maintenance'], maintenance: ['Maintenance', 'Rebuilding its library'], unavailable: ['Not found', 'No library folder'] };
// The problems that keep an album out of the index are red, the warnings orange.
const WARNINGS = new Set(['cover_invalid', 'tags_incomplete', 'lyrics_unreadable']);

const initial = name => name[0].toUpperCase();
const available = counts => (counts.unavailable ? `${number.format(counts.unavailable)} not available` : 'All available');

// Today, 12:35 · Yesterday, 09:10 · Sep 30, 12:35
function when(iso) {
  const date = new Date(iso), day = date.toDateString();
  const label = day === new Date().toDateString() ? 'Today' : day === new Date(Date.now() - 864e5).toDateString() ? 'Yesterday' : formatDate(iso);
  return `${label}, ${clock.format(date)}`;
}

function took(scan) {
  const seconds = Math.round((Date.parse(scan.finished_at) - Date.parse(scan.started_at)) / 1000);
  if (seconds < 1) return 'Took under a second';
  return seconds < 60 ? `Took ${seconds} ${seconds === 1 ? 'second' : 'seconds'}` : `Took ${formatLength(seconds * 1000)}`;
}

export async function render(main, params, signal) {
  let me, library, users, summary, server;
  try {
    // Everything at once; the catalog's figures and the version are extras.
    [me, library, users, summary, server] = await Promise.all([
      api.get('/me', null, { signal }),
      status.refresh(),
      api.get('/admin/users', null, { signal }).then(list => list.users),
      optional(api.get('/catalog/summary', null, { signal })).catch(() => null),
      optional(api.get('/server', null, { signal })).catch(() => null),
    ]);
  } catch (error) {
    if (error.status !== 403 && error.status !== 404) throw error;
    return (await import('./not-found.js')).render(main);
  }
  if (signal.aborted) return;

  const head = pageHead(main, 'Administration');
  if (server) {
    const version = document.createElement('span');
    version.className = 'note';
    version.textContent = `Vibrance ${server.version} · API v${server.api_version}`;
    $('.head-tools', head).append(version);
  }
  const view = clone('t-admin');
  main.append(view);

  // ---- Library
  const band = $('.band', view);
  function paintLibrary(state) {
    const [dot, word, note] = status.STATES[state.state];
    $('.state .dot', band).className = `dot ${dot}`;
    $('.band-state', band).textContent = word;
    $('.band-note', band).textContent = note;
    const progress = state.state === 'scanning' ? state.progress : null;
    show($('.scanbar', band), !!progress);
    if (progress) {
      const bar = $('.bar', band);
      bar.style.setProperty('--p', progress.discovered ? progress.indexed / progress.discovered : 0);
      bar.setAttribute('aria-valuemax', progress.discovered);
      bar.setAttribute('aria-valuenow', progress.indexed);
      $('.scan-count', band).textContent = `${number.format(progress.indexed)} of ${number.format(progress.discovered)} albums`;
    }

    const set = (key, value, text) => { $(`.st-${key}`, view).textContent = value; $(`.st-${key}-note`, view).textContent = text; };
    set('albums', number.format(state.albums.available), [summary && plural(summary.artists, 'artist'), available(state.albums)].filter(Boolean).join(' · '));
    set('tracks', number.format(state.tracks.available), [summary && formatLength(summary.duration_ms), available(state.tracks)].filter(Boolean).join(' · '));
    const scan = state.last_scan;
    if (!scan) set('scan', '–', 'No scan yet');
    else set('scan', when(scan.started_at), !scan.finished_at ? 'In progress' : scan.ok ? took(scan) : `Stopped: ${scan.error || 'it did not finish'}`);
    set('ml', ...MUSICLIB[state.state]);

    const problems = state.problems;
    show($('.attention', view), problems.length > 0);
    $('.attention-title .note', view).textContent = problems.length;
    $('.probs', view).replaceChildren(...problems.map(problem => {
      const row = clone('t-prob');
      const dot = $('.dot', row);
      dot.className = `dot ${WARNINGS.has(problem.code) ? 'warn' : 'err'}`;
      dot.setAttribute('aria-label', WARNINGS.has(problem.code) ? 'Warning' : 'Problem');
      $('.mono', row).textContent = problem.rel_path || 'The library folder';
      $('.prob-msg', row).textContent = problem.message;
      $('.code', row).textContent = problem.code;
      return row;
    }));
  }
  // One reader (status.js) polls while a scan runs and the page is visible.
  document.addEventListener('library:status', event => paintLibrary(event.detail), { signal });
  paintLibrary(library);

  const scanButton = $('.band-scan', band);
  scanButton.addEventListener('click', async () => {
    scanButton.disabled = true;
    try {
      await status.scan();
    } catch (error) {
      toast({ title: 'Couldn’t start a scan', sub: sayError(error, WORDS), badge: 'alert', error: true });
    } finally {
      scanButton.disabled = false;
    }
  });

  // ---- Users
  const table = $('.users', view);

  const userRow = user => {
    const el = clone('t-user');
    el.dataset.id = user.id;
    el.classList.toggle('is-off', user.disabled);
    $('.u-av', el).textContent = initial(user.username);
    $('.u-name', el).textContent = user.username;
    $('.u-you', el).textContent = user.id === me.id ? 'You' : '';
    $('.c-role', el).textContent = user.role === 'admin' ? 'Admin' : 'User';
    $('.u-state .dot', el).classList.toggle('ok', !user.disabled);
    $('.u-status', el).textContent = user.disabled ? 'Disabled' : 'Active';
    $('.c-created', el).textContent = formatDate(user.created_at);
    $('.dots', el).setAttribute('aria-label', `More actions, ${user.username}`);
    return el;
  };
  function paintUsers() {
    const focused = document.activeElement?.closest?.('.ur')?.dataset.id; // the rows are made again: the focus stays on its own
    for (const old of table.querySelectorAll('.ur:not(.hd)')) old.remove();
    table.append(...users.map(userRow));
    if (focused) $(`.ur[data-id="${CSS.escape(focused)}"] .dots`, table)?.focus();
  }

  // Changes the user in the list now, asks the server, and puts it back if it says no.
  async function change(user, patch, said) {
    const before = { role: user.role, disabled: user.disabled };
    Object.assign(user, patch);
    paintUsers();
    announce(said);
    try {
      Object.assign(user, await api.put(`/admin/users/${user.id}`, { role: user.role, disabled: user.disabled }));
      paintUsers();
    } catch (error) {
      Object.assign(user, before);
      if (error.code === 'user_not_found') users = users.filter(other => other !== user);
      paintUsers();
      toast({ title: `Couldn’t change ${user.username}`, sub: sayError(error, WORDS), badge: 'alert', error: true });
    }
  }

  async function remove(user) {
    const sure = await confirmSheet({
      title: `Delete “${user.username}”?`,
      text: 'The account is deleted with its sessions, favorites and playlists. This can’t be undone.',
      action: 'Delete account',
      danger: true,
    });
    if (!sure) return;
    const at = users.indexOf(user);
    users.splice(at, 1);
    paintUsers();
    $('.new-user', view).focus();
    try {
      await api.del(`/admin/users/${user.id}`);
      announce(`${user.username} deleted`);
    } catch (error) {
      if (error.code === 'user_not_found') return;
      users.splice(at, 0, user);
      paintUsers();
      toast({ title: `Couldn’t delete ${user.username}`, sub: sayError(error, WORDS), badge: 'alert', error: true });
    }
  }

  function setPassword(user) {
    const dialog = openSheet('t-sheet-user-password', d => {
      $('.sheet-text', d).textContent = `${user.username} is signed out everywhere, and uses this password next.`;
    });
    const form = $('form', dialog), field = $('.field', form);
    form.addEventListener('submit', async event => {
      if (event.submitter?.value !== 'set') return;
      event.preventDefault();
      fieldError(field);
      if (new TextEncoder().encode(form.elements.password.value).length < 12) { fieldError(field, WORDS.password_invalid); form.elements.password.focus(); return; }
      const button = event.submitter;
      button.disabled = true;
      try {
        await api.put(`/admin/users/${user.id}/password`, { password: form.elements.password.value });
        dialog.close('set');
        toast({ title: 'Password set', sub: `${user.username} is signed out everywhere`, badge: 'check' });
      } catch (error) {
        if (error.code === 'user_not_found') { users = users.filter(other => other !== user); paintUsers(); dialog.close('gone'); }
        fieldError(field, sayError(error, WORDS));
        form.elements.password.focus();
      } finally {
        button.disabled = false;
      }
    });
  }

  function newUser() {
    const dialog = openSheet('t-sheet-new-user');
    const form = $('form', dialog);
    const [nameField, passwordField] = form.querySelectorAll('.field');
    const problem = $('.form-error', form);
    let role = 'user';
    radioGroup($('.radios', form), role, value => { role = value; });
    form.addEventListener('submit', async event => {
      if (event.submitter?.value !== 'create') return;
      event.preventDefault();
      fieldError(nameField);
      fieldError(passwordField);
      fieldError(problem);
      const username = form.elements.username.value.trim().toLowerCase();
      if (!/^[a-z0-9][a-z0-9._-]{2,31}$/.test(username)) { fieldError(nameField, WORDS.username_invalid); form.elements.username.focus(); return; }
      if (new TextEncoder().encode(form.elements.password.value).length < 12) { fieldError(passwordField, WORDS.password_invalid); form.elements.password.focus(); return; }
      const button = event.submitter;
      button.disabled = true;
      try {
        const created = await api.post('/admin/users', { username, password: form.elements.password.value, role });
        users.push(created);
        users.sort((a, b) => (a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0));
        paintUsers();
        dialog.close('create');
        toast({ title: 'User created', sub: `${created.username} can sign in now`, badge: 'check' });
      } catch (error) {
        if (error.code === 'username_taken' || error.code === 'username_invalid') { fieldError(nameField, sayError(error, WORDS)); form.elements.username.focus(); }
        else if (error.code === 'password_invalid') { fieldError(passwordField, WORDS.password_invalid); form.elements.password.focus(); }
        else fieldError(problem, sayError(error, WORDS));
      } finally {
        button.disabled = false;
      }
    });
  }

  // The rules of the API, so the menu offers only what can work: the last
  // enabled admin cannot be demoted, disabled or deleted; nobody disables or
  // deletes their own account, nor sets their own password here (Account does).
  function userMenu(user) {
    const self = user.id === me.id;
    const lastAdmin = user.role === 'admin' && !user.disabled && users.filter(u => u.role === 'admin' && !u.disabled).length === 1;
    const items = [];
    if (!lastAdmin) {
      const admin = user.role === 'admin';
      items.push({ label: admin ? 'Make user' : 'Make admin', icon: 'shield', run: () => change(user, { role: admin ? 'user' : 'admin' }, `${user.username} is ${admin ? 'a user' : 'an admin'} now`) });
    }
    items.push(self ? { label: 'Change your password…', icon: 'lock', href: '/account' } : { label: 'Set a new password…', icon: 'lock', run: () => setPassword(user) });
    if (!self && !lastAdmin) {
      items.push({ label: user.disabled ? 'Enable account' : 'Disable account', icon: 'ban', run: () => change(user, { disabled: !user.disabled }, user.disabled ? `${user.username} enabled` : `${user.username} disabled and signed out everywhere`) });
      items.push('-', { label: 'Delete account…', icon: 'trash', danger: true, run: () => remove(user) });
    }
    return items;
  }

  table.addEventListener('click', event => {
    const button = event.target.closest('.dots');
    if (!button) return;
    const user = users.find(u => u.id === button.closest('.ur').dataset.id);
    if (user) openMenu(button, userMenu(user));
  });
  $('.new-user', view).addEventListener('click', newUser);
  paintUsers();
}
