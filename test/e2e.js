// xvfb-run -a node test/e2e.js (HIVE_APP_BIN=dist/linux-unpacked/hive-app tests the packaged app): the real app in a real (hidden) window, against a stand-in site and a stand-in Hive.
// Covers: no Hive -> welcome -> installer shows its warning -> accept -> installs -> the web app loads in the window;
// a second start goes straight in; outside links don't load in the window; a Hive that won't start says so.
const assert = require('assert');
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { _electron } = require('playwright-core');

const win = process.platform === 'win32';    // Windows: install.ps1 run by PowerShell, hive.cmd, and wsl.exe (a compiled stand-in on PATH)
const mac = process.platform === 'darwin';   // the Mac installer has no accept step and keeps the Hive's command in ~/.hive/bin
const root = path.join(__dirname, '..');
const home = fs.mkdtempSync(path.join(process.env.TMPDIR || os.tmpdir(), 'hive-app-e2e-'));
const shots = path.join(root, 'test', 'shots'); fs.mkdirSync(shots, { recursive: true });

// Windows: the Hive's command lives under this LOCALAPPDATA (the real one; the test wipes it at the end and on a failure, so real.js's later run never meets the stand-in).
// (Electron won't start with a different one, so this uses the real one: refuse to touch a person's own machine.)
if (win && !process.env.CI) { console.error('On Windows this test replaces %LOCALAPPDATA%\\Hive: it only runs on a CI runner (CI=1)'); process.exit(2); }
const localApp = process.env.LOCALAPPDATA;
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
`;
};
const server = http.createServer((req, res) => {
  if (req.url === '/install.ps1') {
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
`);
  } else if (req.url.startsWith('/api/proof?n=')) {
    // The stand-in Hive holds the key, so it can make the mark. With `impostor` set it is some other program on the Hive's port.
    asked.push(req.url);
    if (impostor === 'down') return req.destroy();
    if (impostor) { res.statusCode = impostor === 'old' ? 404 : 200; return res.end(impostor === 'old' ? 'no' : 'f'.repeat(64)); }
    res.end(require('crypto').createHmac('sha256', 'SECRETKEY').update(`proof:${port}:${req.url.split('n=')[1]}`).digest('hex'));
  } else if (req.url.startsWith('/login')) { seen.push(req.url); res.setHeader('content-type', 'text/html'); res.end('<title>Hive web app</title><h1 id="app">The web app</h1><a id="out" href="https://example.com/">out</a><a id="in" href="/thread">in</a><a id="away" href="/away">away</a>'); }
  else if (req.url === '/away') { res.statusCode = 302; res.setHeader('location', 'https://example.com/'); res.end(); }
  else if (req.url === '/thread') { res.setHeader('content-type', 'text/html'); res.end('<h1 id="thread">A thread</h1>'); }
  else { res.statusCode = 404; res.end('no'); }
});
let port, impostor = '', needRestart = false, failInstall = false, slowInstall = false, siteOverride = '';
const asked = [], seen = [];   // the proof questions, and the page loads, the stand-in server got

