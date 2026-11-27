'use strict';
// gate.js against the stand-in gate (test/gatefake.js): the checks that decide whether the app trusts a gate and a token.
process.env.HIVE_APP_TEST = '1';
const assert = require('assert');
const crypto = require('crypto');
const nacl = require('tweetnacl');
const gate = require('../gate');
const { make } = require('./gatefake');

(async () => {
  const f = await make().listen();
  process.env.HIVE_GATE_TEAM = f.team; process.env.HIVE_GATE_STORE = f.origin + '/';
  const other = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 }).privateKey;

  // The app's own box open, against tweetnacl: every length around the 16- and 64-byte edges, and nothing changed taken.
  for (const n of [0, 1, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 1000, 5000]) {
    const me = gate.boxKeyPair(), them = nacl.box.keyPair(), msg = crypto.randomBytes(n), nonce = crypto.randomBytes(24);
    assert.strictEqual(nacl.box.keyPair.fromSecretKey(me.secretKey).publicKey.toString(), new Uint8Array(me.publicKey).toString(), 'key pair');
    const sealed = Buffer.from(nacl.box(msg, nonce, me.publicKey, them.secretKey));
    assert.deepStrictEqual(gate.boxOpen(sealed, nonce, Buffer.from(them.publicKey), me.secretKey), msg, 'opens ' + n);
    for (const at of [0, 15, 16, sealed.length - 1]) {
      if (at >= sealed.length) continue;
      const bad = Buffer.from(sealed); bad[at] ^= 1;
      assert.strictEqual(gate.boxOpen(bad, nonce, Buffer.from(them.publicKey), me.secretKey), null, `changed at ${at} of ${n}`);
    }
    assert.strictEqual(gate.boxOpen(sealed, nonce, Buffer.from(nacl.box.keyPair().publicKey), me.secretKey), null, 'another sender');
    const bn = Buffer.from(nonce); bn[23] ^= 1;
    assert.strictEqual(gate.boxOpen(sealed, bn, Buffer.from(them.publicKey), me.secretKey), null, 'another nonce');
  }
  assert.strictEqual(gate.boxOpen(Buffer.alloc(40), Buffer.alloc(24), Buffer.alloc(32), gate.boxKeyPair().secretKey), null, 'a low-order key');
  console.log('ok gate box');

  assert.strictEqual(gate.teamOf('https://Team-1.cloudflareaccess.com/x'), 'team-1.cloudflareaccess.com');
  assert.strictEqual(gate.teamOf('team.cloudflareaccess.com'), 'team.cloudflareaccess.com');
  assert.ok(gate.teamOk('team.cloudflareaccess.com') && !gate.teamOk('cloudflareaccess.com.evil.com') && !gate.teamOk('evil.com') && !gate.teamOk(''));
  const aud = 'ab'.repeat(32);
  assert.deepStrictEqual(gate.validGate({ team: 'Team.cloudflareaccess.com', aud, extra: 1 }), { team: 'team.cloudflareaccess.com', aud });
  for (const g of [null, 'x', { team: 'evil.com', aud }, { team: 'team.cloudflareaccess.com', aud: 'XYZ' }, { team: 'team.cloudflareaccess.com' }]) assert.strictEqual(gate.validGate(g), null, JSON.stringify(g));
  console.log('ok gate names');

  const claims = { aud: [f.aud], exp: Math.floor(Date.now() / 1000) + 600, iss: 'http://' + f.team };
  const good = f.sign(claims);
  assert.deepStrictEqual(gate.verify(good, [f.jwk]), claims);
  const [h, b, s] = good.split('.');
  const forged = h + '.' + Buffer.from(JSON.stringify({ ...claims, aud: ['x'] })).toString('base64url') + '.' + s;
  const none = Buffer.from('{"alg":"none","kid":"k1"}').toString('base64url') + '.' + b + '.';
  for (const [t, why] of [[forged, 'signature'], [f.sign(claims, other), 'signature'], [f.sign(claims, undefined, 'k2'), 'key'], [none, 'way'], ['a.b', 'signed'], ['', 'signed']])
    assert.throws(() => gate.verify(t, [f.jwk]), new RegExp(why), why);
  const g = { team: f.team, aud: f.aud };
  assert.strictEqual(gate.check(good, g, [f.jwk]).exp, claims.exp);
  assert.throws(() => gate.check(f.sign({ ...claims, aud: ['ff'.repeat(32)] }), g, [f.jwk]), /another app/);
  assert.throws(() => gate.check(f.sign({ ...claims, exp: Math.floor(Date.now() / 1000) + 5 }), g, [f.jwk]), /run out/);
  assert.throws(() => gate.check(f.sign({ ...claims, iss: 'https://other.cloudflareaccess.com' }), g, [f.jwk]), /another team/);
  assert.throws(() => gate.check(forged, g, [f.jwk]), /didn't check out/);
  console.log('ok gate tokens');

  assert.deepStrictEqual(await gate.probe(fetch, f.origin + '/thread'), g);
  const plain = require('http').createServer((_q, r) => r.end('hi'));
  await new Promise(r => plain.listen(0, '127.0.0.1', r));
  assert.strictEqual(await gate.probe(fetch, `http://127.0.0.1:${plain.address().port}/`), null, 'no gate');
  assert.strictEqual(await gate.probe(fetch, 'http://127.0.0.1:9/'), null, 'nothing answers');
  plain.close();
  for (const [meta, key, why] of [[{ auth_domain: 'evil.example.com' }, null, /isn't Cloudflare Access/], [{ hostname: 'other.example.com' }, null, /another address/],
    [{ type: 'prefix' }, null, /another address/], [{ aud: 'nothex' }, null, /another address/], [{}, other, /didn't check out/]]) {
    f.meta = meta; f.metaKey = key;
    await assert.rejects(gate.probe(fetch, f.origin), why, JSON.stringify(meta));
  }
  f.meta = {}; f.metaKey = null;
  delete process.env.HIVE_GATE_TEAM;
  assert.strictEqual(await gate.probe(fetch, f.origin), null, 'outside the tests a gate is only looked for over https');
  process.env.HIVE_GATE_TEAM = f.team;
  console.log('ok gate probe');

  const a = gate.ask(f.origin + '/x', g), u = new URL(a.url);
  assert.strictEqual(u.origin + u.pathname, f.origin + '/cdn-cgi/access/cli');
  assert.strictEqual(u.searchParams.get('token'), a.pub); assert.strictEqual(u.searchParams.get('aud'), f.aud);
  assert.strictEqual(new URL(u.searchParams.get('redirect_url')).origin, f.origin);
  assert.strictEqual(Buffer.from(a.pub, 'base64url').length, 32);
  const stop = new AbortController();
  setTimeout(() => fetch(a.url), 300);   // the person signs in, in their browser
  const got = await gate.wait(fetch, g, a, stop.signal);
  assert.ok(f.tokens.includes(got.token) && got.exp > Date.now() / 1000 + 3000, 'the token came back');
  const b2 = gate.ask(f.origin, g), s2 = new AbortController();
  setTimeout(() => s2.abort(), 500);
  assert.strictEqual(await gate.wait(fetch, g, b2, s2.signal), null, 'stopped');
  f.sealKey = nacl.box.keyPair().publicKey;
  const c = gate.ask(f.origin, g); await fetch(c.url);
  await assert.rejects(gate.wait(fetch, g, c, new AbortController().signal), /wasn't sealed for this app/);
  f.sealKey = null;
  const d = gate.ask(f.origin, g);
  await assert.rejects(gate.wait(fetch, g, d, new AbortController().signal, 0.05), /in time/);
  console.log('ok gate wait');
  f.close();
})().catch(e => { console.error('FAILED', e); process.exit(1); });
