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
