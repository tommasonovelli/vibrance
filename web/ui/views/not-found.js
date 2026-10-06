import { emptyState, setTitle } from '../ui.js';

export async function render(main) {
  setTitle('Page not found');
  main.append(emptyState({ icon: 'search', title: 'Page not found.', link: { href: '/', label: 'Go to Library' } }));
}
