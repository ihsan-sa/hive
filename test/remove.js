// node test/remove.js : run the README's "Remove it" command for this system on a machine that has the real Hive installed
// (a CI runner, after test/real.js), then check nothing of the Hive is left. Throwaway machines only: set HIVE_APP_REAL=1.
// Prints `ok remove linux|darwin|win32`.
const fs = require('fs'), os = require('os'), path = require('path');
const { spawnSync } = require('child_process');
if (process.env.HIVE_APP_REAL !== '1') { console.error('this deletes the Hive in this account: set HIVE_APP_REAL=1 on a throwaway machine'); process.exit(2); }
const home = os.homedir(), p = process.platform, env = { ...process.env, HIVE_KEEP: 'none' };
const left = [];
const gone = f => { if (fs.existsSync(f)) left.push(f); };
function run(cmd, args) { const r = spawnSync(cmd, args, { input: 'delete\n', env, encoding: 'utf8', timeout: 20 * 60000 }); console.log(r.stdout, r.stderr); if (r.status !== 0) { console.error(`${cmd} exited ${r.status}`); process.exit(1); } }
// The site's removers read their answer from the terminal, so give them one (script makes a pty).
if (p === 'linux') {
  if (!fs.existsSync(path.join(home, 'hive'))) { console.error('no ~/hive: nothing installed to remove'); process.exit(1); }
  run('script', ['-qec', 'curl -fsSL https://hivelab.ca/uninstall.sh | sh -s -- --this-account', '/dev/null']);
  gone(path.join(home, 'hive')); gone(path.join(home, '.local/share/applications/hive.desktop'));
} else if (p === 'darwin') {
  if (!fs.existsSync(path.join(home, '.hive'))) { console.error('no ~/.hive: nothing installed to remove'); process.exit(1); }
  run('script', ['-q', '/dev/null', 'sh', '-c', 'curl -fsSL https://hivelab.ca/uninstall-mac.sh | sh']);
  gone(path.join(home, '.hive')); gone(path.join(home, 'Applications/Hive.app'));
} else if (p === 'win32') {
  const wsl = () => spawnSync('wsl.exe', ['-l', '-q'], { encoding: 'utf16le' }).stdout || '';
  if (!/^Hive\s*$/m.test(wsl().replace(/\0/g, ''))) { console.error('no WSL system Hive: nothing installed to remove'); process.exit(1); }
  run('powershell.exe', ['-NoProfile', '-Command', 'irm https://hivelab.ca/uninstall.ps1 | iex']);
  if (/^Hive\s*$/m.test(wsl().replace(/\0/g, ''))) left.push('WSL system Hive');
  gone(path.join(process.env.LOCALAPPDATA || '', 'Hive'));
  const ap = process.env.APPDATA || '';
  for (const d of [path.join(ap, 'Microsoft/Windows/Start Menu/Programs'), path.join(ap, 'Microsoft/Windows/Start Menu/Programs/Startup'), path.join(home, 'Desktop')])
    for (const f of fs.existsSync(d) ? fs.readdirSync(d) : []) if (/^Hive\.lnk$/i.test(f)) left.push(path.join(d, f));
} else { console.error('unsupported system ' + p); process.exit(2); }
if (left.length) { console.error('FAILED, still there:\n' + left.join('\n')); process.exit(1); }
console.log('ok remove ' + p);
