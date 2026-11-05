// Local fixture subset based on Firebase REST send/error documentation.
// Synthetic addresses select scenarios; they are never real device IDs.
const http = require('http');
let accepted = 0;
function send(res, code, body, headers = {}) {
  res.writeHead(code, { 'Content-Type': 'application/json', ...headers });
  res.end(JSON.stringify(body));
}
function failure(res, code, status, errorCode, headers) {
  send(res, code, { error: { code, status, message: 'Integration fixture',
    details: [{ '@type': 'type.googleapis.com/google.firebase.fcm.v1.FcmError', errorCode }] } }, headers);
}
http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/__health') {
    return send(res, 200, { fixture: 'fcm', ready: true });
  }
  if (req.method !== 'POST' || req.url !== '/v1/projects/integration-fixture/messages:send') {
    return send(res, 501, { error: 'Operation not modeled by fixture' });
  }
  const message = req.body && req.body.message;
  if (!message || Boolean(message.token) === Boolean(message.fid)) {
    return failure(res, 400, 'INVALID_ARGUMENT', 'INVALID_ARGUMENT');
  }
  switch (message.fid || message.token) {
    case 'fixture-unregistered':
      return failure(res, 404, 'NOT_FOUND', 'UNREGISTERED');
    case 'fixture-sender':
      return failure(res, 403, 'PERMISSION_DENIED', 'SENDER_ID_MISMATCH');
    case 'fixture-auth':
      return failure(res, 401, 'UNAUTHENTICATED', 'UNSPECIFIED_ERROR');
    case 'fixture-apns':
      return failure(res, 401, 'UNAUTHENTICATED', 'THIRD_PARTY_AUTH_ERROR');
    case 'fixture-quota':
      return failure(res, 429, 'RESOURCE_EXHAUSTED', 'QUOTA_EXCEEDED', { 'Retry-After': '60' });
    case 'fixture-unavailable':
      return failure(res, 503, 'UNAVAILABLE', 'UNAVAILABLE', { 'Retry-After': '0' });
    case 'fixture-timeout':
      return setTimeout(() => send(res, 200, { name: 'projects/integration-fixture/messages/delayed' }), 2000);
    case 'execution-address-0':
    case 'execution-address-1':
    case 'fixture-accepted':
      if (!message.data || message.data.fixture !== 'integration') {
        return failure(res, 400, 'INVALID_ARGUMENT', 'INVALID_ARGUMENT');
      }
      if (message.data.schema_version === '1') {
        const required = ['application_id', 'notification_id', 'delivery_id', 'registration_id', 'owner_epoch', 'receipt_token'];
        const scoped = required.every(key => typeof message.data[key] === 'string' && message.data[key].length > 0);
        const expiry = (message.android && typeof message.android.ttl === 'string') ||
          (message.apns && message.apns.headers && typeof message.apns.headers['apns-expiration'] === 'string');
        if (!scoped || !expiry || !message.data.receipt_token.startsWith('r1.')) {
          return failure(res, 400, 'INVALID_ARGUMENT', 'INVALID_ARGUMENT');
        }
      }
      return send(res, 200, { name: `projects/integration-fixture/messages/accepted-${++accepted}` });
    default:
      return failure(res, 400, 'INVALID_ARGUMENT', 'INVALID_ARGUMENT');
  }
}).listen(process.env.PORT || 14011, '127.0.0.1');
