const appID = '11111111-1111-4111-8111-111111111111';
const largeStaffID = '9007199254740993';

function envelope(response, status, data, headers = {}) {
  response.writeHead(status, { 'Content-Type': 'application/json', ...headers });
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

function raw(response, status, value, headers = {}) {
  response.writeHead(status, { 'Content-Type': 'application/json', ...headers });
  response.end(JSON.stringify(value));
}

function error(response, status, message) {
  raw(response, status, { success: false, message });
}

function staffRow(id, name, email, role = 'OPERATOR', active = true) {
  const roleIDs = { SUPER_ADMIN: 1, ADMIN: 2, OPERATOR: 3, VIEWER: 4 };
  return {
    id: typeof id === 'string' ? id : Number(id),
    role_id: roleIDs[role],
    role_name: role,
    name,
    email,
    is_active: active,
    created_at: '2026-11-14T00:00:00Z',
    updated_at: '2026-11-14T00:00:00Z',
  };
}

function encodeStaff(staff) {
  if (String(staff.id) !== largeStaffID) return JSON.stringify(staff);
  const copy = { ...staff, id: '__LARGE_STAFF_ID__' };
  return JSON.stringify(copy).replace('"__LARGE_STAFF_ID__"', largeStaffID);
}

export function createAdminFixture() {
  const staff = new Map([
    ['1', staffRow(1, 'Staf Pengujian', 'staff@example.invalid', 'SUPER_ADMIN')],
    [largeStaffID, staffRow(largeStaffID, 'Akun ID besar', 'large@example.invalid')],
  ]);
  const applications = new Map([
    [
      appID,
      {
        id: appID,
        key: 'fixture-app',
        name: 'Aplikasi Uji',
        status: 'ACTIVE',
        revision: '1',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      },
    ],
  ]);
  const grants = new Map();
  let nextStaffID = 2;
  let nextApplicationID = 1;

  function appETag(app) {
    return `"app:${app.id}:${app.revision}"`;
  }
  function grantETag(app, grant) {
    return `"grant:${app.id}:${grant.staff_id}:${grant.revision}"`;
  }
  function grantMap(id) {
    if (!grants.has(id)) grants.set(id, new Map());
    return grants.get(id);
  }
  function matchPage(url, rows) {
    const page = Number(url.searchParams.get('page') ?? 1);
    const limit = Number(url.searchParams.get('limit') ?? 20);
    const start = (page - 1) * limit;
    return { items: rows.slice(start, start + limit), total: rows.length, page, limit };
  }

  return {
    handle(request, response, url, body, rawBody) {
      const path = url.pathname;
      if (path === '/api/v1/profile') {
        if (request.method === 'GET') {
          envelope(response, 200, staff.get('1'));
          return true;
        }
        if (request.method === 'PATCH') {
          const actor = staff.get('1');
          if (body.name) actor.name = body.name;
          if (body.email) actor.email = body.email;
          envelope(response, 200, actor);
          return true;
        }
      }

      if (path === '/api/v1/profile/password' && request.method === 'PATCH') {
        if (body.current_password !== 'fixture-only') {
          error(response, 401, 'current password is incorrect');
          return true;
        }
        if (!body.new_password) {
          error(response, 400, 'new password is required');
          return true;
        }
        envelope(response, 200, null);
        return true;
      }

      if (path === '/api/v1/staff') {
        if (request.method === 'GET') {
          const search = url.searchParams.get('search')?.toLowerCase() ?? '';
          const rows = [...staff.values()].filter((item) =>
            `${item.name} ${item.email}`.toLowerCase().includes(search),
          );
          const page = Number(url.searchParams.get('page') ?? 1);
          const limit = Number(url.searchParams.get('limit') ?? 10);
          const values = rows.slice((page - 1) * limit, page * limit);
          const serialized = values.map(encodeStaff).join(',');
          const data = JSON.stringify({
            total: rows.length,
            page,
            limit,
            total_pages: Math.ceil(rows.length / limit),
          });
          const detail = data.slice(1, -1);
          // Preserve an int64 value above JavaScript's safe integer limit in the wire fixture.
          response.writeHead(200, { 'Content-Type': 'application/json' });
          response.end(
            `{"success":true,"message":"success","data":{"data":[${serialized}],${detail}}}`,
          );
          return true;
        }
        if (request.method === 'POST') {
          const id = nextStaffID++;
          const created = staffRow(id, body.name, body.email, body.role);
          staff.set(String(id), created);
          envelope(response, 201, created);
          return true;
        }
      }

      const staffMatch = path.match(/^\/api\/v1\/staff\/([1-9][0-9]*)(?:\/(status|password))?$/);
      if (staffMatch) {
        const id = staffMatch[1];
        const action = staffMatch[2];
        const member = staff.get(id);
        if (!member) {
          error(response, 404, 'staff not found');
          return true;
        }
        if (!action && request.method === 'GET') {
          const serialized = encodeStaff(member);
          response.writeHead(200, { 'Content-Type': 'application/json' });
          response.end(`{"success":true,"message":"success","data":${serialized}}`);
          return true;
        }
        if (!action && request.method === 'PATCH') {
          if (body.name) member.name = body.name;
          if (body.email) member.email = body.email;
          if (body.role) member.role_name = body.role;
          envelope(response, 200, member);
          return true;
        }
        if (action === 'status' && request.method === 'PATCH') {
          member.is_active = body.is_active;
          envelope(response, 200, member);
          return true;
        }
        if (action === 'password' && request.method === 'PATCH') {
          if (body.current_password !== 'fixture-only') {
            error(response, 403, 'current password is incorrect');
            return true;
          }
          envelope(response, 200, null);
          return true;
        }
        if (!action && request.method === 'DELETE') {
          if (id === '1') {
            error(response, 403, 'cannot delete your own account');
            return true;
          }
          staff.delete(id);
          envelope(response, 200, null);
          return true;
        }
      }

      if (path === '/api/v1/admin/apps' && request.method === 'GET') {
        const key = url.searchParams.get('key');
        const rows = [...applications.values()].filter((item) => !key || item.key === key);
        envelope(response, 200, matchPage(url, rows));
        return true;
      }
      if (path === '/api/v1/admin/apps' && request.method === 'POST') {
        const id = `22222222-2222-4222-8222-${String(nextApplicationID++).padStart(12, '0')}`;
        const app = {
          id,
          key: body.key,
          name: body.name,
          status: 'PROVISIONING',
          revision: '1',
          created_at: '2026-11-14T00:00:00Z',
          updated_at: '2026-11-14T00:00:00Z',
        };
        if ([...applications.values()].some((item) => item.key === app.key)) {
          error(response, 409, 'application key already exists');
          return true;
        }
        applications.set(id, app);
        envelope(response, 201, app, { ETag: appETag(app), Location: `/api/v1/admin/apps/${id}` });
        return true;
      }

      const managed = path.match(
        /^\/api\/v1\/admin\/apps\/([0-9a-f-]+)(?:\/staff-grants(?:\/([1-9][0-9]*))?)?$/,
      );
      if (managed) {
        const app = applications.get(managed[1]);
        if (!app) {
          error(response, 404, 'application not found');
          return true;
        }
        const hasGrantPath = path.includes('/staff-grants');
        const staffID = managed[2];
        const grantRows = grantMap(app.id);
        if (hasGrantPath && !staffID && request.method === 'GET') {
          envelope(response, 200, matchPage(url, [...grantRows.values()]));
          return true;
        }
        if (!hasGrantPath && request.method === 'GET') {
          envelope(response, 200, app, { ETag: appETag(app) });
          return true;
        }
        if (!hasGrantPath && !staffID && request.method === 'PATCH') {
          const current = appETag(app);
          if (!request.headers['if-match']) {
            error(response, 428, 'If-Match required');
            return true;
          }
          if (request.headers['if-match'] !== current) {
            error(response, 412, 'application changed; reload and review');
            return true;
          }
          app.name = body.name;
          app.revision = String(Number(app.revision) + 1);
          app.updated_at = '2026-11-14T00:01:00Z';
          envelope(response, 200, app, { ETag: appETag(app) });
          return true;
        }
        if (staffID && request.method === 'GET') {
          const grant = grantRows.get(staffID);
          if (!grant) {
            error(response, 404, 'grant not found');
            return true;
          }
          envelope(response, 200, grant, { ETag: grantETag(app, grant) });
          return true;
        }
        if (staffID && request.method === 'PUT') {
          const existing = grantRows.get(staffID);
          if (!existing) {
            if (!request.headers['if-none-match']) {
              error(response, 428, 'If-None-Match required');
              return true;
            }
            if (request.headers['if-none-match'] !== '*') {
              error(response, 412, 'grant row already exists; reload and review');
              return true;
            }
            if (!staff.has(staffID)) {
              error(response, 404, 'staff not found');
              return true;
            }
            const grant = {
              application_id: app.id,
              staff_id: staffID,
              is_active: true,
              revision: '1',
              created_at: '2026-11-14T00:00:00Z',
              updated_at: '2026-11-14T00:00:00Z',
            };
            grantRows.set(staffID, grant);
            envelope(response, 201, grant, { ETag: grantETag(app, grant) });
            return true;
          }
          if (request.headers['if-none-match']) {
            error(response, 412, 'grant row already exists; reload and review');
            return true;
          }
          const current = grantETag(app, existing);
          if (!request.headers['if-match']) {
            error(response, 428, 'If-Match required');
            return true;
          }
          if (request.headers['if-match'] !== current) {
            error(response, 412, 'grant changed; reload and review');
            return true;
          }
          existing.is_active = true;
          existing.revision = String(Number(existing.revision) + 1);
          envelope(response, 200, existing, { ETag: grantETag(app, existing) });
          return true;
        }
        if (staffID && request.method === 'DELETE') {
          const grant = grantRows.get(staffID);
          if (!grant) {
            error(response, 404, 'grant not found');
            return true;
          }
          const current = grantETag(app, grant);
          if (!request.headers['if-match']) {
            error(response, 428, 'If-Match required');
            return true;
          }
          if (request.headers['if-match'] !== current) {
            error(response, 412, 'grant changed; reload and review');
            return true;
          }
          if (rawBody !== '') {
            error(response, 400, 'DELETE body must be empty');
            return true;
          }
          grant.is_active = false;
          grant.revision = String(Number(grant.revision) + 1);
          envelope(response, 204, null);
          return true;
        }
        return false;
      }

      const scoped = path.match(new RegExp(`^/api/v1/apps/${appID}(?:/(.*))?$`));
      if (scoped?.[1] === 'audit-logs' && request.method === 'GET') {
        envelope(response, 200, {
          items: [
            {
              id: '18',
              actor_id: '1',
              action: 'APPLICATION_UPDATED',
              entity_type: 'APPLICATION',
              entity_id: appID,
              created_at: '2026-11-14T00:00:00Z',
            },
          ],
          next_after_id: null,
        });
        return true;
      }
      if (scoped?.[1] === 'uploads' && request.method === 'GET') {
        envelope(response, 200, {
          items: [
            {
              id: '17',
              total_rows: 4,
              valid_rows: 3,
              invalid_rows: 1,
              created_at: '2026-11-13T00:00:00Z',
            },
          ],
          next_after_id: null,
        });
        return true;
      }
      if (scoped?.[1] === 'uploads/17/rows' && request.method === 'GET') {
        envelope(response, 200, {
          items: [
            { id: '9007199254740993', is_valid: true, created_at: '2026-11-13T00:00:00Z' },
            { id: '9007199254740994', is_valid: false, created_at: '2026-11-13T00:01:00Z' },
          ],
          next_after_id: null,
        });
        return true;
      }
      return false;
    },
  };
}
