// The only module that calls fetch. Every path is under /api/v1; JSON in,
// JSON out; every error is an ApiError with the stable code of the contract.
const BASE = '/api/v1';

export class ApiError extends Error {
  constructor(status, code, message, details = {}, request = '') {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.details = details;
    this.request = request; // "GET /albums", for the Details of an error notice
  }
}

// A path the server does not have yet answers 404 not_found: the caller
// shows nothing in its place instead of an error.
export const isMissing = e => e instanceof ApiError && e.code === 'not_found';
// A playlist change made on an old revision (412): read it again.
export const isStale = e => e instanceof ApiError && e.code === 'precondition_failed';
export const optional = promise => promise.catch(e => { if (isMissing(e)) return null; throw e; });

// Whether the last request got an answer, any answer: the banner of status.js
// says "Can't reach Vibrance" while it did not. Told once per change.
let reachable = true;
function reach(ok) {
  if (ok === reachable) return;
  reachable = ok;
  document.dispatchEvent(new CustomEvent(ok ? 'net:up' : 'net:down'));
}

function url(path, query) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query || {})) {
    if (value !== null && value !== undefined && value !== '') params.set(key, value);
  }
  const text = params.toString();
  return BASE + path + (text ? '?' + text : '');
}

function signIn() {
  // On the sign-in page a 401 is the answer to be shown, not a redirect.
  if (location.pathname === '/login') return null;
  location.assign('/login?next=' + encodeURIComponent(location.pathname + location.search));
  return new Promise(() => {}); // the page is leaving: nothing after this runs
}

async function request(method, path, { query, body, ifMatch, signal, keepalive } = {}) {
  const label = `${method} ${path}`;
  const headers = {};
  // Every request other than GET and HEAD carries it, sign-in included.
  if (method !== 'GET' && method !== 'HEAD') headers['X-Vibrance-Request'] = '1';
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (ifMatch) headers['If-Match'] = ifMatch;
  let response;
  try {
    response = await fetch(url(path, query), { method, headers, signal, keepalive, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (e) {
    if (e.name === 'AbortError') throw e;
    reach(false);
    throw new ApiError(0, 'network', 'Can’t reach Vibrance.', {}, label);
  }
  reach(true);
  let data = null;
  if (response.status !== 204 && /json/.test(response.headers.get('Content-Type') || '')) {
    try { data = await response.json(); } catch { /* an empty or broken body: handled below */ }
  }
  if (response.ok) {
    // A page of playlist items has no etag field: the header is the
    // playlist's tag, which tells that the playlist changed between two pages.
    if (data && !('etag' in data) && response.headers.has('ETag')) {
      Object.defineProperty(data, 'etag', { value: response.headers.get('ETag') });
    }
    return data;
  }
  const error = new ApiError(response.status, data?.code || 'internal', data?.message || response.statusText, data?.details || {}, label);
  if (error.code === 'login_required') {
    const pending = signIn();
    if (pending) return pending;
  }
  throw error;
}

export const api = {
  get: (path, query, options) => request('GET', path, { ...options, query }),
  post: (path, body, options) => request('POST', path, { ...options, body }),
  put: (path, body, options) => request('PUT', path, { ...options, body }),
  del: (path, options) => request('DELETE', path, options),
};

// Walks a paginated list page by page: `for await (const page of walk('/albums', {sort: 'year'}))`,
// each page being the answer as the contract gives it ({albums: [...], next}).
export async function* walk(path, query = {}, { signal, limit = 200 } = {}) {
  let after = null;
  do {
    const page = await api.get(path, { ...query, limit, after }, { signal });
    yield page;
    after = page.next;
  } while (after);
}

// The URL of a cover at a size: 256 for rows, 640 for grids and heads,
// 'original' only to download. A missing cover is null.
export function coverUrl(cover, size = 256) {
  if (!cover) return null;
  return cover.url + (cover.url.includes('?') ? '&' : '?') + 'size=' + size;
}

// The file of a track, as it is: an <audio src> needs nothing else, the
// session cookie goes with it. `Range` is the browser's business.
export const audioUrl = track => `${BASE}/tracks/${track.id}/audio`;
