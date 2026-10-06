// The remembered theme and sidebar state, put on <html> before the first
// paint so nothing flashes or moves while the app starts. A classic script
// in <head>, not a module: a module would run after the first paint. app.js
// reads the same two keys and changes them; without storage the theme is
// dark and the sidebar open. The queue panel, too: open or closed as it was
// left (queue.js), so the page does not narrow after it has painted.
(() => {
  const root = document.documentElement;
  try {
    const theme = localStorage.getItem('vibrance.theme');
    root.dataset.theme = theme === 'light' ? 'light' : 'dark';
    if (localStorage.getItem('vibrance.sidebar') === 'collapsed') root.dataset.sidebar = 'collapsed';
    if (localStorage.getItem('vibrance.queue') === 'open') root.dataset.queue = 'open';
  } catch { /* no storage: the defaults */ }
  // Now playing takes the whole window: the sidebar and the bar are never drawn
  // there, so a reload on it does not draw them first and then take them away.
  if (location.pathname === '/now-playing') root.dataset.now = '';
})();
