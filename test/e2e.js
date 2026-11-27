// xvfb-run -a node test/e2e.js (HIVE_APP_BIN=dist/linux-unpacked/hive-app tests the packaged app): the real app in a real (hidden) window, against a stand-in site and a stand-in Hive.
// Covers: no Hive -> welcome -> installer shows its warning -> accept -> installs -> the web app loads in the window;
// a second start goes straight in; outside links don't load in the window; a Hive that won't start says so.
const assert = require('assert');
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { _electron } = require('playwright-core');
const lib = require('../lib.js');

const win = process.platform === 'win32';    // Windows: install.ps1 run by PowerShell, hive.cmd, and wsl.exe (a compiled stand-in on PATH)
const mac = process.platform === 'darwin';   // the Mac installer has no accept step and keeps the Hive's command in ~/.hive/bin
const root = path.join(__dirname, '..');
const home = fs.mkdtempSync(path.join(process.env.TMPDIR || os.tmpdir(), 'hive-app-e2e-'));
const shots = path.join(root, 'test', 'shots'); fs.mkdirSync(shots, { recursive: true });

// Windows: the Hive's command lives under this LOCALAPPDATA (the real one; the test wipes it at the end and on a failure, so real.js's later run never meets the stand-in).
// (Electron won't start with a different one, so this uses the real one: refuse to touch a person's own machine.)
if (win && !process.env.CI) { console.error('On Windows this test replaces %LOCALAPPDATA%\\Hive: it only runs on a CI runner (CI=1)'); process.exit(2); }
const localApp = process.env.LOCALAPPDATA;
// Signed from the start (step 1): the stand-in installers pin the key the app hands them (in HIVE_APP_KEY) in pins.txt, as
// root will in pins.json, and print the Hive's id; the stand-in Hives check each post against it, as the root checker will.
const HIVE = require('crypto').randomBytes(16).toString('hex'), OTHERHIVE = require('crypto').randomBytes(16).toString('hex');
const pinsFile = path.join(home, 'pins.txt');
const posts = [];   // every post a stand-in Hive was sent: {at (which Hive), body, raw, r (its check), dash (the X-H2-Dash header)}
function postsTo(which, hiveId) {
  const nonces = new Set();
  return (req, res) => {
    let b = ''; req.on('data', d => { b += d; });
    req.on('end', () => {
      let body = null; try { body = JSON.parse(b); } catch { }
      const pinned = fs.existsSync(pinsFile) ? [fs.readFileSync(pinsFile, 'utf8').trim()] : [];
      const r = lib.checkPost(body, { hive: hiveId, pinned, seen: nonces, now: Date.now() });
      if (r.ok) nonces.add(body.sig.nonce);
      posts.push({ at: which, body, raw: b, r, dash: req.headers['x-h2-dash'] });
      res.setHeader('content-type', 'application/json'); res.statusCode = r.ok ? 200 : 403;
      res.end(JSON.stringify(r.ok ? { id: 'p' + posts.length } : { error: r.why }));
    });
  };
}
const localPosts = postsTo('local', HIVE), remotePosts = postsTo('remote', OTHERHIVE);
const rePost = (p, raw) => new Promise((ok, no) => { const q = http.request({ host: '127.0.0.1', port: p, path: '/api/post', method: 'POST', headers: { 'content-type': 'application/json' } }, r => { let o = ''; r.on('data', d => { o += d; }); r.on('end', () => ok({ code: r.statusCode, j: JSON.parse(o) })); }); q.on('error', no); q.end(raw); });
const ps1 = () => {
  const bad = (home + '\\broken');
  return `Write-Host "The Hive will run in your own account, as you."
if ($env:HIVE_ACCEPT -ne '1') { Write-Host "there's no terminal to ask you on, and nothing changed. If you accept the above, set HIVE_ACCEPT=1"; return }
Write-Host 'Setting up the Hive'
if ($env:HIVE_STUB_RESTART -eq '1') { Write-Host 'WSL is installed. Restart Windows, then run the same line again.'; return }
$b = Join-Path $env:LOCALAPPDATA 'Hive\\bin'
New-Item -ItemType Directory -Force -Path $b | Out-Null
Remove-Item -Force '${bad}' -ErrorAction SilentlyContinue
$cmd = @(
  '@echo off',
  'if exist "${bad}" goto bad',
  'echo http://localhost:${port}/login#SECRETKEY',
  'exit /b 0',
  ':bad',
  'echo The app didn''t start. Look at: systemctl --user status h2-dash',
  'exit /b 1') -join "\`r\`n"
[IO.File]::WriteAllText((Join-Path $b 'hive.cmd'), $cmd + "\`r\`n")
New-Item -ItemType Directory -Force -Path '${home}\\hive' | Out-Null
New-Item -ItemType File -Force -Path '${home}\\hive\\.hive-installed' | Out-Null
Write-Host "The Hive is installed. Open this address: http://localhost:${port}/login#SECRETKEY"
if ($env:HIVE_APP_KEY) { [IO.File]::WriteAllText('${pinsFile}', $env:HIVE_APP_KEY) }
Write-Host "Hive id: ${HIVE}"
`;
};
const server = http.createServer((req, res) => {
  if (req.method === 'POST' && req.url === '/api/post') return localPosts(req, res);
  if (req.url === '/install.ps1') {
    ps1Asked++;
    res.setHeader('content-type', 'text/plain; charset=utf-8');
    return res.end(failInstall ? "Write-Host 'Downloading the Hive'\nWrite-Host 'could not reach github.com (stub failure)'\nreturn\n" : ps1());
  }
  if ((req.url === '/install.sh' || req.url === '/install-mac.sh') && failInstall) {
    res.end('#!/bin/sh\necho "Downloading the Hive"\necho "could not reach github.com (stub failure)"\nexit 5\n');
  } else if (req.url === '/install.sh' || req.url === '/install-mac.sh') {
    // Stands in for get.sh --this-account: warns, wants HIVE_ACCEPT=1 with no terminal, then makes ~/hive with a hive command.
    res.end(`#!/bin/sh
[ "$HIVE_MAC_TEST" = 1 ] || [ "$1" = --this-account ] || { echo "expected --this-account"; exit 2; }
echo "The Hive will run in your own account, as you, with its files in $HOME/hive."
if [ "$HIVE_ACCEPT" != 1 ] && [ "$HIVE_MAC_TEST" != 1 ]; then
  if ( : </dev/tty ) 2>/dev/null; then echo "BUG: the app gave the installer a terminal"; exit 3; fi
  echo "there's no terminal to ask you on, and nothing changed. If you accept the above, set HIVE_ACCEPT=1"; exit 1
fi
[ "$HIVE_NO_LAUNCHER" = 1 ] || [ "$HIVE_MAC_TEST" = 1 ] || { echo "launcher not skipped"; exit 4; }
printf 'Installing Claude Code\\rSetting up the Hive\\n'
${slowInstall ? 'echo $$ > "$HOME/installer.pid"; sleep 60' : ''}
[ "$HIVE_STUB_RESTART" != 1 ] || { echo "WSL is installed. Restart Windows, then run the same line again."; exit 0; }
if [ "$HIVE_MAC_TEST" = 1 ]; then B="$HOME/.hive/bin"; else B="$HOME/hive/.local/bin"; fi
mkdir -p "$B"
rm -f "$HOME/broken"   # a repair run puts a broken install right
cat > "$B/hive" <<X
#!/bin/sh
[ ! -e "$HOME/broken" ] || { echo "The app didn't start. Look at: systemctl --user status h2-dash"; exit 1; }
echo "http://localhost:${port}/login#SECRETKEY"
X
chmod +x "$B/hive"; touch "$HOME/hive/.hive-installed"
echo "The Hive is installed. Open this address: http://localhost:${port}/login#SECRETKEY"
[ -z "$HIVE_APP_KEY" ] || printf '%s' "$HIVE_APP_KEY" > '${pinsFile}'
echo "Hive id: ${HIVE}"
`);
  } else if (req.url.startsWith('/api/proof?n=')) {
    // The stand-in Hive holds the key, so it can make the mark. With `impostor` set it is some other program on the Hive's port.
    asked.push(req.url);
    if (impostor === 'down') return req.destroy();
    if (impostor) { res.statusCode = impostor === 'old' ? 404 : 200; return res.end(impostor === 'old' ? 'no' : 'f'.repeat(64)); }
    res.end(require('crypto').createHmac('sha256', 'SECRETKEY').update(`proof:${port}:${req.url.split('n=')[1]}`).digest('hex'));
  } else if (req.url.startsWith('/login')) { seen.push(req.url); res.setHeader('content-type', 'text/html'); res.end('<title>Hive web app</title><h1 id="app">The web app</h1><a id="out" href="https://example.com/">out</a><a id="in" href="/thread">in</a><a id="away" href="/away">away</a><a id="dl" href="/file.txt" download>file</a>'); }
  else if (req.url === '/app-version.json' && appVersion === 'MOVED') { siteAsked.push(req.url); res.statusCode = 302; res.setHeader('location', '/v2.json'); res.end(); }
  else if (req.url === '/v2.json') { res.setHeader('content-type', 'application/json'); res.end('{"version":"99.0.0"}'); }
  else if (req.url === '/app-version.json' && !appVersion) { siteAsked.push(req.url); res.statusCode = 404; res.end(); }
  else if (req.url === '/app-version.json' && appVersion) { siteAsked.push(req.url); res.setHeader('content-type', 'application/json'); res.end(appVersion); }
  else if (req.url === '/file.txt') { res.setHeader('content-type', 'text/plain'); res.setHeader('content-disposition', 'attachment; filename="notes.txt"'); res.end('hello from the hive'); }
  else if (req.url === '/away') { res.statusCode = 302; res.setHeader('location', 'https://example.com/'); res.end(); }
  else if (req.url === '/thread') { res.setHeader('content-type', 'text/html'); res.end('<h1 id="thread">A thread</h1>'); }
  else { res.statusCode = 404; res.end('no'); }
});
// A second Hive somewhere else (here another port of this computer), for a window with more than one (DESIGN-many-hives.md, step 3).
// Its key is OTHERKEY. /login signs in with a cookie of its own; its pages show only to that cookie.
let rport = 0; const rseen = [];
const LOGO = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';
const remote = http.createServer((req, res) => {
  if (req.method === 'POST' && req.url === '/api/post') return remotePosts(req, res);
  if (req.url.startsWith('/api/proof?n=')) return res.end(require('crypto').createHmac('sha256', 'OTHERKEY').update(`proof:${rport}:${req.url.split('n=')[1]}`).digest('hex'));
  // Its logo (step 8b) is a picture only its signed-in pages can fetch, as a Hive's /api/brand/logo is.
  if (req.url === '/logo.png' && /(^|;\s*)hive=remote/.test(req.headers.cookie || '')) { res.setHeader('content-type', 'image/png'); return res.end(Buffer.from(LOGO, 'base64')); }
  const page = `<title>Other Hive</title><link rel="icon" href="/logo.png"><h1 id="app">The other Hive</h1><p id="remote">remote</p><button id="pop" onclick="window.open('/', 'signin', 'popup,width=500,height=600')">pop</button>`
    + `<a id="same" href="/" target="_blank">same</a><a id="other" href="http://127.0.0.1:${port}/thread">other</a><a id="far" href="https://example.com/">far</a>`;
  res.setHeader('content-type', 'text/html');
  if (req.url.startsWith('/login')) { rseen.push(req.url); res.setHeader('set-cookie', 'hive=remote; Path=/; HttpOnly; Max-Age=86400'); return res.end(page); }
  if (/(^|;\s*)hive=remote/.test(req.headers.cookie || '')) return res.end(page);
  res.statusCode = 401; res.end('<h1 id="nope">Sign in first</h1>');
});
// A wait for the installer to run: a cold runner under emulation (HIVE_TEST_SLOW=1, Windows on ARM) starts PowerShell slowly, each time.
const WAIT = process.env.HIVE_TEST_SLOW === '1' ? 90000 : 20000;
let appVersion = '', port, impostor = '', needRestart = false, failInstall = false, slowInstall = false, siteOverride = '';
const asked = [], seen = [], siteAsked = [];   // the proof questions, and the page loads, the stand-in server got

