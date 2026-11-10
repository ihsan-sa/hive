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
  assert.ok(w.file.endsWith('powershell.exe')); assert.ok(w.args.at(-1).includes('/install.ps1 | iex')); assert.strictEqual(w.env.HIVE_ACCEPT, undefined);
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
