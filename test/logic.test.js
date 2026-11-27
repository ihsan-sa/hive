// node test/logic.test.js : the per-system choices, with no Electron.
const assert = require('assert');
const lib = require('../lib');
const has = (...paths) => p => paths.includes(p);
let n = 0; const t = (name, fn) => { fn(); n++; };

t('windows: no Hive', () => assert.strictEqual(lib.openCommand('win32', { LOCALAPPDATA: 'C:\\Users\\a\\AppData\\Local' }, has()), null));
t('windows: Hive', () => {
  const c = lib.openCommand('win32', { LOCALAPPDATA: 'C:\\Users\\a\\AppData\\Local' }, has('C:\\Users\\a\\AppData\\Local\\Hive\\bin\\hive.cmd'));
  assert.strictEqual(c.file, 'wsl.exe'); assert.deepStrictEqual(c.args.slice(0, 4), ['-d', 'Hive', '-u', 'hive']); assert.strictEqual(c.args.at(-1), 'hive open --print');
});
t('update notice: only a newer x.y.z from the version file', () => {
  assert.strictEqual(lib.newerApp('0.1.0', '{"version":"0.2.0"}'), '0.2.0');
  assert.strictEqual(lib.newerApp('0.1.0', '{"version":"0.1.10"}'), '0.1.10');
  assert.strictEqual(lib.newerApp('0.10.0', '{"version":"0.9.9"}'), null);
  assert.strictEqual(lib.newerApp('0.1.0', '{"version":"0.1.0"}'), null);
  assert.strictEqual(lib.newerApp('0.2.0-beta.1', '{"version":"0.2.1"}'), '0.2.1');
  assert.strictEqual(lib.newerApp('0.2.0-beta.1', '{"version":"0.2.0"}'), null);
  assert.strictEqual(lib.newerApp('0.1.0', '{"version":"999999.999999.999999"}'), '999999.999999.999999');
  assert.strictEqual(lib.newerApp('0.1.0', '{"version":"1000000.0.0"}'), null);
  for (const mine of ['', 'x', '1.0']) assert.strictEqual(lib.newerApp(mine, '{"version":"9.9.9"}'), null, mine);
  for (const bad of ['', 'nope', '{}', '{"version":"1.0"}', '{"version":"1.0.0-beta"}', '{"version":"<b>9.9.9</b>"}', '{"version":9}']) assert.strictEqual(lib.newerApp('0.1.0', bad), null, bad);
  assert.ok(lib.downloadPage().startsWith(lib.SITE + '/'));
  assert.strictEqual(lib.downloadPage('update'), lib.SITE + '/download#update');
  console.log('ok update notice logic');
});
t('mac', () => {
  assert.strictEqual(lib.openCommand('darwin', { HOME: '/Users/a' }, has()), null);
  assert.deepStrictEqual(lib.openCommand('darwin', { HOME: '/Users/a' }, has('/Users/a/.hive/bin/hive')), { file: '/Users/a/.hive/bin/hive', args: ['web'] });
});
t('linux: this account', () => {
  const c = lib.openCommand('linux', { HOME: '/home/a' }, has('/home/a/hive/.hive-installed', '/home/a/hive/.local/bin/hive'));
  assert.strictEqual(c.file, '/home/a/hive/.local/bin/hive'); assert.strictEqual(c.env.HOME, '/home/a/hive');
});
t('linux: the Hive gets its own surroundings, as the installer gives it', () => {
  const c = lib.openCommand('linux', { HOME: '/home/a', PATH: '/usr/bin', XDG_CONFIG_HOME: '/home/a/.config' }, has('/home/a/hive/.hive-installed', '/home/a/hive/.local/bin/hive'));
  assert.strictEqual(c.env.PATH, '/home/a/hive/.local/bin:/usr/bin');
  assert.ok(c.unset.includes('XDG_CONFIG_HOME') && c.unset.includes('CLAUDE_CONFIG_DIR'));
  assert.match(c.env.XDG_RUNTIME_DIR, /^\/run\/user\/-?\d+$/);
});
t('windows: the installer runs without another PowerShell\'s module folders', () => assert.deepStrictEqual(lib.installCommand('win32', { windir: 'C:\\Windows' }, false).unset, ['PSModulePath']));
t('windows: the installer is told the machine\'s own chip, not the one a translated app sees', () => {
  const c = lib.installCommand('win32', { windir: 'C:\\Windows' }, false).args.at(-1);
  assert.match(c, /^\$a = \(Get-ItemProperty 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Environment'[^;]*\)\.PROCESSOR_ARCHITECTURE; if \(\$a\) \{ \$env:PROCESSOR_ARCHITECTURE = \$a \}; try \{ \$s = irm \S+\/install\.ps1 \} catch \{[^}]*Couldn.t reach[^}]*\}; iex \$s$/);
});
t('proof: the mark only the key can make, for this port and this question', () => {
  const mark = (key, port, n) => require('crypto').createHmac('sha256', key).update(`proof:${port}:${n}`).digest('hex');
  const p = lib.proofAsk('http://localhost:8077/login#KEY', 'nonce-nonce-nonce');
  assert.strictEqual(p.ask, 'http://localhost:8077/api/proof?n=nonce-nonce-nonce');
  assert.ok(!p.ask.includes('KEY'), 'the question never carries the key');
  assert.ok(lib.proofOk(p.want, mark('KEY', 8077, 'nonce-nonce-nonce') + '\n'));
  assert.ok(!lib.proofOk(p.want, mark('KEY', 8078, 'nonce-nonce-nonce')), 'a Hive on another port');
  assert.ok(!lib.proofOk(p.want, mark('KEY', 8077, 'another-question')), 'an old answer');
  assert.ok(!lib.proofOk(p.want, mark('OTHER', 8077, 'nonce-nonce-nonce')), 'another key');
  assert.ok(!lib.proofOk(p.want, '') && !lib.proofOk(p.want, 'no'));
  assert.strictEqual(lib.proofAsk('http://localhost:8077/', 'n'), null);
});
t('the app uses only the address the Hive listens on, never the other "localhost"', () => {
  assert.strictEqual(lib.pin('http://localhost:8077/login#KEY'), 'http://127.0.0.1:8077/login#KEY');
  assert.strictEqual(lib.pin('http://[::1]:8077/'), 'http://127.0.0.1:8077/');
  assert.strictEqual(lib.pin('http://127.0.0.1:8077/login#KEY'), 'http://127.0.0.1:8077/login#KEY');
  assert.ok(lib.sameApp('http://127.0.0.1:8077/thread', lib.pin('http://localhost:8077/login#KEY')));
});
t('windows: the Hive system is kept awake', () => {
  const c = lib.openCommand('win32', { LOCALAPPDATA: 'C:\\Users\\a\\AppData\\Local', windir: 'C:\\Windows' }, () => true);
  assert.strictEqual(c.keep.file, 'C:\\Windows\\System32\\conhost.exe');
  assert.deepStrictEqual(c.keep.args.slice(-3), ['--exec', 'sleep', 'infinity']);
});
t('linux: a missing curl or git is named, not called a bad connection', () => {
  if (process.platform === 'win32') return;   // runs the Linux line itself, which needs sh
  const c = lib.installCommand('linux', { HOME: '/home/a' }, false);
  const r = require('child_process').spawnSync('/bin/sh', ['-c', c.args[1]], { env: { PATH: '/nonexistent' }, encoding: 'utf8' });
  assert.strictEqual(r.status, 1); assert.match(r.stdout, /needs these first: curl git python3/); assert.match(r.stdout, /sudo apt install curl git python3/);
});
t('linux: half an install is no install', () => assert.strictEqual(lib.openCommand('linux', { HOME: '/home/a' }, has('/home/a/hive/.local/bin/hive')), null));
t('linux: own user, by its launcher', () => {
  const d = '/home/a/.local/share/applications/hive.desktop';
  assert.deepStrictEqual(lib.openCommand('linux', { HOME: '/home/a' }, has(d), () => '[Desktop Entry]\nExec=xdg-open http://localhost:8077/\nIcon=hive\n'), { url: 'http://localhost:8077/' });
  assert.strictEqual(lib.openCommand('linux', { HOME: '/home/a' }, has(d), () => 'Exec=xdg-open http://evil.example/\n'), null);
  assert.strictEqual(lib.openCommand('linux', { HOME: '/home/a' }, has(d), () => 'Exec=/home/a/.local/bin/hive open\n'), null);
});
t('install commands', () => {
  const w = lib.installCommand('win32', { windir: 'C:\\Windows' }, false);
  assert.ok(w.file.endsWith('powershell.exe')); assert.ok(w.args.at(-1).includes('/install.ps1 }')); assert.strictEqual(w.env.HIVE_ACCEPT, undefined);
  assert.strictEqual(lib.installCommand('win32', {}, true).env.HIVE_ACCEPT, '1');
  const m = lib.installCommand('darwin', {}, false); assert.ok(m.args[1].includes('/install-mac.sh')); assert.strictEqual(m.env.HIVE_MAC_TEST, '1');
  const l = lib.installCommand('linux', {}, true); assert.ok(l.args[1].includes('--this-account')); assert.strictEqual(l.env.HIVE_NO_LAUNCHER, '1'); assert.strictEqual(l.env.HIVE_REPORT, '0');
});
t('install results', () => {
  assert.strictEqual(lib.installResult(1, "there's no terminal to ask you on, and nothing changed. If you accept the above, set HIVE_ACCEPT=1"), 'accept');
  assert.strictEqual(lib.installResult(0, 'Nothing changed. To accept with no question, set HIVE_ACCEPT=1'), 'accept');
  assert.strictEqual(lib.installResult(0, 'WSL is installed. Restart Windows, then run the same line again.'), 'restart');
  assert.strictEqual(lib.installResult(0, 'The Hive is installed.'), 'done');
  assert.strictEqual(lib.installResult(1, 'curl: (6)'), 'failed');
  // Windows' installer ends 0 whatever happened: only its last line means done (a repair that stopped has hive.cmd already).
  assert.strictEqual(lib.installResult(0, 'Step 3\nCould not download the system.', 'win32'), 'failed');
  assert.strictEqual(lib.installResult(0, 'The Hive is installed. This address is the key', 'win32'), 'done');
});
t('addresses', () => {
  assert.strictEqual(lib.localUrl('warning\r\nhttp://localhost:8077/login#abc\r\n'), 'http://localhost:8077/login#abc');
  assert.strictEqual(lib.localUrl('https://evil.example/\n'), null);
  assert.strictEqual(lib.localUrl('http://localhost.evil.example:8077/\n'), null);
  assert.strictEqual(lib.localUrl("The web app isn't set up"), null);
  assert.ok(lib.sameApp('http://localhost:8077/t/1', 'http://localhost:8077/login#x'));
  assert.ok(!lib.sameApp('http://localhost:9000/', 'http://localhost:8077/login#x'));
  assert.ok(!lib.sameApp('https://example.com/', 'http://localhost:8077/'));
  assert.strictEqual(lib.redact('open http://localhost:8077/login#SECRETKEY now'), 'open http://localhost:8077/login#… now');
  assert.strictEqual(lib.plain('\x1b[32mok\x1b[0m\rnext'), 'next');
  assert.strictEqual(lib.plain('a\r\nb\n 10%\r 50%\r100%\nc'), 'a\r\nb\n100%\nc');
});
console.log(`ok ${n} tests`);

// Keys for zoom and reload where there is no menu.
{
  const k = (key, o = {}) => lib.shortcut({ type: 'keyDown', key, ...o });
  assert.strictEqual(k('r', { control: true }), 'reload'); assert.strictEqual(k('F5'), 'reload');
  assert.strictEqual(k('=', { control: true }), 'zoomIn'); assert.strictEqual(k('-', { control: true }), 'zoomOut'); assert.strictEqual(k('0', { control: true }), 'zoomReset');
  assert.strictEqual(k('r'), null); assert.strictEqual(k('-', { control: true, alt: true }), null);
  assert.strictEqual(lib.shortcut({ type: 'keyUp', key: 'r', control: true }), null);
  // The Hives menu's keys: Ctrl (or Cmd) + 1..9 and Ctrl+Shift+N; not with Shift on a digit, not bare.
  assert.strictEqual(k('1', { control: true }), 'hive:1'); assert.strictEqual(k('9', { meta: true }), 'hive:9');
  assert.strictEqual(k('3'), null); assert.strictEqual(k('3', { control: true, shift: true }), null);
  assert.strictEqual(k('N', { control: true, shift: true }), 'newWindow'); assert.strictEqual(k('n', { control: true }), null);
  console.log('ok shortcuts logic');
t('right-click menu: link, box, selection', () => {
  assert.deepStrictEqual(lib.contextItems({ linkURL: 'https://auth.openai.com/codex/device', selectionText: '' }), ['copyLink']);
  assert.deepStrictEqual(lib.contextItems({ linkURL: 'javascript:alert(1)', selectionText: 'x' }), ['copy']);
  assert.deepStrictEqual(lib.contextItems({ isEditable: true, selectionText: '' }), ['cut', 'copy', 'paste', 'selectAll']);
  assert.deepStrictEqual(lib.contextItems({ selectionText: 'ABCD-EFGH' }), ['copy']);
  assert.deepStrictEqual(lib.contextItems({ selectionText: '  ' }), []);
  console.log('ok right-click menu logic');
});
  const scr = [{ x: 0, y: 0, width: 1920, height: 1080 }];
  assert.deepStrictEqual(lib.windowState({ x: 10, y: 20, width: 900, height: 700 }, scr), { width: 900, height: 700, x: 10, y: 20 });
  assert.deepStrictEqual(lib.windowState({ x: 5000, y: 20, width: 900, height: 700 }, scr), { width: 900, height: 700 }, 'off every screen: size only');
  assert.strictEqual(lib.windowState({ width: 10, height: 10 }, scr), null);
  assert.strictEqual(lib.windowState('x', scr), null);
  console.log('ok window state logic');
}

// Mac: short on disk space is said before the install starts.
{
  const { spawnSync } = require('child_process');
  if (process.platform !== 'win32') { // no /bin/sh on Windows; the check only runs on Macs
  const run = gb => spawnSync('/bin/sh', ['-c', lib.diskCheck() + 'echo passed'], { env: { ...process.env, HIVE_MIN_FREE_GB: String(gb) }, encoding: 'utf8' });
  const low = run(99999999); assert.strictEqual(low.status, 1); assert.match(low.stdout, /not enough free disk space/); assert.ok(!/passed/.test(low.stdout));
  const ok = run(0); assert.strictEqual(ok.status, 0); assert.match(ok.stdout, /passed/);
  assert.match(lib.installCommand('darwin', {}, false).args[1], /^need=\$\{HIVE_MIN_FREE_GB:-15\}/);
  assert.ok(!/free=/.test(lib.installCommand('linux', {}, false).args[1]));
  }
  console.log('ok disk space');
}
{
  const P = n => require('path').join('/dl', n);
  const d = '/dl', none = () => false;
  assert.strictEqual(lib.downloadPath(d, '../../etc/passwd', none), P('passwd'));
  assert.strictEqual(lib.downloadPath(d, 'a\\b\\c.txt', none), P('c.txt'));
  assert.strictEqual(lib.downloadPath(d, '.bashrc', none), P('bashrc'));
  assert.strictEqual(lib.downloadPath(d, '', none), P('download'));
  assert.strictEqual(lib.downloadPath(d, 'x.pdf', p => p === P('x.pdf')), P('x (1).pdf'));
  console.log('ok download name');
}

// site name matches the one the site's own install/uninstall lines use
{
  const { SITE } = require('../lib');
  if (!process.env.HIVE_SITE) assert.strictEqual(SITE, 'https://hivelab.ca');
  console.log('ok site name');
}

{
  const { redact } = require('../lib');
  for (const k of ['key=abcd1234efgh5678', 'token: "abcd1234efgh5678"', 'Authorization: Bearer abcd1234efgh5678', 'HIVE_KEY=abcd1234efgh5678']) {
    assert.ok(!/abcd1234efgh5678/.test(redact('before ' + k + ' after')), k);
  }
  assert.strictEqual(redact('Downloading 12 files, key ok'), 'Downloading 12 files, key ok');
  console.log('ok redact keys');
}

{
  const { appendLog, tailLog } = require('../lib');
  const f = require('path').join(require('os').tmpdir(), 'ab190-applog-' + process.pid);
  for (const x of [f, f + '.1']) try { require('fs').unlinkSync(x); } catch { }
  appendLog(f, 'opened http://127.0.0.1:1/login#SECRETKEYVALUE12345\nsecond');
  const got = tailLog(f);
  assert.ok(/opened/.test(got) && !/SECRETKEYVALUE/.test(got) && /\| second/.test(got), 'redacted, one line');
  appendLog(f, 'x'.repeat(100), 50); appendLog(f, 'after', 50);
  assert.ok(require('fs').existsSync(f + '.1'), 'rotated');
  assert.ok(!/opened/.test(tailLog(f)) && /after/.test(tailLog(f)));
  for (const x of [f, f + '.1']) try { require('fs').unlinkSync(x); } catch { }
  console.log('ok app log');
}
{
  // The shell ships no third-party code of its own: Electron and the build tools are dev-only, nothing is bundled from npm at run time.
  const pkg = require('../package.json');
  assert.deepStrictEqual(Object.keys(pkg.dependencies || {}), [], 'no runtime dependencies');
  const sh = require('child_process').spawnSync('npm', ['audit', '--omit=dev', '--audit-level=high'], { cwd: require('path').join(__dirname, '..'), encoding: 'utf8', shell: process.platform === 'win32', timeout: 60000 });
  assert.ok(sh.status === 0 || /ENOTFOUND|EAI_AGAIN|ECONNREFUSED|audit endpoint|network/i.test(sh.stderr + sh.stdout), 'no high-severity advisory in what ships: ' + sh.stdout);
  console.log('ok no runtime deps');
}

// ---- Many Hives, step 1 (DESIGN-many-hives.md) ----
{
  const { parseHives, stringifyHives } = require('../lib');
  const id1 = 'a1b2c3d4e5f60718', id2 = '0123456789abcdef';
  assert.deepStrictEqual(parseHives(null).hives, [], 'no file: first run');
  assert.ok(!parseHives(null).error);
  assert.ok(parseHives('{not json').error, 'broken file says so');
  assert.ok(parseHives('{"hives":{}}').error, 'hives must be a list');
  assert.ok(/newer app/.test(parseHives('{"v":2,"hives":[]}').error), 'a newer file is left alone');
  const text = JSON.stringify({ v: 1, theme: 'dark', hives: [
    { id: id2, kind: 'remote', url: 'https://h2.example.cc/login#SECRETKEY?x', name: '', order: 2, pinned: true },
    { id: id1, kind: 'local', user: 'hive', name: 'Home', order: 1, lastOpened: 5 },
    { id: id1, kind: 'local', user: 'other', order: 3 },                 // the same id twice: the second is dropped
    { id: 'XYZ', kind: 'local', user: 'hive' },                          // bad id
    { id: 'ffffffffffffffff', kind: 'remote', url: 'http://h2.example.cc' }, // plain http off this computer
    { id: 'eeeeeeeeeeeeeeee', kind: 'local', user: 'Root;rm' },           // not a user name
    { id: 'dddddddddddddddd', kind: 'ssh', url: 'https://x.cc' },          // unknown kind
  ] });
  const p = parseHives(text);
  assert.strictEqual(p.dropped, 5);
  assert.deepStrictEqual(p.hives.map(h => h.id), [id1, id2], 'sorted by order');
  assert.strictEqual(p.hives[1].url, 'https://h2.example.cc/', 'no key, no query, no /login kept');
  assert.strictEqual(p.hives[1].name, 'h2.example.cc', 'a remote Hive with no name is called by its host');
  assert.strictEqual(p.hives[1].pinned, true, 'unknown fields are kept');
  const back = stringifyHives(p);
  assert.ok(!/SECRETKEY/.test(back), 'the key is never written');
  const again = parseHives(back);
  assert.deepStrictEqual(again.hives, p.hives, 'round trip');
  assert.strictEqual(again.extra.theme, 'dark', 'unknown top-level fields are kept');
  assert.strictEqual(JSON.parse(back).v, 1);
  const named = parseHives(JSON.stringify({ v: 1, hives: [{ id: id1, kind: 'local', user: 'hive', name: 'a\u0007b\nc' }] }));
  assert.strictEqual(named.hives[0].name, 'abc', 'no control characters in a name');
  console.log('ok hives list');
}
{
  const { addressRule } = require('../lib');
  const ok = u => addressRule(u).ok, url = u => addressRule(u).url;
  assert.strictEqual(url('https://h2.ihsan.cc'), 'https://h2.ihsan.cc/');
  assert.strictEqual(url('  https://h2.ihsan.cc/login#abc  '), 'https://h2.ihsan.cc/');
  assert.strictEqual(url('https://h2.ihsan.cc:8443/hive/?k=1#x'), 'https://h2.ihsan.cc:8443/hive/');
  for (const u of ['http://127.0.0.1:8077', 'http://127.5.6.7:1', 'http://[::1]:8077/']) assert.ok(ok(u), u);
  for (const u of ['http://localhost:8077', 'http://192.168.1.5:8077', 'http://h2.ihsan.cc', 'http://127.0.0.1.evil.cc',
    'http://127.0.0.256', 'http://[::ffff:127.0.0.1]', 'ftp://h2.ihsan.cc', 'file:///etc/passwd', 'javascript:alert(1)',
    'https://user:pw@h2.ihsan.cc', 'https://user@h2.ihsan.cc', 'h2.ihsan.cc', '', 'not a url']) assert.ok(!ok(u), u);
  assert.ok(/https/.test(addressRule('http://h2.ihsan.cc').why), 'says why');
  console.log('ok address rule');
}
{
  const { sameHive, proofString } = require('../lib');
  const base = 'https://h2.ihsan.cc/';
  assert.ok(sameHive('https://h2.ihsan.cc/api/x?y', base));
  assert.ok(sameHive('https://H2.IHSAN.CC:443/', base), 'case and the default port');
  for (const u of ['https://evil.h2.ihsan.cc/', 'https://h2.ihsan.cc.evil.cc/', 'http://h2.ihsan.cc/', 'https://h2.ihsan.cc:8443/', 'https://ihsan.cc/', 'garbage'])
    assert.ok(!sameHive(u, base), u);
  assert.ok(sameHive('http://127.0.0.1:8077/x', 'http://127.0.0.1:8077/') && !sameHive('http://127.0.0.1:8078/', 'http://127.0.0.1:8077/'), 'port counts');
  assert.strictEqual(proofString('https://h2.ihsan.cc', 'n1'), 'proof:https://h2.ihsan.cc:n1');
  assert.notStrictEqual(proofString('http://127.0.0.1:8077', 'n1'), proofString('http://127.0.0.1:8078', 'n1'));
  console.log('ok same hive');
}
{
  const { hiveUserName, newId } = require('../lib');
  for (const n of ['hive', 'work', 'hive-2', 'a', 'w' + '1'.repeat(20)]) assert.strictEqual(hiveUserName(n, []), null, n);
  for (const n of ['', 'Hive', '2hive', 'hive_2', 'hive-', 'w' + '1'.repeat(21), 'a b', 'root', 'www-data', 'systemd-x', 'lima', 'docker', '../x', 'x;rm'])
    assert.ok(hiveUserName(n, []), n);
  assert.ok(/already/.test(hiveUserName('work', ['hive', 'work'])));
  const id = newId([]);
  assert.ok(/^[a-f0-9]{16}$/.test(id));
  let calls = 0;
  const rand = () => Buffer.from(calls++ ? '0102030405060708' : 'a1b2c3d4e5f60718', 'hex');
  assert.strictEqual(newId(['a1b2c3d4e5f60718'], rand), '0102030405060708', 'skips an id already used');
  assert.throws(() => newId(['a1b2c3d4e5f60718'], () => Buffer.from('a1b2c3d4e5f60718', 'hex')));
  console.log('ok hive user name');
}
{
  const { windowsState, windowState } = require('../lib');
  const screens = [{ x: 0, y: 0, width: 1920, height: 1080 }];
  const old = { x: 10, y: 20, width: 1200, height: 800, maximized: true };
  assert.deepStrictEqual(windowsState(old, screens, ['a1b2c3d4e5f60718']), [{ hive: null, rect: windowState(old, screens) }], 'the old single window opens on the local Hive');
  assert.deepStrictEqual(windowsState(null, screens), [{ hive: null, rect: null }], 'nothing saved: one window');
  assert.deepStrictEqual(windowsState('junk', screens), [{ hive: null, rect: null }]);
  const saved = { v: 1, windows: [
    { hive: 'a1b2c3d4e5f60718', x: 0, y: 0, width: 900, height: 700 },
    { hive: 'gone000000000000', x: 5000, y: 5000, width: 900, height: 700 },
    null,
  ] };
  const got = windowsState(saved, screens, ['a1b2c3d4e5f60718']);
  assert.strictEqual(got.length, 2);
  assert.strictEqual(got[0].hive, 'a1b2c3d4e5f60718');
  assert.deepStrictEqual(got[0].rect, { width: 900, height: 700, x: 0, y: 0 });
  assert.strictEqual(got[1].hive, null, 'a removed Hive opens as the local one');
  assert.deepStrictEqual(got[1].rect, { width: 900, height: 700 }, 'off every screen: size kept, place dropped');
  const many = { v: 1, windows: Array.from({ length: 30 }, () => ({ width: 900, height: 700 })) };
  assert.strictEqual(windowsState(many, screens).length, 12, 'at most 12 windows come back');
  console.log('ok window state old shape');
}
{
  const { redact } = require('../lib');
  assert.strictEqual(redact('open https://h2.ihsan.cc/login#SECRETKEY12345 now'), 'open https://h2.ihsan.cc/login#… now');
  assert.strictEqual(redact('open http://[::1]:8077/login/#SECRETKEY12345'), 'open http://[::1]:8077/login/#…');
  console.log('ok redact remote key');
}
{
  const { linkMenu, browserLabel } = require('../lib');
  const where = u => u.startsWith('https://h2.ihsan.cc/') ? 'this' : u.startsWith('http://127.0.0.1:8077/') ? 'hive' : 'outside';
  assert.deepStrictEqual(linkMenu({ linkURL: 'https://h2.ihsan.cc/files/a.pdf' }, where), ['openWindow', 'openBrowser', 'saveLink', 'copyLink']);
  assert.deepStrictEqual(linkMenu({ linkURL: 'http://127.0.0.1:8077/#t' }, where), ['openWindow', 'openBrowser', 'copyLink'], 'another Hive: no save through this one');
  assert.deepStrictEqual(linkMenu({ linkURL: 'https://example.com/' }, where), ['openBrowser', 'copyLink']);
  assert.deepStrictEqual(linkMenu({ linkURL: 'javascript:alert(1)', selectionText: 'x' }, where), ['copy'], 'no menu for a script link');
  assert.deepStrictEqual(linkMenu({ linkURL: 'https://example.com/', mediaType: 'image', srcURL: 'https://h2.ihsan.cc/i.png' }, where),
    ['openBrowser', 'copyLink', '-', 'copyImage', 'saveImage']);
  assert.deepStrictEqual(linkMenu({ mediaType: 'image', srcURL: 'https://cdn.example.com/i.png' }, where), ['copyImage']);
  assert.deepStrictEqual(linkMenu({ isEditable: true }, where), ['cut', 'copy', 'paste', 'selectAll']);
  assert.deepStrictEqual(linkMenu({ selectionText: '  ' }, where), []);
  assert.strictEqual(browserLabel('Google Chrome'), 'Open link in Google Chrome');
  assert.strictEqual(browserLabel('firefox.desktop'), 'Open link in firefox');
  assert.strictEqual(browserLabel(''), 'Open link in your browser');
  assert.strictEqual(browserLabel('x'.repeat(80)), 'Open link in your browser');
  console.log('ok link menu');
}
{
  // A remote Hive's sign-in gate (step 6) is kept only in the form the app reads.
  const { validHive } = require('../lib');
  const base = { id: 'a'.repeat(16), kind: 'remote', url: 'https://h2.example.cc/', name: 'H2' };
  const aud = 'c'.repeat(64);
  assert.deepStrictEqual(validHive({ ...base, gate: { team: 'Team.cloudflareaccess.com', aud, x: 1 } }).gate, { team: 'team.cloudflareaccess.com', aud });
  assert.ok(!('gate' in validHive({ ...base, gate: { team: 'evil.com', aud } })), 'a gate somewhere else is dropped');
  assert.ok(!('gate' in validHive({ ...base, gate: 'x' })));
  assert.ok(!('gate' in validHive(base)));
  console.log('ok saved gate');
}

// ---- Many Hives, step 7d: the list core's `hive-admin list --json` prints ----
{
  const { parseAdminList } = require('../lib');
  // ADMIN_JSON: a file core's test printed, read as the app reads it (dist/test_install.sh writes one).
  const text = process.env.ADMIN_JSON ? require('fs').readFileSync(process.env.ADMIN_JSON, 'utf8') : JSON.stringify({ v: 1, hives: [
    { user: 'hive', home: '/home/hive', port: 8077, version: 'v2.5.1', state: 'running',
      update: { tag: 'v2.5.1', newest: null, failed: [], error: null, checked: 1764200000 } },
    { user: 'hive-work', home: '/home/hive-work', port: 8078, version: 'v2.4.0', state: 'stopped',
      update: { tag: 'v2.4.0', newest: 'v2.5.1', failed: ['v2.5.0'], error: 'services did not come back', checked: 1764200000.5 } },
    { user: 'Bad User', home: '/home/x', port: 1, state: 'running' },        // not a user name: dropped
    { user: 'hive', home: '/home/hive', port: 8077, state: 'running' },       // the same user twice: dropped
    { user: 'odd', home: 'relative', port: 8079, state: 'running' },         // a home that isn't a path: dropped
  ] });
  const r = parseAdminList(text);
  assert.ok(!r.error, r.error);
  const by = Object.fromEntries(r.hives.map(h => [h.user, h]));
  assert.deepStrictEqual(Object.keys(by).sort(), ['hive', 'hive-work'], 'two Hives');
  assert.strictEqual(by.hive.state, 'running');
  assert.ok(Number.isInteger(by.hive.port) && Number.isInteger(by['hive-work'].port) && by.hive.port !== by['hive-work'].port, 'each its own port');
  assert.strictEqual(by['hive-work'].state, 'stopped');
  assert.deepStrictEqual(by['hive-work'].update, { tag: 'v2.4.0', newest: 'v2.5.1', failed: ['v2.5.0'], error: 'services did not come back', checked: 1764200000.5 });
  if (!process.env.ADMIN_JSON) assert.strictEqual(r.dropped, 3, 'malformed entries dropped');
  const odd = parseAdminList(JSON.stringify({ v: 1, hives: [{ user: 'hive', home: '/h', port: 99999, state: 'exploded', version: 7, update: { failed: [1, 'v2.0.1'] } }] })).hives[0];
  assert.deepStrictEqual([odd.port, odd.state, odd.version, odd.update.failed], [null, 'unknown', null, ['v2.0.1']], 'odd fields read as nothing');
  assert.ok(parseAdminList('nope').error && /newer/.test(parseAdminList('{"v":2,"hives":[]}').error) && parseAdminList('{"v":1}').error);
  console.log('ok admin list' + (process.env.ADMIN_JSON ? ' (from core)' : ''));
}
{
  // Step 8b: a Hive's own icon on its rail tile; its built-in mark, or anything that isn't a picture from that Hive, leaves the letter.
  const { pickIcon, iconData, validIcons, ICON_MAX } = require('../lib');
  const same = u => u.startsWith('https://h2.example/');
  assert.deepStrictEqual(pickIcon(['https://h2.example/api/brand/logo?v=17'], same), { kind: 'fetch', url: 'https://h2.example/api/brand/logo?v=17' });
  assert.strictEqual(pickIcon(['https://elsewhere.example/logo.png'], same), null, 'another site\'s picture is not fetched');
  assert.strictEqual(pickIcon(['file:///etc/passwd'], () => true), null);
  assert.deepStrictEqual(pickIcon([], same), { kind: 'none' });
  const mark = 'data:image/svg+xml,' + encodeURIComponent('<svg><path d="M781 389.2l106.6 61.4"/></svg>');
  assert.deepStrictEqual(pickIcon([mark], same), { kind: 'none' }, 'the built-in hex mark keeps the letter');
  assert.strictEqual(pickIcon(['https://h2.example/icons/icon-192.png'], same), null, 'the stock icon (before the page\'s script, or signed out) keeps what the tile had');
  assert.strictEqual(pickIcon(['https://h2.example/favicon.ico'], same), null);
  const png = 'data:image/png;base64,iVBORw0KGgo=';
  assert.deepStrictEqual(pickIcon([png], same), { kind: 'data', url: png });
  assert.strictEqual(pickIcon(['data:text/html,<script>1</script>'], same), null, 'only pictures');
  assert.strictEqual(pickIcon(['data:image/png;base64,' + 'A'.repeat(ICON_MAX * 2)], same), null, 'not a huge one');
  assert.strictEqual(iconData('image/png; charset=binary', Buffer.from([1, 2, 3])), 'data:image/png;base64,AQID');
  assert.strictEqual(iconData('text/html', Buffer.from('x')), null);
  assert.strictEqual(iconData('image/png', Buffer.alloc(ICON_MAX + 1)), null);
  assert.deepStrictEqual(validIcons({ local: png, a1b2c3d4e5f60718: 'https://x/y.png', '../x': png, b1b2c3d4e5f60718: mark }), { local: png }, 'icons.json read back keeps only pictures under Hive ids');
  assert.deepStrictEqual(validIcons([png]), {});
  console.log('ok rail icon choice');
}

t('signed from the start: the device key signs, and a Hive takes only its own fresh, pinned, unchanged posts', () => {
  const { newDeviceKey, deviceKey, signPost, checkPost, keyFingerprint, threadOf, hiveIdFrom, installCommand } = lib;
  const dev = newDeviceKey(), other = newDeviceKey();
  assert.match(dev.key, /^[A-Za-z0-9_-]{87}$/);
  assert.deepStrictEqual(deviceKey(dev.pkcs8), dev, 'the key read back from its file is the same key');
  assert.strictEqual(deviceKey('not a key'), null);
  const H = 'ab'.repeat(16), now = Date.parse('2026-11-27T12:00:00Z'), at = '2026-11-27T12:00:00.000Z';
  const o = (more = {}) => ({ hive: H, pinned: [dev.key], seen: new Set(), now, ...more });
  const b = signPost(dev, { hive: H, thread: 'abc', words: 'Ship it.', at, nonce: 'n1' });
  assert.deepStrictEqual(Object.keys(b), ['text', 'thread', 'reply_to', 'sig']);
  assert.deepStrictEqual(checkPost(b, o()), { ok: true, fp: keyFingerprint(dev.key) });
  const no = (body, more) => checkPost(body, o(more)).why;
  const edit = (f, s = {}) => ({ ...b, ...f, sig: { ...b.sig, ...s } });
  assert.strictEqual(no(b, { hive: 'cd'.repeat(16) }), 'signed for another Hive');
  assert.strictEqual(no(b, { seen: new Set(['n1']) }), 'a copy: this signature was used before');
  assert.strictEqual(no(b, { pinned: [other.key] }), 'the key is not pinned');
  assert.strictEqual(no(b, { now: now + 301e3 }), 'signed too long ago, or the clocks differ');
  assert.strictEqual(no(edit({ text: 'Ship it!' })), 'the signature does not match the words');
  assert.strictEqual(no(edit({ text: 'Do not. Ship it.' })), 'the signed length does not fit the text');
  assert.strictEqual(no(edit({ thread: 'xyz' })), 'signed for another thread');
  assert.strictEqual(no(edit({ reply_to: 'p1' })), 'signed for another thread');
  assert.strictEqual(no(edit({}, { thread: 'xyz' })), 'signed for another thread');
  assert.strictEqual(no(edit({}, { key: other.key }), { pinned: [other.key] }), 'the signature does not match the words', 'a pinned key can\'t be swapped onto another\'s signature');
  assert.strictEqual(no(edit({}, { nonce: 'a\nb' })), 'the signature is not in the expected form');
  assert.strictEqual(no({ text: 'x', thread: 'abc' }), 'not signed');
  assert.strictEqual(no(edit({}, { v: 1 })), 'not signed');
  assert.strictEqual(no(edit({}, { s: '%%%' })), 'the signature does not match the words');
  // Newlines in the words are signed as they are; the fields before them can't hold one, so the words can't be shifted.
  const ml = signPost(dev, { hive: H, thread: '', words: 'line one\nline two', at, nonce: 'n2' });
  assert.ok(checkPost(ml, o()).ok);
  const emoji = signPost(dev, { hive: H, words: 'ok 👍 é', at, nonce: 'n3' });
  assert.strictEqual(emoji.sig.n, 10, 'the length is in UTF-8 bytes'); assert.ok(checkPost(emoji, o()).ok);
  // The open thread, from the Hive page's address.
  assert.strictEqual(threadOf('http://127.0.0.1:8077/#comms/111014537970'), '111014537970');
  assert.strictEqual(threadOf('http://127.0.0.1:8077/#comms'), '');
  assert.strictEqual(threadOf('http://127.0.0.1:8077/#comms/a%2Fb'), '', 'only a plain id');
  assert.strictEqual(threadOf('http://127.0.0.1:8077/#comms/%E0%A4'), '');
  assert.strictEqual(hiveIdFrom(`\x1b[1mDone.\x1b[0m\nHive id: ${H}\n`), H);
  assert.strictEqual(hiveIdFrom('Hive id: 1234'), null);
  // The installer is handed the public key, never the private one.
  assert.strictEqual(installCommand('linux', {}, false, dev.key).env.HIVE_APP_KEY, dev.key);
  assert.strictEqual(installCommand('win32', {}, false, dev.key).env.HIVE_APP_KEY, dev.key);
  assert.strictEqual(installCommand('linux', {}, false, dev.pkcs8).env.HIVE_APP_KEY, undefined);
  assert.strictEqual(installCommand('linux', {}, false).env.HIVE_APP_KEY, undefined);
  console.log('ok signed post');
});
