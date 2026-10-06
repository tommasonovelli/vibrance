// The sign-in page: its own small script, so it loads nothing of the app.
import { api } from './api.js';

const $ = id => document.getElementById(id);
const form = $('form'), username = $('username'), password = $('password');
let busy = false;

// "Firefox on macOS": what the list of sessions shows for this browser.
function deviceName() {
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua) ? 'Edge' : /OPR\//.test(ua) ? 'Opera' : /Firefox\//.test(ua) ? 'Firefox'
    : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser';
  // iPhones and Androids name other systems in their user agent: ask first.
  const system = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Windows/.test(ua) ? 'Windows'
    : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /CrOS/.test(ua) ? 'ChromeOS' : /Linux/.test(ua) ? 'Linux' : '';
  return system ? `${browser} on ${system}` : browser;
}

// Where to go after: a path of this site, never another address.
function nextPath() {
  const raw = new URLSearchParams(location.search).get('next') || '';
  if (!raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return '/';
  const url = new URL(raw, location.origin);
  if (url.origin !== location.origin || /^\/(login|api)(\/|$)/.test(url.pathname)) return '/';
  return url.pathname + url.search + url.hash;
}

function setBusy(on) {
  busy = on;
  $('submit').setAttribute('aria-busy', String(on));
  $('spin').hidden = !on;
  $('submit-label').textContent = on ? 'Signing in…' : 'Sign in';
}

function showNotice(text) {
  const notice = $('notice');
  notice.hidden = false;
  $('notice-text').textContent = text; // after it shows, so it is announced
}

form.addEventListener('submit', async event => {
  event.preventDefault();
  if (busy) return;
  setBusy(true);
  $('notice').hidden = true;
  try {
    await api.post('/auth/login', { username: username.value.trim(), password: password.value, device_name: deviceName() });
    location.assign(nextPath()); // a page load: the app starts with the session
  } catch (error) {
    setBusy(false);
    if (error.code === 'invalid_credentials') {
      showNotice('Wrong username or password. Try again.');
      for (const field of form.querySelectorAll('.input')) field.classList.add('has-error');
      password.value = '';
      password.focus();
    } else {
      showNotice(error.code === 'network' ? 'Can’t reach Vibrance. Try again in a moment.' : 'Something went wrong. Try again in a moment.');
    }
  }
});

for (const input of [username, password]) {
  input.addEventListener('input', () => {
    for (const field of form.querySelectorAll('.has-error')) field.classList.remove('has-error');
  });
}

$('reveal').addEventListener('click', () => {
  const show = password.type === 'password';
  password.type = show ? 'text' : 'password';
  $('reveal').setAttribute('aria-pressed', String(show));
  $('reveal').setAttribute('aria-label', show ? 'Hide password' : 'Show password');
});

// The footer's version, from the one public endpoint.
api.get('/server').then(info => { $('version').textContent = `Vibrance ${info.version}`; }).catch(() => {});
