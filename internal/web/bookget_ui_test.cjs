const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
class Element {
  constructor() { this.children = []; this.listeners = {}; this.textContent = ''; this.value = ''; this.disabled = false; this.attributes = {}; }
  set innerHTML(_) { throw new Error('untrusted metadata must never become HTML'); }
  append(...elements) { this.children.push(...elements); }
  replaceChildren(...elements) { this.children = elements; }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  querySelectorAll() { return this.children.flatMap(el => [el, ...el.querySelectorAll()]); }
}
function rig(fetch) {
  const elements = Object.fromEntries(['catalog-search', 'catalog-query', 'catalog-format', 'catalog-language', 'catalog-results', 'catalog-status'].map(id => [id, new Element()]));
  elements['catalog-query'].value = 'Dom Casmurro'; elements['catalog-format'].value = 'epub';
  let refreshed = 0;
 let tick;
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'static/bookget.js'), 'utf8'), {
    document: { getElementById: id => elements[id], createElement: () => new Element() },
    fetch, URLSearchParams, setInterval: fn => { tick = fn; return 1; }, clearInterval: () => { tick = undefined; }, t: key => key, refreshLibrary: () => { refreshed++; },
  });
  return { elements, search: () => elements['catalog-search'].listeners.submit({ preventDefault() {} }), refreshed: () => refreshed, poll: () => tick?.() };
}
const book = { Title: '<script>alert(1)</script>', Author: 'Machado', Source: 'Project Gutenberg', Formats: { epub: 'https://untrusted.test/book' }, token: 'server-token' };
test('search, choose, download and refresh through local API; metadata stays text', async () => {
  const calls = [];
  const r = rig(async (url, options) => {
    calls.push([url, options]);
    return { ok: true, json: async () => options.method ? { path: '/Downloads/books/Book.epub', sent: true, messages: [] } : { books: [book], warnings: '' } };
  });
  await r.search();
  const li = r.elements['catalog-results'].children[0];
  assert.match(li.children[0].textContent, /<script>/);
  assert.equal(li.children[1].disabled, false);
  await li.children[1].listeners.click();
  assert.match(calls[0][0], /^api\/books\/search\?q=Dom\+Casmurro$/);
  assert.equal(calls[1][0], 'api/books/get');
  assert.deepEqual(JSON.parse(calls[1][1].body), { token: 'server-token', format: 'epub' });
  assert.equal(r.refreshed(), 1);
  assert.match(r.elements['catalog-status'].textContent, /catalog.sent/);
});
test('offline Kindle reports local save and does not refresh device', async () => {
  const r = rig(async (_, options) => ({ ok: true, json: async () => options.method ? { path: '/saved/book.epub', sent: false, messages: ['not linked'] } : { books: [book] } }));
  await r.search(); await r.elements['catalog-results'].children[0].children[1].listeners.click();
  assert.match(r.elements['catalog-status'].textContent, /catalog.saved.*not linked/); assert.equal(r.refreshed(), 0);
});
test('API errors are visible and controls recover', async () => {
  const r = rig(async () => ({ ok: false, json: async () => ({ message: 'bookget is missing' }) }));
  await r.search(); assert.equal(r.elements['catalog-status'].textContent, 'bookget is missing');
  assert.equal(r.elements['catalog-results'].attributes['aria-busy'], 'false');
});
test('blank searches do nothing; repeated submits cannot start parallel jobs', async () => {
  let finish, calls = 0;
  const r = rig(() => { calls++; return new Promise(resolve => { finish = () => resolve({ ok: true, json: async () => ({ books: [] }) }); }); });
  r.elements['catalog-query'].value = ' '; await r.search(); assert.equal(calls, 0);
  r.elements['catalog-query'].value = 'a'; const pending = r.search(); await r.search(); assert.equal(calls, 1);
  finish(); await pending; assert.match(r.elements['catalog-status'].textContent, /catalog.empty/);
});

test('live browser verification progress appears without replacing the final result', async () => {
  let finish;
  const r = rig(async (url) => {
    if (url === 'api/books/status') return { ok: true, json: async () => ({ busy: true, message: 'Aguardando verificação no navegador' }) };
    return new Promise(resolve => { finish = () => resolve({ ok: true, json: async () => ({ books: [book] }) }); });
  });
  const pending = r.search(); await r.poll();
  assert.equal(r.elements['catalog-status'].textContent, 'Aguardando verificação no navegador');
  finish(); await pending; await r.poll();
  assert.match(r.elements['catalog-status'].textContent, /catalog.choose/);
});

test('selected book language reaches search independently of title', async () => {
  let requested;
  const r = rig(async url => { requested = url; return { ok: true, json: async () => ({ books: [] }) }; });
  r.elements['catalog-query'].value = 'Verity';
  r.elements['catalog-language'].value = 'pt';
  await r.search();
  assert.equal(requested, 'api/books/search?q=Verity&language=pt');
  r.elements['catalog-language'].value = '';
  await r.search();
  assert.equal(requested, 'api/books/search?q=Verity');
});
