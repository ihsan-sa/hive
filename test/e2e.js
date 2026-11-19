// xvfb-run -a node test/e2e.js (HIVE_APP_BIN=dist/linux-unpacked/hive-app tests the packaged app): the real app in a real (hidden) window, against a stand-in site and a stand-in Hive.
// Covers: no Hive -> welcome -> installer shows its warning -> accept -> installs -> the web app loads in the window;
// a second start goes straight in; outside links don't load in the window; a Hive that won't start says so.
const assert = require('assert');
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { _electron } = require('playwright-core');

const mac = process.platform === 'darwin';   // the Mac installer has no accept step and keeps the Hive's command in ~/.hive/bin
const root = path.join(__dirname, '..');
const home = fs.mkdtempSync(path.join(process.env.TMPDIR || os.tmpdir(), 'hive-app-e2e-'));
const shots = path.join(root, 'test', 'shots'); fs.mkdirSync(shots, { recursive: true });

const server = http.createServer((req, res) => {
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
    if (impostor) { res.statusCode = impostor === 'old' ? 404 : 200; return res.end(impostor === 'old' ? 'no' : 'f'.repeat(64)); }
    res.end(require('crypto').createHmac('sha256', 'SECRETKEY').update(`proof:${port}:${req.url.split('n=')[1]}`).digest('hex'));
  } else if (req.url.startsWith('/login')) { seen.push(req.url); res.setHeader('content-type', 'text/html'); res.end('<title>Hive web app</title><h1 id="app">The web app</h1><a id="out" href="https://example.com/">out</a><a id="in" href="/thread">in</a><a id="away" href="/away">away</a>'); }
  else if (req.url === '/away') { res.statusCode = 302; res.setHeader('location', 'https://example.com/'); res.end(); }
  else if (req.url === '/thread') { res.setHeader('content-type', 'text/html'); res.end('<h1 id="thread">A thread</h1>'); }
  else { res.statusCode = 404; res.end('no'); }
});
let port, impostor = '', needRestart = false, failInstall = false;
const asked = [], seen = [];   // the proof questions, and the page loads, the stand-in server got

async function start() {
  const env = { ...process.env, HOME: home, HIVE_APP_TEST: '1', HIVE_APP_WATCH_MS: '700', ...(needRestart ? { HIVE_STUB_RESTART: '1' } : {}), HIVE_SITE: `http://127.0.0.1:${port}`, XDG_CONFIG_HOME: path.join(home, '.config'), XDG_DATA_HOME: path.join(home, '.local/share') };
  delete env.HIVE_DIR;
  const app = await _electron.launch(process.env.HIVE_APP_BIN ? { executablePath: process.env.HIVE_APP_BIN, args: ['--no-sandbox'], env } : { executablePath: require('electron'), args: [root, '--no-sandbox'], env });
  const page = await app.firstWindow();
  return { app, page };
}
const title = page => page.locator('#title').textContent();

(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r)); port = server.address().port;
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

  // The installer fails part way (no network, a download that breaks): the screen says it stopped, shows what the installer said,
  // nothing is half-shown as installed, and Try again carries on once the cause is gone.
  fs.rmSync(path.join(home, 'hive'), { recursive: true, force: true }); fs.rmSync(path.join(home, '.hive'), { recursive: true, force: true }); failInstall = true;
  ({ app, page } = await start());
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive');
  await page.getByRole('button', { name: 'Install' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: 20000 }).catch(async e => {
    // Linux asks for the accept first; the stub script ignores that, so accept if it is showing.
    if (await page.getByRole('button', { name: 'Accept and install' }).count()) { await page.getByRole('button', { name: 'Accept and install' }).click(); await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'The install stopped', null, { timeout: 20000 }); } else throw e;
  });
  assert.ok((await page.locator('#log').textContent()).includes('stub failure'), 'what the installer said stays on the screen');
  assert.ok(await page.getByRole('button', { name: 'Try again' }).count() && await page.getByRole('button', { name: 'Close' }).count());
  await page.screenshot({ path: path.join(shots, '7-install-stopped.png') });
  assert.ok(!fs.existsSync(path.join(home, 'hive', '.hive-installed')), 'no Hive left behind');
  failInstall = false;
  await page.getByRole('button', { name: 'Try again' }).click();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs', null, { timeout: 20000 }).then(() => page.getByRole('button', { name: 'Accept and install' }).click()).catch(() => {});
  await page.waitForSelector('#app', { timeout: 20000 });
  console.log('ok install-failed screen');
  await app.close();

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

  // A PC without WSL: the Windows installer turns it on and says to restart. The screen must say so, in plain words, and offer only Close.
  // (Stands in for it by the same words from the installer; the real words are in core/dist/get.ps1.)
  fs.rmSync(path.join(home, 'hive'), { recursive: true, force: true }); fs.rmSync(path.join(home, '.hive'), { recursive: true, force: true }); needRestart = true;
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

  server.close(); fs.rmSync(home, { recursive: true, force: true });
  console.log('ok e2e');
})().catch(e => { console.error('FAILED', e); process.exit(1); });
