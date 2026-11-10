// xvfb-run -a node test/e2e.js (HIVE_APP_BIN=dist/linux-unpacked/hive-app tests the packaged app): the real app in a real (hidden) window, against a stand-in site and a stand-in Hive.
// Covers: no Hive -> welcome -> installer shows its warning -> accept -> installs -> the web app loads in the window;
// a second start goes straight in; outside links don't load in the window; a Hive that won't start says so.
const assert = require('assert');
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { _electron } = require('playwright-core');

const root = path.join(__dirname, '..');
const home = fs.mkdtempSync(path.join(process.env.TMPDIR || os.tmpdir(), 'hive-app-e2e-'));
const shots = path.join(root, 'test', 'shots'); fs.mkdirSync(shots, { recursive: true });

const server = http.createServer((req, res) => {
  if (req.url === '/install.sh') {
    // Stands in for get.sh --this-account: warns, wants HIVE_ACCEPT=1 with no terminal, then makes ~/hive with a hive command.
    res.end(`#!/bin/sh
[ "$1" = --this-account ] || { echo "expected --this-account"; exit 2; }
echo "The Hive will run in your own account, as you, with its files in $HOME/hive."
if [ "$HIVE_ACCEPT" != 1 ]; then
  if ( : </dev/tty ) 2>/dev/null; then echo "BUG: the app gave the installer a terminal"; exit 3; fi
  echo "there's no terminal to ask you on, and nothing changed. If you accept the above, set HIVE_ACCEPT=1"; exit 1
fi
[ "$HIVE_NO_LAUNCHER" = 1 ] || { echo "launcher not skipped"; exit 4; }
printf 'Installing Claude Code\\rSetting up the Hive\\n'
mkdir -p "$HOME/hive/.local/bin"
cat > "$HOME/hive/.local/bin/hive" <<X
#!/bin/sh
[ ! -e "$HOME/broken" ] || { echo "The app didn't start. Look at: systemctl --user status h2-dash"; exit 1; }
echo "http://localhost:${port}/login#SECRETKEY"
X
chmod +x "$HOME/hive/.local/bin/hive"; touch "$HOME/hive/.hive-installed"
echo "The Hive is installed. Open this address: http://localhost:${port}/login#SECRETKEY"
`);
  } else if (req.url.startsWith('/login')) { res.setHeader('content-type', 'text/html'); res.end('<title>Hive web app</title><h1 id="app">The web app</h1><a id="out" href="https://example.com/">out</a><a id="in" href="/thread">in</a>'); }
  else if (req.url === '/thread') { res.setHeader('content-type', 'text/html'); res.end('<h1 id="thread">A thread</h1>'); }
  else { res.statusCode = 404; res.end('no'); }
});
let port;

async function start() {
  const env = { ...process.env, HOME: home, HIVE_APP_TEST: '1', HIVE_SITE: `http://127.0.0.1:${port}`, XDG_CONFIG_HOME: path.join(home, '.config'), XDG_DATA_HOME: path.join(home, '.local/share') };
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

  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Before it installs');
  const warn = await page.locator('#log').textContent();
  assert.ok(warn.includes('The Hive will run in your own account'), 'the installer\'s own warning is on the screen');
  assert.ok(!warn.includes('BUG'), warn);
  assert.ok(!fs.existsSync(path.join(home, 'hive')), 'nothing installed before the yes');
  await page.screenshot({ path: path.join(shots, '2-accept.png') });
  await page.getByRole('button', { name: 'Accept and install' }).click();

  await page.waitForSelector('#app', { timeout: 20000 });
  assert.strictEqual(new URL(page.url()).port, String(port));
  assert.strictEqual(await page.evaluate(() => typeof window.hive), 'undefined', 'the web app gets no bridge to the shell... ');
  await page.screenshot({ path: path.join(shots, '3-app.png') });

  // An inside link loads in the window; an outside one must not (asked of the shell itself: the driver loses track after a blocked link).
  await page.locator('#in').click(); await page.waitForSelector('#thread');
  await page.goBack(); await page.waitForSelector('#out');
  await page.evaluate(() => { document.querySelector('#out').click(); });
  await new Promise(r => setTimeout(r, 1500));
  const at = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL()));
  assert.strictEqual(at.length, 1); assert.ok(at[0].startsWith(`http://localhost:${port}/`), 'outside link stayed out of the window: ' + at);
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

  server.close(); fs.rmSync(home, { recursive: true, force: true });
  console.log('ok e2e');
})().catch(e => { console.error('FAILED', e); process.exit(1); });
