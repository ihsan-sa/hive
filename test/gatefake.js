'use strict';
// A stand-in for a Hive behind Cloudflare Access, on 127.0.0.1 only: one server plays the edge (the metadata answer and
// the redirect to sign in), the team (its keys), the transfer store, and the Hive's page behind the gate. Opening the
// sign-in address (`cli`) stands for the person signing in in their browser: the token for that key is then ready.
const http = require('http');
const crypto = require('crypto');
const nacl = require('tweetnacl');

const b64u = b => Buffer.from(b).toString('base64url');
function make() {
  const { privateKey, publicKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
  const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'k1', alg: 'RS256', use: 'sig' };
  const sign = (claims, key = privateKey, kid = 'k1') => {
    const head = b64u(JSON.stringify({ alg: 'RS256', kid, typ: 'JWT' })), body = b64u(JSON.stringify(claims));
    return head + '.' + body + '.' + b64u(crypto.sign('RSA-SHA256', Buffer.from(head + '.' + body), key));
  };
  const store = nacl.box.keyPair();
  const f = { aud: crypto.randomBytes(32).toString('hex'), ttl: 3600, approved: new Set(), tokens: [], meta: {}, metaKey: null, sealKey: null, asked: [], sign, jwk };
  const now = () => Math.floor(Date.now() / 1000);
  const valid = tok => {
    try {
      const [h, b, s] = tok.split('.');
      if (!crypto.verify('RSA-SHA256', Buffer.from(h + '.' + b), publicKey, Buffer.from(s, 'base64url'))) return false;
      const c = JSON.parse(Buffer.from(b, 'base64url'));
      return c.aud.includes(f.aud) && c.exp > now();
    } catch { return false; }
  };
  f.server = http.createServer((req, res) => {
    const u = new URL(req.url, 'http://x');
    f.asked.push(req.method + ' ' + u.pathname);
    const login = `http://localhost:${f.port}/cdn-cgi/access/login/127.0.0.1?redirect_url=${encodeURIComponent(u.pathname)}`;
    if (req.method === 'HEAD' && req.headers['cf-access-metadata-request'] === 'true') {
      const m = { hostname: '127.0.0.1', type: 'match', aud: f.aud, auth_domain: f.team, ...f.meta };   // f.meta, f.metaKey: a gate that lies
      res.writeHead(302, { location: login, 'cf-access-metadata': f.metaKey ? sign(m, f.metaKey) : sign(m) });
      return res.end();
    }
    if (u.pathname === '/cdn-cgi/access/certs') { res.setHeader('content-type', 'application/json'); return res.end(JSON.stringify({ keys: [jwk] })); }
    if (u.pathname === '/cdn-cgi/access/cli') { if (u.searchParams.get('aud') === f.aud) f.approved.add(u.searchParams.get('token')); return res.end('signed in'); }
    const t = u.pathname.match(/^\/transfer\/([\w=-]+)$/);
    if (t) {
      if (!f.approved.has(t[1])) { res.statusCode = 404; return res.end(); }
      f.approved.delete(t[1]);
      const tok = sign({ aud: [f.aud], exp: now() + f.ttl, iat: now(), iss: 'http://' + f.team, email: 'tester@example.com' });
      f.tokens.push(tok);
      const nonce = nacl.randomBytes(24), box = nacl.box(Buffer.from(JSON.stringify({ app_token: tok })), nonce, f.sealKey || Buffer.from(t[1], 'base64url'), store.secretKey);   // f.sealKey: sealed for someone else
      res.setHeader('service-public-key', b64u(store.publicKey));
      return res.end(Buffer.concat([Buffer.from(nonce), Buffer.from(box)]).toString('base64'));
    }
    const c = (req.headers.cookie || '').match(/(?:^|;\s*)CF_Authorization=([^;]+)/);
    if (c && valid(c[1]) && f.forward) { res.writeHead(302, { location: f.forward }); return res.end(); }   // f.forward: a gated address that sends on to another
    if (c && valid(c[1])) { res.setHeader('content-type', 'text/html'); return res.end('<title>Gated Hive</title><h1 id="app">The gated Hive</h1>'); }
    res.writeHead(302, { location: login }); res.end();
  });
  f.listen = () => new Promise(r => f.server.listen(0, '127.0.0.1', () => { f.port = f.server.address().port; f.team = '127.0.0.1:' + f.port; f.origin = 'http://' + f.team; r(f); }));
  f.close = () => f.server.close();
  return f;
}
module.exports = { make };
