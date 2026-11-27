// What differs between the three systems, kept apart from Electron so it can be tested with plain node.
// The app holds no Hive logic of its own: it runs the same installers as the one-line install, and asks the installed
// Hive for its address with `hive open --print`. So a new Hive version needs no new app.
'use strict';
const path = require('path');
const fs = require('fs');

const SITE = process.env.HIVE_SITE || 'https://hivelab.ca';

// The command that starts the Hive if it's stopped and prints its sign-in address. null: no Hive found on this machine.
function openCommand(platform, env, exists = fs.existsSync, read = p => fs.readFileSync(p, 'utf8')) {
  if (platform === 'win32') {
    // The installer leaves hive.cmd here once the Hive system exists.
    const cmd = path.win32.join(env.LOCALAPPDATA || '', 'Hive', 'bin', 'hive.cmd');
    if (!env.LOCALAPPDATA || !exists(cmd)) return null;
    // WSL stops a system with nothing attached, so the installer's own icon starts a hidden process that keeps it up; so does the app.
    const keep = { file: path.win32.join(env.windir || env.SystemRoot || 'C:\\Windows', 'System32', 'conhost.exe'), args: ['--headless', 'wsl.exe', '-d', 'Hive', '-u', 'hive', '--exec', 'sleep', 'infinity'] };
    return { file: 'wsl.exe', args: ['-d', 'Hive', '-u', 'hive', '--cd', '~', '--', 'bash', '-lc', 'hive open --print'], env: { WSL_UTF8: '1' }, keep };
  }
  const home = env.HOME || '';
  if (platform === 'darwin') {
    const h = path.posix.join(home, '.hive', 'bin', 'hive');
    // Not `open`: the Mac command's `open` shows the address in the browser and prints nothing. `web` starts the VM and prints it.
    return exists(h) ? { file: h, args: ['web'] } : null;
  }
  // Linux, in this account: the Hive's own home is ~/hive (or HIVE_DIR), with its command inside.
  const dir = env.HIVE_DIR || path.posix.join(home, 'hive');
  const own = path.posix.join(dir, '.local', 'bin', 'hive');
  if (exists(path.posix.join(dir, '.hive-installed')) && exists(own)) {
    // The same surroundings the installer gives the Hive's commands (its in_hive): its own home, its own config, its command first.
    return {
      file: own, args: ['open', '--print'],
      env: { HOME: dir, PATH: path.posix.join(dir, '.local', 'bin') + ':' + (env.PATH || '/usr/local/bin:/usr/bin:/bin'), ...(env.XDG_RUNTIME_DIR ? {} : { XDG_RUNTIME_DIR: '/run/user/' + uid() }) },
      unset: ['XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME', 'CLAUDE_CONFIG_DIR'],
    };
  }
  // Linux, installed earlier under a user of its own: that account can't be asked without sudo, but its launcher holds the address.
  const desk = path.posix.join(env.XDG_DATA_HOME || path.posix.join(home, '.local', 'share'), 'applications', 'hive.desktop');
  if (exists(desk)) {
    let m = null;
    try { m = /^Exec=xdg-open (http:\/\/localhost:\d+\/)\s*$/m.exec(read(desk)); } catch { }
    if (m) return { url: m[1] };
  }
  return null;
}

