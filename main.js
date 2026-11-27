// The Hive as a desktop app. A thin shell: it finds the Hive on this machine (or installs it with the site's own installer),
// asks it for its address, and shows the web app in a window. All the product is the web app, so it has one version on every system.
'use strict';
const { app, BrowserWindow, WebContentsView, ipcMain, shell, session, Menu, screen, clipboard } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const lib = require('./lib');

let win = null, hiveUrl = null, child = null, kept = false, fails = 0, newer = null, shown = null;   // shown: the update notification, kept so its click still arrives
// The Hives this window can show (DESIGN-many-hives.md). The local one is found as before; its rules read its address as it is
// at the moment of asking. A remote one is one the person connected to by its address, kept in hives.json (step 3).
const local = { id: 'local', kind: 'local', first: true, name: 'This computer', url: () => hiveUrl, same: u => !!hiveUrl && lib.sameApp(u, hiveUrl), view: null, popups: new Set() };
let remotes = [], hivesFile = null, chosen = local, aside = false, localState = ['starting', ''];
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

// The local Hive's own screen (setup, starting, didn't start). It shows when the window is on the local Hive.
async function showSetup(state, note) {
  log('screen ' + state + (note ? ': ' + note : ''));
  localState = [state, note || ''];
  closeView(local);
  if (chosen === local && !aside) display();
}
function render(state, extra) {
  send('state', state, { platform: process.platform, note: '', ...extra });
  if (newer) send('newer', newer);
}

// What the window shows: the chosen Hive's view over the app's own page, or the app's page (setup, Hives, Connect, can't reach).
function display() {
  if (!win || win.isDestroyed()) return;
  const h = chosen, v = !aside && h.view && !h.pending ? h.view : null;
  for (const c of win.contentView.children) if (c !== v) win.contentView.removeChildView(c);
  if (v) {
    if (!win.contentView.children.includes(v)) win.contentView.addChildView(v);
    layout();
    const t = v.webContents.getTitle();
    win.setTitle(t && !/^https?:|^about:/.test(t) ? t : h.name);
    v.webContents.focus();
    return;
  }
  win.setTitle('Hive');
  if (aside) return;
  if (h === local) render(localState[0], { note: localState[1] });
  else render('cantreach', { name: h.name, host: hostOf(h.saved.url), why: h.why || '' });
  win.webContents.focus();
}
function layout() {
  if (!win || win.isDestroyed()) return;
  const [width, height] = win.getContentSize();
  for (const c of win.contentView.children) c.setBounds({ x: 0, y: 0, width, height });
}
const hostOf = u => { try { return new URL(u).host; } catch { return ''; } };
function closeView(h) {
  const v = h.view;
  if (!v) return;
  h.view = null;
  if (win && !win.isDestroyed()) try { win.contentView.removeChildView(v); } catch { }
  setImmediate(() => { try { if (!v.webContents.isDestroyed()) v.webContents.close(); } catch { } });
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
  closeView(local);
  const v = viewFor(local);
  display();
  try { await v.webContents.loadURL(url); fails = 0; if (chosen === local) display(); }
  catch (e) { if (local.view !== v) return; hiveUrl = null; return showSetup('nostart', sorry); }
}

