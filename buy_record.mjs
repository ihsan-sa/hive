#!/usr/bin/env node
/* buy_record.mjs: the screen-recorded checkout for `hive buy record` (warden ruling 11231556881e, rules 2, 4 and 5).

  node buy_record.mjs run --steps STEPS.json --card CARDFILE --out DIR --allow-host shop.com --max-total 37.63
  node buy_record.mjs scan [--card CARDFILE] FILE...     OCR video frames and images; exit 3 if any shows a card number

`run` drives Chrome through the merchant's checkout from a steps file and records every frame. Before any page script, every frame
(cross-origin payment iframes included) gets a style that draws each text field's value as dots, so the card number, CVC, delivery
address and contact details never reach a frame. Card values come from the 0600 card file only through tokens in the steps
($card.number, $card.cvc, $card.exp_month, $card.exp_year, $card.exp for MM/YY, $card.billing_address.postal_code, ...): the agent
writes the steps without ever seeing the number, and nothing here prints a resolved value. After the run every distinct frame (ffmpeg's
mpdecimate drops only exact repeats) and every screenshot is OCR'd; if any shows a full card number (a Luhn-valid 13-19 digit run, or 12 digits of this card), the
recording and screenshots are deleted and it exits 3.

Steps: a JSON list, run in order. Each may carry "in": "<iframe selector>" to act inside that frame.
  {"goto": "https://..."}              only https, only the approved merchant's host (--allow-host) or its subdomains
  {"click": SEL} {"fill": SEL, "value": V} {"type": SEL, "value": V} (key by key, for payment fields that need it)
  {"select": SEL, "value": V} {"check": SEL} {"press": KEY, "on": SEL} {"wait": MS} {"waitFor": SEL}
  {"expect": "text"}                   stop (exit 4) unless the page shows this text
  {"shot": "name"}                     a screenshot DIR/name.png (masked like the video): the order confirmation is the receipt
  {"place": SEL, "total": "37.63"}     press the place-order button, only if the page shows that total and it is at most the
                                       approved one (--max-total, the default total); otherwise stop with exit 4 and press nothing
Exit: 0 done, 2 a refused steps file, 3 a card number was seen (all output deleted), 4 the checkout stopped, 1 anything else.
Needs node, ffmpeg, tesseract and Chrome (CHROME, else google-chrome or chromium); playwright-core installs itself once into
~/.cache/hive-buy-node (HIVE_BUY_NODE). HIVE_BUY_RECORD_FIXTURE=DIR serves https://*.test/ from DIR, for the tests only. */
import { execFileSync, spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, renameSync, rmSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir, tmpdir } from 'node:os';
import { basename, extname, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const PW = '1.62.1';
const die = (msg, code = 1) => { console.error('hive buy record: ' + msg); process.exit(code); };

function playwright() {
  const cache = process.env.HIVE_BUY_NODE || join(homedir(), '.cache', 'hive-buy-node'), nm = join(cache, 'node_modules');
  if (!existsSync(join(nm, 'playwright-core', 'package.json'))) {
    mkdirSync(cache, { recursive: true });
    writeFileSync(join(cache, 'package.json'), JSON.stringify({ private: true, dependencies: { 'playwright-core': PW } }));
    console.error('hive buy record: installing playwright-core ' + PW + ' into ' + cache + ' (once)');
    try { execFileSync('npm', ['install', '--no-audit', '--no-fund', '--prefer-offline'], { cwd: cache, stdio: ['ignore', 2, 2] }); }
    catch { die('could not install playwright-core ' + PW + ' with npm (no network?); install it into ' + cache + ' and run again'); }
  }
  const pwc = join(homedir(), '.cache', 'ms-playwright');  // recordVideo needs Playwright's own ffmpeg build
  if (!(existsSync(pwc) && readdirSync(pwc).some((d) => d.startsWith('ffmpeg-')))) {
    console.error('hive buy record: installing Playwright\'s ffmpeg (once)');
    try { execFileSync(process.execPath, [join(nm, 'playwright-core', 'cli.js'), 'install', 'ffmpeg'], { stdio: ['ignore', 2, 2] }); }
    catch { die('could not install Playwright\'s ffmpeg (no network?): run `node ' + join(nm, 'playwright-core', 'cli.js') + ' install ffmpeg`'); }
  }
  return createRequire(join(cache, 'x.js'))('playwright-core');
}
function chrome() {
  for (const p of [process.env.CHROME, '/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser']) if (p && existsSync(p)) return p;
  die('no Chrome: set CHROME');
}

// The mask, in every frame before its own scripts: field values draw as dots. Selects, checkboxes and buttons stay readable.
const MASK = `(() => {
  const css = 'input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=submit]):not([type=button]):not([type=reset]):not([type=image]):not([type=range]):not([type=color]):not([type=file]), textarea, [contenteditable]:not([contenteditable=false]) { -webkit-text-security: disc !important; }';
  try { const s = new CSSStyleSheet(); s.replaceSync(css); const keep = () => { if (!document.adoptedStyleSheets.includes(s)) document.adoptedStyleSheets = [...document.adoptedStyleSheets, s]; }; keep(); setInterval(keep, 50); } catch (e) {}
  const add = () => { if (document.documentElement && !document.getElementById('__h2mask')) { const st = document.createElement('style'); st.id = '__h2mask'; st.textContent = css; document.documentElement.appendChild(st); } };
  add(); new MutationObserver(add).observe(document, { childList: true, subtree: true });
})();`;

// --- the card file, read into memory only ---
function cardOf(file) {
  if (!file) return null;
  let j = JSON.parse(readFileSync(file, 'utf8'));
  if (Array.isArray(j)) j = j[0];
  const c = (j && j.card) || j;
  if (!c || !c.number) die('the card file has no card in it');
  return c;
}
function resolve(v, card) {
  if (typeof v !== 'string' || !v.startsWith('$card.')) return v;
  if (!card) die('a $card token needs --card', 2);
  const path = v.slice(6);
  if (path === 'exp') return String(card.exp_month).padStart(2, '0') + '/' + String(card.exp_year).slice(-2);
  let x = card; for (const k of path.split('.')) x = x == null ? undefined : x[k];
  if (x == null) die('the card file has no ' + v, 2);
  return String(x);
}
const secrets = (card) => card ? [String(card.number).replace(/\D/g, ''), String(card.cvc || '')].filter((s) => s.length >= 3) : [];
function clean(s, card) {  // anything we print: no card-length digit run, no value of this card
  s = String(s).replace(/\b(\d[ -]?){12,19}\b/g, '[digits]');
  for (const x of secrets(card)) s = s.split(x).join('[card]');
  return s;
}

// --- the OCR check ---
const luhn = (d) => { let s = 0; for (let i = 0; i < d.length; i++) { let n = +d[d.length - 1 - i]; if (i % 2) { n *= 2; if (n > 9) n -= 9; } s += n; } return s % 10 === 0; };
export function leaks(text, card) {
  const num = card ? String(card.number).replace(/\D/g, '') : '';
  for (const line of text.split('\n')) {
    for (const m of line.matchAll(/\d(?:[ -]?\d){12,18}/g)) { const d = m[0].replace(/\D/g, ''); if (d.length >= 13 && d.length <= 19 && luhn(d)) return true; }
    const digits = line.replace(/\D/g, '');
    if (num.length >= 13 && (digits.includes(num.slice(0, 12)) || digits.includes(num.slice(-12)))) return true;
  }
  return false;
}
function ocr(img) {
  return new Promise((ok) => {
    const p = spawn('tesseract', [img, '-', '--psm', '11'], { stdio: ['ignore', 'pipe', 'ignore'] }); let out = '';
    p.stdout.on('data', (d) => { out += d; }); p.on('close', () => ok(out)); p.on('error', () => ok(null));
  });
}
async function scan(files, card) {
  const work = mkdtempSync(join(process.env.TMPDIR || tmpdir(), 'h2buyscan-')); const imgs = [];
  try {
    for (const f of files) {
      if (['.webm', '.mp4', '.mkv', '.mov'].includes(extname(f).toLowerCase())) {
        const dir = join(work, basename(f)); mkdirSync(dir);
        execFileSync('ffmpeg', ['-v', 'error', '-i', f, '-vf', 'mpdecimate=hi=64*4:lo=64*2:frac=0', '-fps_mode', 'vfr', join(dir, 'f%05d.png')]);
        for (const x of readdirSync(dir).sort()) imgs.push({ img: join(dir, x), from: f });
      } else imgs.push({ img: f, from: f });
    }
    const hits = new Set(); let i = 0, unread = 0;
    await Promise.all(Array.from({ length: 4 }, async () => {
      while (i < imgs.length) { const it = imgs[i++]; const t = await ocr(it.img); if (t == null) unread++; else if (leaks(t, card)) hits.add(it.from); }
    }));
    if (unread) die('tesseract could not read ' + unread + ' frames: install tesseract', 1);
    return { frames: imgs.length, hits: [...hits] };
  } finally { rmSync(work, { recursive: true, force: true }); }
}

// --- run ---
function arg(a, k) { const i = a.indexOf(k); return i >= 0 ? a[i + 1] : undefined; }
export function hostOK(url, allow) {
  let u; try { u = new URL(url); } catch { return false; }
  const h = u.hostname.toLowerCase(), base = allow.toLowerCase().replace(/^www\./, '');
  return u.protocol === 'https:' && (h === base || h.endsWith('.' + base));
}
export const cents = (s) => { const m = /^\$?(\d+)(?:\.(\d{1,2}))?$/.exec(String(s).trim().replace(/,/g, '')); return m ? +m[1] * 100 + +(m[2] || '0').padEnd(2, '0') : NaN; };

async function pageText(page) {  // every frame's text, so a total drawn in a payment iframe counts too
  const parts = [];
  for (const f of page.frames()) { try { parts.push(await f.evaluate(() => document.body ? document.body.innerText : '')); } catch {} }
  return parts.join('\n');
}
export function shows(text, total) {  // "37.63", "$37.63", "37,63" ... the amount as a number on the page, not a part of a longer one
  const [d, c] = (cents(total) / 100).toFixed(2).split('.');
  const dd = Number(d).toLocaleString('en-US');
  return new RegExp(`(^|[^\\d.,])(${d}|${dd.replace(/,/g, '[,.\\s]?')})[.,]${c}(?!\\d)`).test(text);
}

async function run(a) {
  const stepsFile = arg(a, '--steps'), out = arg(a, '--out'), allow = arg(a, '--allow-host'), max = arg(a, '--max-total');
  if (!stepsFile || !out || !allow || !max) die('run needs --steps, --out, --allow-host and --max-total', 2);
  let steps; try { steps = JSON.parse(readFileSync(stepsFile, 'utf8')); } catch (e) { die('--steps: ' + e.message, 2); }
  if (!Array.isArray(steps) || !steps.length) die('--steps must be a JSON list of steps', 2);
  const card = cardOf(arg(a, '--card')), maxC = cents(max);
  const KNOWN = ['goto', 'click', 'fill', 'type', 'select', 'check', 'press', 'wait', 'waitFor', 'expect', 'shot', 'place'];
  steps.forEach((s, n) => {
    const k = KNOWN.filter((x) => x in s);
    if (k.length !== 1) die(`step ${n + 1}: needs exactly one of ${KNOWN.join(', ')}`, 2);
    if (s.goto && !hostOK(s.goto, allow)) die(`step ${n + 1}: ${clean(s.goto)} is not https on ${allow}, the approved merchant`, 2);
    if ('place' in s && !(cents(s.total ?? max) <= maxC)) die(`step ${n + 1}: total ${s.total} is more than the approved ${max}`, 2);
    if ('shot' in s && !/^[\w-]{1,40}$/.test(s.shot)) die(`step ${n + 1}: shot names are letters, digits, - and _`, 2);
    resolve(s.value, card);  // a missing card field fails here, before Chrome starts, not halfway through the checkout
    for (const v of [s.value, s.goto, s.expect]) if (typeof v === 'string' && /\d{12}/.test(v.replace(/[ -]/g, ''))) die(`step ${n + 1}: a card number in the steps file; use $card.number`, 2);
  });
  mkdirSync(out, { recursive: true, mode: 0o700 });
  const { chromium } = playwright();
  const vids = mkdtempSync(join(out, '.video-'));
  let browser;
  process.on('exit', () => { try { rmSync(vids, { recursive: true, force: true }); } catch {} });
  browser = await chromium.launch({ executablePath: chrome(), headless: true, args: ['--disable-features=AutofillServerCommunication'] });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, recordVideo: { dir: vids, size: { width: 1280, height: 800 } } });
  await ctx.addInitScript(MASK);
  const fx = process.env.HIVE_BUY_RECORD_FIXTURE;
  if (fx) await ctx.route(/^https:\/\/[^/]+\.test\//, (r) => {
    const u = new URL(r.request().url()); const f = join(fx, u.hostname, u.pathname.endsWith('/') ? u.pathname + 'index.html' : u.pathname);
    return existsSync(f) ? r.fulfill({ path: f }) : r.fulfill({ status: 404, body: 'no fixture' });
  });
  const page = await ctx.newPage().catch(async (e) => { await browser.close(); die(clean(e.message.split('\n')[0], card)); }); const shots = []; let code = 0, why = '';
  try {
    for (const [n, s] of steps.entries()) {
      const at = s.in ? page.frameLocator(s.in) : page, loc = (sel) => at.locator(sel).first();
      const label = Object.entries(s).map(([k, v]) => `${k}=${typeof v === 'string' && v.startsWith('$card.') ? v : clean(JSON.stringify(v), card)}`).join(' ');
      console.log(`step ${n + 1}: ${label}`);
      if ('goto' in s) await page.goto(s.goto, { waitUntil: 'domcontentloaded' });
      else if ('click' in s) await loc(s.click).click();
      else if ('fill' in s) await loc(s.fill).fill(resolve(s.value, card));
      else if ('type' in s) await loc(s.type).pressSequentially(resolve(s.value, card), { delay: 40 });
      else if ('select' in s) await loc(s.select).selectOption(resolve(s.value, card));
      else if ('check' in s) await loc(s.check).check();
      else if ('press' in s) await (s.on ? loc(s.on).press(s.press) : page.keyboard.press(s.press));
      else if ('wait' in s) await page.waitForTimeout(Math.min(+s.wait || 0, 60000));
      else if ('waitFor' in s) await loc(s.waitFor).waitFor({ timeout: 30000 });
      else if ('expect' in s) { if (!(await pageText(page)).includes(s.expect)) { code = 4; why = `the page doesn't show ${JSON.stringify(s.expect)}`; break; } }
      else if ('shot' in s) { const p = join(out, s.shot + '.png'); await page.screenshot({ path: p, fullPage: true }); shots.push(p); }
      else if ('place' in s) {
        const total = s.total ?? max;
        if (!shows(await pageText(page), total)) { code = 4; why = `the page doesn't show the total ${total}: the order was not placed. If the cart came out different or higher, hive buy cancel and ask again`; break; }
        await loc(s.place).click();
      }
      await page.waitForTimeout(300);  // let each step reach a frame
    }
  } catch (e) { code = 4; why = clean(e.message.split('\n')[0], card); }
  await page.waitForTimeout(800);
  const video = page.video(); await ctx.close(); await browser.close();
  const rec = join(out, 'checkout.webm');
  if (existsSync(rec)) unlinkSync(rec);
  renameSync(await video.path(), rec); rmSync(vids, { recursive: true, force: true });
  const r = await scan([rec, ...shots], card);
  if (r.hits.length) {
    for (const f of [rec, ...shots]) rmSync(f, { force: true });
    die(`a card number showed in ${r.hits.map((f) => basename(f)).join(', ')}: the recording and screenshots are deleted. Do not post any of it; say so in the thread.`, 3);
  }
  console.log(JSON.stringify({ recording: rec, shots, frames_scanned: r.frames, card_number_seen: false, stopped: code ? why : null }));
  if (code) die('stopped: ' + why, code);
}

const a = process.argv.slice(2), main = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;  // importable, for the tests
if (!main) {}
else if (a[0] === 'run') await run(a.slice(1));
else if (a[0] === 'scan') {
  const card = cardOf(arg(a, '--card')); const files = a.slice(1).filter((x, i, l) => x !== '--card' && l[i - 1] !== '--card');
  if (!files.length) die('scan FILE...', 2);
  for (const f of files) if (!existsSync(f) || !statSync(f).isFile()) die('no file ' + f, 2);
  const r = await scan(files, card);
  console.log(JSON.stringify({ frames_scanned: r.frames, card_number_seen: r.hits.length > 0, in: r.hits.map((f) => basename(f)) }));
  process.exit(r.hits.length ? 3 : 0);
} else die('usage: buy_record.mjs run --steps F --card F --out DIR --allow-host HOST --max-total N | scan [--card F] FILE...', 2);
