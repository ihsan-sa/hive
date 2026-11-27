// The Hive as a desktop app. A thin shell: it finds the Hive on this machine (or installs it with the site's own installer),
// asks it for its address, and shows the web app in a window. All the product is the web app, so it has one version on every system.
'use strict';
const { app, BrowserWindow, WebContentsView, ipcMain, shell, session, net, Menu, screen, clipboard, dialog, safeStorage } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const lib = require('./lib');
const gate = require('./gate');

let hiveUrl = null, child = null, kept = false, fails = 0, newer = null, shown = null;   // shown: the update notification, kept so its click still arrives
// The Hives the app can show (DESIGN-many-hives.md). The local one is found as before; its rules read its address as it is
// at the moment of asking. A remote one is one the person connected to by its address, kept in hives.json (step 3).
const local = { id: 'local', kind: 'local', first: true, name: 'This computer', url: () => hiveUrl, same: u => !!hiveUrl && lib.sameApp(u, hiveUrl), popups: new Set() };
let remotes = [], hivesFile = null, localState = ['starting', ''];
// The app's windows (step 4). Each shows one Hive at a time: W = { bw, chosen, aside, views: Map(hive id -> its view), recent }.
// Two windows may show the same Hive, each with a view of its own; a window keeps its last few Hives' views so switching back is instant.
const wins = new Set();
let last = null, quitting = false, started = false;
const alive = W => !!W && !!W.bw && !W.bw.isDestroyed();
const all = () => [...wins].filter(alive);
// The local Hive is in the list unless the person connected to a Hive somewhere else instead of setting one up here
// (DESIGN-many-hives.md, step 5), and none is installed: then the app is only for the Hives they connected to.
const hasLocal = () => !store.extra.noLocal || !!hiveUrl || !!child || !!lib.openCommand(process.platform, process.env);
const hives = () => hasLocal() ? [local, ...remotes] : [...remotes];
const home = () => hives()[0] || local;   // where a new window goes when nothing else says
const many = () => hives().length > 1;   // the rail shows once a second Hive exists
const winOf = bw => all().find(W => W.bw === bw);
// The window a menu click, a key in a popup or a second launch means: the focused one, else the one focused last.
const front = () => winOf(BrowserWindow.getFocusedWindow()) || (alive(last) ? last : all().at(-1)) || null;
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
function external(url) {
  if (!/^https?:\/\//.test(url)) return;
  if (process.env.HIVE_APP_TEST === '1') return log('external ' + hostOf(url));   // the tests check where a link went, by its host only
  shell.openExternal(url);
}

// A message to a window's own page (only while it is the app's page), or to every window's.
function sendTo(W, name, ...a) { if (alive(W) && isSetup(W.bw.webContents)) W.bw.webContents.send(name, ...a); }
function sendAll(name, ...a) { for (const W of all()) sendTo(W, name, ...a); }
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

// The local Hive's own screen (setup, starting, didn't start). It shows in every window that is on the local Hive.
async function showSetup(state, note) {
  log('screen ' + state + (note ? ': ' + note : ''));
  localState = [state, note || ''];
  for (const W of all()) { closeView(W, local.id); if (W.chosen === local && !W.aside) display(W); }
}
function render(W, state, extra) {
  sendTo(W, 'state', state, { platform: process.platform, note: '', ...extra });
  if (newer) sendTo(W, 'newer', newer);
}

// What a window shows: its Hive's view over the app's own page, or the app's page (setup, Hives, Connect, can't reach).
// With more than one Hive the page's rail of Hives shows down the left and the view sits beside it.
function display(W) {
  if (!alive(W)) return;
  const h = W.chosen, bw = W.bw, mine = W.views.get(h.id), v = !W.aside && mine && !h.pending ? mine : null;
  const bar = v && barWanted(W) ? barOf(W) : null;
  for (const c of bw.contentView.children) if (c !== v && c !== bar) bw.contentView.removeChildView(c);
  rail(W);
  if (v) {
    // The bar goes under the Hive's view in the list (they don't overlap), so the Hive's view stays the top one.
    if (bar && !bw.contentView.children.includes(bar)) { bw.contentView.addChildView(bar, 0); barThread(W); }
    if (!bw.contentView.children.includes(v)) bw.contentView.addChildView(v);
    layout(W);
    const t = v.webContents.getTitle();
    bw.setTitle(t && !/^https?:|^about:/.test(t) ? t : h.name);
    v.webContents.focus();
    return;
  }
  bw.setTitle('Hive');
  if (W.aside) return;
  if (h === local) render(W, localState[0], { note: localState[1] });
  else if (h.signin) render(W, 'signin', { name: h.name, host: hostOf(h.saved.url), waiting: !!h.gateRun, again: !h.fresh, why: h.why || '' });
  else render(W, 'cantreach', { name: h.name, host: hostOf(h.saved.url), why: h.why || '' });
  bw.webContents.focus();
}
const RAIL = 72, STRIP = 16;   // the rail's width open and folded, in the page's own pixels (setup.html), so they grow with the page's zoom
const folded = () => store.extra.railFolded === true;   // step 8c: the rail folded to a thin strip, for every window, kept in hives.json
function layout(W) {
  if (!alive(W)) return;
  const [width, height] = W.bw.getContentSize();
  const x = many() ? Math.round((folded() ? STRIP : RAIL) * W.bw.webContents.getZoomFactor()) : 0;
  const kids = W.bw.contentView.children, bar = W.bar && kids.includes(W.bar) ? Math.min(BAR, height) : 0;
  for (const c of kids) c.setBounds(c === W.bar ? { x, y: height - bar, width: Math.max(0, width - x), height: bar } : { x, y: 0, width: Math.max(0, width - x), height: height - bar });
}

// Signed from the start (projects/word-shell/SIGNED-FROM-START.md, step 1). The app keeps one device key in the owner's
// own account, and draws its own composer bar under the local Hive's page, from its own file: the Hive's page can't read
// or fill it, and tells it nothing but the open thread, which the app reads from the page's address. What the owner types
// there is signed for this Hive (its id, from the installer), the thread, the time and a nonce, and posted to the Hive.
// The bar shows only for a Hive whose installer gave it an id; today's installers give none, so it shows nowhere yet.
const BAR = 76;
const composerFile = path.join(__dirname, 'composer.html');
const composerUrl = require('url').pathToFileURL(composerFile).href;
const isComposer = u => String(u).split(/[?#]/)[0] === composerUrl;
const barWanted = W => W.chosen === local && !W.aside && !!hiveUrl && lib.HIVE_ID.test(store.extra.localHive || '');
function barOf(W) {
  if (W.bar && !W.bar.webContents.isDestroyed()) return W.bar;
  const b = new WebContentsView({ webPreferences: { preload: path.join(__dirname, 'composer-preload.js'), partition: 'app', ...PREFS } });
  b.setBackgroundColor(bg());
  const wc = b.webContents;
  wc.setWindowOpenHandler(() => ({ action: 'deny' }));
  const own = (e, url) => { if (!isComposer(e.url || url)) e.preventDefault(); };
  wc.on('will-navigate', own); wc.on('will-redirect', own);
  keys(wc, W); menuFor(wc, null, W);
  wc.loadFile(composerFile).then(() => barThread(W), () => { });
  W.bar = b;
  return b;
}
function barThread(W) {
  if (!alive(W) || !W.bar || W.bar.webContents.isDestroyed()) return;
  const v = W.views.get(local.id);
  W.bar.webContents.send('thread', v ? lib.threadOf(v.webContents.getURL()) : '');
}
// The key file: { v: 1, enc (the OS keychain locked it, by safeStorage), key (the private key, PKCS8, base64) }, only the
// owner's to read. An unreadable one is set aside, not deleted, and a new key made (to be pinned again).
let dev = null;
const keyFile = () => path.join(app.getPath('userData'), 'device-key.json');
function deviceKey() {
  if (dev) return dev;
  const f = keyFile();
  if (fs.existsSync(f)) {
    try { const j = JSON.parse(fs.readFileSync(f, 'utf8')); dev = lib.deviceKey(j.enc ? safeStorage.decryptString(Buffer.from(j.key, 'base64')) : j.key); } catch { }
    if (dev) return dev;
    try { fs.renameSync(f, f + '.bad'); } catch { }
    log('device key unreadable: set aside as device-key.json.bad, a new one made');
  }
  const k = lib.newDeviceKey();
  let enc = false, key = k.pkcs8;
  try { if (safeStorage.isEncryptionAvailable()) { key = safeStorage.encryptString(k.pkcs8).toString('base64'); enc = true; } } catch { }
  fs.mkdirSync(path.dirname(f), { recursive: true });
  fs.writeFileSync(f + '.new', JSON.stringify({ v: 1, enc, key }) + '\n', { mode: 0o600 });
  fs.renameSync(f + '.new', f);
  log('device key made, ' + lib.keyFingerprint(k.key) + (enc ? ', kept by the OS keychain' : ''));
  return dev = k;
}
const publicKey = () => { try { return deviceKey().key; } catch (e) { log('device key: ' + e.message); return null; } };
// The signed post, to the Hive's own address, in its session (so its sign-in cookie goes with it).
function postSigned(h, url, body) {
  return new Promise(done => {
    let q, out = '', end = false;
    const fin = r => { if (!end) { end = true; clearTimeout(tm); done(r); } };
    const tm = setTimeout(() => { try { q.abort(); } catch { } fin({ ok: false, why: "The Hive didn't answer. Try again." }); }, 15000);
    try { q = net.request({ url, method: 'POST', session: sessionFor(h), useSessionCookies: true, redirect: 'error' }); }
    catch (e) { return fin({ ok: false, why: "Couldn't reach the Hive." }); }
    q.setHeader('content-type', 'application/json'); q.setHeader('X-H2-Dash', '1');
    q.on('response', r => {
      r.on('data', d => { if (out.length < 4096) out += d; });
      r.on('end', () => {
        let j = {}; try { j = JSON.parse(out); } catch { }
        if (r.statusCode >= 200 && r.statusCode < 300) return fin({ ok: true, id: typeof j.id === 'string' ? j.id.slice(0, 40) : '' });
        fin({ ok: false, why: (typeof j.error === 'string' ? j.error : 'The Hive refused it (' + r.statusCode + ').').slice(0, 200) });
      });
      r.on('error', () => fin({ ok: false, why: "The Hive's answer broke off." }));
    });
    q.on('error', () => fin({ ok: false, why: "Couldn't reach the Hive." }));
    q.end(JSON.stringify(body));
  });
}
// The rail: one tile per Hive (the one this window shows marked), and + for Connect. Names are sent as text, drawn as text.
const COLORS = ['#2f62c9', '#0f7a65', '#8a3fb8', '#b4461b', '#0e6f8a', '#6b6f1a'];
function where(h) {
  return h === local ? (hiveUrl ? 'On this computer' : 'On this computer, not running') : hostOf(h.saved.url) + (h.signin ? ' · sign in needed' : h.down ? ' · not answering' : '');
}
function rail(W) {
  const mac = process.platform === 'darwin';
  sendTo(W, 'rail', { show: many(), folded: folded(), list: hives().map((h, i) => ({ id: h.id, name: h.name, ini: ([...String(h.name).trim()][0] || '?').toUpperCase(), local: h === local,
    where: where(h), current: h === W.chosen, icon: icons[h.id] || null, key: i < 9 ? (mac ? '⌘' : 'Ctrl+') + (i + 1) : '',
    color: h === local ? '#f5b800' : COLORS[remotes.indexOf(h) % COLORS.length], ink: h === local ? '#1a1a1a' : '#ffffff' })) });
}
// Each Hive's icon (step 8b), kept in icons.json beside hives.json as {id: data: address}: the picture its page names as its icon,
// taken inline or fetched from that same Hive in its own session (so with its sign-in), no redirects, 10 s, 256 KB at most.
// The Hive's built-in mark or no icon gives the tile its letter again; a failed fetch keeps the icon it had.
let icons = {};
const iconTry = new Map();   // a count per Hive, so an older fetch that ends late doesn't overwrite a newer answer
const iconsFile = () => path.join(app.getPath('userData'), 'icons.json');
function loadIcons() {
  try { icons = lib.validIcons(JSON.parse(fs.readFileSync(iconsFile(), 'utf8'))); } catch { icons = {}; }
}
function setIcon(id, url) {
  if ((icons[id] || null) === (url || null)) return;
  if (url) icons[id] = url; else delete icons[id];
  const tmp = iconsFile() + '.tmp';
  try { fs.writeFileSync(tmp, JSON.stringify(icons), { mode: 0o600 }); fs.renameSync(tmp, iconsFile()); } catch (e) { log('icons.json not saved: ' + e.message); }
  for (const W of all()) rail(W);
}
function takeIcon(h, wc, urls) {
  const n = (iconTry.get(h.id) || 0) + 1; iconTry.set(h.id, n);
  const p = lib.pickIcon(urls, h.same);
  if (!p) return;
  if (p.kind !== 'fetch') return setIcon(h.id, p.kind === 'data' ? p.url : null);
  wc.session.fetch(p.url, { redirect: 'error', signal: AbortSignal.timeout(10000) })
    .then(async r => r.status === 200 && Number(r.headers.get('content-length') || 0) <= lib.ICON_MAX
      ? lib.iconData(r.headers.get('content-type'), Buffer.from(await r.arrayBuffer())) : null)
    .then(d => { if (d && iconTry.get(h.id) === n && hiveById(h.id)) setIcon(h.id, d); }, e => log('icon not fetched from ' + hostOf(p.url) + ': ' + e.message));
}
const hostOf = u => { try { return new URL(u).host; } catch { return ''; } };
function closeView(W, id) {
  const v = W.views.get(id);
  if (!v) return;
  W.views.delete(id); W.recent = W.recent.filter(x => x !== id);
  if (alive(W)) try { W.bw.contentView.removeChildView(v); } catch { }
  setImmediate(() => { try { if (!v.webContents.isDestroyed()) v.webContents.close(); } catch { } });
}
// A window keeps the views of the last three Hives it showed; an older one goes, unless it is playing sound or was let
// use the microphone (a call in it goes on).
const heard = new WeakSet();
function trim(W) {
  for (const id of W.recent.slice(3)) {
    const v = W.views.get(id);
    if (v && !(v.webContents.isCurrentlyAudible() || heard.has(v.webContents))) closeView(W, id);
  }
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
      sendAll('newer', v);
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

// Finds the Hive and shows it (in every window on it); the setup screen if there's none or it won't start.
let sorry = '';
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
  sorry = "The Hive's app didn't answer. " + (cmd.url ? 'It runs in another account of this machine: start it there (sudo -iu hive hive open --print), then try again.' : '');
  if (!await answers(url, cmd.url ? 4000 : Number(process.env.HIVE_APP_ANSWER_MS) || 90000)) return showSetup('nostart', sorry);
  // The address is the key. It goes only to a server that shows it already has it; anything else on that port gets nothing.
  if (!await proves(url)) return showSetup('nostart', "What answers on the Hive's address didn't show that it is the Hive, so the app didn't sign in to it. If the Hive on this computer is an older one, Repair brings it up to date. If not, another program may be using the Hive's port.");
  hiveUrl = url;
  log('opened the Hive');
  watch();
  for (const W of all()) { closeView(W, local.id); if (W.chosen === local) openLocal(W); else rail(W); }
}
// The local Hive in this window: its sign-in address, or a page of it a link asked for.
function openLocal(W, at) {
  const v = viewFor(W, local), u = hiveUrl;
  display(W);
  return v.webContents.loadURL(at || u).then(() => { fails = 0; if (W.chosen === local && W.views.get(local.id) === v) display(W); },
    () => { if (W.views.get(local.id) !== v || hiveUrl !== u) return; hiveUrl = null; showSetup('nostart', sorry); });
}

// While the Hive is showing, ask again now and then: if another program has taken its port (the Hive restarted under the window),
// the windows leave it for the setup screen instead of sending the sign-in cookie on to it.
let watcher = null;
function watch() {
  clearInterval(watcher);
  watcher = setInterval(async () => {
    if (!hiveUrl || !all().some(W => W.views.has(local.id))) return;
    const u = hiveUrl;
    if (await proves(u) || hiveUrl !== u) return;
    hiveUrl = null;
    showSetup('nostart', "The Hive stopped answering at its address (or another program took it), so the app left it. Try again signs in once the Hive answers.");
  }, Number(process.env.HIVE_APP_WATCH_MS) || 30000);
}

// What the installer says goes to the windows on the local Hive.
const sendLocal = (name, ...a) => { for (const W of all()) if (W.chosen === local && !W.aside) sendTo(W, name, ...a); };
async function install(accept) {
  if (child) return;
  await showSetup('installing');
  // Redact whole lines: a key split across two chunks would slip past the pattern.
  let pend = '';
  const r = await run(lib.installCommand(process.platform, process.env, accept, publicKey()), s => {
    pend += s; const i = Math.max(pend.lastIndexOf('\n'), pend.lastIndexOf('\r'));
    if (i >= 0) { sendLocal('log', lib.redact(lib.plain(pend.slice(0, i + 1)))); pend = pend.slice(i + 1); }
  });
  if (pend) sendLocal('log', lib.redact(lib.plain(pend)));
  child = null;
  let what = lib.installResult(r.code, r.text, process.platform);
  log('installer exit ' + r.code + ' -> ' + what + ': ' + lib.plain(r.text).slice(-1500));
  // "Done" with no Hive to show for it (Windows' installer ends the same way when it stops) is a stop: keep what it said on the screen.
  if (what === 'done' && !lib.openCommand(process.platform, process.env)) what = 'failed';
  if (what === 'done') {
    // The Hive's id, for the signed bar (step 1); an installer that prints none leaves the bar off.
    const id = lib.hiveIdFrom(r.text);
    if (id) store.extra.localHive = id; else delete store.extra.localHive;
    saveHives();
    return open();
  }
  localState = [what, ''];
  sendLocal('state', what, { platform: process.platform, note: '' });
}

// The windows' sizes and places, and the Hive each showed, are kept in the app's own data folder (nothing from the Hive)
// and come back at the next launch: { v: 1, windows: [{ hive, x, y, width, height, maximized }] }, the focused one last.
const stateFile = () => path.join(app.getPath('userData'), 'window-state.json');
function savedWindows() {
  let saved = null;
  try { saved = JSON.parse(fs.readFileSync(stateFile(), 'utf8')); } catch { }
  return lib.windowsState(saved, screen.getAllDisplays().map(d => d.workArea), hives().map(h => h.id));
}
// Saved when a window opens, changes Hive, moves or resizes (a moment after), and closes; Playwright's close and a
// system shutdown don't always reach before-quit, so nothing waits for the end.
function saveAll(but) {
  if (!started) return;
  const f = front(), list = all().filter(W => W !== but).sort((a, b) => (a === f) - (b === f));
  if (!list.length) return;
  const windows = list.map(W => {
    const b = W.bw;
    if (!b.isMinimized() && !b.isFullScreen()) W.rect = { ...b.getNormalBounds(), maximized: b.isMaximized() };
    return { hive: W.chosen.id, ...(W.rect || {}) };
  });
  try { fs.writeFileSync(stateFile(), JSON.stringify({ v: 1, windows })); } catch { }
}

const PREFS = { contextIsolation: true, nodeIntegration: false, sandbox: true, devTools: !app.isPackaged };
const bg = () => require('electron').nativeTheme.shouldUseDarkColors ? '#111111' : '#ffffff';
// A window on Hive h, at rect (as saved) or a little down from the front one; at, if given, is the page of h to open.
function create(h, rect, at) {
  const f = front(), fb = !rect && f ? f.bw.getNormalBounds() : null;
  const place = rect ? { width: rect.width, height: rect.height, ...('x' in rect ? { x: rect.x, y: rect.y } : {}) }
    : fb ? { width: fb.width, height: fb.height, x: fb.x + 28, y: fb.y + 28 } : {};
  const bw = new BrowserWindow({
    width: 1280, height: 860, ...place, minWidth: 380, minHeight: 500, title: 'Hive', backgroundColor: bg(),
    // Windows and Linux show the menu bar, since its Hives menu is how you move between Hives (DESIGN-many-hives.md, step 3).
    autoHideMenuBar: false,
    // A Mac can gather the windows as tabs of one (Window > Merge All Windows, or its "prefer tabs" setting).
    ...(process.platform === 'darwin' ? { tabbingIdentifier: 'hive' } : {}),
    icon: path.join(__dirname, 'build', 'icon.png'),
    // The app's own page has a session of its own, in memory: it shares nothing with any Hive.
    webPreferences: { preload: path.join(__dirname, 'preload.js'), partition: 'app', ...PREFS },
  });
  const W = { bw, chosen: h, aside: false, asideState: null, views: new Map(), recent: [], rect: null, tick: null };
  wins.add(W); last = W;
  if (rect && rect.maximized) bw.maximize();
  const later = () => { clearTimeout(W.tick); W.tick = setTimeout(saveAll, 500); };
  bw.on('resize', later); bw.on('move', later);
  for (const ev of ['resize', 'maximize', 'unmaximize', 'enter-full-screen', 'leave-full-screen']) bw.on(ev, () => layout(W));
  bw.on('page-title-updated', e => e.preventDefault());   // the title is the Hive's (display sets it)
  bw.on('focus', () => { last = W; menu(); });
  bw.on('close', () => { clearTimeout(W.tick); if (!quitting) saveAll(all().length > 1 ? W : null); });
  bw.on('closed', () => {
    for (const id of [...W.views.keys()]) closeView(W, id);
    if (W.bar) { const b = W.bar; W.bar = null; setImmediate(() => { try { if (!b.webContents.isDestroyed()) b.webContents.close(); } catch { } }); }
    wins.delete(W);
    if (last === W) last = all().at(-1) || null;
    menu();
  });
  // The app's own page only ever shows itself: every link from it goes to the browser.
  const wc = bw.webContents;
  wc.setWindowOpenHandler(({ url }) => { external(url); return { action: 'deny' }; });
  const own = (e, url) => { if (!isSetupUrl(e.url || url)) e.preventDefault(); };
  wc.on('will-navigate', own); wc.on('will-redirect', own);
  menuFor(wc, null, W); keys(wc, W);
  if (h.kind === 'remote') openRemote(W, h, at);
  else if (hiveUrl) openLocal(W, at);
  W.recent = [h.id];
  menu(); saveAll();
  bw.loadFile(setupFile).then(() => display(W), () => { });
  return W;
}

// One Hive's page in one window: a view of its own, in the Hive's session, held while the window shows another Hive so
// switching back is instant. Its guards are bound to its Hive and its window.
function viewFor(W, h) {
  const v = new WebContentsView({ webPreferences: { session: sessionFor(h), ...PREFS } });
  v.setBackgroundColor(bg());
  W.views.set(h.id, v); W.recent = [h.id, ...W.recent.filter(x => x !== h.id)];
  const wc = v.webContents;
  guard(wc, h, W); keys(wc, W); menuFor(wc, h, W);
  wc.on('page-favicon-updated', (_e, urls) => takeIcon(h, wc, urls));
  if (h === local) for (const ev of ['did-navigate', 'did-navigate-in-page']) wc.on(ev, () => { if (W.views.get(h.id) === v) barThread(W); });
  wc.on('page-title-updated', (_e, t) => { if (alive(W) && W.chosen === h && !W.aside && W.views.get(h.id) === v) W.bw.setTitle(t || h.name); });
  wc.on('did-fail-load', (_e, code, desc, url, main) => {
    if (!main || code === -3 || W.views.get(h.id) !== v) return;
    if (h === local) {
      // The Hive stopped under the window (an update, a restart): go and find it again rather than show a blank page.
      if (!(hiveUrl && lib.isLocal(url))) return;
      hiveUrl = null;
      // A few tries, then the screen that says so (with Try again and Repair), not a loop.
      if (++fails > 3) { fails = 0; showSetup('nostart', "The Hive's app stopped answering."); } else setTimeout(open, 1500);
      return;
    }
    if (!h.pending) remoteFailed(W, h, failWords(code, desc));
  });
  return v;
}
function openRemote(W, h, at) {
  if (h.signin) return;   // its window shows the sign-in screen until a sign-in comes back
  h.down = false; h.why = '';
  const v = viewFor(W, h);
  if (!h.saved.gate) return void v.webContents.loadURL(at || h.saved.url).catch(() => { });   // a failure arrives as did-fail-load
  // Behind a gate: with no sign-in left in its session the window asks for one, rather than load the gate's page.
  gateCookie(h).then(ok => {
    if (!alive(W) || W.views.get(h.id) !== v) return;
    if (ok) v.webContents.loadURL(at || h.saved.url).catch(() => { }); else needSignIn(h);
  });
}
function remoteFailed(W, h, why) {
  h.down = true; h.why = why;
  log('cannot reach ' + hostOf(h.saved.url) + ': ' + why);
  closeView(W, h.id);
  if (W.chosen === h && !W.aside) display(W); else rail(W);
}
function failWords(code, desc) {
  const d = (String(desc || '').match(/ERR_[A-Z_]+/) || [''])[0];   // the error's name only: the message carries the address, key and all
  if (/NAME_NOT_RESOLVED|NAME_RESOLUTION/.test(d)) return 'No computer answers to that name. Check the address.';
  if (/CERT|SSL/.test(d)) return "Its certificate isn't one this computer trusts, so the app didn't connect. It has no way to go on anyway.";
  if (/CONNECTION_REFUSED/.test(d)) return 'Nothing answered at that address. The Hive may be stopped.';
  if (/TIMED_OUT|INTERNET_DISCONNECTED|NETWORK_CHANGED|ADDRESS_UNREACHABLE/.test(d)) return "It didn't answer. Check this computer's internet connection.";
  return `It didn't answer (${d || 'error ' + code}).`;
}

// Links that leave a Hive's own app open in the person's browser. The same rules for the Hive's view, for a window its
// page opens, and for where a server sends either of them on. Each guard is bound to its own Hive and window, not to
// whichever Hive the window shows now (DESIGN-many-hives.md R12). W is null for a window the Hive's page opened.
const otherHive = (h, url) => hives().find(o => o !== h && o.url() && o.same(url));
// A page of Hive o, asked for from another Hive's page: in a window already showing o, else in a new one.
function showIn(o, url) {
  const W = [front(), ...all()].find(x => alive(x) && x.chosen === o && !x.aside);
  if (!W) return create(o, null, url);
  const v = W.views.get(o.id);
  if (v) v.webContents.loadURL(url).catch(() => { }); else if (o.kind === 'remote') openRemote(W, o, url); else if (hiveUrl) openLocal(W, url);
  if (W.bw.isMinimized()) W.bw.restore();
  W.bw.focus();
}
function guard(wc, h, W) {
  wc.setWindowOpenHandler(({ url, disposition }) => {
    if (h.same(url)) {
      // A plain link to the Hive's own page (target=_blank, a middle click) opens a window of the app on it; a page the
      // Hive opens as a popup (a sign-in) stays a popup, which keeps the opener's session: the same Hive and no other.
      if (W && (disposition === 'foreground-tab' || disposition === 'background-tab')) { create(h, null, url); return { action: 'deny' }; }
      return { action: 'allow', overrideBrowserWindowOptions: { autoHideMenuBar: true, backgroundColor: bg(), webPreferences: { ...PREFS } } };
    }
    const o = otherHive(h, url);
    if (o) showIn(o, url); else external(url);
    return { action: 'deny' };
  });
  wc.on('will-navigate', (e, url) => {
    const u = e.url || url;
    if (h.same(u)) return;
    e.preventDefault();
    const o = otherHive(h, u);
    if (o) showIn(o, u); else external(u);
  });
  wc.on('will-redirect', (e, url, _inPlace, isMain) => {
    const u = e.url || url;
    if (h.same(u)) return;
    e.preventDefault();
    // A remote Hive behind a sign-in gate sends its page to the gate. That isn't the Hive, so the view doesn't follow:
    // the window asks for a sign-in in the browser (a Cloudflare Access gate, step 6), or says where it was sent.
    if (h.kind === 'remote' && (e.isMainFrame ?? isMain) && W && W.views.get(h.id)?.webContents === wc) {
      const at = hostOf(u);
      if (h.pending) h.blocked = at; else gateOrFail(W, h, u);
      return;
    }
    external(u);
  });
  wc.on('did-create-window', w => {
    h.popups.add(w); w.on('closed', () => h.popups.delete(w));
    if (process.platform !== 'darwin') w.removeMenu();
    guard(w.webContents, h, null); keys(w.webContents, null); menuFor(w.webContents, h, null);
  });
}
// Windows and Linux have no View menu, so the keys one would give: zoom, and reload (a Hive's page, not the app's own);
// and those of the Hives menu, which a key in a Hive's page would otherwise not reach: Ctrl+1..9 and Ctrl+Shift+N.
function keys(wc, W) {
  if (process.platform === 'darwin') return;
  wc.on('before-input-event', (e, input) => {
    const a = lib.shortcut(input);
    if (!a) return;
    e.preventDefault();
    const at = alive(W) ? W : front();
    if (a.startsWith('hive:')) { const h = hives()[Number(a.slice(5)) - 1]; if (h && at) choose(at, h); return; }
    if (a === 'newWindow') return void create(at ? at.chosen : local);
    if (a === 'reload') { if (!(alive(W) && wc === W.bw.webContents)) wc.reload(); return; }
    wc.setZoomLevel(a === 'zoomReset' ? 0 : Math.max(-3, Math.min(5, wc.getZoomLevel() + (a === 'zoomIn' ? 0.5 : -0.5))));
    // The app's page zooms with its rail, so the Hive's view beside it moves over.
    if (alive(W) && wc === W.bw.webContents) { layout(W); setTimeout(() => layout(W), 50); }
  });
}
// The right-click menu. With one Hive, as before: copy a link, cut/copy/paste. With many, a link can open in a new window
// of the app (on its own Hive) or in the browser, and this Hive's own links and images can be saved.
function menuFor(wc, h, W) {
  wc.on('context-menu', (_e, p) => {
    const owner = BrowserWindow.fromWebContents(wc) || (alive(W) ? W.bw : null);
    let items;
    if (h && many()) {
      const whereIs = u => h.same(u) ? 'this' : otherHive(h, u) ? 'hive' : 'outside';
      const browser = lib.browserLabel(app.getApplicationNameForProtocol('https://'));
      items = lib.linkMenu(p, whereIs).map(k => {
        if (k === '-') return { type: 'separator' };
        if (k === 'openWindow') return { label: 'Open link in new window', click: () => { const o = h.same(p.linkURL) ? h : otherHive(h, p.linkURL); if (o) create(o, null, p.linkURL); } };
        if (k === 'openBrowser') return { label: browser, click: () => external(p.linkURL) };
        if (k === 'saveLink') return { label: 'Save link as…', click: () => wc.downloadURL(p.linkURL) };
        if (k === 'copyLink') return { label: 'Copy link', click: () => clipboard.writeText(p.linkURL) };
        if (k === 'copyImage') return { label: 'Copy image', click: () => wc.copyImageAt(p.x, p.y) };
        if (k === 'saveImage') return { label: 'Save image as…', click: () => wc.downloadURL(p.srcURL) };
        return { role: k };
      });
    } else items = lib.contextItems(p).map(k => k === 'copyLink' ? { label: 'Copy link', click: () => clipboard.writeText(p.linkURL) } : { role: k });
    if (items.length) Menu.buildFromTemplate(items).popup(owner ? { window: owner } : {});
  });
}

// The remote Hives, kept in hives.json in the app's own data folder: each one's id, name and address, never its key
// (the key opens the Hive once; after that the sign-in is the cookie in its own session).
let store = lib.parseHives(null), connecting = false;
const partitionDir = id => path.join(app.getPath('userData'), 'Partitions', 'hive-' + id);
const removedIds = () => Array.isArray(store.extra.removed) ? store.extra.removed.filter(id => /^[a-f0-9]{16}$/.test(id)) : [];
function remoteFrom(s) {
  return { id: s.id, kind: 'remote', saved: s, name: s.name, url: () => s.url, same: u => lib.sameHive(u, s.url), popups: new Set(), down: false, why: '' };
}
function loadHives() {
  hivesFile = path.join(app.getPath('userData'), 'hives.json');
  let text = null;
  try { text = fs.readFileSync(hivesFile, 'utf8'); } catch (e) { if (e.code !== 'ENOENT') text = ''; }
  store = lib.parseHives(text);
  if (store.error) log('hives.json: ' + store.error);
  remotes = store.hives.filter(s => s.kind === 'remote').map(remoteFrom);
  // What a removed Hive's session left on disk goes before any session opens on it.
  const gone = removedIds();
  for (const id of gone) try { fs.rmSync(partitionDir(id), { recursive: true, force: true }); } catch (e) { log('left a removed Hive\'s files: ' + e.message); }
  if (gone.length && !store.error) { store.extra.removed = []; saveHives(); }
}
function saveHives() {
  if (store.error || !hivesFile) return;   // a list that couldn't be read is kept as it is, not written over
  store.hives = [...store.hives.filter(s => s.kind !== 'remote'), ...remotes.map(h => h.saved)];
  const tmp = hivesFile + '.tmp';
  try { fs.writeFileSync(tmp, lib.stringifyHives(store), { mode: 0o600 }); fs.renameSync(tmp, hivesFile); } catch (e) { log('hives.json not saved: ' + e.message); }
}

// This window shows Hive h. Its view is opened if the window has none, and the views of Hives it showed long ago go.
function choose(W, h) {
  if (!alive(W) || !h) return;
  W.chosen = h; W.aside = false;
  if (!W.views.has(h.id)) { if (h.kind === 'remote') openRemote(W, h); else if (hiveUrl) openLocal(W); }
  W.recent = [h.id, ...W.recent.filter(x => x !== h.id)];
  trim(W);
  display(W);
  if (store.extra.last !== h.id) { store.extra.last = h.id; saveHives(); }
  menu(); saveAll();
}
// The app's own Hives and Connect screens, over whichever Hive the window shows (Back returns to it).
function showAside(W, state) {
  if (!alive(W)) return;
  W.aside = true; W.asideState = state;
  display(W);
  render(W, state, state === 'hives' ? { list: hives().map(h => ({ id: h.id, name: h.name, current: h === W.chosen, where: where(h), remote: h.kind === 'remote' })), noLocal: !hasLocal() } : {});
  menu();
}

async function connect(W, text) {
  if (connecting) return;
  connecting = true;
  try { const why = await connectNow(W, String(text || '').trim().slice(0, 2000)); if (why) sendTo(W, 'connect-error', why); }
  catch (e) { sendTo(W, 'connect-error', "The app couldn't connect: " + String(e.message || e).slice(0, 200)); }
  finally { connecting = false; }
}
async function connectNow(W, t) {
  if (!t) return "Type the Hive's address.";
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(t)) t = (/^(localhost|127\.|\[::1\])/i.test(t) ? 'http://' : 'https://') + t;
  t = lib.pin(t);
  const a = lib.addressRule(t);
  if (!a.ok) return a.why;
  if (hiveUrl && lib.sameHive(a.url, hiveUrl)) { choose(W, local); return ''; }
  const have = remotes.find(h => lib.sameHive(a.url, h.saved.url));
  if (have) { choose(W, have); return ''; }
  const full = new URL(t); full.search = '';   // the address as typed, #key and all: it is loaded once, never stored
  const s = { id: lib.newId([...remotes.map(h => h.id), ...store.hives.map(x => x.id), ...removedIds()]), kind: 'remote', url: a.url, name: new URL(a.url).hostname, order: remotes.length + 1 };
  const h = remoteFrom(s);
  // A Hive behind a sign-in gate (step 6) is kept at once and signed in to in the browser; its window waits for that.
  let g = null;
  try { g = await gate.probe(sessionFetch(h), a.url); }
  catch (e) { forget(h); store.extra.removed = [...removedIds(), h.id]; saveHives(); log('connect refused ' + hostOf(a.url) + ': ' + e.message); return e.message; }
  if (g) {
    if (!alive(W)) return '';
    s.gate = g; h.signin = true; h.fresh = true;
    keep(W, h, ' (behind a sign-in gate)');
    signIn(h);
    return '';
  }
  // Over plain http (this computer only) the server must first show it holds the key; over https the certificate is the proof.
  if (full.protocol === 'http:' && !await proves(full.href)) return "What answers at that address didn't show it is the Hive with that key, so the app sent it nothing.";
  if (!alive(W)) return '';
  h.pending = true;
  const v = viewFor(W, h);
  let why = '', timer;
  try {
    await Promise.race([v.webContents.loadURL(full.href), new Promise((_r, no) => { timer = setTimeout(() => no(new Error('ERR_TIMED_OUT')), 30000); })]);
  } catch (e) { why = failWords(0, e.code || e.message); }
  clearTimeout(timer);
  if (h.blocked) why = sentOn(h, h.blocked);
  if (!why && !alive(W)) why = 'The window closed.';
  if (why) {
    if (alive(W)) closeView(W, h.id);
    forget(h);
    store.extra.removed = [...removedIds(), h.id]; saveHives();
    log('connect refused ' + hostOf(a.url) + ': ' + why);
    return why;
  }
  h.pending = false;
  keep(W, h, '');
  return '';
}
// A Hive just connected to joins the list, and window W shows it.
function keep(W, h, note) {
  remotes.push(h);
  // Connected with no Hive on this computer (the welcome's "somewhere else"): nothing is installed, and the windows that
  // were offering to set one up go to this Hive. Setting one up later is on the Hives screen.
  const none = !hasLocal() || (!hiveUrl && !child && !lib.openCommand(process.platform, process.env));
  if (none) store.extra.noLocal = true;
  saveHives();
  log('connected ' + hostOf(h.saved.url) + note + (none ? ' (no Hive on this computer)' : ''));
  choose(W, h);
  if (none) for (const o of all()) if (o !== W && o.chosen === local) choose(o, h);
  // The other windows' rails gain the new Hive (and the rail shows, now that there are two).
  for (const o of all()) if (o !== W) { if (o.aside) rail(o); else display(o); }
}

// Sign-in gates (DESIGN-many-hives.md step 6). A Hive behind Cloudflare Access is signed in to in the person's own
// browser (gate.js). The token that comes back becomes the CF_Authorization cookie in that Hive's session only, and when
// it runs out the windows on that Hive ask for a sign-in again. The token itself is never logged or kept anywhere else.
// Words for a Hive whose page sends the app on to another address. The app never follows it, as that address isn't the
// Hive the person added; the person can add it themselves if it is theirs.
const sentOn = (h, at) => `${hostOf(h.saved.url)} sends the app on to ${at}, another address, so the app stopped there. If your Hive is at ${at}, connect to that instead: Hives…, then Connect to a Hive….`;
// A request through a Hive's own session (its name lookup, proxy and certificate rules) for gate.js. No cookies go with
// it, and a redirect is answered, not followed (Electron's own fetch refuses that).
function sessionFetch(h) {
  return (url, o = {}) => new Promise((res, rej) => {
    const q = net.request({ url, method: o.method || 'GET', session: sessionFor(h), redirect: 'manual', credentials: 'omit', useSessionCookies: false });
    for (const [k, v] of Object.entries(o.headers || {})) q.setHeader(k, v);
    const done = (status, headers, body) => res({ status, ok: status >= 200 && status < 300, text: async () => body, json: async () => JSON.parse(body), arrayBuffer: async () => Buffer.from(body),
      headers: { get: n => { const v = headers[String(n).toLowerCase()]; return v === undefined ? null : Array.isArray(v) ? v.join(', ') : String(v); } } });
    q.on('redirect', (status, _m, _to, headers) => { q.abort(); done(status, headers, ''); });
    q.on('response', r => {
      const bits = []; let n = 0;
      r.on('data', d => { n += d.length; if (n > 1 << 20) { q.abort(); rej(new Error('answer too long')); } else bits.push(d); });
      r.on('end', () => done(r.statusCode, r.headers, Buffer.concat(bits).toString('utf8'))); r.on('error', rej);
    });
    q.on('error', rej);
    q.end();
  });
}
const gateOrigin = h => new URL(h.saved.url).origin;
// True when the Hive's session holds a sign-in with time left; it sets the timer for when that runs out.
async function gateCookie(h) {
  let c = [];
  try { c = await sessionFor(h).cookies.get({ url: gateOrigin(h) + '/', name: 'CF_Authorization' }); } catch { }
  const exp = Math.max(0, ...c.map(x => x.expirationDate || 0));
  if (!(exp > Date.now() / 1000 + 10)) return false;
  runsOut(h, exp);
  return true;
}
function runsOut(h, exp) {
  clearTimeout(h.expiry);
  // (a timer can't wait past about 24 days; a longer sign-in is looked at again then)
  const ms = Math.min(exp * 1000 - Date.now(), 2 ** 31 - 1);
  h.expiry = setTimeout(() => { if (exp * 1000 - Date.now() > 1000) return runsOut(h, exp); log('sign-in to ' + hostOf(h.saved.url) + ' ran out'); needSignIn(h); }, Math.max(0, ms));
}
// Every window on Hive h shows its sign-in screen (its views go: without a sign-in they would show the gate's page).
function needSignIn(h) {
  if (!remotes.includes(h)) return;
  h.signin = true;
  // A sign-in the gate no longer takes goes too, so the next open doesn't try it again.
  sessionFor(h).cookies.remove(gateOrigin(h) + '/', 'CF_Authorization').catch(() => { });
  for (const W of all()) { closeView(W, h.id); if (W.chosen === h && !W.aside) display(W); else rail(W); }
}
// A Hive that sent its page somewhere else: if that is a Cloudflare Access gate, sign in to it; else say where it went.
async function gateOrFail(W, h, to) {
  const at = hostOf(to);
  // Only a gate's own sign-in page means the sign-in is missing or turned away. A gated address that, once signed in,
  // forwards to another one (h2.ihsan.cc forwards to dash.ihsan.cc) is not asked again: each sign-in would lead back here.
  let where = ''; try { where = new URL(to).pathname; } catch { }
  if (!where.startsWith('/cdn-cgi/access/')) return remoteFailed(W, h, sentOn(h, at));
  let g = null;
  try { g = await gate.probe(sessionFetch(h), h.saved.url); } catch (e) { return remoteFailed(W, h, e.message); }
  if (!g) return remoteFailed(W, h, sentOn(h, at));
  if (!h.saved.gate || h.saved.gate.aud !== g.aud || h.saved.gate.team !== g.team) { h.saved.gate = g; saveHives(); }
  needSignIn(h);
}
// Opens the sign-in in the browser and waits for it; once it is back the windows on h load the Hive. Asked again while
// waiting, it opens the same page again.
async function signIn(h) {
  if (h.gateRun) return void external(h.gateRun.url);
  const run = h.gateRun = { stop: new AbortController(), url: '' };
  h.why = '';
  const redo = () => { for (const W of all()) { if (W.chosen === h && !W.aside) display(W); else rail(W); } };
  try {
    const f = sessionFetch(h);
    // The gate is asked again first: its keys or its app may have changed since, and an address no longer gated just opens.
    const g = await gate.probe(f, h.saved.url);
    if (run.stop.signal.aborted) return;
    if (!g) { h.signin = false; h.gateRun = null; delete h.saved.gate; saveHives(); return void reopen(h); }
    if (JSON.stringify(g) !== JSON.stringify(h.saved.gate)) { h.saved.gate = g; saveHives(); }
    const a = gate.ask(h.saved.url, g);
    run.url = a.url;
    redo();
    log('sign-in to ' + hostOf(h.saved.url) + ' opened in the browser');
    if (process.env.HIVE_APP_TEST === '1' && process.env.HIVE_GATE_BROWSER) fs.writeFileSync(process.env.HIVE_GATE_BROWSER, a.url);   // the test plays the browser
    external(a.url);
    const got = await gate.wait(f, g, a, run.stop.signal);
    if (!got || run.stop.signal.aborted) return;
    const https = new URL(h.saved.url).protocol === 'https:';
    // Host-only (no domain), HttpOnly, Secure over https, and gone when the token runs out.
    await sessionFor(h).cookies.set({ url: gateOrigin(h) + '/', name: 'CF_Authorization', value: got.token, httpOnly: true, secure: https, sameSite: 'lax', expirationDate: got.exp });
    log('signed in to ' + hostOf(h.saved.url) + ' for ' + Math.round((got.exp - Date.now() / 1000) / 60) + ' min');
    h.signin = false; h.fresh = false; h.gateRun = null;
    runsOut(h, got.exp);
    reopen(h);
  } catch (e) {
    if (run.stop.signal.aborted) return;
    h.why = String(e.message || e).slice(0, 300);
    log('sign-in to ' + hostOf(h.saved.url) + ' stopped: ' + h.why);
  } finally {
    if (h.gateRun === run) { h.gateRun = null; redo(); }
  }
}
function stopSignIn(h) {
  if (!h.gateRun) return;
  h.gateRun.stop.abort(); h.gateRun = null;
  for (const W of all()) { if (W.chosen === h && !W.aside) display(W); else rail(W); }
}
// The windows on h load it again (after a sign-in, or when it turned out to have no gate after all).
function reopen(h) {
  for (const W of all()) {
    if (W.chosen !== h) { rail(W); continue; }
    closeView(W, h.id); openRemote(W, h);
    if (!W.aside) display(W); else rail(W);
  }
}
// What a Hive's session holds (its sign-in, its stored pages) goes when the Hive is removed; its folder goes at the next start.
function forget(h) {
  const s = session.fromPartition('persist:hive-' + h.id);
  for (const p of [s.clearStorageData(), s.clearCache(), s.clearAuthCache()]) Promise.resolve(p).catch(() => { });
}
function removeHive(id) {
  const h = remotes.find(r => r.id === id);
  if (!h) return;
  remotes = remotes.filter(r => r !== h);
  stopSignIn(h); clearTimeout(h.expiry);
  if (!remotes.length) delete store.extra.noLocal;   // with no Hive left, the window offers the choice again
  for (const w of [...h.popups]) try { w.close(); } catch { }
  for (const W of all()) {
    closeView(W, h.id);
    if (W.chosen === h) { W.chosen = home(); if (W.chosen === local) { if (hiveUrl && !W.views.has(local.id)) openLocal(W); } else if (!W.views.has(W.chosen.id)) openRemote(W, W.chosen); }
  }
  forget(h); iconTry.delete(h.id); setIcon(h.id, null);
  store.extra.removed = [...removedIds(), h.id];
  if (store.extra.last === h.id) store.extra.last = home().id;
  saveHives();
  log('removed ' + hostOf(h.saved.url));
  for (const W of all()) { if (W.aside) showAside(W, W.asideState); else display(W); }
  menu(); saveAll();
}
// A tile's own menu (a right click on the rail): open it in a new window; a remote one can be removed, once confirmed.
async function tileMenu(W, id) {
  const h = hiveById(id);
  if (!h || !alive(W)) return;
  const items = [{ label: 'Open in new window', click: () => create(h) }];
  if (h.kind === 'remote') items.push({ type: 'separator' }, { label: `Remove ${h.name}…`, click: async () => {
    const r = await dialog.showMessageBox(W.bw, { type: 'warning', buttons: ['Remove', 'Cancel'], defaultId: 1, cancelId: 1, message: `Remove ${h.name}?`,
      detail: 'The app forgets this Hive and its sign-in on this computer. The Hive itself is not changed: you can connect to it again with its address.' });
    if (r.response === 0) removeHive(h.id);
  } });
  Menu.buildFromTemplate(items).popup({ window: W.bw });
}

// The Hives menu: one entry per Hive (the front window's is checked), New Window, then Connect and Manage.
// Ctrl+1..9 reach the first nine; on Windows and Linux keys() catches them in the pages, so the menu only shows them.
// The same items open from the rail's Menu button (step 8c), for window W, with the sidebar's fold at the end.
function hivesItems(f) {
  const mac = process.platform === 'darwin';
  const act = fn => () => { const W = alive(f) ? f : front(); if (W) fn(W); else fn(create(home())); };
  const item = (h, i) => ({ id: 'hive-' + h.id, label: h.name.replace(/&/g, '&&'), type: 'radio', checked: !!f && h === f.chosen && !f.aside,
    ...(i < 9 ? { accelerator: 'CmdOrCtrl+' + (i + 1), registerAccelerator: mac } : {}), click: act(W => choose(W, h)) });
  return [...hives().map(item), { type: 'separator' },
    { id: 'hives-new-window', label: 'New Window', accelerator: 'CmdOrCtrl+Shift+N', registerAccelerator: mac, click: () => create((alive(f) ? f : front())?.chosen || local) },
    { type: 'separator' },
    { id: 'hives-connect', label: 'Connect to a Hive…', click: act(W => showAside(W, 'connect')) },
    { id: 'hives-manager', label: 'Manage Hives…', click: act(W => showAside(W, 'hives')) },
    ...(many() ? [{ type: 'separator' }, { id: 'hives-sidebar', label: 'Show Hives Sidebar', type: 'checkbox', checked: !folded(), click: () => fold(!folded()) }] : [])];
}
function menu() {
  const mac = process.platform === 'darwin';
  const list = { label: mac ? 'Hives' : '&Hives', submenu: hivesItems(front()) };
  // No View menu on the Mac: its Reload would skip the guards.
  Menu.setApplicationMenu(Menu.buildFromTemplate(mac ? [{ role: 'appMenu' }, { role: 'editMenu' }, list, { role: 'windowMenu' }] : [list]));
}
// The rail folded to a strip (it opens again on a click on the strip) or open, in every window; kept for the next start.
function fold(on) {
  if (folded() === !!on) return;
  if (on) store.extra.railFolded = true; else delete store.extra.railFolded;
  saveHives();
  for (const W of all()) { rail(W); layout(W); }
  menu();
}
// The Menu button's menu, under the button (x, y in the page's pixels); the tests read what it held.
function mainMenu(W, x, y) {
  if (!alive(W)) return;
  const items = hivesItems(W), z = W.bw.webContents.getZoomFactor();
  const m = Menu.buildFromTemplate(items);
  if (process.env.HIVE_APP_TEST === '1') globalThis.lastMainMenu = { ids: items.filter(i => i.id).map(i => i.id), menu: m };
  m.popup({ window: W.bw, x: Math.round(x * z), y: Math.round(y * z) });
}

// Every session is set up before anything loads in it (DESIGN-many-hives.md R1). sessionFor(hive) gives that Hive its own
// rules: the microphone (calls), notifications and the clipboard for its own pages only, and its files saved to Downloads.
// Any session it didn't set up (one Electron or a page makes on its own) refuses every permission and every download.
const PERMS = ['media', 'notifications', 'clipboard-read', 'clipboard-sanitized-write', 'fullscreen'];
const setUp = new WeakSet();
app.on('session-created', s => {
  s.setPermissionRequestHandler((_wc, _perm, cb) => cb(false));
  s.setPermissionCheckHandler(() => false);
  s.on('will-download', (_e, item) => { if (!setUp.has(s)) item.cancel(); });
});
function sessionFor(hive) {
  // The first local Hive keeps the default session, so today's people keep their sign-in and the page's stored state (R11).
  const s = hive.first ? session.defaultSession : session.fromPartition('persist:hive-' + hive.id);
  if (setUp.has(s)) return s;
  const ok = (wc, perm, origin) => {
    const at = hive.url(), u = origin || (wc && wc.getURL()) || '';
    return !!at && hive.same(u) && PERMS.includes(perm);
  };
  s.setPermissionRequestHandler((wc, perm, cb, d) => {
    const yes = ok(wc, perm, d && d.requestingUrl);
    if (yes && perm === 'media' && wc) heard.add(wc);   // a call: its view is kept when the window moves on (trim)
    cb(yes);
  });
  s.setPermissionCheckHandler((wc, perm, origin) => ok(wc, perm, origin));
  // A file the Hive page offers is saved to Downloads under its own name, with no dialog and nothing opened in the window.
  s.on('will-download', (_e, item) => {
    const at = hive.url();
    if (!at || !hive.same(item.getURL())) return item.cancel();
    const dir = (process.env.HIVE_APP_TEST && process.env.HIVE_APP_DOWNLOADS) || app.getPath('downloads');
    try { fs.mkdirSync(dir, { recursive: true }); } catch { }
    const dest = lib.downloadPath(dir, item.getFilename(), fs.existsSync);
    item.setSavePath(dest);
    log('download ' + path.basename(dest));
  });
  setUp.add(s);
  return s;
}
// A certificate the system doesn't trust is refused, with no "continue anyway" (R2), and no client certificate is ever
// sent (R3): Electron would otherwise pick the first one in the store by itself.
app.on('certificate-error', (_e, _wc, url, err, _cert, cb) => { log('certificate refused ' + lib.redact(String(url).split(/[?#]/)[0]) + ': ' + err); cb(false); });
app.on('select-client-certificate', (e, _wc, _url, _list, cb) => { e.preventDefault(); cb(); });

// A message from a window's own app page: that window, or nothing for any other sender.
const fromSetup = e => (e.senderFrame && isSetupUrl(e.senderFrame.url) && all().find(W => e.sender === W.bw.webContents)) || null;
ipcMain.on('install', (e, accept) => { if (fromSetup(e)) install(accept); });
ipcMain.on('retry', e => { if (fromSetup(e)) open(); });
ipcMain.on('copy', (e, text) => { if (fromSetup(e)) clipboard.writeText(lib.redact(text).slice(0, 200000) + '\n\nApp log (latest):\n' + lib.tailLog(logFile())); });
ipcMain.on('version', e => { e.returnValue = fromSetup(e) ? app.getVersion() : ''; });
ipcMain.on('download', e => { if (fromSetup(e)) external(lib.downloadPage('update')); });   // the setup screen's "newer app" link
// The Intel app on an Apple chip Mac runs under Rosetta: it works, but its first start is slower (96 s against 3 s on the
// runner), so the setup screen says the Apple chip download is there and links to the download page.
ipcMain.on('rosetta', e => { e.returnValue = !!fromSetup(e) && process.platform === 'darwin' && !!app.runningUnderARM64Translation; });
ipcMain.on('chip-download', e => { if (fromSetup(e)) external(lib.downloadPage()); });
// The composer bar's send (signed from the start, step 1): only from a window's own bar page, only to the local Hive it
// sits under, and only into the thread the bar showed when the owner pressed send.
ipcMain.handle('bar-send', async (e, text, thread) => {
  const W = e.senderFrame && isComposer(e.senderFrame.url) && all().find(x => x.bar && e.sender === x.bar.webContents);
  if (!W) return { ok: false, why: 'Not from the bar.' };
  if (typeof text !== 'string' || !text.trim() || text.length > lib.WORDS_MAX) return { ok: false, why: 'Nothing to send, or too long.' };
  const v = W.views.get(local.id);
  if (!barWanted(W) || !v) return { ok: false, why: "The Hive isn't open." };
  const now = v ? lib.threadOf(v.webContents.getURL()) : '';
  if (String(thread || '') !== now) { barThread(W); return { ok: false, why: 'The thread changed. Check it and send again.' }; }
  let body;
  try { body = lib.signPost(deviceKey(), { hive: store.extra.localHive, thread: now, words: text }); }
  catch (err) { log('bar sign: ' + err.message); return { ok: false, why: "Couldn't sign it." }; }
  let url; try { url = new URL(hiveUrl).origin + '/api/post'; } catch { return { ok: false, why: "The Hive's address is wrong." }; }
  const r = await postSigned(local, url, body);
  log('bar post ' + (r.ok ? 'sent' : 'refused: ' + r.why) + (now ? ' to thread ' + now : ' to a new thread'));
  return r;
});
// The setup screen asks once its listener is up, so a notice that arrived while the page was loading isn't lost.
ipcMain.on('newer?', e => { if (fromSetup(e) && newer) e.sender.send('newer', newer); });
// Close (Not now, Don't install) closes this window; with it the last, the app.
ipcMain.on('quit', e => { const W = fromSetup(e); if (!W) return; if (all().length > 1) W.bw.close(); else app.quit(); });
// Many Hives (steps 3 and 4): connect by address, open, remove, the rail, and the app's own screens for them.
const hiveById = id => hives().find(h => h.id === String(id));
ipcMain.on('connect', (e, text) => { const W = fromSetup(e); if (W) connect(W, text); });
ipcMain.on('open-hive', (e, id) => { const W = fromSetup(e), h = W && hiveById(id); if (h) choose(W, h); });
ipcMain.on('rail-pick', (e, id) => { const W = fromSetup(e), h = W && hiveById(id); if (h) choose(W, h); });
ipcMain.on('tile-menu', (e, id) => { const W = fromSetup(e); if (W) tileMenu(W, String(id)); });
ipcMain.on('main-menu', (e, x, y) => { const W = fromSetup(e); if (W) mainMenu(W, Math.max(0, Number(x) || 0), Math.max(0, Number(y) || 0)); });
ipcMain.on('rail-fold', (e, on) => { if (fromSetup(e)) fold(!!on); });
ipcMain.on('new-window', e => { const W = fromSetup(e); if (W) create(W.chosen); });
ipcMain.on('remove-hive', (e, id) => { if (fromSetup(e)) removeHive(String(id)); });
ipcMain.on('browser-hive', e => { const W = fromSetup(e); if (W && W.chosen.kind === 'remote') external(W.chosen.saved.url); });
ipcMain.on('hives', e => { const W = fromSetup(e); if (W) showAside(W, 'hives'); });
ipcMain.on('connect-screen', e => { const W = fromSetup(e); if (W) showAside(W, 'connect'); });
ipcMain.on('back', e => { const W = fromSetup(e); if (W) { W.aside = false; display(W); menu(); } });
ipcMain.on('retry-hive', e => { const W = fromSetup(e); if (W) choose(W, W.chosen); });
// A gated Hive's sign-in screen (step 6): sign in in the browser (again, while waiting, opens the page again), or stop waiting.
ipcMain.on('sign-in', e => { const W = fromSetup(e); if (W && W.chosen.kind === 'remote' && W.chosen.signin) signIn(W.chosen); });
ipcMain.on('cancel-sign-in', e => { const W = fromSetup(e); if (W && W.chosen.kind === 'remote') stopSignIn(W.chosen); });
// Set up a Hive on this computer after all (the Hives screen): the local Hive is back in the list and this window shows its setup.
ipcMain.on('setup-local', e => {
  const W = fromSetup(e); if (!W) return;
  delete store.extra.noLocal; saveHives();
  log('set up a Hive on this computer');
  choose(W, local);
  for (const o of all()) if (o !== W) { if (o.aside) rail(o); else display(o); }
});

// Windows ties notifications (and their clicks) to the Start menu shortcut by this ID; it must match the installer's appId.
if (process.platform === 'win32') app.setAppUserModelId('cc.ihsan.hive');
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => {
    const W = front();
    if (!W) return void create(home());
    if (W.bw.isMinimized()) W.bw.restore();
    W.bw.focus();
  });
  app.whenReady().then(() => {
    loadHives(); loadIcons(); sessionFor(local);
    // The windows open as they were left, each on its Hive; a window saved before there were many shows the Hive shown last.
    const lastRemote = remotes.find(h => h.id === store.extra.last);
    for (const s of savedWindows()) create(s.hive ? hiveById(s.hive) : lastRemote || home(), s.rect);
    started = true; saveAll();
    open(); checkNewer();
    app.on('activate', () => { if (!all().length) create(home()); });
  });
  // Closing the last window closes the app. The Hive itself keeps running in the background, as it does without the app.
  app.on('window-all-closed', () => app.quit());
  app.on('before-quit', () => {
    quitting = true; saveAll();
    if (child) { try { process.platform === 'win32' ? child.kill() : process.kill(-child.pid); } catch { } }
  });
}
