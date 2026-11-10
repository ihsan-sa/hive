// The Hive as a desktop app. A thin shell: it finds the Hive on this machine (or installs it with the site's own installer),
// asks it for its address, and shows the web app in a window. All the product is the web app, so it has one version on every system.
'use strict';
const { app, BrowserWindow, ipcMain, shell, session, Menu } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const lib = require('./lib');

let win = null, hiveUrl = null, child = null, kept = false, fails = 0;
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

async function showSetup(state, note) {
  if (!isSetup(win.webContents)) await win.loadFile(setupFile);
  send('state', state, { platform: process.platform, note: note || '' });
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
  const sorry = "The Hive's app didn't answer. " + (cmd.url ? 'It runs in another account of this machine: start it there (sudo -iu hive hive open --print), then try again.' : '');
  if (!await answers(url, cmd.url ? 4000 : 90000)) return showSetup('nostart', sorry);
  hiveUrl = url;
  try { await win.loadURL(url); fails = 0; }
  catch (e) { hiveUrl = null; return showSetup('nostart', sorry); }
}

async function install(accept) {
  if (child) return;
  await showSetup('installing');
  const r = await run(lib.installCommand(process.platform, process.env, accept), s => send('log', lib.redact(lib.plain(s))));
  child = null;
  let what = lib.installResult(r.code, r.text, process.platform);
  // "Done" with no Hive to show for it (Windows' installer ends the same way when it stops) is a stop: keep what it said on the screen.
  if (what === 'done' && !lib.openCommand(process.platform, process.env)) what = 'failed';
  if (what === 'done') return open();
  send('state', what, { platform: process.platform, note: '' });
}

function create() {
  win = new BrowserWindow({
    width: 1280, height: 860, minWidth: 380, minHeight: 500, title: 'Hive', backgroundColor: '#111111', autoHideMenuBar: true,
    icon: path.join(__dirname, 'build', 'icon.png'),
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true, devTools: !app.isPackaged },
  });
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
}

const fromSetup = e => e.senderFrame && isSetupUrl(e.senderFrame.url) && win && e.sender === win.webContents;
ipcMain.on('install', (e, accept) => { if (fromSetup(e)) install(accept); });
ipcMain.on('retry', e => { if (fromSetup(e)) open(); });
ipcMain.on('quit', e => { if (fromSetup(e)) app.quit(); });

if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => { if (win) { if (win.isMinimized()) win.restore(); win.focus(); } });
  app.whenReady().then(() => {
    if (process.platform !== 'darwin') Menu.setApplicationMenu(null);
    permissions(); create();
    app.on('activate', () => { if (!win) create(); });
  });
  // Closing the window closes the app. The Hive itself keeps running in the background, as it does without the app.
  app.on('window-all-closed', () => app.quit());
  app.on('before-quit', () => { if (child) { try { process.platform === 'win32' ? child.kill() : process.kill(-child.pid); } catch { } } });
}
