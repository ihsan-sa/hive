'use strict';
// Signing in to a Hive behind a Cloudflare Access gate (DESIGN-many-hives.md, "Sign-in to a remote Hive" b), the way
// `cloudflared access login` does it. Google and most sign-ins refuse an app's own window, so the person signs in in
// their own browser, and the token comes back to the app sealed to a key the app made for this one sign-in.
//   1. probe: ask the edge which Access app guards the address, and check that answer's signature with the team's keys.
//   2. signIn: open HOST/cdn-cgi/access/cli in the browser, then wait at Cloudflare's transfer store for the sealed token.
//   3. check the token (signed by the team, for this app, not expired). The caller puts it in that Hive's session only.
// The token is never logged or written anywhere by this module. fetch is passed in (the Hive's own session's, so the
// same name lookup, proxy and certificate rules hold as for its page); it must support method, headers and redirect.
const crypto = require('crypto');

// In the tests one stand-in on 127.0.0.1 plays the edge, the team and the transfer store (never outside the tests).
const TEST = process.env.HIVE_APP_TEST === '1';
const testTeam = () => (TEST && process.env.HIVE_GATE_TEAM) || '';
const store = () => (TEST && process.env.HIVE_GATE_STORE) || 'https://login.cloudflareaccess.org/';
const teamOk = d => /^[a-z0-9-]{1,63}\.cloudflareaccess\.com$/.test(d) || (!!testTeam() && d === testTeam());
const teamUrl = d => (testTeam() && d === testTeam() ? 'http://' : 'https://') + d;

