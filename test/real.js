// node test/real.js PATH-TO-PACKAGED-APP : the real thing, on a throwaway machine only (a CI runner). With no Hive on the
// machine, the packaged app runs the site's real installer, shows its warning, takes the yes, and ends in the Hive's own
// web app in the window. It installs a Hive into this account, so it refuses to run unless HIVE_APP_REAL=1.
const fs = require('fs');
const { _electron } = require('playwright-core');
const bin = process.argv[2];
if (process.env.HIVE_APP_REAL !== '1' || !bin || !fs.existsSync(bin)) { console.error('usage: HIVE_APP_REAL=1 node test/real.js PATH-TO-APP-EXECUTABLE (on a throwaway machine)'); process.exit(2); }
let logText = '';
(async () => {
  const env = { ...process.env, HIVE_APP_TEST: '1' };
  const app = await _electron.launch({ executablePath: bin, args: process.platform === 'linux' ? ['--no-sandbox'] : [], env });
  const page = await app.firstWindow();
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => w.webContents.getURL().split('#')[0])[0] || '');
  const shot = n => page.screenshot({ path: `test/shots/real-${process.platform}-${n}.png` }).catch(() => { });
  fs.mkdirSync('test/shots', { recursive: true });
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive', null, { timeout: 60000 });
  await page.getByRole('button', { name: 'Install' }).click();
  const end = Date.now() + 70 * 60000; let accepted = false, last = '';
  // if the app goes away mid-install, say how it ended and what the installer had printed up to then
  app.process().on('exit', (code, sig) => console.log(new Date().toISOString(), `the app's process ended: code ${code}, signal ${sig}`));
  for (;;) {
    logText = await page.locator('#log').textContent({ timeout: 3000 }).catch(() => logText);
    const at = await where();
    if (/^http:\/\/(localhost|127\.0\.0\.1):\d+\//.test(at)) break;
    const t = await title();
    if (t !== last) { console.log(new Date().toISOString(), t); last = t; }
    if (t === 'Before it installs' && !accepted) { accepted = true; await shot('accept'); await page.getByRole('button', { name: 'Accept and install' }).click(); }
    else if (['The install stopped', "The Hive didn't start", 'Restart Windows'].includes(t) || Date.now() > end) {
      await shot('stopped');
      throw new Error(`ended at "${t}":\n` + await page.locator('#log').textContent().catch(() => ''));
    }
    await new Promise(r => setTimeout(r, 2000));
  }
  await page.waitForLoadState('load').catch(() => { });
  await new Promise(r => setTimeout(r, 5000));
  const html = await page.content();
  await shot('hive');
  if (html.length < 500) throw new Error('the Hive\'s page is nearly empty: ' + html);
  console.log(`ok real ${process.platform}: the window shows ${await where()} (${await page.title()})`);
  await app.close();
})().catch(e => { console.error('FAILED', e.message || e); console.error('the installer\'s output up to then:\n' + logText.slice(-6000)); process.exit(1); });