// The installer for this system: the same script the site's one line runs. accept: the person pressed "Accept and install".
function installCommand(platform, env, accept) {
  const add = { HIVE_REPORT: env.HIVE_REPORT || '0', HIVE_FROM_APP: '1' };
  if (accept) add.HIVE_ACCEPT = '1';
  if (platform === 'win32') {
    const ps = path.win32.join(env.windir || env.SystemRoot || 'C:\\Windows', 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
    // On an ARM PC this app runs translated, and Windows tells a translated program (and what it starts) that the chip is an Intel one.
    // The installer picks the Hive's Linux system by that, so it is told the machine's own answer, which Windows keeps here.
    const chip = "$a = (Get-ItemProperty 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Environment' -ErrorAction SilentlyContinue).PROCESSOR_ARCHITECTURE; if ($a) { $env:PROCESSOR_ARCHITECTURE = $a }; ";
    return { file: ps, args: ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', chip + `try { $s = irm ${SITE}/install.ps1 } catch { Write-Host "Couldn't reach ${SITE}. Check the internet connection, then try again."; exit 1 }; iex $s`], env: add,
      // Started from PowerShell 7, the app would hand Windows' own PowerShell the newer one's module folders, and it then can't find its own commands (Get-FileHash).
      unset: ['PSModulePath'] };
  }
  // To a file first: a failed curl piped into sh looks like success.
  const get = name => `f=$(mktemp) && curl -fsSL ${SITE}/${name} -o "$f" || { echo "Couldn't reach ${SITE}. Check the internet connection, then try again."; exit 1; }; `;
  if (platform === 'darwin') {
    // The Mac install is still a test build, and says so unless told the person knows. The app's first screen says it.
    return { file: '/bin/sh', args: ['-c', diskCheck() + get('install-mac.sh') + 'sh "$f"; c=$?; rm -f "$f"; exit $c'], env: { ...add, HIVE_MAC_TEST: '1' } };
  }
  // Linux: in this account, which needs no password. The launcher icon is the app itself, so the installer's own is skipped.
  // The installer can't add what it needs (no sudo), and a missing curl would read as "no internet": say what to do instead.
  const need = 'm=""; for t in curl git python3; do command -v $t >/dev/null 2>&1 || m="$m $t"; done; [ -z "$m" ] || { echo "The Hive needs these first:$m"; echo "Open a terminal and run:  sudo apt install$m"; echo "(on Fedora: sudo dnf install$m). Then press Try again."; exit 1; }; ';
  return { file: '/bin/sh', args: ['-c', need + get('install.sh') + 'sh "$f" --this-account; c=$?; rm -f "$f"; exit $c'], env: { ...add, HIVE_NO_LAUNCHER: '1' } };
}

// The Mac install needs about 15 GB free in the home folder; said in plain words before anything is downloaded.
// HIVE_MIN_FREE_GB changes the number (the tests use it).
function diskCheck() {
  return 'need=${HIVE_MIN_FREE_GB:-15}; free=$(df -Pk "$HOME" 2>/dev/null | awk \'NR==2{print int($4/1048576)}\'); '
    + 'if [ ! -d "$HOME/.hive/lima/hive" ] && [ -n "$free" ] && [ "$free" -lt "$need" ]; then echo "There is not enough free disk space for the Hive: it needs about $need GB and this Mac has $free GB free."; echo "Free up some space (empty the Bin, remove large files), then press Try again."; exit 1; fi; ';
}

// What the installer's output and exit code mean for the screen.
//   done: installed. accept: it showed its warning and wants a yes. restart: Windows must restart first. failed: anything else.
function installResult(code, text, platform) {
  // Windows' installer ends with code 0 whatever happened, so the words decide.
  if (/HIVE_ACCEPT=1/.test(text) && /nothing changed/i.test(text)) return 'accept';
  if (/Restart Windows, then/i.test(text)) return 'restart';
  // ... and on Windows only its last line says it got to the end (a repair that stopped must not read as done).
  if (platform === 'win32') return /The Hive is installed/.test(text) ? 'done' : 'failed';
  if (code === 0) return 'done';
  return 'failed';
}

// The first http address in what `hive open --print` said, and only a local one: the window must never be sent elsewhere.
function localUrl(text) {
  for (const line of String(text).split(/\r?\n/)) {
    const l = line.trim();
    if (isLocal(l)) return l;
  }
  return null;
}
// "localhost" can mean two addresses, and the Hive listens on one of them (127.0.0.1). The window would try the other (::1) first,
// where another account's program could sit and pass the proof question on to the real Hive. So the app only ever uses the one the Hive holds.
function pin(u) {
  try { const x = new URL(u); if (x.hostname === 'localhost' || x.hostname === '[::1]') x.hostname = '127.0.0.1'; return x.href; } catch { return u; }
}
function isLocal(u) {
  try { const x = new URL(u); return x.protocol === 'http:' && (x.hostname === 'localhost' || x.hostname === '127.0.0.1'); } catch { return false; }
}
// The address carries the key after its #. Before the window is sent there, whatever answers on that port has to show it
// already holds the key: it is asked for a mark only the key can make, for a question made up this moment and for this port
// (so an answer fetched from a Hive on another port doesn't fit). Another program on the port can't make it, and is never sent the key.
function proofAsk(url, nonce) {
  const u = new URL(url), key = u.hash.slice(1);
  if (!key) return null;   // no key in the address: nothing to guard
  return { ask: `${u.origin}/api/proof?n=${nonce}`, want: require('crypto').createHmac('sha256', key).update(`proof:${u.port}:${nonce}`).digest('hex') };
}
function proofOk(want, got) {
  const a = Buffer.from(String(want)), b = Buffer.from(String(got).trim());
  return a.length === b.length && require('crypto').timingSafeEqual(a, b);
}
// Same app (same host and port) as the Hive's address: such links stay in the window, all others go to the browser.
function sameApp(u, base) {
  try { return isLocal(u) && new URL(u).port === new URL(base).port; } catch { return false; }
}
// The address is the key to the Hive, so it never goes on the screen or in a log.
function redact(text) {
  return String(text)
    .replace(/(https?:\/\/[^\s\/#]+\/login\/?#)\S+/g, '$1…')
    .replace(/((?:key|token|secret|password|authorization)["']?\s*[:=]\s*(?:bearer\s+)?["']?)[A-Za-z0-9_\-.~+\/]{8,}=*/gi, '$1…');
}
// Installers draw progress with carriage returns and colour codes; the screen wants plain lines.
// A line redrawn in place (a download's progress bar) keeps only its last drawing.
function plain(text) { return String(text).replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '').replace(/[^\n\r]*\r(?!\n)/g, ''); }
function uid() { try { return require('os').userInfo().uid; } catch { return 1000; } }

// Windows and Linux have no View menu: these keys do what it would, and the Hives menu's keys. input: Electron's before-input-event data.
function shortcut(i) {
  if (!i || i.type !== 'keyDown' || i.alt) return null;
  if (i.key === 'F5') return 'reload';
  if (!(i.control || i.meta)) return null;
  const k = String(i.key).toLowerCase();
  // The Hives menu's keys (a page's keys don't reach the menu bar): Ctrl+1..9 shows a Hive, Ctrl+Shift+N opens a window.
  if (/^[1-9]$/.test(k) && !i.shift) return 'hive:' + k;
  if (k === 'n' && i.shift) return 'newWindow';
  if (k === 'r') return 'reload';
  if (k === '=' || k === '+') return 'zoomIn';
  if (k === '-' || k === '_') return 'zoomOut';
  if (k === '0') return 'zoomReset';
  return null;
}

// A saved window rectangle, if it is sane and still on a screen (a monitor may have been unplugged since); otherwise null.
function windowState(saved, displays) {
  if (!saved || typeof saved !== 'object') return null;
  const n = k => Number.isFinite(saved[k]) ? Math.round(saved[k]) : null;
  const w = n('width'), h = n('height'), x = n('x'), y = n('y');
  if (w === null || h === null || w < 380 || h < 500 || w > 10000 || h > 10000) return null;
  const out = { width: w, height: h };
  if (x !== null && y !== null) {
    // At least a 100 x 50 piece of it must lie on some screen, or the person couldn't reach it.
    const on = (displays || []).some(d => Math.min(x + w, d.x + d.width) - Math.max(x, d.x) >= 100 && Math.min(y + h, d.y + d.height) - Math.max(y, d.y) >= 50);
    if (on) { out.x = x; out.y = y; }
  }
  if (saved.maximized === true) out.maximized = true;
  return out;
}

// The app's own log (in its data folder): redacted lines, one file kept as .1 once it passes max bytes.
function appendLog(file, text, max = 1000000) {
  const fs = require('fs');
  try {
    try { if (fs.statSync(file).size > max) fs.renameSync(file, file + '.1'); } catch { }
    fs.appendFileSync(file, new Date().toISOString() + ' ' + redact(String(text)).replace(/\s*\n\s*/g, ' | ').slice(0, 4000) + '\n');
  } catch { }
}
function tailLog(file, bytes = 20000) {
  try { const b = require('fs').readFileSync(file, 'utf8'); return b.slice(-bytes); } catch { return ''; }
}

// Where a download goes: the folder, the file's own name (no path parts, nothing hidden), and "name (1).ext" if that exists.
function downloadPath(dir, name, exists) {
  const path = require('path');
  let base = path.basename(String(name || '').replace(/\\/g, '/')).replace(/[\x00-\x1f<>:"|?*]/g, '_').replace(/^\.+/, '').trim() || 'download';
  const ext = path.extname(base), stem = base.slice(0, base.length - ext.length);
  let out = path.join(dir, base);
  for (let i = 1; exists(out) && i < 1000; i++) out = path.join(dir, `${stem} (${i})${ext}`);
  return out;
}

// The site says which app is current in /app-version.json ({"version": "1.2.3"}). Returns that version when it is newer than
// this app's, else null (a missing, odd or older file says nothing). Only plain x.y.z numbers count on the site's side.
// The site must serve the file itself with a 200: a redirect reads as no file.
function newerApp(mine, text) {
  let v; try { v = JSON.parse(String(text)).version; } catch { return null; }
  const num = x => /^\d{1,6}\.\d{1,6}\.\d{1,6}$/.test(String(x)) ? String(x).split('.').map(Number) : null;
  const a = num(String(mine).replace(/-.*$/, '')), b = num(v);   // a test build (1.2.0-beta.1) compares as 1.2.0
  if (!a || !b) return null;
  for (let i = 0; i < 3; i++) if (b[i] !== a[i]) return b[i] > a[i] ? v : null;
  return null;
}
// The page's #update part says how to update on each system; the update notice opens there.
const downloadPage = part => SITE + '/download' + (part ? '#' + part : '');

// The right-click menu: with no menu Electron shows none, so selected text, a sign-in link or a box couldn't be copied with the mouse.
// Returns the items to show for a context-menu event's params: 'copyLink' for a web link, the edit roles for a box, 'copy' for selected text.
function contextItems(p) {
  const out = [];
  if (/^https?:\/\//i.test(p.linkURL || '')) out.push('copyLink');
  if (p.isEditable) out.push('cut', 'copy', 'paste', 'selectAll');
  else if ((p.selectionText || '').trim()) out.push('copy');
  return out;
}


// ---- Many Hives (DESIGN-many-hives.md, step 1): plain logic, not used by main.js yet. ----

// Where a Hive may live. https anywhere; plain http only on a literal loopback address (127.0.0.0/8 or ::1), since
// on any other network the key and the cookie would cross it in the clear. No user:password@ in the address.
// Returns { ok, url, why }: url is the address cut to what is stored (origin and path, no query, no #key).
function addressRule(text) {
  let u;
  try { u = new URL(String(text).trim()); } catch { return { ok: false, why: "That isn't a web address." }; }
  if (u.username || u.password) return { ok: false, why: 'An address with a name and password in it is not accepted.' };
  if (!u.hostname) return { ok: false, why: "That isn't a web address." };
  if (u.protocol === 'http:') {
    if (!loopbackHost(u.hostname)) return { ok: false, why: 'Plain http is only for this computer (127.0.0.1). Use the https:// address.' };
  } else if (u.protocol !== 'https:') return { ok: false, why: 'Only https:// addresses (or http:// on this computer).' };
  u.hash = ''; u.search = '';
  if (/\/login\/?$/.test(u.pathname)) u.pathname = '/';
  return { ok: true, url: u.href };
}
function loopbackHost(h) {
  if (h === '[::1]') return true;
  const m = /^127\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
  return !!m && m.slice(1).every(x => Number(x) <= 255);
}
// Same Hive: the same scheme, host and port as the Hive's own address, exactly (no subdomains, no other port).
function sameHive(u, base) {
  try { return new URL(u).origin === new URL(base).origin; } catch { return false; }
}
// A Hive's id: 16 random hex, never one already used.
function newId(used = [], rand = n => require('crypto').randomBytes(n)) {
  const seen = new Set(used);
  for (let i = 0; i < 100; i++) { const id = rand(8).toString('hex'); if (/^[a-f0-9]{16}$/.test(id) && !seen.has(id)) return id; }
  throw new Error('no free id');
}
// The Linux user name for a new local Hive: lower-case letters, digits and -, starting with a letter, at most 21
// long, not a name the system already uses or keeps for itself. Returns null when it's fine, else why not.
const RESERVED_USERS = new Set(['root', 'daemon', 'bin', 'sys', 'sync', 'games', 'man', 'lp', 'mail', 'news', 'uucp', 'proxy',
  'www-data', 'backup', 'list', 'irc', 'gnats', 'nobody', 'nogroup', 'messagebus', 'syslog', 'ubuntu', 'admin', 'sudo', 'lima', 'docker']);
function hiveUserName(name, taken = []) {
  const n = String(name);
  if (!/^[a-z][a-z0-9-]{0,20}$/.test(n)) return 'Use lower-case letters, digits and -, starting with a letter, at most 21 long.';
  if (n.endsWith('-')) return "It can't end with -.";
  if (RESERVED_USERS.has(n) || /^systemd-/.test(n)) return 'That name belongs to the system.';
  if (taken.includes(n)) return 'There is already a user with that name.';
  return null;
}
// One saved Hive, or null if it can't be trusted. Unknown fields are kept, so an older app keeps what a newer one wrote.
function validHive(h) {
  if (!h || typeof h !== 'object' || Array.isArray(h)) return null;
  if (typeof h.id !== 'string' || !/^[a-f0-9]{16}$/.test(h.id)) return null;
  const name = typeof h.name === 'string' ? h.name.replace(/[\u0000-\u001f\u007f]/g, '').trim().slice(0, 60) : '';
  const out = { ...h, name };
  if (h.kind === 'remote') {
    const a = addressRule(h.url);
    if (!a.ok) return null;
    out.url = a.url;
    if (!name) out.name = new URL(a.url).hostname;
    // Its sign-in gate (step 6), kept only in the one form the app reads; any other gate field is dropped.
    if (h.gate !== undefined) { const g = require('./gate').validGate(h.gate); if (g) out.gate = g; else delete out.gate; }
  } else if (h.kind === 'local') {
    if (typeof h.user !== 'string' || !/^[a-z_][a-z0-9_-]{0,31}$/.test(h.user)) return null;
    if (!name) out.name = h.user === 'hive' ? 'Hive' : h.user;
  } else return null;
  out.order = Number.isFinite(h.order) ? h.order : 0;
  if (h.lastOpened !== undefined && !Number.isFinite(h.lastOpened)) delete out.lastOpened;
  return out;
}
// hives.json as read. A missing file means the first run (only the local Hive); a file that can't be read gives
// { error } and an empty list, and the caller keeps the file rather than writing over it.
function parseHives(text) {
  if (text === null || text === undefined) return { v: 1, hives: [], extra: {} };
  let d;
  try { d = JSON.parse(String(text)); } catch { return { v: 1, hives: [], extra: {}, error: "The list of Hives couldn't be read." }; }
  if (!d || typeof d !== 'object' || !Array.isArray(d.hives)) return { v: 1, hives: [], extra: {}, error: "The list of Hives couldn't be read." };
  if (d.v !== 1) return { v: d.v, hives: [], extra: {}, error: 'The list of Hives was written by a newer app. Update the app to see it.' };
  const { v, hives, ...extra } = d;
  const ids = new Set(), list = [];
  let dropped = 0;
  for (const h of hives) { const x = validHive(h); if (!x || ids.has(x.id)) { dropped++; continue; } ids.add(x.id); list.push(x); }
  list.sort((a, b) => a.order - b.order);
  return { v: 1, hives: list, extra, dropped };
}
function stringifyHives(state) {
  const hives = (state.hives || []).map(validHive).filter(Boolean);
  return JSON.stringify({ ...(state.extra || {}), v: 1, hives }, null, 1) + '\n';
}
// The open windows, as saved. The old file was one rectangle ({x, y, width, height, maximized}): it becomes one window
// on the local Hive. The new one is { v: 1, windows: [{ hive, x, y, width, height, maximized }] }. A window whose Hive
// is gone opens on the local Hive; at most 12 come back; with none, one window opens.
function windowsState(saved, displays, ids = []) {
  const known = new Set(ids);
  const list = saved && typeof saved === 'object' && Array.isArray(saved.windows) ? saved.windows : saved && typeof saved === 'object' && 'width' in saved ? [saved] : [];
  const out = [];
  for (const w of list.slice(0, 12)) {
    if (!w || typeof w !== 'object') continue;
    out.push({ hive: typeof w.hive === 'string' && known.has(w.hive) ? w.hive : null, rect: windowState(w, displays) });
  }
  return out.length ? out : [{ hive: null, rect: null }];
}
// The right-click menu once there are many Hives (DESIGN-many-hives.md, "Open in new tab"). where(url) says where a URL
// points: 'this' (this window's Hive), 'hive' (another Hive in the list) or 'outside'. Returns item names in order; '-'
// separates groups. Saving goes only through this Hive's own session, so only this Hive's files are offered.
function linkMenu(p, where) {
  const web = u => /^https?:\/\//i.test(u || '');
  const groups = [];
  if (web(p.linkURL)) {
    const w = where(p.linkURL), g = [];
    if (w === 'this' || w === 'hive') g.push('openWindow');
    g.push('openBrowser');
    if (w === 'this') g.push('saveLink');
    g.push('copyLink');
    groups.push(g);
  }
  if (p.mediaType === 'image' && web(p.srcURL)) groups.push(where(p.srcURL) === 'this' ? ['copyImage', 'saveImage'] : ['copyImage']);
  const edit = p.isEditable ? ['cut', 'copy', 'paste', 'selectAll'] : (p.selectionText || '').trim() ? ['copy'] : [];
  if (edit.length) groups.push(edit);
  return groups.flatMap((g, i) => i ? ['-', ...g] : g);
}
// The menu's words for opening a link outside: the default browser's own name when the system gives one.
function browserLabel(name) {
  const n = String(name || '').replace(/\.(app|exe|desktop)$/i, '').replace(/[\u0000-\u001f]/g, '').trim();
  return n && n.length <= 40 ? `Open link in ${n}` : 'Open link in your browser';
}
// The proof a Hive gives that it holds the key, tied to the whole origin (scheme, host and port) as well as the moment.
function proofString(origin, nonce) { return `proof:${origin}:${nonce}`; }

module.exports = { linkMenu, browserLabel, addressRule, sameHive, newId, hiveUserName, validHive, parseHives, stringifyHives, windowsState, proofString, newerApp, contextItems, downloadPage, downloadPath, appendLog, tailLog, diskCheck, windowState, shortcut, SITE, openCommand, installCommand, installResult, localUrl, isLocal, pin, proofAsk, proofOk, sameApp, redact, plain };
