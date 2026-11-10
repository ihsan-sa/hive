// node test/smoke.js PATH-TO-PACKAGED-APP : on any of the three systems, the packaged app starts with no Hive on the
// machine and shows the setup screen. Run by the build on real Windows, macOS and Linux machines. (Linux: under xvfb-run.)
const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { _electron } = require('playwright-core');
const bin = process.argv[2];
if (!bin || !fs.existsSync(bin)) { console.error('usage: node test/smoke.js PATH-TO-APP-EXECUTABLE'); process.exit(2); }
const home = fs.mkdtempSync(path.join(os.tmpdir(), 'hive-app-smoke-'));
(async () => {
  // An empty home everywhere the app looks, so a Hive on the build machine can't be found.
  const env = { ...process.env, HIVE_APP_TEST: '1', HOME: home, LOCALAPPDATA: home, XDG_DATA_HOME: path.join(home, 'share'), XDG_CONFIG_HOME: path.join(home, 'config') };
  delete env.HIVE_DIR;
  const app = await _electron.launch({ executablePath: bin, args: process.platform === 'linux' ? ['--no-sandbox'] : [], env });
  const page = await app.firstWindow();
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive', null, { timeout: 60000 });
  assert.ok(await page.getByRole('button', { name: 'Install' }).isVisible());
  fs.mkdirSync('test/shots', { recursive: true });
  await page.screenshot({ path: `test/shots/smoke-${process.platform}-${process.arch}.png` });
  await app.close();
  console.log(`ok smoke ${process.platform} ${process.arch}`);
})().catch(e => { console.error('FAILED', e); process.exit(1); });
