// What differs between the three systems, kept apart from Electron so it can be tested with plain node.
// The app holds no Hive logic of its own: it runs the same installers as the one-line install, and asks the installed
// Hive for its address with `hive open --print`. So a new Hive version needs no new app.
'use strict';
const path = require('path');
const fs = require('fs');

const SITE = process.env.HIVE_SITE || 'https://hive.ihsan.cc';

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
    return { file: ps, args: ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', `irm ${SITE}/install.ps1 | iex`], env: add };
  }
  // To a file first: a failed curl piped into sh looks like success.
  const get = name => `f=$(mktemp) && curl -fsSL ${SITE}/${name} -o "$f" || { echo "Couldn't reach ${SITE}. Check the internet connection, then try again."; exit 1; }; `;
  if (platform === 'darwin') {
    // The Mac install is still a test build, and says so unless told the person knows. The app's first screen says it.
    return { file: '/bin/sh', args: ['-c', get('install-mac.sh') + 'sh "$f"; c=$?; rm -f "$f"; exit $c'], env: { ...add, HIVE_MAC_TEST: '1' } };
  }
  // Linux: in this account, which needs no password. The launcher icon is the app itself, so the installer's own is skipped.
  // The installer can't add what it needs (no sudo), and a missing curl would read as "no internet": say what to do instead.
  const need = 'm=""; for t in curl git python3; do command -v $t >/dev/null 2>&1 || m="$m $t"; done; [ -z "$m" ] || { echo "The Hive needs these first:$m"; echo "Open a terminal and run:  sudo apt install$m"; echo "(on Fedora: sudo dnf install$m). Then press Try again."; exit 1; }; ';
  return { file: '/bin/sh', args: ['-c', need + get('install.sh') + 'sh "$f" --this-account; c=$?; rm -f "$f"; exit $c'], env: { ...add, HIVE_NO_LAUNCHER: '1' } };
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
function isLocal(u) {
  try { const x = new URL(u); return x.protocol === 'http:' && (x.hostname === 'localhost' || x.hostname === '127.0.0.1'); } catch { return false; }
}
// Same app (same host and port) as the Hive's address: such links stay in the window, all others go to the browser.
function sameApp(u, base) {
  try { return isLocal(u) && new URL(u).port === new URL(base).port; } catch { return false; }
}
// The address is the key to the Hive, so it never goes on the screen or in a log.
function redact(text) { return String(text).replace(/(https?:\/\/(?:localhost|127\.0\.0\.1):\d+\/login#)\S+/g, '$1…'); }
// Installers draw progress with carriage returns and colour codes; the screen wants plain lines.
// A line redrawn in place (a download's progress bar) keeps only its last drawing.
function plain(text) { return String(text).replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '').replace(/[^\n\r]*\r(?!\n)/g, ''); }
function uid() { try { return require('os').userInfo().uid; } catch { return 1000; } }

module.exports = { SITE, openCommand, installCommand, installResult, localUrl, isLocal, sameApp, redact, plain };