const b64u = b => Buffer.from(b).toString('base64').replace(/\+/g, '-').replace(/\//g, '_');   // Go's URLEncoding, padded: cloudflared's form
const part = (jwt, i) => { try { return JSON.parse(Buffer.from(String(jwt).split('.')[i] || '', 'base64url').toString()); } catch { return null; } };
const sleep = (ms, signal) => new Promise(r => { const t = setTimeout(r, ms); if (signal) signal.addEventListener('abort', () => { clearTimeout(t); r(); }, { once: true }); });
// A request that gives up after ms (a stalled connection would otherwise hold the sign-in for ever).
const within = (ms, p) => { let t; return Promise.race([p, new Promise((_r, no) => { t = setTimeout(() => no(new Error('timed out')), ms); })]).finally(() => clearTimeout(t)); };

// NaCl's box (X25519, XSalsa20, Poly1305), the one way the store seals a token, on Node's own crypto: the app ships no
// third-party code (goals.md). Only what a sign-in needs: a key pair, and opening one sealed answer. test/gate.test.js
// checks it against tweetnacl, a test-only dependency.
const rotl = (v, n) => (v << n) | (v >>> (32 - n));
function salsa(x) {   // 20 rounds of the Salsa20 core on 16 words, in place
  const q = (a, b, c, d) => { x[b] ^= rotl((x[a] + x[d]) | 0, 7); x[c] ^= rotl((x[b] + x[a]) | 0, 9); x[d] ^= rotl((x[c] + x[b]) | 0, 13); x[a] ^= rotl((x[d] + x[c]) | 0, 18); };
  for (let i = 0; i < 10; i++) {
    q(0, 4, 8, 12); q(5, 9, 13, 1); q(10, 14, 2, 6); q(15, 3, 7, 11);
    q(0, 1, 2, 3); q(5, 6, 7, 4); q(10, 11, 8, 9); q(15, 12, 13, 14);
  }
}
const SIGMA = [0x61707865, 0x3320646e, 0x79622d32, 0x6b206574];   // "expand 32-byte k"
const words = (b, n) => Array.from({ length: n }, (_, i) => b.readUInt32LE(4 * i));
function state(key, n16) { const k = words(key, 8), n = words(n16, 4); return [SIGMA[0], ...k.slice(0, 4), SIGMA[1], ...n, SIGMA[2], ...k.slice(4), SIGMA[3]]; }
function hsalsa(key, n16) {
  const x = Int32Array.from(state(key, n16)); salsa(x);
  const out = Buffer.alloc(32);
  [0, 5, 10, 15, 6, 7, 8, 9].forEach((w, i) => out.writeInt32LE(x[w], 4 * i));
  return out;
}
function xsalsa(key, nonce, len) {   // the first len bytes of XSalsa20's stream
  const sub = hsalsa(key, nonce.subarray(0, 16)), out = Buffer.alloc(Math.ceil(len / 64) * 64);
  const init = state(sub, Buffer.concat([nonce.subarray(16, 24), Buffer.alloc(8)]));
  for (let blk = 0; blk * 64 < len; blk++) {
    init[8] = blk >>> 0; init[9] = Math.floor(blk / 2 ** 32);
    const x = Int32Array.from(init); salsa(x);
    for (let i = 0; i < 16; i++) out.writeInt32LE((x[i] + init[i]) | 0, blk * 64 + 4 * i);
  }
  return out.subarray(0, len);
}
const le = b => { let n = 0n; for (let i = b.length - 1; i >= 0; i--) n = (n << 8n) | BigInt(b[i]); return n; };
function poly1305(key, m) {
  const r = le(key.subarray(0, 16)) & 0x0ffffffc0ffffffc0ffffffc0fffffffn, s = le(key.subarray(16, 32)), p = (1n << 130n) - 5n;
  let acc = 0n;
  for (let i = 0; i < m.length; i += 16) { const c = m.subarray(i, i + 16); acc = ((acc + le(c) + (1n << BigInt(8 * c.length))) * r) % p; }
  acc = (acc + s) & ((1n << 128n) - 1n);
  const out = Buffer.alloc(16);
  for (let i = 0; i < 16; i++) { out[i] = Number(acc & 255n); acc >>= 8n; }
  return out;
}
function boxKeyPair() {
  const { privateKey, publicKey } = crypto.generateKeyPairSync('x25519');
  return { publicKey: Buffer.from(publicKey.export({ format: 'jwk' }).x, 'base64url'), secretKey: Buffer.from(privateKey.export({ format: 'jwk' }).d, 'base64url') };
}
// crypto_box_open: the message, or null if it wasn't sealed by `them` for `secret` (or was changed on the way).
function boxOpen(sealed, nonce, them, secret) {
  if (sealed.length < 16 || nonce.length !== 24 || them.length !== 32 || secret.length !== 32) return null;
  let shared;
  try {   // the raw keys in their DER wrappers (PKCS#8 and SPKI for X25519)
    shared = crypto.diffieHellman({ privateKey: crypto.createPrivateKey({ key: Buffer.concat([Buffer.from('302e020100300506032b656e04220420', 'hex'), secret]), format: 'der', type: 'pkcs8' }),
      publicKey: crypto.createPublicKey({ key: Buffer.concat([Buffer.from('302a300506032b656e032100', 'hex'), them]), format: 'der', type: 'spki' }) });
  } catch { return null; }
  const k = hsalsa(shared, Buffer.alloc(16)), body = sealed.subarray(16);
  const stream = xsalsa(k, nonce, 32 + body.length);
  if (!crypto.timingSafeEqual(poly1305(stream.subarray(0, 32), body), sealed.subarray(0, 16))) return null;
  const out = Buffer.alloc(body.length);
  for (let i = 0; i < body.length; i++) out[i] = body[i] ^ stream[32 + i];
  return out;
}

// The team's sign-in domain from the edge's answer: a bare host, lowercase, or '' if it isn't one.
function teamOf(authDomain) {
  try {
    const s = String(authDomain || '');
    const u = new URL(/^https?:\/\//i.test(s) ? s : 'https://' + s);
    return u.host.toLowerCase();
  } catch { return ''; }
}
async function teamKeys(fetch, team) {
  const r = await within(15000, fetch(teamUrl(team) + '/cdn-cgi/access/certs', { headers: { accept: 'application/json' } }));
  if (!r.ok) throw new Error(`The sign-in gate's keys didn't come (HTTP ${r.status}).`);
  const d = await r.json().catch(() => null);
  if (!d || !Array.isArray(d.keys)) throw new Error("The sign-in gate's keys came in a form the app doesn't know.");
  return d.keys;
}
// The claims of a token signed (RS256) by one of the team's keys; throws if it isn't.
function verify(jwt, keys) {
  const bits = String(jwt).split('.');
  const head = part(jwt, 0), claims = part(jwt, 1);
  if (bits.length !== 3 || !head || !claims) throw new Error('not a signed answer');
  if (head.alg !== 'RS256') throw new Error('signed in a way the app does not accept');
  const jwk = keys.find(k => k && k.kid === head.kid && k.kty === 'RSA');
  if (!jwk) throw new Error('signed by a key the gate does not have');
  let ok = false;
  try { ok = crypto.verify('RSA-SHA256', Buffer.from(bits[0] + '.' + bits[1]), crypto.createPublicKey({ key: jwk, format: 'jwk' }), Buffer.from(bits[2], 'base64url')); } catch { }
  if (!ok) throw new Error('its signature does not match');
  return claims;
}

// Which gate guards origin: { team, aud } when an Access app does, null when none answers. Throws (with words for the
// person) when one answers but its answer doesn't check out, so the app sends that address nothing more.
async function probe(fetch, origin) {
  const host = new URL(origin).hostname.toLowerCase();
  if (new URL(origin).protocol !== 'https:' && !testTeam()) return null;   // a real gate is only ever in front of https
  let r;
  try { r = await within(10000, fetch(new URL(origin).origin + '/', { method: 'HEAD', redirect: 'manual', headers: { 'cf-access-metadata-request': 'true' } })); }
  catch { return null; }   // nothing answered: loading the page says so in its own words
  const meta = r.headers.get('cf-access-metadata');
  if (!meta) return null;
  const team = teamOf((part(meta, 1) || {}).auth_domain);
  if (!teamOk(team)) throw new Error(`It is behind a sign-in gate at ${team || 'an address it did not give'}, which isn't Cloudflare Access, so the app sent it nothing.`);
  let m;
  try { m = verify(meta, await teamKeys(fetch, team)); } catch (e) { throw new Error(`Its sign-in gate's answer didn't check out (${e.message}), so the app sent it nothing.`); }
  if (String(m.hostname || '').toLowerCase() !== host || m.type !== 'match' || typeof m.aud !== 'string' || !/^[a-f0-9]{16,128}$/.test(m.aud))
    throw new Error("Its sign-in gate answered for another address, so the app sent it nothing.");
  return { team, aud: m.aud };
}

// The address the person opens to sign in, and the key the token comes back sealed to.
function ask(origin, gate) {
  const kp = boxKeyPair(), pub = b64u(kp.publicKey);
  const o = new URL(origin).origin;
  const back = new URL(o + '/'); back.searchParams.set('token', pub); back.searchParams.set('aud', gate.aud);
  const u = new URL(o + '/cdn-cgi/access/cli');
  u.searchParams.set('token', pub); u.searchParams.set('aud', gate.aud); u.searchParams.set('redirect_url', back.href);
  u.searchParams.set('send_org_token', 'true'); u.searchParams.set('edge_token_transfer', 'true'); u.searchParams.set('close_interstitial', 'true');
  return { url: u.href, pub, secret: kp.secretKey };
}

// Waits for the person's sign-in (begun at a.url, opened by the caller) and returns { token, exp } (exp in seconds), or
// null if signal stopped it. Throws with words for the person when the wait ran out or what came back doesn't check out.
async function wait(fetch, gate, a, signal, minutes = 15) {
  const until = Date.now() + minutes * 60000;
  let body = '', sender = '', errors = 0;
  while (!signal.aborted && Date.now() < until) {
    let r;
    try { r = await within(70000, fetch(store() + 'transfer/' + a.pub, { headers: { accept: '*/*' } })); }   // the store holds the question up to a minute
    catch { if (!signal.aborted) await sleep(3000, signal); continue; }
    if (signal.aborted) return null;
    if (r.status === 200) { body = (await r.text()).trim(); sender = r.headers.get('service-public-key') || ''; break; }
    try { await r.arrayBuffer(); } catch { }
    if (r.status >= 500 && ++errors >= 5) throw new Error(`Cloudflare's sign-in service kept answering with an error (HTTP ${r.status}). Try again in a while.`);
    await sleep(2000, signal);
  }
  if (signal.aborted) return null;
  if (!body) throw new Error('No sign-in came back in time. Try again, and sign in on the page that opens in your browser.');
  const sealed = Buffer.from(body, 'base64'), them = Buffer.from(sender, 'base64url');
  const opened = sealed.length > 24 + 16 && them.length === 32 ? boxOpen(sealed.subarray(24), sealed.subarray(0, 24), them, a.secret) : null;
  if (!opened) throw new Error("What came back from the sign-in wasn't sealed for this app, so it wasn't used.");
  let tok = '';
  try { tok = JSON.parse(Buffer.from(opened).toString()).app_token; } catch { }
  if (typeof tok !== 'string' || !/^[\w-]+\.[\w-]+\.[\w-]+$/.test(tok)) throw new Error('The sign-in came back without a token.');
  return check(tok, gate, await teamKeys(fetch, gate.team));
}
// A token is used only if the team signed it, for this app, and it has time left.
function check(tok, gate, keys, now = Date.now() / 1000) {
  let c;
  try { c = verify(tok, keys); } catch (e) { throw new Error(`The sign-in's token didn't check out (${e.message}).`); }
  if (!(Array.isArray(c.aud) ? c.aud : [c.aud]).includes(gate.aud)) throw new Error('The sign-in came back for another app.');
  if (!(Number(c.exp) > now + 10)) throw new Error('The sign-in came back already run out. Try again.');
  if (c.iss && teamOf(c.iss) !== gate.team) throw new Error('The sign-in came back from another team.');
  return { token: tok, exp: Number(c.exp) };
}
// A saved gate as kept in hives.json: { team, aud }, or null when it isn't one.
function validGate(g) {
  if (!g || typeof g !== 'object') return null;
  const team = teamOf(g.team);
  return teamOk(team) && typeof g.aud === 'string' && /^[a-f0-9]{16,128}$/.test(g.aud) ? { team, aud: g.aud } : null;
}

module.exports = { probe, ask, wait, check, verify, teamOf, teamOk, validGate, b64u, boxKeyPair, boxOpen };
