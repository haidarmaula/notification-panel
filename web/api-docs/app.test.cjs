const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { URL, URLSearchParams } = require('node:url');
async function load(query) {
  const nodes = new Map();
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, { replaceChildren(...children) { this.children = children; }, addEventListener(name, listener) { this[name] = listener; }, classList: { add() {} } });
    return nodes.get(id);
  };
  const guides = [{ id: 'staff', title: 'Staff & campaigns', file: 'staff.json', operations: 38 }, { id: 'mobile', title: 'Mobile & receipts', file: 'mobile.json', operations: 9 }];
  const calls = []; let config;
  const window = { location: { origin: 'https://staging.test', search: query }, SwaggerUIBundle(value) { config = value; value.onComplete(); } };
  const document = { body: { dataset: { docsBase: '/docs/releases/reference' } }, getElementById: node, createElement: () => ({}) };
  await vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8'), { window, document, URL, URLSearchParams, fetch: async (url, options) => {
    calls.push({ url, options });
    return { ok: true, json: async () => url.endsWith('catalog.json') ? { guides } : { info: { title: 'Original' }, paths: {} } };
  } });
  return { config, calls, nodes, window };
}
test('loads only local docs and keeps authentication ephemeral', async () => {
  const result = await load('?api=mobile');
  assert.equal(result.config.spec.info.title, 'Mobile & receipts');
  assert.equal(result.config.persistAuthorization, false);
  assert.equal(result.config.validatorUrl, null);
  assert.equal(result.config.queryConfigEnabled, false);
  assert.ok(result.calls.every(call => call.url.startsWith('/docs/releases/reference/') && call.options.credentials === 'omit'));
  assert.equal(result.config.spec.servers[0].url, 'https://staging.test');
});
test('rejects foreign targets and allows same-origin API requests', async () => {
  const { config } = await load('');
  for (const url of ['https://evil.test/api/v1/apps', 'https://user:pass@staging.test/api/v1/apps', '/docs/private']) assert.throws(() => config.requestInterceptor({ url }));
  const request = config.requestInterceptor({ url: '/api/v1/apps' });
  assert.equal(request.credentials, 'omit');
});
test('unknown guide parameters cannot inject a specification URL', async () => {
  const result = await load('?api=https://evil.test/schema');
  assert.equal(result.nodes.get('api-picker').value, 'staff');
  assert.ok(result.calls[1].url.endsWith('/staff.json'));
});
