import {dashboardViews} from './route.mjs';

const editorTags = new Set(['INPUT', 'TEXTAREA', 'SELECT']);
const editorRoles = new Set(['textbox', 'searchbox', 'combobox', 'listbox', 'spinbutton']);

export function dashboardShortcut(event, blocked = false) {
  if (
    blocked || event.defaultPrevented || event.repeat || event.isComposing || event.keyCode === 229 ||
    event.altKey || event.ctrlKey || event.metaKey || event.shiftKey ||
    typeof event.key !== 'string' || event.key.length !== 1 || !/^[1-7]$/.test(event.key)
  ) return null;
  const path = event.composedPath?.() ?? [event.target];
  for (const node of path) {
    if (
      editorTags.has(node?.tagName?.toUpperCase()) || node?.isContentEditable ||
      editorRoles.has(node?.getAttribute?.('role')) ||
      node?.getAttribute?.('contenteditable') === '' ||
      node?.getAttribute?.('contenteditable') === 'true' ||
      node?.getAttribute?.('contenteditable') === 'plaintext-only'
    ) return null;
  }
  return dashboardViews[Number(event.key) - 1];
}

export function installDashboardShortcuts({document, selectView}) {
  const blocked = () => [...document.querySelectorAll(
    'dialog[open], [role="dialog"], .pool-review:not([hidden]), .message-review:not([hidden])'
  )].some(node => node.getClientRects().length > 0);
  const onKey = event => {
    const view = dashboardShortcut(event, blocked());
    if (!view) return;
    event.preventDefault();
    selectView(view);
  };
  document.addEventListener('keydown', onKey);
  return () => document.removeEventListener('keydown', onKey);
}
