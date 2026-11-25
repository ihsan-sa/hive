// The Hive as a desktop app. A thin shell: it finds the Hive on this machine (or installs it with the site's own installer),
// asks it for its address, and shows the web app in a window. All the product is the web app, so it has one version on every system.
'use strict';
const { app, BrowserWindow, ipcMain, shell, session, Menu, screen, clipboard } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const lib = require('./lib');

let win = null, hiveUrl = null, child = null, kept = false, fails = 0, newer = null, shown = null;   // shown: the update notification, kept so its click still arrives
const logFile = () => path.join(app.getPath('userData'), 'app.log');
const log = t => lib.appendLog(logFile(), t);
const setupFile = path.join(__dirname, 'setup.html');

// Runs a command with no terminal of its own (so an installer that would ask on one says so instead of hanging), calling out(text) as it prints.
function run(cmd, out, timeoutMs) {
  return new Promise(resolve => {
    let all = '', t = null, p;
    try {
      const env = { ...process.env, ...(cmd.env || {}) };
      // Windows keeps the case a name was set with (PSModulePath, Path), so match it loosely.
      for (const k of cmd.unset || []) for (const have of Object.keys(env)) if (have.toLowerCase() === k.toLowerCase()) delete env[have];
      p = spawn(cmd.file, cmd.args, { env, detached: process.platform !== 'win32', windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    } catch (e) { return resolve({ code: -1, text: String(e.message || e) }); }
    const take = d => { const s = d.toString(); all += s; if (out) out(s); };
    p.stdout.on('data', take); p.stderr.on('data', take);
    if (timeoutMs) t = setTimeout(() => { try { p.kill(); } catch { } }, timeoutMs);
    p.on('error', e => { clearTimeout(t); resolve({ code: -1, text: all + String(e.message || e) }); });
    p.on('close', code => { clearTimeout(t); resolve({ code: code === null ? -1 : code, text: all }); });
    if (out) child = p;
  });
}

// Outside links go to the person's own browser (not in the tests, which have none).
function external(url) { if (/^https?:\/\//.test(url) && process.env.HIVE_APP_TEST !== '1') shell.openExternal(url); }

function send(name, ...a) { if (win && !win.isDestroyed() && isSetup(win.webContents)) win.webContents.send(name, ...a); }
const setupUrl = require('url').pathToFileURL(setupFile).href;
const isSetupUrl = u => String(u).split(/[?#]/)[0] === setupUrl;
function isSetup(wc) { return isSetupUrl(wc.getURL()); }

// Waits until the address answers: a VM's port can come a moment after the Hive prints its address.
async function answers(url, ms) {
  const base = new URL(url).origin + '/', end = Date.now() + ms;
  for (;;) {
    const ok = await new Promise(res => {
      const q = require('http').get(base, { timeout: 2000 }, r => { r.resume(); res(true); });
      q.on('error', () => res(false)); q.on('timeout', () => { q.destroy(); res(false); });
    });
    if (ok) return true;
    if (Date.now() > end) return false;
    await new Promise(r => setTimeout(r, 1500));
  }
}

// True when the server at the address shows it holds the key (see lib.proofAsk), or the address carries none.
async function proves(url) {
  const p = lib.proofAsk(url, require('crypto').randomBytes(24).toString('base64url'));
  if (!p) return true;
  const got = await new Promise(res => {
    const q = require('http').get(p.ask, { timeout: 5000 }, r => {
      let b = ''; r.setEncoding('utf8');
      r.on('data', d => { b += d; if (b.length > 200) { q.destroy(); res(''); } });
      r.on('end', () => res(r.statusCode === 200 ? b : '')); r.on('error', () => res(''));
    });
    q.on('error', () => res('')); q.on('timeout', () => { q.destroy(); res(''); });
    setTimeout(() => { q.destroy(); res(''); }, 8000);   // an answer that drips in slowly is no answer
  });
  return lib.proofOk(p.want, got);
}

async function showSetup(state, note) {
  log('screen ' + state + (note ? ': ' + note : ''));
  if (!isSetup(win.webContents)) await win.loadFile(setupFile);
  send('state', state, { platform: process.platform, note: note || '' });
  if (newer) send('newer', newer);
}

// Once a launch, ask the site (and only the site) which app is current. A newer one is said on the setup screen and, once per
// version, in a system notification that opens the download page. Nothing installs itself.
function checkNewer() {
  // Chromium's own fetch: it goes through the system's proxy and certificates as the browser does. No redirects, 10 s in all, 4 KB.
  const { net } = require('electron');
  net.fetch(lib.SITE + '/app-version.json', { redirect: 'error', signal: AbortSignal.timeout(10000), cache: 'no-store' })
    .then(r => r.status === 200 && Number(r.headers.get('content-length') || 0) <= 4000 ? r.text() : '')
    .then(b => {
      const v = b.length <= 4000 ? lib.newerApp(app.getVersion(), b) : null;
      if (!v) return;
      newer = v; log('update notice ' + v);
      send('newer', v);
      const seenFile = path.join(app.getPath('userData'), 'newer-seen.txt');
      let seen = ''; try { seen = fs.readFileSync(seenFile, 'utf8').trim(); } catch { }
      if (seen === v) return;
      // Marked seen only once the notification has really shown, so a launch where it couldn't show tries again next time.
      const mark = () => { try { fs.writeFileSync(seenFile, v); } catch { } };
      const { Notification } = require('electron');
      if (process.env.HIVE_APP_TEST === '1') { mark(); return; }
      if (!Notification.isSupported()) { log('notification not supported'); return; }
      shown = new Notification({ title: 'A newer Hive app is available', body: `Version ${v}. Click to open the download page.` });
      // Said in the log either way, so a report (and the Windows runner's toast test) shows whether it really appeared.
      shown.on('show', () => { log('notification shown ' + v); mark(); });
      shown.on('failed', (e, err) => log('notification failed: ' + String(err).slice(0, 300)));
      shown.on('click', () => { external(lib.downloadPage('update')); shown = null; });
      shown.on('close', () => { shown = null; });
      shown.show();
    })
    .catch(() => { });
}

// Finds the Hive and shows it; the setup screen if there's none or it won't start.
async function open() {
  const cmd = lib.openCommand(process.platform, process.env);
  if (!cmd) return showSetup('welcome');
  await showSetup('starting');
  let url = cmd.url || null, said = '';
  if (cmd.keep && !kept) {
    kept = true;
    try { spawn(cmd.keep.file, cmd.keep.args, { detached: true, windowsHide: true, stdio: 'ignore' }).on('error', () => { }).unref(); } catch { }
  }
  if (!url) {
    // A Mac's or Windows' Linux system can take a couple of minutes to wake after a boot.
    const r = await run(cmd, null, 240000);
    url = lib.localUrl(r.text); said = lib.redact(lib.plain(r.text)).trim();
  }
  if (!url) return showSetup('nostart', said.slice(-2000));
  url = lib.pin(url);
  const sorry = "The Hive's app didn't answer. " + (cmd.url ? 'It runs in another account of this machine: start it there (sudo -iu hive hive open --print), then try again.' : '');
  if (!await answers(url, cmd.url ? 4000 : Number(process.env.HIVE_APP_ANSWER_MS) || 90000)) return showSetup('nostart', sorry);
  // The address is the key. It goes only to a server that shows it already has it; anything else on that port gets nothing.
  if (!await proves(url)) return showSetup('nostart', "What answers on the Hive's address didn't show that it is the Hive, so the app didn't sign in to it. If the Hive on this computer is an older one, Repair brings it up to date. If not, another program may be using the Hive's port.");
  hiveUrl = url;
  log('opened the Hive');
  watch();
  try { await win.loadURL(url); fails = 0; }
  catch (e) { hiveUrl = null; return showSetup('nostart', sorry); }
}

// While the Hive is showing, ask again now and then: if another program has taken its port (the Hive restarted under the window),
// the window leaves it for the setup screen instead of sending the sign-in cookie on to it.
let watcher = null;
function watch() {
  clearInterval(watcher);
  watcher = setInterval(async () => {
    if (!win || !hiveUrl || isSetup(win.webContents)) return;
    const u = hiveUrl;
    if (await proves(u) || hiveUrl !== u) return;
    hiveUrl = null;
    showSetup('nostart', "The Hive stopped answering at its address (or another program took it), so the app left it. Try again signs in once the Hive answers.");
  }, Number(process.env.HIVE_APP_WATCH_MS) || 30000);
}

async function install(accept) {
  if (child) return;
  await showSetup('installing');
  // Redact whole lines: a key split across two chunks would slip past the pattern.
  let pend = '';
  const r = await run(lib.installCommand(process.platform, process.env, accept), s => {
    pend += s; const i = Math.max(pend.lastIndexOf('\n'), pend.lastIndexOf('\r'));
    if (i >= 0) { send('log', lib.redact(lib.plain(pend.slice(0, i + 1)))); pend = pend.slice(i + 1); }
  });
  if (pend) send('log', lib.redact(lib.plain(pend)));
  child = null;
  let what = lib.installResult(r.code, r.text, process.platform);
  log('installer exit ' + r.code + ' -> ' + what + ': ' + lib.plain(r.text).slice(-1500));
  // "Done" with no Hive to show for it (Windows' installer ends the same way when it stops) is a stop: keep what it said on the screen.
  if (what === 'done' && !lib.openCommand(process.platform, process.env)) what = 'failed';
  if (what === 'done') return open();
  send('state', what, { platform: process.platform, note: '' });
}

// The window's size and place are kept in the app's own data folder (nothing from the Hive), and used at the next launch.
const stateFile = () => path.join(app.getPath('userData'), 'window-state.json');
function savedState() {
  try { return lib.windowState(JSON.parse(fs.readFileSync(stateFile(), 'utf8')), screen.getAllDisplays().map(d => d.workArea)); } catch { return null; }
}
function saveState(w) {
  try {
    if (!w || w.isDestroyed() || w.isMinimized() || w.isFullScreen()) return;
    fs.writeFileSync(stateFile(), JSON.stringify({ ...w.getNormalBounds(), maximized: w.isMaximized() }));
  } catch { }
}

function create() {
  const was = savedState();
  win = new BrowserWindow({
    width: 1280, height: 860, ...(was ? { width: was.width, height: was.height, ...('x' in was ? { x: was.x, y: was.y } : {}) } : {}), minWidth: 380, minHeight: 500, title: 'Hive', backgroundColor: require('electron').nativeTheme.shouldUseDarkColors ? '#111111' : '#ffffff', autoHideMenuBar: true,
    icon: path.join(__dirname, 'build', 'icon.png'),
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true, devTools: !app.isPackaged },
  });
  if (was && was.maximized) win.maximize();
  let tick = null;
  const later = () => { clearTimeout(tick); tick = setTimeout(() => saveState(win), 500); };
  win.on('resize', later); win.on('move', later);
  win.on('close', () => { clearTimeout(tick); saveState(win); });
  win.on('closed', () => { win = null; });
  // Links that leave the Hive's own app open in the person's browser; the window itself only ever shows the Hive or the setup screen.
  const stays = u => (hiveUrl && lib.sameApp(u, hiveUrl));
  // The same rules for the main window, for a window the web app opens, and for where a server sends either of them on.
  const guard = wc => {
    wc.setWindowOpenHandler(({ url }) => {
      if (stays(url)) return { action: 'allow', overrideBrowserWindowOptions: { autoHideMenuBar: true, webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, devTools: !app.isPackaged } } };
      external(url);
      return { action: 'deny' };
    });
    const nav = (e, url) => {
      if (stays(url) || (wc === win.webContents && isSetupUrl(url))) return;
      e.preventDefault();
      external(url);
    };
    wc.on('will-navigate', nav);
    wc.on('will-redirect', nav);
    wc.on('did-create-window', w => guard(w.webContents));
  };
  guard(win.webContents);
  // No menu on Windows and Linux, so the keys a View menu would give: zoom, and reload (only while the Hive shows).
  if (process.platform !== 'darwin') win.webContents.on('before-input-event', (e, input) => {
    const a = lib.shortcut(input), wc = win.webContents;
    if (!a) return;
    if (a === 'reload') { if (hiveUrl && !isSetup(wc)) { e.preventDefault(); wc.reload(); } return; }
    e.preventDefault();
    wc.setZoomLevel(a === 'zoomReset' ? 0 : Math.max(-3, Math.min(5, wc.getZoomLevel() + (a === 'zoomIn' ? 0.5 : -0.5))));
  });
  // The Hive stopped under the window (an update, a restart): go and find it again rather than show a blank page.
  win.webContents.on('did-fail-load', (_e, code, _d, url, main) => {
    if (!(main && code !== -3 && hiveUrl && lib.isLocal(url))) return;
    hiveUrl = null;
    // A few tries, then the screen that says so (with Try again and Repair), not a loop.
    if (++fails > 3) { fails = 0; showSetup('nostart', "The Hive's app stopped answering."); } else setTimeout(open, 1500);
  });
  open();
}

// Microphone (calls), notifications and the clipboard, for the Hive's own app only.
function permissions() {
  const ok = (wc, perm, origin) => {
    const u = origin || (wc && wc.getURL()) || '';
    return !!hiveUrl && lib.sameApp(u, hiveUrl) && ['media', 'notifications', 'clipboard-read', 'clipboard-sanitized-write', 'fullscreen'].includes(perm);
  };
  session.defaultSession.setPermissionRequestHandler((wc, perm, cb, d) => cb(ok(wc, perm, d && d.requestingUrl)));
  session.defaultSession.setPermissionCheckHandler((wc, perm, origin) => ok(wc, perm, origin));
  // A file the Hive page offers is saved to Downloads under its own name, with no dialog and nothing opened in the window.
  session.defaultSession.on('will-download', (_e, item, wc) => {
    const dir = (process.env.HIVE_APP_TEST && process.env.HIVE_APP_DOWNLOADS) || app.getPath('downloads');
    try { fs.mkdirSync(dir, { recursive: true }); } catch { }
    if (!hiveUrl || !lib.sameApp(item.getURL(), hiveUrl)) return item.cancel();
    const dest = lib.downloadPath(dir, item.getFilename(), fs.existsSync);
    item.setSavePath(dest);
    log('download ' + path.basename(dest));
  });
}

const fromSetup = e => e.senderFrame && isSetupUrl(e.senderFrame.url) && win && e.sender === win.webContents;
ipcMain.on('install', (e, accept) => { if (fromSetup(e)) install(accept); });
ipcMain.on('retry', e => { if (fromSetup(e)) open(); });
ipcMain.on('copy', (e, text) => { if (fromSetup(e)) clipboard.writeText(lib.redact(text).slice(0, 200000) + '\n\nApp log (latest):\n' + lib.tailLog(logFile())); });
ipcMain.on('version', e => { e.returnValue = fromSetup(e) ? app.getVersion() : ''; });
ipcMain.on('download', e => { if (fromSetup(e)) external(lib.downloadPage('update')); });   // the setup screen's "newer app" link
// The setup screen asks once its listener is up, so a notice that arrived while the page was loading isn't lost.
ipcMain.on('newer?', e => { if (fromSetup(e) && newer) e.sender.send('newer', newer); });
ipcMain.on('quit', e => { if (fromSetup(e)) app.quit(); });

// Windows ties notifications (and their clicks) to the Start menu shortcut by this ID; it must match the installer's appId.
if (process.platform === 'win32') app.setAppUserModelId('cc.ihsan.hive');
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => { if (win) { if (win.isMinimized()) win.restore(); win.focus(); } });
  app.whenReady().then(() => {
    if (process.platform !== 'darwin') Menu.setApplicationMenu(null);
    else Menu.setApplicationMenu(Menu.buildFromTemplate([{ role: 'appMenu' }, { role: 'editMenu' }, { role: 'windowMenu' }])); // no View menu: its Reload would skip our guard
    permissions(); create(); checkNewer();
    app.on('activate', () => { if (!win) create(); });
  });
  // Closing the window closes the app. The Hive itself keeps running in the background, as it does without the app.
  app.on('window-all-closed', () => app.quit());
  app.on('before-quit', () => { if (child) { try { process.platform === 'win32' ? child.kill() : process.kill(-child.pid); } catch { } } });
}
