// node test/real.js PATH-TO-PACKAGED-APP : the real thing, on a throwaway machine only (a CI runner). With no Hive on the
// machine, the packaged app runs the site's real installer, shows its warning, takes the yes, and ends in the Hive's own
// web app in the window. It installs a Hive into this account, so it refuses to run unless HIVE_APP_REAL=1.
const fs = require('fs');
const { _electron } = require('playwright-core');
const bin = process.argv[2];
if (process.env.HIVE_APP_REAL !== '1' || !bin || !fs.existsSync(bin)) { console.error('usage: HIVE_APP_REAL=1 node test/real.js PATH-TO-APP-EXECUTABLE (on a throwaway machine)'); process.exit(2); }
let logText = '';
// The app's own page (setup.html) is the window's; each Hive shows in a view over it (DESIGN-many-hives.md), so what the
// window shows is its top view's address, and the Hive's page is the Playwright page at that address.
async function ownPage(app) {
  for (const end = Date.now() + 60000; Date.now() < end; await new Promise(r => setTimeout(r, 200))) {
    const p = app.windows().find(w => w.url().includes('setup.html'));
    if (p) return p;
  }
  return app.firstWindow();
}
const frontPage = (app, at) => app.windows().find(p => at && p.url().split('#')[0] === at);
(async () => {
  const env = { ...process.env, HIVE_APP_TEST: '1' };
  const app = await _electron.launch({ executablePath: bin, args: process.platform === 'linux' ? ['--no-sandbox'] : [], env });
  const page = await ownPage(app);
  const title = () => page.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
  const where = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => (w.contentView.children.at(-1)?.webContents ?? w.webContents).getURL().split('#')[0])[0] || '');
  const shot = (n, p = page) => p.screenshot({ path: `test/shots/real-${process.platform}-${n}.png` }).catch(() => { });
  fs.mkdirSync('test/shots', { recursive: true });
  await page.waitForFunction(() => document.querySelector('#title')?.textContent === 'Set up the Hive', null, { timeout: 60000 });
  await page.getByRole('button', { name: 'Install' }).click();
  const end = Date.now() + (+process.env.HIVE_REAL_MINUTES || 70) * 60000; let accepted = false, last = '';
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
  const hp = frontPage(app, await where()) || page;
  await hp.waitForLoadState('load').catch(() => { });
  await new Promise(r => setTimeout(r, 5000));
  const html = await hp.content();
  await shot('hive', hp);
  if (html.length < 500) throw new Error('the Hive\'s page is nearly empty: ' + html);
  console.log(`ok real ${process.platform}: the window shows ${await where()} (${await hp.title()})`);
  // The installer's own words, so the run shows which path it took (on Windows: "Getting Ubuntu" means it built its own Linux system).
  console.log('the installer said:\n' + logText.split('\n').map(l => l.trim()).filter(l => l && l.length < 160).slice(-40).join('\n'));
  await app.close();
  // Second launch: the Hive is installed now, so the app must go straight to it, never showing the install step.
  const app2 = await _electron.launch({ executablePath: bin, args: process.platform === 'linux' ? ['--no-sandbox'] : [], env });
  const page2 = await ownPage(app2);
  const where2 = () => app2.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map(w => (w.contentView.children.at(-1)?.webContents ?? w.webContents).getURL().split('#')[0])[0] || '');
  const end2 = Date.now() + 3 * 60000; let sawSetup = false;
  for (;;) {
    const t2 = await page2.evaluate(() => document.querySelector('#title')?.textContent || '').catch(() => '');
    if (t2 === 'Set up the Hive' || t2 === 'Before it installs') sawSetup = true;
    const at2 = await where2();
    if (/^http:\/\/(localhost|127\.0\.0\.1):\d+\//.test(at2)) { console.log(`ok second launch ${process.platform}: straight to ${at2}${sawSetup ? ' (but the install step showed first)' : ''}`); if (sawSetup) throw new Error('second launch showed the install step'); break; }
    if (Date.now() > end2) throw new Error(`second launch ended at "${t2}" (${at2})`);
    await new Promise(r => setTimeout(r, 1000));
  }
  await app2.close();
})().catch(e => { console.error('FAILED', e.message || e); console.error('the installer\'s output up to then:\n' + logText.slice(-6000)); process.exit(1); });