// While the Hive is showing, ask again now and then: if another program has taken its port (the Hive restarted under the window),
// the window leaves it for the setup screen instead of sending the sign-in cookie on to it.
let watcher = null;
function watch() {
  clearInterval(watcher);
  watcher = setInterval(async () => {
    if (!win || !hiveUrl || !local.view) return;
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
  localState = [what, ''];
  if (chosen === local && !aside) send('state', what, { platform: process.platform, note: '' });
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

const PREFS = { contextIsolation: true, nodeIntegration: false, sandbox: true, devTools: !app.isPackaged };
const bg = () => require('electron').nativeTheme.shouldUseDarkColors ? '#111111' : '#ffffff';
function create() {
  const was = savedState();
  win = new BrowserWindow({
    width: 1280, height: 860, ...(was ? { width: was.width, height: was.height, ...('x' in was ? { x: was.x, y: was.y } : {}) } : {}), minWidth: 380, minHeight: 500, title: 'Hive', backgroundColor: bg(),
    // Windows and Linux show the menu bar, since its Hives menu is how you move between Hives (DESIGN-many-hives.md, step 3).
    autoHideMenuBar: false,
    icon: path.join(__dirname, 'build', 'icon.png'),
    // The app's own page has a session of its own, in memory: it shares nothing with any Hive.
    webPreferences: { preload: path.join(__dirname, 'preload.js'), partition: 'app', ...PREFS },
  });
  if (was && was.maximized) win.maximize();
  let tick = null;
  const later = () => { clearTimeout(tick); tick = setTimeout(() => saveState(win), 500); };
  win.on('resize', later); win.on('move', later);
  for (const ev of ['resize', 'maximize', 'unmaximize', 'enter-full-screen', 'leave-full-screen']) win.on(ev, layout);
  win.on('page-title-updated', e => e.preventDefault());   // the title is the Hive's (display sets it)
  win.on('close', () => { clearTimeout(tick); saveState(win); });
  win.on('closed', () => { win = null; });
  // The app's own page only ever shows itself: every link from it goes to the browser.
  const wc = win.webContents;
  wc.setWindowOpenHandler(({ url }) => { external(url); return { action: 'deny' }; });
  const own = (e, url) => { if (!isSetupUrl(e.url || url)) e.preventDefault(); };
  wc.on('will-navigate', own); wc.on('will-redirect', own);
  menuFor(wc); keys(wc);
  if (chosen.kind === 'remote') openRemote(chosen);
  menu();
  win.loadFile(setupFile).then(() => { display(); open(); }, () => open());
}

// One Hive's page: a view of its own, in its own session, held while the window shows another Hive so switching back is instant.
function viewFor(h) {
  const v = new WebContentsView({ webPreferences: { session: sessionFor(h), ...PREFS } });
  v.setBackgroundColor(bg());
  h.view = v;
  const wc = v.webContents;
  guard(wc, h); keys(wc); menuFor(wc);
  wc.on('page-title-updated', (_e, t) => { if (win && !win.isDestroyed() && chosen === h && !aside && h.view === v) win.setTitle(t || h.name); });
  wc.on('did-fail-load', (_e, code, desc, url, main) => {
    if (!main || code === -3 || h.view !== v) return;
    if (h === local) {
      // The Hive stopped under the window (an update, a restart): go and find it again rather than show a blank page.
      if (!(hiveUrl && lib.isLocal(url))) return;
      hiveUrl = null;
      // A few tries, then the screen that says so (with Try again and Repair), not a loop.
      if (++fails > 3) { fails = 0; showSetup('nostart', "The Hive's app stopped answering."); } else setTimeout(open, 1500);
      return;
    }
    if (!h.pending) remoteFailed(h, failWords(code, desc));
  });
  return v;
}
function openRemote(h) {
  h.down = false; h.why = '';
  viewFor(h).webContents.loadURL(h.saved.url).catch(() => { });   // a failure arrives as did-fail-load
}
function remoteFailed(h, why) {
  h.down = true; h.why = why;
  log('cannot reach ' + hostOf(h.saved.url) + ': ' + why);
  closeView(h);
  if (chosen === h && !aside) display();
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
// page opens, and for where a server sends either of them on. Each guard is bound to its own Hive, not to whichever
// Hive the window shows now (DESIGN-many-hives.md R12).
function guard(wc, h) {
  wc.setWindowOpenHandler(({ url }) => {
    // No session here: the new window keeps the opener's, so it is signed in to the same Hive and no other.
    if (h.same(url)) return { action: 'allow', overrideBrowserWindowOptions: { autoHideMenuBar: true, backgroundColor: bg(), webPreferences: { ...PREFS } } };
    external(url);
    return { action: 'deny' };
  });
  wc.on('will-navigate', (e, url) => { if (h.same(e.url || url)) return; e.preventDefault(); external(e.url || url); });
  wc.on('will-redirect', (e, url, _inPlace, isMain) => {
    const u = e.url || url;
    if (h.same(u)) return;
    e.preventDefault();
    // A remote Hive behind a sign-in gate sends its page to the gate. That isn't the Hive, so the view doesn't follow
    // (the gate sign-in is step 6): the window says so instead of showing a blank page.
    if (h.kind === 'remote' && (e.isMainFrame ?? isMain) && h.view && wc === h.view.webContents) {
      const at = hostOf(u);
      if (h.pending) h.blocked = at; else remoteFailed(h, `It sent the app to ${at} to sign in, which the app can't do yet.`);
      return;
    }
    external(u);
  });
  wc.on('did-create-window', w => {
    h.popups.add(w); w.on('closed', () => h.popups.delete(w));
    if (process.platform !== 'darwin') w.removeMenu();
    guard(w.webContents, h); keys(w.webContents); menuFor(w.webContents);
  });
}
// Windows and Linux have no View menu, so the keys one would give: zoom, and reload (a Hive's page, not the app's own).
function keys(wc) {
  if (process.platform === 'darwin') return;
  wc.on('before-input-event', (e, input) => {
    const a = lib.shortcut(input);
    if (!a) return;
    if (a === 'reload') { if (!(win && !win.isDestroyed() && wc === win.webContents)) { e.preventDefault(); wc.reload(); } return; }
    e.preventDefault();
    wc.setZoomLevel(a === 'zoomReset' ? 0 : Math.max(-3, Math.min(5, wc.getZoomLevel() + (a === 'zoomIn' ? 0.5 : -0.5))));
  });
}
function menuFor(wc) {
  wc.on('context-menu', (_e, p) => {
    const items = lib.contextItems(p).map(k => k === 'copyLink' ? { label: 'Copy link', click: () => clipboard.writeText(p.linkURL) } : { role: k });
    if (items.length) Menu.buildFromTemplate(items).popup({ window: BrowserWindow.fromWebContents(wc) || win });
  });
}

// The remote Hives, kept in hives.json in the app's own data folder: each one's id, name and address, never its key
// (the key opens the Hive once; after that the sign-in is the cookie in its own session).
let store = lib.parseHives(null), connecting = false;
const partitionDir = id => path.join(app.getPath('userData'), 'Partitions', 'hive-' + id);
const removedIds = () => Array.isArray(store.extra.removed) ? store.extra.removed.filter(id => /^[a-f0-9]{16}$/.test(id)) : [];
function remoteFrom(s) {
  return { id: s.id, kind: 'remote', saved: s, name: s.name, url: () => s.url, same: u => lib.sameHive(u, s.url), view: null, popups: new Set(), down: false, why: '' };
}
function loadHives() {
  hivesFile = path.join(app.getPath('userData'), 'hives.json');
  let text = null;
  try { text = fs.readFileSync(hivesFile, 'utf8'); } catch (e) { if (e.code !== 'ENOENT') text = ''; }
  store = lib.parseHives(text);
  if (store.error) log('hives.json: ' + store.error);
  remotes = store.hives.filter(s => s.kind === 'remote').map(remoteFrom);
  chosen = remotes.find(h => h.id === store.extra.last) || local;
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

// The window shows this Hive.
function choose(h) {
  chosen = h; aside = false;
  if (h.kind === 'remote' && !h.view) openRemote(h);
  display();
  if (store.extra.last !== h.id) { store.extra.last = h.id; saveHives(); }
  menu();
}
// The app's own Hives and Connect screens, over whichever Hive is chosen (Back returns to it).
function showAside(state) {
  aside = true;
  display();
  render(state, state === 'hives' ? { list: [local, ...remotes].map(h => ({ id: h.id, name: h.name, current: h === chosen,
    where: h === local ? (hiveUrl ? 'On this computer' : 'On this computer, not running') : hostOf(h.saved.url) + (h.down ? ' · not answering' : ''), remote: h.kind === 'remote' })) } : {});
  menu();
}

async function connect(text) {
  if (connecting) return;
  connecting = true;
  try { const why = await connectNow(String(text || '').trim().slice(0, 2000)); if (why) send('connect-error', why); }
  catch (e) { send('connect-error', "The app couldn't connect: " + String(e.message || e).slice(0, 200)); }
  finally { connecting = false; }
}
async function connectNow(t) {
  if (!t) return "Type the Hive's address.";
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(t)) t = (/^(localhost|127\.|\[::1\])/i.test(t) ? 'http://' : 'https://') + t;
  t = lib.pin(t);
  const a = lib.addressRule(t);
  if (!a.ok) return a.why;
  if (hiveUrl && lib.sameHive(a.url, hiveUrl)) { choose(local); return ''; }
  const have = remotes.find(h => lib.sameHive(a.url, h.saved.url));
  if (have) { choose(have); return ''; }
  const full = new URL(t); full.search = '';   // the address as typed, #key and all: it is loaded once, never stored
  // Over plain http (this computer only) the server must first show it holds the key; over https the certificate is the proof.
  if (full.protocol === 'http:' && !await proves(full.href)) return "What answers at that address didn't show it is the Hive with that key, so the app sent it nothing.";
  const s = { id: lib.newId([...remotes.map(h => h.id), ...store.hives.map(x => x.id), ...removedIds()]), kind: 'remote', url: a.url, name: new URL(a.url).hostname, order: remotes.length + 1 };
  const h = remoteFrom(s);
  h.pending = true;
  const v = viewFor(h);
  let why = '', timer;
  try {
    await Promise.race([v.webContents.loadURL(full.href), new Promise((_r, no) => { timer = setTimeout(() => no(new Error('ERR_TIMED_OUT')), 30000); })]);
  } catch (e) { why = failWords(0, e.code || e.message); }
  clearTimeout(timer);
  if (h.blocked) why = `It sent the app to ${h.blocked} to sign in, which the app can't do yet.`;
  if (why) {
    closeView(h); forget(h);
    store.extra.removed = [...removedIds(), h.id]; saveHives();
    log('connect refused ' + hostOf(a.url) + ': ' + why);
    return why;
  }
  h.pending = false;
  remotes.push(h);
  saveHives();
  log('connected ' + hostOf(a.url));
  choose(h);
  return '';
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
  for (const w of [...h.popups]) try { w.close(); } catch { }
  closeView(h); forget(h);
  if (chosen === h) chosen = local;
  store.extra.removed = [...removedIds(), h.id];
  if (store.extra.last === h.id) store.extra.last = local.id;
  saveHives();
  log('removed ' + hostOf(h.saved.url));
  if (aside) showAside('hives'); else display();
  menu();
}

// The Hives menu: one entry per Hive (the one showing is checked), then Connect and Manage.
function menu() {
  const item = h => ({ id: 'hive-' + h.id, label: h.name.replace(/&/g, '&&'), type: 'radio', checked: h === chosen && !aside, click: () => choose(h) });
  const hives = { label: process.platform === 'darwin' ? 'Hives' : '&Hives', submenu: [item(local), ...remotes.map(item), { type: 'separator' },
    { id: 'hives-connect', label: 'Connect to a Hive…', click: () => showAside('connect') },
    { id: 'hives-manager', label: 'Manage Hives…', click: () => showAside('hives') }] };
  // No View menu on the Mac: its Reload would skip the guards.
  Menu.setApplicationMenu(Menu.buildFromTemplate(process.platform === 'darwin' ? [{ role: 'appMenu' }, { role: 'editMenu' }, hives, { role: 'windowMenu' }] : [hives]));
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
  s.setPermissionRequestHandler((wc, perm, cb, d) => cb(ok(wc, perm, d && d.requestingUrl)));
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

const fromSetup = e => e.senderFrame && isSetupUrl(e.senderFrame.url) && win && e.sender === win.webContents;
ipcMain.on('install', (e, accept) => { if (fromSetup(e)) install(accept); });
ipcMain.on('retry', e => { if (fromSetup(e)) open(); });
ipcMain.on('copy', (e, text) => { if (fromSetup(e)) clipboard.writeText(lib.redact(text).slice(0, 200000) + '\n\nApp log (latest):\n' + lib.tailLog(logFile())); });
ipcMain.on('version', e => { e.returnValue = fromSetup(e) ? app.getVersion() : ''; });
ipcMain.on('download', e => { if (fromSetup(e)) external(lib.downloadPage('update')); });   // the setup screen's "newer app" link
// The Intel app on an Apple chip Mac runs under Rosetta: it works, but its first start is slower (96 s against 3 s on the
// runner), so the setup screen says the Apple chip download is there and links to the download page.
ipcMain.on('rosetta', e => { e.returnValue = fromSetup(e) && process.platform === 'darwin' && !!app.runningUnderARM64Translation; });
ipcMain.on('chip-download', e => { if (fromSetup(e)) external(lib.downloadPage()); });
// The setup screen asks once its listener is up, so a notice that arrived while the page was loading isn't lost.
ipcMain.on('newer?', e => { if (fromSetup(e) && newer) e.sender.send('newer', newer); });
ipcMain.on('quit', e => { if (fromSetup(e)) app.quit(); });
// Many Hives (step 3): connect by address, open, remove, and the app's own screens for them.
const hiveById = id => id === local.id ? local : remotes.find(h => h.id === id);
ipcMain.on('connect', (e, text) => { if (fromSetup(e)) connect(text); });
ipcMain.on('open-hive', (e, id) => { const h = fromSetup(e) && hiveById(String(id)); if (h) choose(h); });
ipcMain.on('remove-hive', (e, id) => { if (fromSetup(e)) removeHive(String(id)); });
ipcMain.on('browser-hive', e => { if (fromSetup(e) && chosen.kind === 'remote') external(chosen.saved.url); });
ipcMain.on('hives', e => { if (fromSetup(e)) showAside('hives'); });
ipcMain.on('connect-screen', e => { if (fromSetup(e)) showAside('connect'); });
ipcMain.on('back', e => { if (fromSetup(e)) { aside = false; display(); menu(); } });
ipcMain.on('retry-hive', e => { if (fromSetup(e)) choose(chosen); });

// Windows ties notifications (and their clicks) to the Start menu shortcut by this ID; it must match the installer's appId.
if (process.platform === 'win32') app.setAppUserModelId('cc.ihsan.hive');
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => { if (win) { if (win.isMinimized()) win.restore(); win.focus(); } });
  app.whenReady().then(() => {
    loadHives(); sessionFor(local); create(); checkNewer();
    app.on('activate', () => { if (!win) create(); });
  });
  // Closing the window closes the app. The Hive itself keeps running in the background, as it does without the app.
  app.on('window-all-closed', () => app.quit());
  app.on('before-quit', () => { if (child) { try { process.platform === 'win32' ? child.kill() : process.kill(-child.pid); } catch { } } });
}