let cur = null, ps1Asked = 0;   // the window now open, and how often the stand-in site served install.ps1 (both said on a failure)
async function start(extra = {}) {
  const env = { ...process.env, HOME: home, HIVE_APP_TEST: '1', HIVE_APP_DOWNLOADS: path.join(home, 'Downloads'), HIVE_APP_WATCH_MS: '700', ...(needRestart ? { HIVE_STUB_RESTART: '1' } : {}), HIVE_SITE: siteOverride || `http://127.0.0.1:${port}`, XDG_CONFIG_HOME: path.join(home, '.config'), XDG_DATA_HOME: path.join(home, '.local/share'), ...extra };
  delete env.HIVE_DIR;
  if (win) {   // the stand-in wsl.exe first on PATH (Windows keeps the name's case: Path)
    const k = Object.keys(env).find(x => x.toLowerCase() === 'path') || 'Path';
    env[k] = wslDir + ';' + (env[k] || '');
  }
  const app = await _electron.launch(process.env.HIVE_APP_BIN ? { executablePath: process.env.HIVE_APP_BIN, args: ['--no-sandbox'], env } : { executablePath: require('electron'), args: [root, '--no-sandbox'], env });
  // The window's own page is the app's page (setup, Hives, Connect); a Hive shows in a view of its own over it, which can start first.
  let page = await app.firstWindow();
  for (let i = 0; i < 300 && !/setup\.html/.test(page.url()); i++) { await new Promise(r => setTimeout(r, 50)); page = app.windows().find(p => /setup\.html/.test(p.url())) || page; }
  if (process.env.HIVE_TEST_SLOW === '1') page.setDefaultTimeout(WAIT);   // the waits with no timeout of their own too
  cur = page;
  return { app, page };
}
// Windows: a stand-in wsl.exe (the real one needs a distro): `bash -lc 'hive open --print'` runs the stand-in Hive's hive.cmd; keeping WSL up just sleeps.
const wslDir = path.join(home, 'wslbin');
function buildWsl() {
  if (!win) return;
  fs.mkdirSync(wslDir, { recursive: true });
  fs.writeFileSync(path.join(wslDir, 'wsl.cs'), `using System; using System.Diagnostics; using System.IO;
class P { static int Main(string[] a) {
  if (string.Join(" ", a).Contains("sleep")) { System.Threading.Thread.Sleep(60000); return 0; }
  string c = Path.Combine(Environment.GetEnvironmentVariable("LOCALAPPDATA"), "Hive", "bin", "hive.cmd");
  var i = new ProcessStartInfo("cmd.exe", "/c \\"\\"" + c + "\\"\\"") { UseShellExecute = false };
  var p = Process.Start(i); p.WaitForExit(); return p.ExitCode; } }
`);
  const csc = path.join(process.env.windir || 'C:\\Windows', 'Microsoft.NET', 'Framework64', 'v4.0.30319', 'csc.exe');
  const r = require('child_process').spawnSync(csc, ['/nologo', `/out:${path.join(wslDir, 'wsl.exe')}`, path.join(wslDir, 'wsl.cs')], { encoding: 'utf8' });
  assert.strictEqual(r.status, 0, 'compiling the stand-in wsl.exe: ' + r.stdout + r.stderr);
}
// Windows: the app keeps its Hives and windows in the real %APPDATA%\\Hive (as with LOCALAPPDATA, Electron won't take another).
// The test's go at its start, its end and on a failure, so the next run (the packaged app's, real.js) doesn't open a Hive of the test's.
function forgetHives() {
  if (!win) return;
  const d = path.join(process.env.APPDATA, 'Hive');
  for (const f of ['hives.json', 'window-state.json']) fs.rmSync(path.join(d, f), { force: true });
  try { for (const p of fs.readdirSync(path.join(d, 'Partitions'))) if (p.startsWith('hive-')) fs.rmSync(path.join(d, 'Partitions', p), { recursive: true, force: true }); } catch { }
}
// A machine with no Hive on it.
function wipe() { for (const d of ['hive', '.hive']) fs.rmSync(path.join(home, d), { recursive: true, force: true });
  if (win) fs.rmSync(path.join(localApp, 'Hive'), { recursive: true, force: true }); }
