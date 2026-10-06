// The remembered theme and sidebar state, put on <html> before the first
// paint so nothing flashes or moves while the app starts. A classic script
// in <head>, not a module: a module would run after the first paint. app.js
// reads the same two keys and changes them; without storage the theme is
// dark and the sidebar open.
(() => {
  const root = document.documentElement;
  try {
    const theme = localStorage.getItem('vibrance.theme');
    root.dataset.theme = theme === 'light' ? 'light' : 'dark';
    if (localStorage.getItem('vibrance.sidebar') === 'collapsed') root.dataset.sidebar = 'collapsed';
  } catch { /* no storage: the defaults */ }
})();
