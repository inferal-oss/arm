// A DOM-only renderer: values are text, never HTML. Children are materialized
// on expansion and paged so a large response cannot create an unbounded tree.
export function jsonView(value, {label = 'JSON', open = false, document = globalThis.document, pageSize = 100, nodeLimit = 5000, expansion = new Map()} = {}) {
  const element = (tag, className = '', text = null) => {
    const el = document.createElement(tag); el.className = className;
    if (text !== null) el.textContent = text;
    return el;
  };
  const root = element('div', 'json-view');
  root.setAttribute('role', 'group'); root.setAttribute('aria-label', label);
  const branches = [];
  let nodes = 0;
  function branch(value, key, depth, path = []) {
    nodes++;
    const prefix = () => element('span', 'json-key', key);
    const composite = value !== null && typeof value === 'object';
    if (!composite) {
      const row = element('div', 'json-leaf'), type = value === null ? 'null' : typeof value;
      const text = type === 'string' ? JSON.stringify(value) : String(value);
      row.append(prefix(), element('span', 'json-colon', ': '), element('span', 'json-value json-' + type, text));
      return row;
    }
    const array = Array.isArray(value), keys = Object.keys(value);
    const detail = element('details', 'json-branch'), summary = element('summary'), children = element('div', 'json-children');
    branches.push(detail);
    const identity = JSON.stringify(path);
    detail.open = expansion.has(identity) ? expansion.get(identity) : depth === 0 && open;
    summary.append(prefix(), element('span', 'json-kind', ` ${array ? '[…]' : '{…}'} `), element('span', 'json-count', `${keys.length} ${array ? 'items' : 'fields'}`));
    detail.append(summary, children);
    let cursor = 0, loaded = false;
    const more = element('button', 'json-more', 'Show more'); more.type = 'button';
    function load() {
      loaded = true; more.remove();
      const end = Math.min(keys.length, cursor + pageSize);
      while (cursor < end && nodes < nodeLimit && depth < 64) {
        const key = keys[cursor++]; children.append(branch(value[key], array ? `[${key}]` : JSON.stringify(key), depth + 1, [...path, key]));
      }
      if (cursor < keys.length) {
        if (nodes >= nodeLimit || depth >= 64) children.append(element('p', 'json-limit', 'Tree display limit reached. Inspect a smaller record for the remaining values.'));
        else { more.textContent = `Show next ${Math.min(pageSize, keys.length - cursor)} (${keys.length - cursor} remaining)`; children.append(more); }
      }
      if (!keys.length) children.append(element('span', 'json-empty', array ? '[]' : '{}'));
    }
    more.addEventListener('click', load);
    detail.addEventListener('toggle', () => { expansion.set(identity, detail.open); if (detail.open && !loaded) load(); });
    if (detail.open) load();
    return detail;
  }
  const tree = branch(value, label, 0);
  if (value !== null && typeof value === 'object') {
    const controls = element('div', 'json-controls');
    const expand = element('button', '', 'Expand root'), collapse = element('button', '', 'Collapse all');
    expand.type = collapse.type = 'button';
    expand.addEventListener('click', () => { tree.open = true; });
    collapse.addEventListener('click', () => { for (const branch of branches) branch.open = false; });
    controls.append(expand, collapse); root.append(controls);
  }
  root.append(tree);
  return root;
}