async function start() {
  const env = { ...process.env, HOME: home, HIVE_APP_TEST: '1', HIVE_APP_WATCH_MS: '700', ...(needRestart ? { HIVE_STUB_RESTART: '1' } : {}), HIVE_SITE: siteOverride || `http://127.0.0.1:${port}`, XDG_CONFIG_HOME: path.join(home, '.config'), XDG_DATA_HOME: path.join(home, '.local/share') };
  delete env.HIVE_DIR;
  if (win) {   // the stand-in wsl.exe first on PATH (Windows keeps the name's case: Path)
    const k = Object.keys(env).find(x => x.toLowerCase() === 'path') || 'Path';
    env[k] = wslDir + ';' + (env[k] || '');
  }
  const app = await _electron.launch(process.env.HIVE_APP_BIN ? { executablePath: process.env.HIVE_APP_BIN, args: ['--no-sandbox'], env } : { executablePath: require('electron'), args: [root, '--no-sandbox'], env });
  const page = await app.firstWindow();
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
// A machine with no Hive on it.
function wipe() { for (const d of ['hive', '.hive']) fs.rmSync(path.join(home, d), { recursive: true, force: true });
  if (win) fs.rmSync(path.join(localApp, 'Hive'), { recursive: true, force: true }); }
const title = page => page.locator('#title').textContent();

(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r)); port = server.address().port; buildWsl();
  let { app, page } = await start();

  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.screenshot({ path: path.join(shots, '1-welcome.png') });
  await page.getByRole('button', { name: 'Install' }).click();

  if (!mac) {
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs');
    const warn = await page.locator('#log').textContent();
    assert.ok(warn.includes('The Hive will run in your own account'), 'the installer\'s own warning is on the screen');
    assert.ok(!warn.includes('BUG'), warn);
    assert.ok(!fs.existsSync(path.join(home, 'hive')), 'nothing installed before the yes');
    await page.screenshot({ path: path.join(shots, '2-accept.png') });
    await page.getByRole('button', { name: 'Accept and install' }).click();
  }

  await page.waitForSelector('#app', { timeout: 20000 });
  assert.strictEqual(new URL(page.url()).port, String(port));
  assert.strictEqual(new URL(page.url()).hostname, '127.0.0.1', 'the window uses the one address the Hive listens on, not "localhost" (which is also ::1)');
  assert.ok(asked.length && asked.every(u => !u.includes('SECRETKEY')));
  assert.strictEqual(await page.evaluate(() => typeof window.hive), 'undefined', 'the web app gets no bridge to the shell... ');
  await page.screenshot({ path: path.join(shots, '3-app.png') });

  // An inside link loads in the window; an outside one must not (asked of the shell itself: the driver loses track after a blocked link).
  await page.locator('#in').click(); await page.waitForSelector('#thread');
  await page.goBack(); await page.waitForSelector('#out');
  await page.evaluate(() => { document.querySelector('#out').click(); });
  await new Promise(r => setTimeout(r, 1500));
  const at = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL()));
  assert.strictEqual(at.length, 1); assert.ok(at[0].startsWith(`http://127.0.0.1:${port}/`), 'outside link stayed out of the window: ' + at);
  // Nor may the Hive's own address send the window elsewhere.
  await page.evaluate(() => { document.querySelector('#away').click(); });
  await new Promise(r => setTimeout(r, 1500));
  const at2 = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL()));
  assert.ok(at2.length === 1 && at2[0].startsWith(`http://127.0.0.1:${port}/`), 'a redirect stayed out of the window: ' + at2);
  await app.close();

  // Second start: straight in, no setup.
  ({ app, page } = await start());
  await page.waitForSelector('#app', { timeout: 20000 });
  await app.close();

  // A Hive that won't start: says so, hides nothing, and Try again works once it's back.
  fs.writeFileSync(path.join(home, 'broken'), '');
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start");
  assert.ok((await page.locator('#log').textContent()).includes("The app didn't start"));
  await page.screenshot({ path: path.join(shots, '4-nostart.png') });
  fs.unlinkSync(path.join(home, 'broken'));
  await page.getByRole('button', { name: 'Try again' }).click();
  await page.waitForSelector('#app', { timeout: 20000 });
  await app.close();

  // Repair on the "didn't start" screen: runs the installer again (with its accept step), which puts the install right, and the Hive opens.
  fs.writeFileSync(path.join(home, 'broken'), '');
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start");
  await page.getByRole('button', { name: /^Repair/ }).click();
  if (!mac) {
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: 20000 });
    assert.ok(fs.existsSync(path.join(home, 'broken')), 'nothing changed before the yes');
    await page.getByRole('button', { name: 'Accept and install' }).click();
  }
  await page.waitForSelector('#app', { timeout: 20000 });
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
    const urls = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL()));
    assert.ok(urls.every(u => u.startsWith('file:')), 'the window stayed on the setup screen: ' + urls);
    if (kind === 'wrong') await page.screenshot({ path: path.join(shots, '5-not-the-hive.png') });
    // ... and once the real Hive answers there, Try again goes in.
    impostor = '';
    await page.getByRole('button', { name: 'Try again' }).click();
    await page.waitForSelector('#app', { timeout: 20000 });
    await app.close();
  }

  // The Hive restarts under an open window and another program takes its port: the window leaves for the setup screen.
  ({ app, page } = await start());
  await page.waitForSelector('#app', { timeout: 20000 });
  impostor = 'wrong';
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start", null, { timeout: 15000 });
  assert.ok((await page.locator('#log').textContent()).includes('stopped showing'));
  impostor = '';
  await page.getByRole('button', { name: 'Try again' }).click();
  await page.waitForSelector('#app', { timeout: 20000 });
  await app.close();

  // The Hive stops altogether under an open window (nothing answers): the screen says so and offers Repair, not a blank page.
  ({ app, page } = await start());
  await page.waitForSelector('#app', { timeout: 20000 });
  impostor = 'down';
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === "The Hive didn't start", null, { timeout: 15000 });
  assert.ok(await page.getByRole('button', { name: /^Repair/ }).count(), 'Repair is offered');
  impostor = '';
  await page.getByRole('button', { name: 'Try again' }).click();
  await page.waitForSelector('#app', { timeout: 20000 });
  console.log('ok hive stopped');
  await app.close();

  // The installer fails part way (no network, a download that breaks): the screen says it stopped, shows what the installer said,
  // nothing is half-shown as installed, and Try again carries on once the cause is gone.
  wipe(); failInstall = true;
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: 20000 }).catch(async e => {
    // Linux asks for the accept first; the stub script ignores that, so accept if it is showing.
    if (await page.getByRole('button', { name: 'Accept and install' }).count()) { await page.getByRole('button', { name: 'Accept and install' }).click(); await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: 20000 }); } else throw e;
  });
  assert.ok((await page.locator('#log').textContent()).includes('stub failure'), 'what the installer said stays on the screen');
  assert.ok(await page.getByRole('button', { name: 'Try again' }).count() && await page.getByRole('button', { name: 'Close' }).count());
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
  const acc = await page.evaluate(() => ({ live: document.querySelector('#say').getAttribute('aria-live'), h1: !!document.querySelector('#say h1'), log: document.querySelector('#log').getAttribute('role'), tab: document.querySelector('#log').tabIndex, name: document.querySelector('#log').getAttribute('aria-label') }));
  assert.deepStrictEqual(acc, { live: 'polite', h1: true, log: 'log', tab: 0, name: 'What the installer said' });
  let reached = false;
  for (let i = 0; i < 6 && !reached; i++) { await page.keyboard.press('Tab'); reached = await page.evaluate(() => document.activeElement.id === 'log'); }
  assert.ok(reached, 'Tab reaches the log');
  console.log('ok setup access');
  await page.screenshot({ path: path.join(shots, '7-install-stopped.png') });
  assert.ok(!fs.existsSync(path.join(home, 'hive', '.hive-installed')), 'no Hive left behind');
  failInstall = false;
  await page.getByRole('button', { name: 'Try again' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: 20000 }).then(() => page.getByRole('button', { name: 'Accept and install' }).click()).catch(() => {});
  await page.waitForSelector('#app', { timeout: 20000 });
  console.log('ok install-failed screen');
  await app.close();

  // No internet: nothing answers at the site's address. The install stops with the installer's own words and leaves no Hive;
  // with the site answering again (the app started again, as after a reconnect), Try again / Install gets in.
  wipe(); siteOverride = 'http://127.0.0.1:9';
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: 30000 });
  const offlineLog = await page.locator('#log').textContent();
  assert.ok(offlineLog.includes("Couldn't reach http://127.0.0.1:9"), 'the offline words are on the screen: ' + offlineLog);
  assert.ok(await page.getByRole('button', { name: 'Try again' }).count(), 'Try again is offered');
  assert.ok(!fs.existsSync(path.join(home, 'hive', '.hive-installed')), 'no Hive left behind');
  await app.close();
  siteOverride = '';
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: 20000 }).then(() => page.getByRole('button', { name: 'Accept and install' }).click()).catch(() => {});
  await page.waitForSelector('#app', { timeout: 20000 });
  console.log('ok offline');
  await app.close();

  // No menu on Windows and Linux: Ctrl+plus/minus/0 zoom the Hive, and Ctrl+R reloads it.
  if (process.platform !== 'darwin') {
    ({ app, page } = await start());
    await page.waitForSelector('#app', { timeout: 20000 });
    // (page.keyboard goes round the window's key handling, so keys are sent to the window itself.)
    const press = (k) => app.evaluate(({ BrowserWindow }, k) => { const wc = BrowserWindow.getAllWindows()[0].webContents; for (const type of ['keyDown', 'keyUp']) wc.sendInputEvent({ type, keyCode: k, modifiers: ['control'] }); }, k);
    const zoom = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].webContents.getZoomLevel());
    await press('=');  assert.ok(await zoom() > 0, 'Ctrl+= zooms in');
    await press('-'); await press('-'); assert.ok(await zoom() < 0, 'Ctrl+- zooms out');
    await press('0'); assert.strictEqual(await zoom(), 0, 'Ctrl+0 resets');
    await page.evaluate(() => { window.__mark = 1; });
    await press('r');
    await page.waitForFunction(() => !window.__mark, null, { timeout: 10000 });
    await page.waitForSelector('#app', { timeout: 20000 });
    console.log('ok shortcuts');
    await app.close();
  }

  // One copy at a time: a second start while one runs leaves at once and the first stays (still one window); closing the window quits (Linux, Windows).
  ({ app, page } = await start());
  await page.waitForSelector('#app', { timeout: 20000 });
  const second = await start().catch(e => ({ failed: e }));
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
  await page.waitForSelector('#app', { timeout: 20000 });
  await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setBounds({ x: 60, y: 40, width: 900, height: 640 }));
  await new Promise(r => setTimeout(r, 1000));
  await app.close();
  ({ app, page } = await start());
  await page.waitForSelector('#app', { timeout: 20000 });
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
    await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Installing the Hive', null, { timeout: 20000 });
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
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Restart Windows', null, { timeout: 20000 });
  const body = await page.locator('body').innerText();
  assert.ok(body.includes('WSL 2') && body.includes('has to restart') && body.includes('open Hive again and it carries on'), 'the restart words: ' + body);
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
  await page.waitForSelector('#app', { timeout: 30000 });
  console.log('ok after restart');
  await app.close();

  if (win) wipe();
  server.close(); fs.rmSync(home, { recursive: true, force: true });
  console.log('ok e2e');
})().catch(e => { console.error('FAILED', e); try { if (win) wipe(); } catch { } process.exit(1); });
