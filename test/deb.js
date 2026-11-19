// node test/deb.js : on a throwaway Linux machine (a CI runner) where the .deb is installed with dpkg and a Hive is already installed:
// starts the app by the command in its menu entry (hive-app.desktop) and checks the window reaches the Hive.
const fs = require('fs');
const { _electron } = require('playwright-core');
const entry = '/usr/share/applications/hive-app.desktop';
if (!fs.existsSync(entry)) { console.error('no menu entry at ' + entry + ': install the .deb first'); process.exit(2); }
const exec = (fs.readFileSync(entry, 'utf8').match(/^Exec=(.+)$/m) || [])[1];
if (!exec) { console.error('the menu entry has no Exec line'); process.exit(1); }
const words = exec.replace(/%[a-zA-Z]/g, '').trim().split(/\s+/).map(w => w.replace(/^"|"$/g, ''));
const bin = words[0];
if (!fs.existsSync(bin)) { console.error(`the menu entry starts ${bin}, which isn't there`); process.exit(1); }
(async () => {
  const app = await _electron.launch({ executablePath: bin, args: [...words.slice(1), '--no-sandbox'], env: { ...process.env, HIVE_APP_TEST: '1' } });
  await app.firstWindow();
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '');
  const end = Date.now() + 4 * 60000; let at = '';
  while (!/^http:\/\/(localhost|127\.0\.0\.1):\d+\//.test(at = await where())) {
    if (Date.now() > end) throw new Error(`the window never reached the Hive (at "${at}")`);
    await new Promise(r => setTimeout(r, 1000));
  }
  console.log(`ok deb launch: ${exec} -> ${at}`);
  await app.close();
})().catch(e => { console.error('FAILED', e.message || e); process.exit(1); });
