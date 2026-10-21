import {test} from 'node:test';
import assert from 'node:assert/strict';
import {jsonView} from './json-view.mjs';

// A small renderer fixture, not browser acceptance or a browser driver.
class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.attributes = {}; this.events = {}; this._text = ''; this._open = false; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  remove() { if (this.parent) this.parent.children = this.parent.children.filter(x => x !== this); }
  setAttribute(k, v) { this.attributes[k] = v; }
  addEventListener(type, fn) { (this.events[type] ||= []).push(fn); }
  fire(type) { for (const fn of this.events[type] || []) fn(); }
  set textContent(v) { this._text = v; this.children = []; }
  get textContent() { return this._text + this.children.map(x => x.textContent).join(''); }
  set open(value) { this._open = value; this.fire('toggle'); }
  get open() { return this._open; }
}
const document = {createElement: tag => new Element(tag)};
const all = (root, predicate) => [...(predicate(root) ? [root] : []), ...root.children.flatMap(x => all(x, predicate))];

test('objects and arrays expand independently, collapse and retain exact values', () => {
  const tree = jsonView({count: 9223372036854775807n, nested: {list: [1, null, '<script>literal</script>']}}, {document, open: true});
  assert.match(tree.textContent, /9223372036854775807/);
  const nested = all(tree, x => x.tagName === 'details')[1];
  assert.equal(nested.open, false); assert.doesNotMatch(tree.textContent, /<script>/);
  nested.open = true;
  const array = all(tree, x => x.tagName === 'details')[2]; array.open = true;
  assert.match(tree.textContent, /"<script>literal<\/script>"/);
  assert.equal(all(tree, x => x.tagName === 'script').length, 0);
  all(tree, x => x.tagName === 'button' && x.textContent === 'Collapse all')[0].fire('click');
  assert.ok(all(tree, x => x.tagName === 'details').every(x => !x.open));
});
test('large arrays are lazy, paginated, bounded and show empty collections', () => {
  const tree = jsonView(Array.from({length: 205}, (_, i) => i), {document});
  assert.equal(all(tree, x => x.className === 'json-leaf').length, 0);
  all(tree, x => x.tagName === 'button' && x.textContent === 'Expand root')[0].fire('click');
  assert.equal(all(tree, x => x.className === 'json-leaf').length, 100);
  all(tree, x => x.className === 'json-more')[0].fire('click');
  assert.equal(all(tree, x => x.className === 'json-leaf').length, 200);
  all(tree, x => x.className === 'json-more')[0].fire('click');
  assert.equal(all(tree, x => x.className === 'json-leaf').length, 205);
  const limited = jsonView([1, 2, 3, 4], {document, open: true, nodeLimit: 3});
  assert.equal(all(limited, x => x.className === 'json-leaf').length, 2);
  assert.match(limited.textContent, /display limit/);
  assert.match(jsonView([], {document, open: true}).textContent, /\[\]/);
});

test('expansion choices survive record refresh and unusual keys do not collide', () => {
  const expansion = new Map(), value = {'a.b': {value: 1}, a: {b: 2}};
  const first = jsonView(value, {document, open: true, expansion});
  all(first, x => x.tagName === 'details')[1].open = true;
  const refreshed = jsonView(value, {document, open: true, expansion});
  const details = all(refreshed, x => x.tagName === 'details');
  assert.equal(details[1].open, true);
  assert.equal(details[2].open, false);
  details[0].open = false;
  assert.equal(all(jsonView(value, {document, open: true, expansion}), x => x.tagName === 'details')[0].open, false);
});
