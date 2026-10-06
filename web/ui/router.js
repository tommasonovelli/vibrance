// Routing with the History API: paths, not hashes. The table maps a path
// pattern to the module of its view, loaded when first needed; a view
// exports `render(main, params, signal)`, where params holds the parts of the
// path (`:id`), `path` itself and `query` (URLSearchParams), and `signal` aborts when the
// reader has already gone elsewhere: pass it to the API and stop writing.
import { announce, errorNotice } from './ui.js';

let routes = [];
let main;
let controller = null;
let first = true;

const compile = ([pattern, load]) => {
  const keys = [];
  const source = pattern.replace(/:(\w+)/g, (_, key) => { keys.push(key); return '([^/]+)'; });
  return { regex: new RegExp(`^${source}/?$`), keys, load };
};

function match(path) {
  for (const route of routes) {
    const found = route.regex.exec(path);
    if (found) return { route, params: Object.fromEntries(route.keys.map((key, i) => [key, decodeURIComponent(found[i + 1])])) };
  }
  return null;
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const motion = () => !matchMedia('(prefers-reduced-motion: reduce)').matches;

async function show(href, restore) {
  controller?.abort();
  controller = new AbortController();
  const { signal } = controller;
  const url = new URL(href, location.origin);
  const found = match(url.pathname);
  const params = { ...found?.params, path: url.pathname, query: url.searchParams };
  document.dispatchEvent(new CustomEvent('route', { detail: { path: url.pathname, query: url.searchParams } }));
  main.setAttribute('aria-busy', 'true');

  const run = async () => {
    main.replaceChildren();
    // The colour field of a head (app.css §11) belongs to the page that set it.
    main.removeAttribute('data-glow');
    main.style.removeProperty('--cover');
    for (const name of ['--cover-b', '--cover-c', '--cover-d']) main.style.removeProperty(name);
    try {
      const view = await (found ? found.route.load() : import('./views/not-found.js'));
      await view.render(main, params, signal);
    } catch (error) {
      if (signal.aborted || error.name === 'AbortError') return;
      main.append(errorNotice(error, () => show(href)));
    }
  };
  let work;
  if (!first && motion() && document.startViewTransition) {
    // The page cross-fades, waiting for the new view at most 1 s, the old page staying in view meanwhile: a slow
    // answer fills the page after the transition instead of freezing it.
    const transition = document.startViewTransition(() => Promise.race([work = run(), sleep(1000)]));
    await transition.updateCallbackDone;
  } else {
    work = run();
  }
  await work;
  if (signal.aborted) return;
  main.removeAttribute('aria-busy');
  scrollTo(0, restore ? restore.y : 0);
  if (!first) {
    // A view that put the focus in one of its fields (Search, an empty playlist) keeps it.
    if (!main.contains(document.activeElement)) main.focus({ preventScroll: true });
    announce(document.title);
  }
  first = false;
}

export function navigate(href, { replace = false } = {}) {
  history[replace ? 'replaceState' : 'pushState']({ y: 0 }, '', href);
  return show(href, null);
}

export const reload = () => show(location.pathname + location.search, null);

export function start(table, element) {
  routes = Object.entries(table).map(compile);
  main = element;
  history.scrollRestoration = 'manual';

  // Each history entry remembers how far its page was scrolled.
  let saving = 0;
  addEventListener('scroll', () => {
    clearTimeout(saving);
    saving = setTimeout(() => history.replaceState({ y: scrollY }, ''), 150);
  }, { passive: true });
  // A view's module loads while the pointer is still on its link: the click finds it ready.
  document.addEventListener('pointerover', event => {
    const link = event.target.closest?.('a[href]');
    if (link && link.origin === location.origin) match(link.pathname)?.route.load().catch(() => {});
  }, { passive: true });
  addEventListener('popstate', event => show(location.pathname + location.search, event.state));

  // A link to a page of the app is followed here, without a page load.
  document.addEventListener('click', event => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = event.target.closest('a[href]');
    if (!link || link.target || link.hasAttribute('download') || link.origin !== location.origin) return;
    if (!match(link.pathname)) return;
    if (link.pathname + link.search === location.pathname + location.search && link.hash) return; // a jump inside the page
    event.preventDefault();
    const same = link.pathname + link.search === location.pathname + location.search;
    navigate(link.pathname + link.search, { replace: same });
  });

  return show(location.pathname + location.search, history.state);
}