const title = page => page.locator('#title').textContent();
// A Hive's page (DESIGN-many-hives.md, step 3): a view of its own over the window's page, so a page of its own to the driver.
// Waits for one at that origin showing the web app (#app), and returns it.
async function hive(app, origin = `http://127.0.0.1:${port}`, timeout = WAIT) {
  const end = Date.now() + timeout;
  for (;;) {
    for (const p of app.windows()) { try { if (p.url().startsWith(origin + '/') && await p.$('#app')) return p; } catch { } }
    if (Date.now() > end) throw new Error(`no Hive page at ${origin} in ${timeout} ms: ` + app.windows().map(p => p.url().replace(/#.*/, '#…')).join(' '));
    await new Promise(r => setTimeout(r, 200));
  }
}
// What each window shows: its top view's address (a Hive), else its own page's (the app's page).
const showing = app => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => (w.contentView.children.at(-1)?.webContents ?? w.webContents).getURL()));
const menuClick = (app, id) => app.evaluate(({ Menu }, id) => { const m = Menu.getApplicationMenu().getMenuItemById(id); if (!m) throw new Error('no menu item ' + id); m.click(); }, id);

(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r)); port = server.address().port; buildWsl(); forgetHives();
  const t0 = Date.now();
  let { app, page } = await start();

  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  const startupMs = Date.now() - t0;   // launch to the setup screen showing (cold, packaged or not)
  // The Windows on ARM runner (HIVE_TEST_SLOW=1) starts a just-unpacked Electron cold under its virus scan: about 10 s there
  // against 0.5 to 5 s elsewhere, so it gets twice the budget; the number is in the log either way.
  const budget = process.env.HIVE_TEST_SLOW === '1' ? 20000 : 10000;
  assert.ok(startupMs < budget, `the window shows its first screen within ${budget / 1000} s (took ${startupMs} ms)`);
  console.log(`ok startup time (${startupMs} ms)`);
  await page.screenshot({ path: path.join(shots, '1-welcome.png') });
  await page.getByRole('button', { name: 'Install' }).click();

  if (!mac) {   // the first PowerShell start on a cold runner can be slow: up to 90 s there
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: process.env.HIVE_TEST_SLOW === '1' ? 90000 : 30000 });
    const warn = await page.locator('#log').textContent();
    assert.ok(warn.includes('The Hive will run in your own account'), 'the installer\'s own warning is on the screen');
    assert.ok(!warn.includes('BUG'), warn);
    assert.ok(!fs.existsSync(path.join(home, 'hive')), 'nothing installed before the yes');
    await page.screenshot({ path: path.join(shots, '2-accept.png') });
    await page.getByRole('button', { name: 'Accept and install' }).click();
  }

  // From here `page` is the Hive's own page (its view); `setup` the app's page under it.
  const setup = page; page = await hive(app, undefined, WAIT);
  assert.strictEqual(new URL(page.url()).port, String(port));
  assert.strictEqual(new URL(page.url()).hostname, '127.0.0.1', 'the window uses the one address the Hive listens on, not "localhost" (which is also ::1)');
  assert.ok(asked.length && asked.every(u => !u.includes('SECRETKEY')));
  assert.strictEqual(await page.evaluate(() => typeof window.hive), 'undefined', 'the web app gets no bridge to the shell... ');
  await page.screenshot({ path: path.join(shots, '3-app.png') });

  // Signed from the start: the app's own bar sits under the Hive's page, and the first thing typed there reaches the Hive
  // signed with the key the installer pinned, into the thread the page has open. Nothing to click but Send.
  let bar = null;
  for (let end = Date.now() + WAIT; !bar; await new Promise(r => setTimeout(r, 100))) {
    bar = app.windows().find(p => /composer\.html$/.test(p.url().split(/[?#]/)[0]));
    if (!bar && Date.now() > end) throw new Error('no composer bar: ' + app.windows().map(p => p.url().replace(/#.*/, '#…')).join(' '));
  }
  assert.ok(fs.existsSync(pinsFile), 'the installer was handed the app\'s key');
  const pin = fs.readFileSync(pinsFile, 'utf8').trim();
  assert.match(pin, /^[A-Za-z0-9_-]{87}$/, 'a public key, not the private one');
  const keyAt = path.join(await app.evaluate(({ app }) => app.getPath('userData')), 'device-key.json');
  const kf = fs.readFileSync(keyAt, 'utf8');
  if (!win) assert.strictEqual(fs.statSync(keyAt).mode & 0o077, 0, 'the key file is the owner\'s only');
  const kj = JSON.parse(kf);
  assert.ok(typeof kj.store === 'string', 'the key file says what locked it');
  if (win || mac) assert.strictEqual(kj.enc, true, 'Windows and the Mac lock the key with the OS keychain');
  assert.ok(!kf.includes(pin), 'the key file holds the private key only');
  assert.strictEqual(await page.evaluate(() => typeof window.bar), 'undefined', 'the Hive\'s page has no way to the bar');
  await bar.waitForFunction(() => document.querySelector('#to').textContent === 'To a new thread');
  await page.evaluate(() => { location.hash = '#comms/abc'; });
  await bar.waitForFunction(() => document.querySelector('#to').textContent === 'To thread: abc');
  await bar.locator('#t').fill('Ship the first build.');
  await bar.locator('#go').click();
  await bar.waitForFunction(() => /Sent|refused|Couldn|changed|said/.test(document.querySelector('#note').textContent));
  assert.strictEqual(await bar.locator('#note').textContent(), 'Sent, signed', 'the bar says it went');
  const first = posts.at(-1);
  assert.ok(first && first.at === 'local' && first.r.ok, 'the Hive took it: ' + JSON.stringify(first && first.r));
  assert.strictEqual(first.body.text, 'Ship the first build.'); assert.strictEqual(first.body.thread, 'abc');
  assert.strictEqual(first.body.sig.key, pin, 'signed with the pinned key'); assert.strictEqual(first.dash, '1');
  assert.strictEqual(await bar.locator('#t').inputValue(), '', 'the box is cleared');
  await bar.screenshot({ path: path.join(shots, '3b-bar.png') });
  console.log('ok signed from start');
  // The same post sent again, or to another Hive (even one that pins the same key), is refused; so is a send for a thread
  // that is no longer the open one.
  if (!rport) { await new Promise(r => remote.listen(0, '127.0.0.1', r)); rport = remote.address().port; }
  const again = await rePost(port, first.raw);
  assert.strictEqual(again.code, 403); assert.strictEqual(again.j.error, 'a copy: this signature was used before');
  const there = await rePost(rport, first.raw);
  assert.strictEqual(there.code, 403); assert.strictEqual(there.j.error, 'signed for another Hive');
  const stale = await bar.evaluate(() => window.bar.send('Delete it all.', 'zzz'));
  assert.ok(!stale.ok && /thread changed/.test(stale.why), 'a stale thread is refused: ' + JSON.stringify(stale));
  assert.ok(!posts.some(p => p.body && p.body.text === 'Delete it all.'), 'and never reaches the Hive');
  // The page changes its thread while the owner types: the next Send only says so, and nothing goes until a second one.
  await page.evaluate(() => { location.hash = '#comms/abc'; });
  await bar.waitForFunction(() => document.querySelector('#to').textContent === 'To thread: abc');
  await bar.locator('#t').fill('Second one.');
  await page.evaluate(() => { location.hash = '#comms/def'; });
  await bar.waitForFunction(() => document.querySelector('#note').textContent === 'The thread changed while you typed.');
  await bar.locator('#go').click();
  await bar.waitForFunction(() => /Send again/.test(document.querySelector('#note').textContent));
  assert.ok(!posts.some(p => p.body && p.body.text === 'Second one.'), 'a thread switch under the owner sends nothing');
  await bar.locator('#go').click();
  await bar.waitForFunction(() => /Sent|refused|Couldn|said/.test(document.querySelector('#note').textContent));
  assert.strictEqual(await bar.locator('#note').textContent(), 'Sent, signed');
  assert.strictEqual(posts.at(-1).body.text, 'Second one.'); assert.strictEqual(posts.at(-1).body.thread, 'def', 'to the thread it showed at the second Send');
  // Text with hidden characters, or a lone surrogate, is never signed.
  for (const t of ['Ship it.\u202E.ti pihs', 'a\u200Bb']) {
    const r = await bar.evaluate(t => window.bar.send(t, 'def'), t);
    assert.ok(!r.ok && /hidden/.test(r.why), 'hidden characters refused: ' + JSON.stringify(r));
  }
  assert.ok(!(await bar.evaluate(() => window.bar.send('a\uD800b', 'def'))).ok, 'ill-formed text refused');
  await page.evaluate(() => { location.hash = ''; });
  console.log('ok no replay');

  // An inside link loads in the window; an outside one must not (asked of the shell itself: the driver loses track after a blocked link).
  await page.locator('#in').click(); await page.waitForSelector('#thread');
  await page.goBack(); await page.waitForSelector('#out');
  await page.evaluate(() => { document.querySelector('#out').click(); });
  await new Promise(r => setTimeout(r, 1500));
  const at = await showing(app);
  assert.strictEqual(at.length, 1); assert.ok(at[0].startsWith(`http://127.0.0.1:${port}/`), 'outside link stayed out of the window: ' + at);
  // Nor may the Hive's own address send the window elsewhere.
  await page.evaluate(() => { document.querySelector('#away').click(); });
  await new Promise(r => setTimeout(r, 1500));
  const at2 = await showing(app);
  assert.ok(at2.length === 1 && at2[0].startsWith(`http://127.0.0.1:${port}/`), 'a redirect stayed out of the window: ' + at2);
  // The Hive's own page may use notifications; anything else it asks for (location, say) is refused.
  const notif = await page.evaluate(() => Notification.requestPermission());
  assert.strictEqual(notif, 'granted', 'the Hive page may show notifications');
  const geo = await page.evaluate(() => new Promise(r => navigator.geolocation.getCurrentPosition(() => r('allowed'), e => r(e.code === 1 ? 'denied' : 'error ' + e.code), { timeout: 5000 })));
  assert.strictEqual(geo, 'denied', 'location is refused');
  console.log('ok permissions');
  // Right-click on a link: the app shows its own menu (Electron has none), and "Copy link" puts the address on the clipboard.
  // Menu.popup is wrapped to see what it pops; only Linux also draws it (a Mac or Windows menu holds the app until someone closes it).
  await app.evaluate(({ Menu, clipboard }) => {
    clipboard.writeText('');
    const real = Menu.prototype.popup;
    Menu.prototype.popup = function (o) {
      global.popped = this;
      if (process.platform !== 'linux') return;
      real.call(this, o); setTimeout(() => this.closePopup(), 500);
    };
  });
  await page.locator('#out').click({ button: 'right' });
  let menu = null;
  for (let i = 0; i < 50 && !menu; i++) { await new Promise(r => setTimeout(r, 200)); menu = await app.evaluate(() => global.popped ? global.popped.items.map(m => m.label) : null); }
  // A Mac selects the word under a right-click, as Safari does, so Copy (the link's text) is offered there too.
  assert.deepStrictEqual(menu, process.platform === 'darwin' ? ['Copy link', 'Copy'] : ['Copy link'], 'right-click on a link pops the menu');
  const link = await app.evaluate(({ clipboard }) => { global.popped.items[0].click(); return clipboard.readText(); });
  assert.strictEqual(link, 'https://example.com/', '"Copy link" copies the link');
  assert.strictEqual(new URL(page.url()).port, String(port), 'the right-click did not follow the link');
  console.log('ok context menu');
  // A file link on the Hive page saves into the Downloads folder (twice: the second gets a new name), and the window stays on the page.
  await page.evaluate(() => { document.querySelector('#dl').click(); });
  const dl = path.join(home, 'Downloads');
  for (let i = 0; i < 50 && !fs.existsSync(path.join(dl, 'notes.txt')); i++) await new Promise(r => setTimeout(r, 200));
  assert.strictEqual(fs.readFileSync(path.join(dl, 'notes.txt'), 'utf8'), 'hello from the hive');
  await page.evaluate(() => { document.querySelector('#dl').click(); });
  for (let i = 0; i < 50 && !fs.existsSync(path.join(dl, 'notes (1).txt')); i++) await new Promise(r => setTimeout(r, 200));
  assert.ok(fs.existsSync(path.join(dl, 'notes (1).txt')), 'a second download keeps the first');
  assert.ok(page.url().startsWith(`http://127.0.0.1:${port}/`), 'the window stayed on the Hive page');
  console.log('ok download');
  // Sessions (DESIGN-many-hives.md R1, R11): the local Hive keeps the default session, so a sign-in made before the update stays.
  const parts = await app.evaluate(({ webContents, session }, port) => webContents.getAllWebContents()
    .filter(w => w.getURL().startsWith(`http://127.0.0.1:${port}/`)).map(w => w.session === session.defaultSession), port);
  assert.ok(parts.length && parts.every(Boolean), 'the local Hive is in the default session: ' + parts);
  console.log('ok local keeps default session');
  // A session the app didn't set up refuses everything, even for the Hive's own address: a notification and a download.
  const fresh = await app.evaluate(async ({ BrowserWindow }, port) => {
    const w = new BrowserWindow({ show: false, webPreferences: { partition: 'persist:e2e-fresh', sandbox: true, contextIsolation: true } });
    try {
      await w.loadURL(`http://127.0.0.1:${port}/thread`);
      const perm = await w.webContents.executeJavaScript('Notification.requestPermission()');
      const got = await new Promise(res => {
        w.webContents.session.once('will-download', (_e, item) => setTimeout(() => res(item.getState()), 500));   // the app's own handler ran first
        w.webContents.downloadURL(`http://127.0.0.1:${port}/file.txt`);
        setTimeout(() => res('no answer'), 5000);
      });
      return { perm, got };
    } finally { w.destroy(); }
  }, port);
  assert.deepStrictEqual(fresh, { perm: 'denied', got: 'cancelled' }, 'a fresh partition denies');
  console.log('ok fresh partition denies');
  // A certificate nothing trusts is refused, with no way past it, and the refusal is in the log.
  const tls = require('https').createServer({ key: fs.readFileSync(path.join(__dirname, 'selfsigned.pem')), cert: fs.readFileSync(path.join(__dirname, 'selfsigned.pem')) },
    (_q, r) => r.end('<h1 id="bad">should not load</h1>'));
  await new Promise(r => tls.listen(0, '127.0.0.1', r));
  const certAt = `https://127.0.0.1:${tls.address().port}/`;
  const certGot = await app.evaluate(async ({ BrowserWindow }, u) => {
    const w = new BrowserWindow({ show: false });
    try { await w.loadURL(u); return 'loaded ' + await w.webContents.executeJavaScript('document.body.innerText'); }
    catch (e) { return String(e.code || e.message); } finally { w.destroy(); }
  }, certAt);
  tls.close();
  assert.ok(/CERT/.test(certGot), 'a self-signed https page is refused: ' + certGot);
  const userData = await app.evaluate(({ app }) => app.getPath('userData'));
  const certLog = fs.readFileSync(path.join(userData, 'app.log'), 'utf8');
  assert.ok(certLog.includes('certificate refused ' + certAt), 'the refusal is in the log');
  console.log('ok cert refused');

  // More than one Hive (DESIGN-many-hives.md, step 3): Hives > Connect, by the address as typed; each Hive in its own session.
  if (!rport) { await new Promise(r => remote.listen(0, '127.0.0.1', r)); rport = remote.address().port; }
  const rorigin = `http://127.0.0.1:${rport}`;
  await menuClick(app, 'hives-connect');
  await setup.waitForSelector('#address');
  assert.ok((await showing(app)).every(u => u.startsWith('file:')), 'the Connect screen shows over the Hive');
  const connectTo = async a => {   // what the screen says back (empty once it connected)
    await setup.fill('#address', a); await setup.click('#go');
    await setup.waitForFunction(() => !document.querySelector('#go') || (!document.querySelector('#go').disabled && document.querySelector('#err').textContent), null, { timeout: WAIT });
    return (await setup.locator('#err').count()) ? setup.locator('#err').textContent() : '';
  };
  assert.ok((await connectTo('http://192.168.1.5/')).includes('only for this computer'), 'plain http to another computer is refused');
  assert.ok((await connectTo(`127.0.0.1:${rport}/login#WRONGKEY`)).includes("didn't show it is the Hive"), 'a wrong key is refused');
  assert.strictEqual(rseen.length, 0, 'with the wrong key nothing was loaded from it');
  await setup.screenshot({ path: path.join(shots, '8-connect.png') });
  await setup.fill('#address', `127.0.0.1:${rport}/login#OTHERKEY`); await setup.click('#go');
  const rp = await hive(app, rorigin);
  assert.ok((await showing(app))[0].startsWith(rorigin + '/'), 'the window shows the other Hive');
  assert.strictEqual(await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].getTitle()), 'Other Hive');
  const hivesJson = () => fs.readFileSync(path.join(userData, 'hives.json'), 'utf8');
  const kept = JSON.parse(hivesJson()), rid = kept.hives.find(h => h.kind === 'remote')?.id;
  assert.ok(rid && kept.hives.find(h => h.id === rid).url === rorigin + '/', 'the Hive is kept by its address: ' + hivesJson());
  assert.ok(!/OTHERKEY|login/.test(hivesJson()), 'and not its key');
  await rp.screenshot({ path: path.join(shots, '9-other-hive.png') });
  console.log('ok add hive');
  // Hives > This computer and back: each Hive's page stays as it was (no reload), the menu says which shows.
  await rp.evaluate(() => { window.__kept = 1; });
  await menuClick(app, 'hive-local');
  assert.ok((await showing(app))[0].startsWith(`http://127.0.0.1:${port}/`), 'Hives > This computer shows the local Hive');
  assert.strictEqual(await app.evaluate(({ Menu }) => Menu.getApplicationMenu().getMenuItemById('hive-local').checked), true);
  await menuClick(app, 'hive-' + rid);
  assert.ok((await showing(app))[0].startsWith(rorigin + '/'), 'and back');
  assert.strictEqual(await rp.evaluate(() => window.__kept), 1, 'the other Hive was kept open, not loaded again');
  console.log('ok switch hive');
  // Its sign-in is in its own session: the local Hive's (the default session) has no sight of it.
  const jars = await app.evaluate(async ({ session }, rid) => ({ def: (await session.defaultSession.cookies.get({})).map(c => c.name),
    own: (await session.fromPartition('persist:hive-' + rid).cookies.get({})).map(c => c.name) }), rid);
  assert.ok(jars.own.includes('hive') && !jars.def.includes('hive'), 'cookies apart: ' + JSON.stringify(jars));
  console.log('ok cookies apart');
  // A window its page opens stays in its session, so it is signed in to that Hive (the cookie-only page shows).
  const [pop] = await Promise.all([app.waitForEvent('window'), rp.click('#pop')]);
  await pop.waitForSelector('#remote', { timeout: WAIT });
  const sess = await app.evaluate(({ webContents, session }, [o, rid]) => webContents.getAllWebContents().filter(w => w.getURL().startsWith(o + '/'))
    .map(w => w.session === session.fromPartition('persist:hive-' + rid)), [rorigin, rid]);
  assert.ok(sess.length === 2 && sess.every(Boolean), 'the popup is in the Hive\'s session: ' + sess);
  await pop.close();
  console.log('ok popup same session');
  // Many windows (step 4). With two Hives the rail shows down the left of the window, the Hive's view beside it.
  await setup.waitForSelector('#rail:not([hidden]) #tile-' + rid + '.on');
  assert.strictEqual(await setup.locator('#rail .tile').count(), 2, 'a tile for each Hive');
  assert.strictEqual(await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].contentView.children.at(-1).getBounds().x), 72, 'the view sits beside the rail');
  await setup.screenshot({ path: path.join(shots, '11-rail.png') });   // the rail (the page under the view) and the view beside it, as two pictures
  fs.writeFileSync(path.join(shots, '11-rail-view.png'), Buffer.from(await app.evaluate(async ({ BrowserWindow }) => (await BrowserWindow.getAllWindows()[0].contentView.children.at(-1).webContents.capturePage()).toPNG().toString('base64')), 'base64'));
  await setup.click('#tile-local');
  await setup.waitForSelector('#tile-local.on');
  assert.ok((await showing(app))[0].startsWith(`http://127.0.0.1:${port}/`), 'its tile shows the local Hive');
  await setup.click('#tile-' + rid);
  await setup.waitForSelector('#tile-' + rid + '.on');
  assert.ok((await showing(app))[0].startsWith(rorigin + '/'), 'and back');
  console.log('ok rail');
  // Step 8b: a Hive's own logo is on its tile, fetched in its own session (the logo needs its sign-in); with none, the letter.
  await setup.waitForSelector(`#tile-${rid} .hex img`, { timeout: WAIT });
  assert.strictEqual(await setup.getAttribute(`#tile-${rid} .hex img`, 'src'), 'data:image/png;base64,' + LOGO, 'the tile shows the Hive\'s logo');
  assert.strictEqual(JSON.parse(fs.readFileSync(path.join(userData, 'icons.json'), 'utf8'))[rid], 'data:image/png;base64,' + LOGO, 'and keeps it');
  assert.strictEqual(await setup.locator('#tile-local .hex img').count(), 0, 'a Hive with no logo has none');
  assert.strictEqual(await setup.locator('#tile-local .hex').textContent(), 'T', 'but its letter');
  await setup.screenshot({ path: path.join(shots, '11b-rail-icon.png') });
  console.log('ok rail icon');
  // Its logo taken away in its Settings: the page draws the built-in mark, and the tile has its letter again.
  await rp.evaluate(() => { document.querySelector('link[rel=icon]').href = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg"><path d="M781 389.2l106.6 61.4"/></svg>'); });
  await setup.waitForFunction(id => !document.querySelector(`#tile-${id} .hex img`), rid, { timeout: WAIT });
  assert.strictEqual(await setup.locator(`#tile-${rid} .hex`).textContent(), '1', 'its letter: ' + await setup.locator(`#tile-${rid} .hex`).textContent());
  assert.ok(!(rid in JSON.parse(fs.readFileSync(path.join(userData, 'icons.json'), 'utf8'))), 'and the kept logo went');
  console.log('ok rail letter');
  const ids0 = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.id));
  const until = async (fn, what) => { const end = Date.now() + WAIT; for (;;) { const v = await fn(); if (v) return v; if (Date.now() > end) throw new Error(what + ': ' + JSON.stringify(await showing(app))); await new Promise(r => setTimeout(r, 200)); } };
  // Step 8c: a labelled Menu button on the rail opens the Hives menu (each Hive, Connect, Manage, the sidebar's fold).
  const viewX = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].contentView.children.at(-1).getBounds().x);
  assert.strictEqual((await setup.locator('#rail-menu').textContent()).trim(), 'Menu', 'the button says Menu');
  await app.evaluate(() => { globalThis.lastMainMenu = null; });
  await setup.click('#rail-menu');
  const mm = await until(() => app.evaluate(() => { const r = globalThis.lastMainMenu; if (!r) return null; r.menu.closePopup(); return r.ids; }), 'the Hives menu');
  for (const id of ['hive-local', 'hive-' + rid, 'hives-new-window', 'hives-connect', 'hives-manager', 'hives-sidebar']) assert.ok(mm.includes(id), id + ' in the menu: ' + mm);
  console.log('ok menu button');
  // Its Hide folds the rail to a strip in every window and the Hive's page gets the width; a click on the strip opens it again.
  await setup.click('#rail-fold');
  await setup.waitForSelector('#rail.folded #rail-open');
  assert.strictEqual(await setup.locator('#rail .tile').count(), 0, 'folded, no tiles');
  await until(async () => await viewX() === 16, 'the view beside the strip');
  assert.strictEqual(JSON.parse(hivesJson()).railFolded, true, 'kept: ' + hivesJson());
  await setup.screenshot({ path: path.join(shots, '11c-rail-folded.png') });
  await setup.click('#rail-open');
  await setup.waitForSelector('#rail:not(.folded) #tile-' + rid);
  await until(async () => await viewX() === 72, 'the view beside the open rail');
  assert.ok(!('railFolded' in JSON.parse(hivesJson())), 'open is kept too');
  await setup.screenshot({ path: path.join(shots, '11c-rail-menu.png') });
  // A link to the Hive's own page with target=_blank opens a window of the app on that Hive, signed in (its session).
  await rp.click('#same');
  await until(async () => { const s = await showing(app); return s.length === 2 && s.every(u => u.startsWith(rorigin + '/')); }, 'a second window on the other Hive');
  console.log('ok link same hive window');
  // A link to another Hive in the list opens there (in its own session), not in this Hive's view, and not in the browser.
  await rp.evaluate(() => document.getElementById('other').click());   // (the app stops the navigation; page.click would wait for it)
  await until(async () => (await showing(app)).includes(`http://127.0.0.1:${port}/thread`), 'the local Hive in a window of its own');
  assert.ok(rp.url().startsWith(rorigin + '/'), 'the other Hive\'s page stayed where it was');
  console.log('ok link other hive');
  const logText = () => fs.readFileSync(path.join(userData, 'app.log'), 'utf8');
  const before = (logText().match(/external example\.com/g) || []).length;
  await rp.evaluate(() => document.getElementById('far').click());
  await until(async () => (logText().match(/external example\.com/g) || []).length > before, 'the link outside went to the browser');
  assert.ok(rp.url().startsWith(rorigin + '/'), 'and the page stayed');
  console.log('ok link outside browser');
  await app.evaluate(({ BrowserWindow }, ids0) => BrowserWindow.getAllWindows().filter(w => !ids0.includes(w.id)).forEach(w => w.close()), ids0);
  await until(async () => (await showing(app)).length === 1, 'back to one window');
  // Hives > New Window (Ctrl+Shift+N; the Mac's menu): a second window on the same Hive, a little down from the first.
  if (process.platform === 'darwin') await menuClick(app, 'hives-new-window');
  else await app.evaluate(({ BrowserWindow }) => { const wc = BrowserWindow.getAllWindows()[0].contentView.children.at(-1).webContents; for (const type of ['keyDown', 'keyUp']) wc.sendInputEvent({ type, keyCode: 'N', modifiers: ['control', 'shift'] }); });
  await until(async () => { const s = await showing(app); return s.length === 2 && s.every(u => u.startsWith(rorigin + '/')); }, 'a new window');
  const bounds = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.getBounds()));
  assert.ok(bounds[0].x !== bounds[1].x || bounds[0].y !== bounds[1].y, 'not right on top of the first: ' + JSON.stringify(bounds));
  console.log('ok new window');
  // The new window moves to the local Hive by its own rail; the first stays on the other Hive.
  const setup2 = await until(async () => app.windows().find(p => p !== setup && /setup\.html/.test(p.url())), 'the new window\'s page');
  await setup2.waitForSelector('#tile-local');
  await setup2.click('#tile-local');
  await until(async () => { const s = await showing(app); return s.some(u => u.startsWith(rorigin + '/')) && s.some(u => u.startsWith(`http://127.0.0.1:${port}/`)); }, 'one window on each Hive');
  await menuClick(app, 'hives-sidebar');   // the menu's own item folds it too; folded, it stays so after a restart
  await setup.waitForSelector('#rail.folded #rail-open');
  await new Promise(r => setTimeout(r, 700));   // the windows' places are saved a moment after a move
  await app.close();

  // Next start: back on the Hive it showed last. Then remove it (Hives > Manage, Remove twice): it and its sign-in go.
  ({ app, page } = await start());
  const rp2 = await hive(app, rorigin);
  assert.strictEqual(await rp2.locator('#remote').count(), 1, 'still signed in to it');
  assert.strictEqual(rseen.length, 1, 'with no second sign-in');
  console.log('ok remote remembered');
  const pg = app.windows().find(p => /setup\.html/.test(p.url()) && !p.isClosed());
  await pg.waitForSelector('#rail.folded #rail-open');
  await hive(app);
  const xs = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.contentView.children.at(-1)?.getBounds().x));
  assert.ok(xs.length === 2 && xs.every(x => x === 16), 'each window has the width back: ' + xs);
  await pg.click('#rail-open');
  await pg.waitForSelector('#rail:not(.folded) #rail-menu');
  console.log('ok rail folds');
  // Both windows come back, each on its Hive. Then the local one closes: the app goes on in the other.
  await hive(app);
  assert.strictEqual(await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().length), 2, 'both windows came back');
  const back = await showing(app);
  assert.ok(back.some(u => u.startsWith(rorigin + '/')) && back.some(u => u.startsWith(`http://127.0.0.1:${port}/`)), 'each on its Hive: ' + back.map(u => u.replace(/#.*/, '#…')));
  await app.evaluate(({ BrowserWindow }, p) => BrowserWindow.getAllWindows().find(w => (w.contentView.children.at(-1)?.webContents.getURL() || '').startsWith(`http://127.0.0.1:${p}/`)).close(), port);
  for (let i = 0; i < 100 && (await showing(app)).length > 1; i++) await new Promise(r => setTimeout(r, 100));
  assert.strictEqual((await showing(app)).length, 1, 'one window left');
  page = app.windows().find(p => /setup\.html/.test(p.url()) && !p.isClosed());
  console.log('ok windows remembered');
  const partDir = path.join(userData, 'Partitions', 'hive-' + rid);
  assert.ok(fs.existsSync(partDir), 'its session is on disk: ' + partDir);
  await menuClick(app, 'hive-local');
  await hive(app);
  await menuClick(app, 'hives-manager');
  await page.waitForSelector('#remove-' + rid);
  await page.screenshot({ path: path.join(shots, '10-hives.png') });
  await page.click('#remove-' + rid);
  assert.strictEqual(JSON.parse(hivesJson()).hives.length, 1, 'one click does not remove it');
  await page.click('#remove-' + rid);
  await page.waitForFunction(id => !document.getElementById('remove-' + id), rid);
  const after = JSON.parse(hivesJson());
  assert.ok(!after.hives.some(h => h.kind === 'remote') && after.removed.includes(rid), 'removed from the list: ' + hivesJson());
  assert.strictEqual(await app.evaluate(({ Menu }, id) => !!Menu.getApplicationMenu().getMenuItemById(id), 'hive-' + rid), false, 'and from the menu');
  await app.close();

  // Second start: straight in, no setup. The removed Hive's files went before anything opened.
  ({ app, page } = await start());
  await hive(app, undefined, WAIT);
  assert.ok(!fs.existsSync(partDir), 'the removed Hive\'s session is gone from disk');
  assert.deepStrictEqual(JSON.parse(hivesJson()).removed, [], 'and the list forgot it');
  console.log('ok remote removed');
  await app.close();

  // A Hive that won't start: says so, hides nothing, and Try again works once it's back.
  fs.writeFileSync(path.join(home, 'broken'), '');
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start");
  assert.ok((await page.locator('#log').textContent()).includes("The app didn't start"));
  await page.screenshot({ path: path.join(shots, '4-nostart.png') });
  fs.unlinkSync(path.join(home, 'broken'));
  await page.getByRole('button', { name: 'Try again' }).click();
  await hive(app, undefined, WAIT);
  await app.close();

  // Repair on the "didn't start" screen: runs the installer again (with its accept step), which puts the install right, and the Hive opens.
  fs.writeFileSync(path.join(home, 'broken'), '');
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start");
  await page.getByRole('button', { name: /^Repair/ }).click();
  if (!mac) {
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: WAIT });
    assert.ok(fs.existsSync(path.join(home, 'broken')), 'nothing changed before the yes');
    await page.getByRole('button', { name: 'Accept and install' }).click();
  }
  await hive(app, undefined, WAIT);
  assert.ok(!fs.existsSync(path.join(home, 'broken')), 'the installer put it right');
  console.log('ok repair');
  await app.close();

  // Another program on the Hive's port (one that answers wrongly, one that has no answer): the window is never sent the address with the key.
  for (const kind of ['wrong', 'old']) {
    impostor = kind; seen.length = 0; asked.length = 0;
    ({ app, page } = await start());
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start");
    assert.ok((await page.locator('#log').textContent()).includes("didn't show that it is the Hive"));
    assert.strictEqual(asked.length, 1, 'it was asked once'); assert.ok(!asked[0].includes('SECRETKEY'), 'the question carries no key');
    assert.strictEqual(seen.length, 0, 'the sign-in page (which would read the key) was never loaded from it: ' + seen);
    const urls = await showing(app);
    assert.ok(urls.every(u => u.startsWith('file:')), 'the window stayed on the setup screen: ' + urls);
    if (kind === 'wrong') await page.screenshot({ path: path.join(shots, '5-not-the-hive.png') });
    // ... and once the real Hive answers there, Try again goes in.
    impostor = '';
    await page.getByRole('button', { name: 'Try again' }).click();
    await hive(app, undefined, WAIT);
    await app.close();
  }

  // The Hive restarts under an open window and another program takes its port: the window leaves for the setup screen.
  ({ app, page } = await start());
  await hive(app, undefined, WAIT);
  impostor = 'wrong';
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start", null, { timeout: 15000 });
  assert.ok((await page.locator('#log').textContent()).includes('stopped answering'));
  impostor = '';
  await page.getByRole('button', { name: 'Try again' }).click();
  await hive(app, undefined, WAIT);
  await app.close();

  // The Hive stops altogether under an open window (the server really closes, so the page load itself fails: the did-fail-load path).
  // The watcher is slowed right down so it is the reload that finds out. The screen says so and offers Repair, not a blank page.
  ({ app, page } = await start({ HIVE_APP_WATCH_MS: '600000', HIVE_APP_ANSWER_MS: '3000' }));
  await hive(app, undefined, WAIT);
  await new Promise(r => { server.close(r); server.closeAllConnections(); });
  await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].contentView.children.at(-1).webContents.reload());   // a reload that fails: the did-fail-load path
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start", null, { timeout: WAIT });
  assert.ok(await page.getByRole('button', { name: /^Repair/ }).count(), 'Repair is offered');
  assert.ok((await showing(app)).every(u => u.startsWith('file:')), 'the window is on the setup screen, not a blank page');
  await new Promise(r => server.listen(port, '127.0.0.1', r));
  await page.getByRole('button', { name: 'Try again' }).click();
  await hive(app, undefined, WAIT);
  console.log('ok hive down');
  console.log('ok hive stopped');
  await app.close();

  // A Hive that is slow to listen (20 s): the window says it is starting the whole time, never "didn't start", then shows the Hive.
  await new Promise(r => { server.close(r); server.closeAllConnections(); });
  ({ app, page } = await start());
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent);
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Opening the Hive', null, { timeout: WAIT });
  for (let i = 0; i < 10; i++) { await new Promise(r => setTimeout(r, 2000)); assert.strictEqual(await title(), 'Opening the Hive', 'still starting, not "didn\'t start"'); }
  await new Promise(r => server.listen(port, '127.0.0.1', r));
  await hive(app, undefined, WAIT);
  console.log('ok slow start');
  await app.close();

  // The installer fails part way (no network, a download that breaks): the screen says it stopped, shows what the installer said,
  // nothing is half-shown as installed, and Try again carries on once the cause is gone.
  wipe(); failInstall = true;
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: WAIT }).catch(async e => {
    // Linux asks for the accept first; the stub script ignores that, so accept if it is showing.
    if (await page.getByRole('button', { name: 'Accept and install' }).count()) { await page.getByRole('button', { name: 'Accept and install' }).click(); await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: WAIT }); } else throw e;
  });
  assert.ok((await page.locator('#log').textContent()).includes('stub failure'), 'what the installer said stays on the screen');
  assert.ok(await page.getByRole('button', { name: 'Try again' }).count() && await page.getByRole('button', { name: 'Close' }).count());
  assert.strictEqual(await page.evaluate(() => document.activeElement.id), 'title', 'focus moved to the new screen heading');
  await page.getByRole('button', { name: 'Copy what it said' }).click();
  await new Promise(r => setTimeout(r, 300));
  const copied = await app.evaluate(({ clipboard }) => clipboard.readText());
  assert.ok(copied.includes('The install stopped') && copied.includes('stub failure'), 'the report is on the clipboard: ' + copied);
  assert.ok(!/SECRETKEY/.test(copied), 'no key in the report');
  const ver = require('../package.json').version;
  assert.ok((await page.locator('#ver').textContent()).includes(ver), 'the version is on the screen');
  assert.ok(copied.includes('Hive app ' + ver), 'the report carries the version');
  console.log('ok version shown');
  console.log('ok copy report');
  // Keyboard and screen reader: the heading and text are announced when the screen changes, and the log can be reached with Tab (to scroll it).
  const acc = await page.evaluate(() => ({ live: document.querySelector('#say').getAttribute('aria-live'), h1: !!document.querySelector('#say h1'), log: document.querySelector('#log').getAttribute('role'), tab: document.querySelector('#log').tabIndex, name: document.querySelector('#log').getAttribute('aria-label'), logLive: document.querySelector('#log').getAttribute('aria-live') }));
  assert.deepStrictEqual(acc, { live: 'polite', h1: true, log: 'log', tab: 0, name: 'What the installer said', logLive: null });
  let reached = false;
  for (let i = 0; i < 6 && !reached; i++) { await page.keyboard.press('Tab'); reached = await page.evaluate(() => document.activeElement.id === 'log'); }
  assert.ok(reached, 'Tab reaches the log');
  console.log('ok setup access');
  await page.screenshot({ path: path.join(shots, '7-install-stopped.png') });
  assert.ok(!fs.existsSync(path.join(home, 'hive', '.hive-installed')), 'no Hive left behind');
  failInstall = false;
  await page.getByRole('button', { name: 'Try again' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: WAIT }).then(() => page.getByRole('button', { name: 'Accept and install' }).click()).catch(() => {});
  await hive(app, undefined, WAIT);
  console.log('ok install-failed screen');
  await app.close();

  // No internet: nothing answers at the site's address. The install stops with the installer's own words and leaves no Hive;
  // with the site answering again (the app started again, as after a reconnect), Try again / Install gets in.
  wipe(); siteOverride = 'http://127.0.0.1:9';
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: WAIT + 10000 });
  const offlineLog = await page.locator('#log').textContent();
  assert.ok(offlineLog.includes("Couldn't reach http://127.0.0.1:9"), 'the offline words are on the screen: ' + offlineLog);
  assert.ok(await page.getByRole('button', { name: 'Try again' }).count(), 'Try again is offered');
  assert.ok(!fs.existsSync(path.join(home, 'hive', '.hive-installed')), 'no Hive left behind');
  await app.close();
  siteOverride = '';
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: WAIT }).then(() => page.getByRole('button', { name: 'Accept and install' }).click()).catch(() => {});
  await hive(app, undefined, WAIT);
  console.log('ok offline');
  await app.close();

  // No menu on Windows and Linux: Ctrl+plus/minus/0 zoom the Hive, and Ctrl+R reloads it.
  if (process.platform !== 'darwin') {
    ({ app, page } = await start());
    page = await hive(app, undefined, WAIT);
    // (page.keyboard goes round the Hive view's key handling, so keys are sent to the view itself.)
    const press = (k) => app.evaluate(({ BrowserWindow }, k) => { const wc = BrowserWindow.getAllWindows()[0].contentView.children.at(-1).webContents; for (const type of ['keyDown', 'keyUp']) wc.sendInputEvent({ type, keyCode: k, modifiers: ['control'] }); }, k);
    const zoom = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].contentView.children.at(-1).webContents.getZoomLevel());
    await press('=');  assert.ok(await zoom() > 0, 'Ctrl+= zooms in');
    await press('-'); await press('-'); assert.ok(await zoom() < 0, 'Ctrl+- zooms out');
    await press('0'); assert.strictEqual(await zoom(), 0, 'Ctrl+0 resets');
    await page.evaluate(() => { window.__mark = 1; });
    await press('r');
    await page.waitForFunction(() => !window.__mark, null, { timeout: 10000 });
    await hive(app, undefined, WAIT);
    console.log('ok shortcuts');
    await app.close();
  }

  // One copy at a time: a second start while one runs leaves at once and the first stays (still one window); closing the window quits (Linux, Windows).
  ({ app, page } = await start());
  await hive(app, undefined, WAIT);
  // The second copy can quit while Playwright is still attaching to it; Playwright then rejects a promise of its own that
  // nobody awaits ("session closed", run 45790781910), which would end this test though the copy did what it should.
  const closed = e => { if (!/session closed|Target page, context or browser has been closed/i.test(String(e && e.message))) throw e; };
  process.on('unhandledRejection', closed);
  const second = await start().catch(e => ({ failed: e }));
  await new Promise(r => setTimeout(r, 500)); process.off('unhandledRejection', closed);
  if (second.app) await second.app.close().catch(() => {});
  assert.ok(second.failed, 'a second copy did not stay open');
  const wins = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().length);
  assert.strictEqual(wins, 1, 'still one window in the first copy');
  if (process.platform !== 'darwin') {
    const gone = new Promise(r => app.process().once('exit', r));
    await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().forEach(w => w.close()));
    await Promise.race([gone, new Promise((_, rej) => setTimeout(() => rej(new Error('closing the window did not quit')), 10000))]);
  } else await app.close();
  console.log('ok single instance');

  // The window's size and place are remembered: set them, close, start again.
  ({ app, page } = await start());
  await hive(app, undefined, WAIT);
  await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setBounds({ x: 60, y: 40, width: 900, height: 640 }));
  await new Promise(r => setTimeout(r, 1000));
  await app.close();
  ({ app, page } = await start());
  await hive(app, undefined, WAIT);
  const b = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].getNormalBounds());
  assert.strictEqual(b.width, 900); assert.strictEqual(b.height, 640);
  console.log('ok window state');
  await app.close();

  // Closing the window during an install stops the installer, and the next launch offers Install again (not on Windows: its installer is PowerShell in WSL).
  if (!win) {
    wipe(); slowInstall = true;
    ({ app, page } = await start());
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
    await page.getByRole('button', { name: 'Install' }).click();
    if (!mac) {
      await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs');
      await page.getByRole('button', { name: 'Accept and install' }).click();
    }
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Installing the Hive', null, { timeout: WAIT });
    const pidf = path.join(home, 'installer.pid');
    for (let i = 0; i < 100 && !fs.existsSync(pidf); i++) await new Promise(r => setTimeout(r, 100));
    const ipid = Number(fs.readFileSync(pidf, 'utf8'));
    const alive = p => { try { process.kill(p, 0); return true; } catch { return false; } };
    assert.ok(alive(ipid), 'the installer is running');
    if (process.platform !== 'darwin') {
      const gone = new Promise(r => app.process().once('exit', r));
      await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().forEach(w => w.close()));
      await gone;
    } else await app.close();
    for (let i = 0; i < 50 && alive(ipid); i++) await new Promise(r => setTimeout(r, 100));
    assert.ok(!alive(ipid), 'closing the window stopped the installer');
    assert.ok(!fs.existsSync(path.join(home, 'hive', '.hive-installed')), 'no half-installed Hive left');
    slowInstall = false;
    ({ app, page } = await start());
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
    assert.ok(await page.getByRole('button', { name: 'Install' }).count(), 'Install is offered again');
    console.log('ok close during install');
    await app.close();
  }

  // A PC without WSL: the Windows installer turns it on and says to restart. The screen must say so, in plain words, and offer only Close.
  // (Stands in for it by the same words from the installer; the real words are in core/dist/get.ps1.)
  wipe(); needRestart = true;
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  if (!mac) {
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs');
    await page.getByRole('button', { name: 'Accept and install' }).click();
  }
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Restart Windows', null, { timeout: WAIT });
  const body = await page.locator('body').innerText();
  assert.ok(body.includes('WSL 2') && body.includes('has to restart') && body.includes('open Hive again and press Install'), 'the restart words: ' + body);
  assert.strictEqual(await page.getByRole('button').count(), 1); assert.ok(await page.getByRole('button', { name: 'Close' }).count(), 'only a Close button');
  await page.screenshot({ path: path.join(shots, '6-restart.png') });
  console.log('ok no-wsl screen');
  await app.close();

  // ... and after the restart: open the app again (WSL is there now), install carries on and reaches the Hive.
  needRestart = false;
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  if (!mac) {
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs');
    await page.getByRole('button', { name: 'Accept and install' }).click();
  }
  await hive(app, undefined, WAIT + 10000);
  console.log('ok after restart');
  await app.close();

  // A newer app on the site: the setup screen says so (once), with a link to the download page; nothing is fetched but the version file.
  // No file, an older version or nonsense: nothing is said.
  const appLog = () => { try { return fs.readFileSync(path.join(home, '.config', 'Hive', 'app.log'), 'utf8'); } catch { return ''; } };
  for (const [file, want] of [['', null], ['{"version":"0.0.1"}', null], ['not json', null], ['MOVED', null], ['{"version":"99.0.0"}', '99.0.0']]) {
    wipe(); appVersion = file; const asked0 = siteAsked.length;
    ({ app, page } = await start());
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
    // The version question runs beside the first screen: wait until the site was asked, then for the notice (or a settle without one).
    for (let i = 0; i < 100 && !siteAsked.slice(asked0).some(a => a.includes('app-version.json')); i++) await new Promise(r => setTimeout(r, 100));
    assert.ok(siteAsked.slice(asked0).some(a => a.includes('app-version.json')), `the site was asked for the version (${JSON.stringify(file)})`);
    if (want) await page.waitForFunction(() => document.querySelector('#newer'), null, { timeout: 10000 });
    else await new Promise(r => setTimeout(r, 1500));
    const line = await page.locator('#ver').textContent();
    if (want) {
      assert.ok(line.includes(`A newer Hive app (${want}) is available`), 'the notice: ' + line);
      assert.strictEqual(await page.locator('#newer').count(), 1, 'said once');
      assert.ok(siteAsked.length > 0 && (process.platform !== 'linux' || appLog().includes('update notice ' + want)), 'asked the site, and logged');
      await page.locator('#newer').click();   // opens the download page in the browser (not in tests); the window stays on setup
      await new Promise(r => setTimeout(r, 300));
      assert.strictEqual(await title(page), 'Set up the Hive');
      if (process.platform === 'linux') assert.strictEqual(fs.readFileSync(path.join(home, '.config', 'Hive', 'newer-seen.txt'), 'utf8'), want, 'marked seen');
    } else assert.ok(!line.includes('newer') && !(await page.locator('#newer').count()), `no notice for ${JSON.stringify(file)}: ` + line);
    await app.close();
  }
  console.log('ok no update notice');
  console.log('ok update notice');
  appVersion = '';

  // Only a Hive somewhere else (step 5): on the first screen, Connect instead of Install. Nothing is installed, no rail shows
  // (there is one Hive), the next start opens that Hive, and the Hives screen still offers a Hive on this computer.
  wipe();
  const ps0 = ps1Asked, logAt = fs.readFileSync(path.join(userData, 'app.log'), 'utf8').length;
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Connect to a Hive…' }).click();
  await page.waitForSelector('#address');
  await page.fill('#address', `127.0.0.1:${rport}/login#OTHERKEY`); await page.click('#go');
  await hive(app, rorigin);
  assert.ok((await showing(app))[0].startsWith(rorigin + '/'), 'the window shows the other Hive');
  assert.strictEqual(await page.evaluate(() => document.getElementById('rail').hidden), true, 'no rail with one Hive');
  assert.strictEqual(JSON.parse(hivesJson()).noLocal, true, 'kept as a Hive somewhere else only: ' + hivesJson());
  await app.close();
  ({ app, page } = await start());
  await hive(app, rorigin);
  assert.strictEqual(await page.evaluate(() => document.getElementById('rail').hidden), true, 'still no rail');
  await menuClick(app, 'hives-manager');
  await page.getByRole('button', { name: 'Set up a Hive on this computer' }).waitFor();
  assert.strictEqual(await page.locator('#open-local').count(), 0, 'no Hive on this computer in the list');
  await app.close();
  const said = fs.readFileSync(path.join(userData, 'app.log'), 'utf8').slice(logAt);
  assert.ok(ps1Asked === ps0 && !said.includes('installer exit') && !fs.existsSync(path.join(home, 'hive')) && !fs.existsSync(path.join(home, '.hive')), 'nothing was installed: ' + said.slice(-800));
  assert.ok(said.includes('no Hive on this computer'), 'and the log says so');
  console.log('ok remote only');

  // A Hive behind a sign-in gate (step 6), against a stand-in Cloudflare Access (test/gatefake.js) on 127.0.0.1. The window
  // asks for a sign-in in the browser (the test plays the browser: it opens the address the app wrote down), the token
  // comes back sealed, and the Hive opens. The sign-in is kept over a restart, asked for again when the gate turns it
  // away, and again when it runs out. The token is never in hives.json or the app's log.
  const gf = await require('./gatefake').make().listen();
  const browserFile = path.join(home, 'gate-browser.txt');
  const gateEnv = { HIVE_GATE_TEAM: gf.team, HIVE_GATE_STORE: gf.origin + '/', HIVE_GATE_BROWSER: browserFile };
  const signInBrowser = async () => {   // waits for the app to open the sign-in, then signs in there
    for (let i = 0; i < 100 && !fs.existsSync(browserFile); i++) await new Promise(r => setTimeout(r, 100));
    const u = fs.readFileSync(browserFile, 'utf8'); fs.rmSync(browserFile);
    assert.ok(u.startsWith(gf.origin + '/cdn-cgi/access/cli?'), 'the sign-in page: ' + u.slice(0, 60));
    await fetch(u);
  };
  const screen = t => page.waitForFunction(t => document.querySelector('#title')?.textContent === t, t, { timeout: WAIT });
  ({ app, page } = await start(gateEnv));
  await hive(app, rorigin);
  await menuClick(app, 'hives-manager');
  await page.getByRole('button', { name: 'Connect to a Hive…' }).click();
  await page.waitForSelector('#address');
  await page.fill('#address', `127.0.0.1:${gf.port}`); await page.click('#go');
  await screen('Sign in to 127.0.0.1');
  await page.getByRole('button', { name: 'Open the sign-in page again' }).waitFor();
  await page.screenshot({ path: path.join(shots, '12-signin.png') });
  assert.ok(await page.evaluate(() => [...document.querySelectorAll('#rail .hex')].every(x => x.style.background)), 'every tile has its colour, with no Hive on this computer too');
  await signInBrowser();
  await hive(app, gf.origin);
  const saved = JSON.parse(hivesJson()).hives.find(x => x.url.startsWith(gf.origin));
  assert.deepStrictEqual(saved.gate, { team: gf.team, aud: gf.aud }, 'the gate is kept: ' + hivesJson());
  await app.close();
  ({ app, page } = await start(gateEnv));
  await hive(app, gf.origin);   // the sign-in is still there after a restart, so it opens with no question
  assert.strictEqual(gf.tokens.length, 1, 'one sign-in so far');
  console.log('ok gate signin');

  // The gate turns the sign-in away (here: it is gone from the session): the window asks again. This sign-in is short.
  gf.ttl = 14;
  await app.evaluate(({ session }, [id, o]) => session.fromPartition('persist:hive-' + id).cookies.remove(o + '/', 'CF_Authorization'), [saved.id, gf.origin]);
  (await hive(app, gf.origin)).reload().catch(() => { });
  await screen('Sign in to 127.0.0.1');
  await page.getByRole('button', { name: 'Sign in again in your browser' }).click();
  await signInBrowser();
  await hive(app, gf.origin);
  const ranAt = Date.now();
  // ... and when it runs out, the window asks again, then opens once signed in.
  await page.getByRole('button', { name: 'Sign in again in your browser' }).waitFor({ timeout: 30000 });
  assert.ok(Date.now() - ranAt > 5000, 'asked again only once it ran out');
  gf.ttl = 3600;
  await page.getByRole('button', { name: 'Sign in again in your browser' }).click();
  await signInBrowser();
  await hive(app, gf.origin);
  // A gated address that, once signed in, sends the app on to another one (as h2.ihsan.cc does to dash.ihsan.cc): the
  // window says where it went, and doesn't ask for a sign-in, which would only lead back here.
  gf.forward = `http://localhost:${rport}/`;
  (await hive(app, gf.origin)).reload().catch(() => { });
  await screen("Can't reach 127.0.0.1");
  const fwd = await page.locator('#text').textContent();
  assert.ok(fwd.includes(`sends the app on to localhost:${rport}`) && !fwd.includes('Sign in again'), 'says where it was sent: ' + fwd.slice(0, 400));
  assert.strictEqual(gf.tokens.length, 3, 'no new sign-in was asked for');
  gf.forward = '';
  await app.close();
  const told = fs.readFileSync(path.join(userData, 'app.log'), 'utf8') + hivesJson();
  assert.strictEqual(gf.tokens.length, 3);
  for (const t of gf.tokens) for (const bit of t.split('.')) assert.ok(!told.includes(bit), 'no token in the log or hives.json');
  assert.ok(told.includes('ran out'), 'the log says it ran out');
  console.log('ok gate expiry');
  console.log('ok gate forward');
  gf.close(); remote.close();

  if (win) { wipe(); forgetHives(); }
  server.close(); fs.rmSync(home, { recursive: true, force: true });
  console.log('ok e2e');
})().catch(async e => { console.error('FAILED', e);
  // What the window showed when it stopped, so a runner's failure can be read from its log and artifacts.
  try { console.error('screen:', await cur.locator('#title').textContent({ timeout: 2000 }), '| log:', await cur.locator('#log').textContent({ timeout: 2000 }), '| install.ps1 served:', ps1Asked);
    await cur.screenshot({ path: path.join(shots, 'e2e-failed.png') }); } catch { } try { if (win) wipe(); } catch { } try { forgetHives(); } catch { } process.exit(1); });
