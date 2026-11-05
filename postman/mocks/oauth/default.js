// Synthetic service-account OAuth subset. Signature validation is exercised by
// the Go unit test; this fixture checks the actual library's HTTP/claim shape.
const http = require('http');
function send(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(body));
}
http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/__health') {
    return send(res, 200, { fixture: 'oauth', ready: true });
  }
  if (req.method !== 'POST' || req.url !== '/token') {
    return send(res, 501, { error: 'Operation not modeled by fixture' });
  }
  try {
    const body = typeof req.body === 'string' ? Object.fromEntries(new URLSearchParams(req.body)) : req.body;
    if (!body || body.grant_type !== 'urn:ietf:params:oauth:grant-type:jwt-bearer') {
      return send(res, 400, { error: 'invalid_request' });
    }
    const parts = body.assertion.split('.');
    if (parts.length !== 3) return send(res, 400, { error: 'invalid_grant' });
    const header = JSON.parse(Buffer.from(parts[0], 'base64url').toString('utf8'));
    const claims = JSON.parse(Buffer.from(parts[1], 'base64url').toString('utf8'));
    if (header.alg !== 'RS256' || claims.iss !== 'fixture@fixture-project.iam.gserviceaccount.com' ||
        claims.aud !== 'https://oauth2.googleapis.com/token' ||
        claims.scope !== 'https://www.googleapis.com/auth/firebase.messaging' || claims.sub ||
        !Number.isInteger(claims.iat) || !Number.isInteger(claims.exp) || claims.exp - claims.iat > 3600) {
      return send(res, 400, { error: 'invalid_grant' });
    }
    if (header.kid === 'fixture-denied') return send(res, 400, { error: 'invalid_grant' });
    if (header.kid === 'fixture-timeout') {
      return setTimeout(() => send(res, 200, { access_token: 'synthetic_access_token', token_type: 'Bearer', expires_in: 3600 }), 2000);
    }
    return send(res, 200, { access_token: 'synthetic_access_token', token_type: 'Bearer', expires_in: 3600 });
  } catch {
    return send(res, 400, { error: 'invalid_request' });
  }
}).listen(process.env.PORT || 14012, '127.0.0.1');
