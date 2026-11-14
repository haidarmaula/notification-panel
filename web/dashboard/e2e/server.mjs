// Isolated end-to-end fixture. This process never contacts staging or a provider.
import { createServer } from 'node:https';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { createAdminFixture } from './admin-fixture.mjs';
const directory = mkdtempSync(join(tmpdir(), 'hello-dashboard-e2e-'));
const appId = '11111111-1111-4111-8111-111111111111';
const adminFixture = createAdminFixture();
const app = {
  id: appId,
  key: 'fixture-app',
  name: 'Aplikasi Uji',
  status: 'ACTIVE',
  revision: '1',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
};
const campaigns = [
  {
    id: '9007199254740993',
    title: 'Pengingat jadwal layanan',
    body: 'Layanan tersedia kembali besok pukul 09.00.',
    status: 'DRAFT',
    template_id: null,
    template_name: null,
    targets: [{ type: 'INDIVIDUAL', id: '42' }],
  },
];
const templates = [
  {
    id: '3',
    name: 'Pengingat',
    title_template: 'Pengingat layanan',
    body_template: 'Buka aplikasi untuk melihat informasi.',
    is_active: true,
  },
];
const segments = [{ id: '7', name: 'Segmen Uji', description: null }];
const members = new Map([['7', []]]);
const users = [
  {
    id: '42',
    external_id: 'fixture-recipient',
    name: 'Penerima Uji',
    email: null,
    status: 'ACTIVE',
  },
];
const sessions = new Set();
let sequence = 0;
function send(response, status, data) {
  response.writeHead(status, { 'Content-Type': 'application/json' });
  response.end(
    status === 204
      ? undefined
      : JSON.stringify({
          success: status < 400,
          message: status < 400 ? 'success' : 'not available',
          data,
        }),
  );
}
writeFileSync(
  join(directory, 'cert.cnf'),
  '[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n[dn]\nCN=127.0.0.1\n[ext]\nsubjectAltName=IP:127.0.0.1\n',
);
execFileSync(
  'openssl',
  [
    'req',
    '-x509',
    '-newkey',
    'rsa:2048',
    '-nodes',
    '-days',
    '1',
    '-config',
    join(directory, 'cert.cnf'),
    '-keyout',
    join(directory, 'key.pem'),
    '-out',
    join(directory, 'cert.pem'),
  ],
  { stdio: 'ignore' },
);
const api = createServer(
  {
    key: readFileSync(join(directory, 'key.pem')),
    cert: readFileSync(join(directory, 'cert.pem')),
  },
  async (request, response) => {
    const url = new URL(request.url, 'https://127.0.0.1:8091');
    if (request.headers['x-api-key'] !== 'isolated-test-key') {
      send(response, 401, {});
      return;
    }
    const parts = [];
    for await (const part of request) parts.push(part);
    const text = Buffer.concat(parts).toString();
    let body;
    try {
      body = text ? JSON.parse(text) : {};
    } catch {
      send(response, 400, {});
      return;
    }
    if (url.pathname === '/api/v1/auth/login') {
      if (body.email !== 'staff@example.invalid' || body.password !== 'fixture-only') {
        send(response, 401, {});
        return;
      }
      const token = `fixture-access-${++sequence}`;
      sessions.add(token);
      send(response, 200, { access_token: token, refresh_token: `fixture-refresh-${sequence}` });
      return;
    }
    if (url.pathname === '/api/v1/auth/logout') {
      sessions.clear();
      send(response, 200, {});
      return;
    }
    if (!sessions.has((request.headers.authorization ?? '').replace('Bearer ', ''))) {
      send(response, 401, {});
      return;
    }
    if (adminFixture.handle(request, response, url, body, text)) return;
    if (url.pathname === '/api/v1/apps') {
      send(response, 200, { items: [app], total: 1, page: 1, limit: 20 });
      return;
    }
    if (url.pathname === `/api/v1/apps/${appId}`) {
      send(response, 200, app);
      return;
    }
    const base = `/api/v1/apps/${appId}`;
    if (url.pathname === `${base}/users`) {
      send(response, 200, { items: users, next_after_id: null });
      return;
    }
    if (url.pathname === `${base}/users/42`) {
      send(response, 200, users[0]);
      return;
    }
    if (url.pathname === `${base}/users/42/devices`) {
      send(response, 200, {
        items: [
          { id: '8', platform: 'ANDROID', is_active: true, last_seen_at: '2026-11-13T00:00:00Z' },
        ],
        next_after_id: null,
      });
      return;
    }
    const directory = url.pathname
      .slice(base.length)
      .match(/^\/(templates|segments)(?:\/(\d+))?(?:\/members(?:\/(\d+))?)?$/);
    if (directory) {
      const collection = directory[1] === 'templates' ? templates : segments;
      const id = directory[2];
      const userID = directory[3];
      const item = collection.find((item) => item.id === id);
      if (id && !item) {
        send(response, 404, {});
        return;
      }
      if (url.pathname.includes('/members')) {
        const group = members.get(id) ?? [];
        if (userID) {
          const index = group.findIndex((member) => member.user_id === userID);
          if (request.method === 'PUT' && index < 0) {
            const user = users.find((user) => user.id === userID);
            group.push({ ...user, id: String(group.length + 1), user_id: userID });
          }
          if (request.method === 'DELETE' && index >= 0) group.splice(index, 1);
          members.set(id, group);
          send(response, 204);
          return;
        }
        send(response, 200, { items: group, next_after_id: null });
        return;
      }
      if (request.method === 'POST') {
        const created = { ...body, id: String(100 + collection.length) };
        collection.push(created);
        send(response, 201, created);
        return;
      }
      if (request.method === 'PUT') {
        Object.assign(item, body);
        send(response, 200, item);
        return;
      }
      if (request.method === 'DELETE') {
        collection.splice(collection.indexOf(item), 1);
        members.delete(id);
        send(response, 204);
        return;
      }
      send(response, 200, id ? item : { items: collection, next_after_id: null });
      return;
    }
    if (url.pathname === `${base}/campaigns`) {
      if (request.method === 'POST') {
        const campaign = {
          ...body,
          id: String(100 + campaigns.length),
          status: 'DRAFT',
          template_name: null,
        };
        campaigns.push(campaign);
        send(response, 201, { id: campaign.id, status: campaign.status });
        return;
      }
      send(response, 200, {
        items: campaigns.map(({ id, title, status }) => ({ id, title, status })),
        next_after_id: null,
      });
      return;
    }
    const match = url.pathname.match(/\/campaigns\/(\d+)(?:\/(\w+))?$/);
    if (match) {
      const index = campaigns.findIndex((item) => item.id === match[1]);
      if (index === -1) {
        send(response, 404, {});
        return;
      }
      const campaign = campaigns[index];
      if (match[2] === 'send' || match[2] === 'schedule') {
        if (campaign.title === 'Fixture acceptance') {
          campaign.status = 'QUEUED';
          send(response, 202, { id: campaign.id });
          return;
        }
        send(response, 503, {});
        return;
      }
      if (match[2] === 'statistics') {
        send(response, 200, {
          schema_version: 1,
          snapshot_complete: false,
          observation_coverage: 'reporting_devices_only',
          counts: null,
        });
        return;
      }
      if (match[2] === 'deliveries' || match[2] === 'reads') {
        send(response, 200, { items: [], next_after_id: null });
        return;
      }
      if (request.method === 'PUT') {
        Object.assign(campaign, body);
        send(response, 200, { id: campaign.id, status: campaign.status });
        return;
      }
      if (request.method === 'DELETE') {
        campaigns.splice(index, 1);
        send(response, 204);
        return;
      }
      send(response, 200, campaign);
      return;
    }
    send(response, 404, {});
  },
);
writeFileSync(join(directory, 'api-key'), 'isolated-test-key', { mode: 0o600 });
writeFileSync(
  join(directory, 'dashboard.json'),
  JSON.stringify({
    origin: 'http://127.0.0.1:4173',
    upstream: 'https://127.0.0.1:8091',
    api_key_file: join(directory, 'api-key'),
    upstream_ca_file: join(directory, 'cert.pem'),
    listen_address: '127.0.0.1:4173',
    assets_directory: resolve('dist'),
    development: true,
    max_sessions: 100,
  }),
  { mode: 0o600 },
);
execFileSync('go', ['build', '-o', join(directory, 'dashboard'), './cmd/dashboard'], {
  cwd: resolve('../..'),
  stdio: 'inherit',
});
let bff;
api.listen(8091, '127.0.0.1', () => {
  bff = spawn(join(directory, 'dashboard'), {
    stdio: 'inherit',
    env: { ...process.env, HELLO_DASHBOARD_CONFIG: join(directory, 'dashboard.json') },
  });
  bff.on('exit', (code) => {
    if (code) process.exit(code);
  });
});
let closing = false;
function cleanup() {
  if (closing) return;
  closing = true;
  bff?.kill('SIGTERM');
  api.close();
  rmSync(directory, { recursive: true, force: true });
  setTimeout(() => process.exit(), 200).unref();
}
process.on('SIGINT', cleanup);
process.on('SIGTERM', cleanup);
process.on('exit', () => {
  bff?.kill('SIGTERM');
  rmSync(directory, { recursive: true, force: true });
});
