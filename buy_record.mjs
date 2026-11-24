#!/usr/bin/env node
/* buy_record.mjs: the screen-recorded checkout for `hive buy record` (warden ruling 11231556881e, rules 2, 4 and 5).

  node buy_record.mjs run --steps STEPS.json --card CARDFILE [--contact FILE] --out DIR --allow-host shop.com --max-total 37.63
  node buy_record.mjs run --dry --steps STEPS.json [--contact FILE] --out DIR --allow-host shop.com [--max-total 37.63]
  node buy_record.mjs scan [--card CARDFILE] [--contact FILE] FILE...   OCR video frames and images; exit 3 if any shows a card
                                                                         number or the person's contact details

--dry is the practice walk through a real merchant's checkout, with no card: --card is refused, $card.number / cvc / exp* steps
type nothing (the field is only checked: is it masked, what is it, is it in a cross-origin iframe), $card.billing_address.* are
fixed placeholders (never the person's), place reads the totals and presses nothing and ends the run, and card-used.json is never
written. The result adds card_fields, place_check and a survey of the last page's frames (cross-origin or not, canvas elements,
payment fields and their autocomplete). Chrome's own autofill popup is browser UI outside the page and a fresh headless profile
has no saved cards, so a dry run can't show it; the survey's autocomplete values say where a real profile would offer one.

`run` drives Chrome through the merchant's checkout from a steps file and records every frame. Before any page script, every frame
(cross-origin payment iframes and shadow roots included) gets a style that draws each text field's value as dots, and any page text
with a run of 10+ digits (a page echoing the number back) as dots too, so the card number, CVC, delivery address and contact details
never reach a frame. Card values come from the 0600 card file only through tokens in the steps ($card.number, $card.cvc,
$card.exp_month, $card.exp_year, $card.exp for MM/YY, $card.billing_address.postal_code, ...): the agent writes the steps without ever
seeing the number, and nothing here prints a resolved value. A card value goes only into a field the mask is seen covering (a
password field, or a text field drawn as discs); anything else stops with exit 4 before a key is typed.

After the run every distinct frame (ffmpeg's mpdecimate drops near-duplicates, so a frame that differs only slightly from the last
kept one, such as a caret, is not read) and every screenshot is OCR'd twice, as is and sharpened 3x for small faint text; lines of
only digits are joined, so a number wrapped over two lines is still read. If any shows a full card number (a Luhn-valid 13-19 digit
run, or 12 digits of this card), every recording and screenshot is deleted and it exits 3. Files are written to DIR/.stage-* and
move into DIR only after a clean scan; the stage is deleted on any exit, a kill (INT, TERM, HUP) included.

--contact FILE is the person's own details, a JSON file opened without following a symlink, owned by this user and readable by
no one else (refused otherwise): {"email", "phone", "name", "first_name", "last_name", "address": {"line1", "line2", "city",
"state", "postal_code", "country"}}. Steps use them only as $contact.email, $contact.phone, $contact.name, $contact.first_name,
$contact.last_name, $contact.address.line1/line2/city/state/state_name/postal_code/country (any other $contact path is exit 2; a
token for an empty value stops the run, line2 excepted), so no detail is ever in a steps file or printed. Page text holding one
of them (a sidebar echoing the email, an address summary, a phone split over elements) is masked like a long number, in every
frame, and so are a select's chosen option, a button's value and a placeholder. The OCR fails the recording (exit 3, everything
deleted) on the email, the phone's last 7 digits, the first and last name together, or the street number and name, and in a real
run on the card's billing name and street too; the city, state and zip are not checked (shipping quotes show them by design).
A dry run never types the person's file: it types fixed placeholders (dry-run@example.com, a 555 number, "Dry Run", 123 Test
Street) with only the file's city, state, zip and country, so the place step reads the checkout's shipping and tax for the
person's own area. With no --contact it uses a New York 10001 placeholder.

Rule 2, one card one purchase: once a card value is typed, DIR/card-used.json is written, and a later run into the same DIR that
uses $card or place is refused (exit 2). Rule: every acting step needs the top page on https on the approved host; a link or
redirect elsewhere is logged ("now at HOST") and the next acting step stops with exit 4. Popups are recorded (popup-N.webm) and
scanned like the main recording.

Steps: a JSON list, run in order. Each may carry "in": "<iframe selector>" (or a list, for nested iframes) to act inside that frame.
  {"goto": "https://..."}              only https, only the approved merchant's host (--allow-host) or its subdomains
  {"click": SEL} {"fill": SEL, "value": V} {"type": SEL, "value": V} (key by key, for payment fields that need it)
  {"select": SEL, "value": V} {"check": SEL} {"press": KEY, "on": SEL} {"wait": MS} {"waitFor": SEL}
  {"expect": "text"}                   stop (exit 4) unless the page shows this text
  {"shot": "name"}                     a screenshot DIR/name.png (masked like the video): the order confirmation is the receipt
  {"place": SEL, "total": "37.63"}     press the place-order button, only if the page's labelled total ("Total", "Order total",
                                       "Amount due", ...) is that total, in --currency (default usd), and no labelled total is above
                                       the approved one (--max-total, the default total); otherwise stop with exit 4, press nothing.
                                       With no labelled total, no amount on the page may be above it. The page is read twice, 0.7 s
                                       apart. A second after the press the page is read again: a total above the approved one then
                                       is exit 5 (the order may be placed; the recording is kept as evidence).
Once the card is in, every step that could submit (a click, key, select, check, or a value with a newline) is checked like place.
Exit: 0 done, 2 a refused steps file or a used card, 3 a card number was seen (all output deleted), 4 the checkout stopped,
5 a higher total showed after the order button, 1 anything else.
Needs node, ffmpeg, tesseract and Chrome (CHROME, else google-chrome or chromium); playwright-core installs itself once into
~/.cache/hive-buy-node (HIVE_BUY_NODE). HIVE_BUY_RECORD_FIXTURE=DIR serves https://*.test/ from DIR, for the tests only. */
import { execFileSync, spawn } from 'node:child_process';
import { closeSync, constants as FS, existsSync, fstatSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, renameSync, rmSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { availableParallelism, homedir, tmpdir } from 'node:os';
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

// The mask, in every frame before its own scripts: field values draw as dots, in shadow roots too (attachShadow is wrapped, and a
// sweep finds declarative ones), and any text with a run of 10 or more digits (a page echoing the number back) draws as dots as well.
// Selects, checkboxes, buttons and ordinary text stay readable.
const FIELDS = 'input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=submit]):not([type=button]):not([type=reset]):not([type=image]):not([type=range]):not([type=color]):not([type=file]), textarea, [contenteditable]:not([contenteditable=false])';
// The person's contact and address values are masked in page text as well, in every frame (a third-party iframe that echoes an
// address too). hide.words are privates()'s normalized words (normw): matched whole-word, so "1 main" is not found in "101 main".
// hide.digits are the phone's last 7 digits, matched in a text node or, for a number split over elements ("(415) <b>555</b>-0123"),
// in its parent's or grandparent's short text. Page scripts run after this and can replace any builtin: the match takes the ones
// it uses first and touches no overridable iterator or regex method with a value in hand, so no page script can read the values.
const MASK = (hide = { words: [], digits: [] }) => `(() => {
  // Fields show discs. Page text holding a long number (an address echoed with its phone, a card shown back) or one of the person's
  // contact values becomes a plain grey bar: rows of discs in page text OCR as digit runs, and one can pass Luhn and wipe a clean recording.
  const css = '${FIELDS} { -webkit-text-security: disc !important; } [data-h2mask], [data-h2mask] * { -webkit-text-security: disc !important;' +
    ' color: transparent !important; text-shadow: none !important; } [data-h2mask] { background: #777 !important; }' +
    ' [data-h2mask]::placeholder, [data-h2mask] *::placeholder { color: transparent !important; }';
  const LONG = /\\d(?:[ \\u00a0.\\-]?\\d){9,}/;
  const ap = Reflect.apply, low = String.prototype.toLowerCase, cc = String.prototype.charCodeAt, inc = String.prototype.includes,
    fcc = String.fromCharCode, rtest = RegExp.prototype.test;
  const WORDS = ${JSON.stringify((hide.words || []).map((w) => ' ' + w + ' '))}, DIGITS = ${JSON.stringify(hide.digits || [])};
  const NW = WORDS.length, ND = DIGITS.length;
  const nm = (s) => {  // as normw: lowercase, runs of anything but a-z, 0-9, @ and letters past Latin-1 become one space, padded
    s = ap(low, '' + s, []); let o = ' ', gap = false;
    for (let i = 0; i < s.length; i++) {
      const c = ap(cc, s, [i]);
      if ((c >= 48 && c <= 57) || (c >= 97 && c <= 122) || c === 64 || c >= 192) { if (gap && o.length > 1) o += ' '; gap = false; o += ap(fcc, null, [c]); }
      else gap = true;
    }
    return o + ' ';
  };
  const nd = (s) => { s = '' + s; let o = ''; for (let i = 0; i < s.length; i++) { const c = ap(cc, s, [i]); if (c >= 48 && c <= 57) o += ap(fcc, null, [c]); } return o; };
  const words = (s) => { if (!NW) return false; const l = nm(s); for (let i = 0; i < NW; i++) if (ap(inc, l, [WORDS[i]])) return true; return false; };
  const digits = (s) => { if (!ND) return false; const d = nd(s); for (let i = 0; i < ND; i++) if (ap(inc, d, [DIGITS[i]])) return true; return false; };
  const mine = (s) => words(s) || digits(s);
  let sheet = null; try { sheet = new CSSStyleSheet(); sheet.replaceSync(css); } catch (e) {}
  const roots = new Set();
  const big = (p) => p === document.body || p === document.documentElement || p.tagName === 'HEAD';
  const hide = (t) => {
    if (!t || t.nodeType !== 3) return;
    const p = t.parentElement; if (!p || p.closest('[data-h2mask]')) return;
    const d = t.data;
    if (ap(rtest, LONG, [d]) || mine(d)) {
      if (big(p)) { const sp = document.createElement('span'); sp.setAttribute('data-h2mask', ''); p.insertBefore(sp, t); sp.appendChild(t); }
      else p.setAttribute('data-h2mask', '');
      return;
    }
    if (!ND || !nd(d).length) return;  // a phone split over elements: the nearest short parent that holds it all
    for (let e = p, k = 0; e && k < 2 && !big(e); e = e.parentElement, k++) {
      const tc = e.textContent || ''; if (tc.length > 200) return;
      if (digits(tc)) { e.setAttribute('data-h2mask', ''); return; }
    }
  };
  // what text nodes don't hold: a select's chosen option, a button's or field's value, a placeholder
  const controls = (root) => {
    if (!NW && !ND) return;
    const els = root.querySelectorAll('select, input, textarea, button');
    for (let i = 0; i < els.length; i++) {
      const e = els[i]; if (e.hasAttribute('data-h2mask')) continue;
      let s = ' ' + (e.getAttribute('placeholder') || '') + ' ' + (e.getAttribute('value') || '');
      if (e.tagName === 'SELECT') { const o = e.selectedOptions; for (let j = 0; j < o.length; j++) s += ' ' + o[j].text; }
      else if (e.tagName !== 'BUTTON') s += ' ' + (e.value || '');
      if (mine(s)) e.setAttribute('data-h2mask', '');
    }
  };
  const walk = (n) => {
    if (!n) return;
    if (n.nodeType === 3) return hide(n);
    if (n.nodeType !== 1 && n.nodeType !== 11 && n.nodeType !== 9) return;
    const w = document.createTreeWalker(n, 4); let t; while ((t = w.nextNode())) hide(t);
  };
  // controls() runs on every mutation batch and before every paint too (a script setting a select's index or a value makes no
  // mutation), so a new option, button or placeholder never reaches a frame unmasked
  let ctl = () => {};
  const mo = new MutationObserver((ms) => { for (const m of ms) { if (m.type === 'characterData') hide(m.target); else if (m.type === 'childList') for (const n of m.addedNodes) walk(n); } ctl(); });
  const OBS = { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['value', 'placeholder'] };
  const watch = (root) => {
    if (roots.has(root)) return; roots.add(root);
    try { if (sheet && !root.adoptedStyleSheets.includes(sheet)) root.adoptedStyleSheets = [...root.adoptedStyleSheets, sheet]; } catch (e) {}
    try { mo.observe(root, OBS); walk(root); } catch (e) {}
  };
  const keep = () => { try { if (sheet && !document.adoptedStyleSheets.includes(sheet)) document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]; } catch (e) {} };
  const add = () => { if (document.documentElement && !document.getElementById('__h2mask')) { const st = document.createElement('style'); st.id = '__h2mask'; st.textContent = css; document.documentElement.appendChild(st); } };
  const att = Element.prototype.attachShadow;
  Element.prototype.attachShadow = function (o) { const r = att.call(this, o); watch(r); return r; };
  const sweep = (root) => { for (const el of root.querySelectorAll('*')) if (el.shadowRoot) { watch(el.shadowRoot); sweep(el.shadowRoot); } };
  mo.observe(document, OBS);
  new MutationObserver(add).observe(document, { childList: true, subtree: true });
  add(); keep(); walk(document);
  ctl = () => { try { controls(document); roots.forEach((r) => controls(r)); } catch (e) {} };
  const raf = window.requestAnimationFrame, frame = () => { ctl(); ap(raf, window, [frame]); }; ap(raf, window, [frame]);
  setInterval(() => { keep(); add(); try { sweep(document); } catch (e) {} ctl(); }, 100);
  document.addEventListener('DOMContentLoaded', () => { try { sweep(document); walk(document); } catch (e) {} ctl(); });
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
// US state codes to names, for a merchant whose state dropdown lists names ($card.billing_address.state_name)
const US_STATES = { AL: 'Alabama', AK: 'Alaska', AZ: 'Arizona', AR: 'Arkansas', CA: 'California', CO: 'Colorado', CT: 'Connecticut', DE: 'Delaware',
  DC: 'District of Columbia', FL: 'Florida', GA: 'Georgia', HI: 'Hawaii', ID: 'Idaho', IL: 'Illinois', IN: 'Indiana', IA: 'Iowa', KS: 'Kansas',
  KY: 'Kentucky', LA: 'Louisiana', ME: 'Maine', MD: 'Maryland', MA: 'Massachusetts', MI: 'Michigan', MN: 'Minnesota', MS: 'Mississippi',
  MO: 'Missouri', MT: 'Montana', NE: 'Nebraska', NV: 'Nevada', NH: 'New Hampshire', NJ: 'New Jersey', NM: 'New Mexico', NY: 'New York',
  NC: 'North Carolina', ND: 'North Dakota', OH: 'Ohio', OK: 'Oklahoma', OR: 'Oregon', PA: 'Pennsylvania', RI: 'Rhode Island', SC: 'South Carolina',
  SD: 'South Dakota', TN: 'Tennessee', TX: 'Texas', UT: 'Utah', VT: 'Vermont', VA: 'Virginia', WA: 'Washington', WV: 'West Virginia',
  WI: 'Wisconsin', WY: 'Wyoming', PR: 'Puerto Rico', GU: 'Guam', VI: 'U.S. Virgin Islands', AS: 'American Samoa', MP: 'Northern Mariana Islands',
  AA: 'Armed Forces Americas', AE: 'Armed Forces Europe', AP: 'Armed Forces Pacific' };
// $card.* from the card file, $contact.* from the contact file (--contact). An address's line2 may be missing: it is then empty.
// A $contact token may name only these paths: each is a value privates() masks and contactLeaks() knows (or one they leave out by
// design, the country), so no other key of the file can be typed unwatched.
const CONTACT_PATHS = ['email', 'phone', 'name', 'first_name', 'last_name', 'address.line1', 'address.line2', 'address.city', 'address.state',
  'address.state_name', 'address.postal_code', 'address.country'];
export function resolve(v, card, contact) {
  if (typeof v !== 'string') return v;
  const [kind, obj] = v.startsWith('$card.') ? ['card', card] : v.startsWith('$contact.') ? ['contact', contact] : [];
  if (!kind) return v;
  const path = v.slice(kind.length + 2);
  if (kind === 'contact' && !CONTACT_PATHS.includes(path)) die(`${v} is not a contact token: use one of ${CONTACT_PATHS.map((x) => '$contact.' + x).join(', ')}`, 2);
  if (!obj) die(`a $${kind} token needs --${kind}`, 2);
  if (kind === 'card' && path === 'exp') return String(card.exp_month).padStart(2, '0') + '/' + String(card.exp_year).slice(-2);
  const dig = (p) => { let x = obj; for (const k of p.split('.')) x = x == null ? undefined : x[k]; return x; };
  if (path.endsWith('state_name')) {
    const st = String(dig(path.slice(0, -5)) ?? '').trim();  // ...state_name reads ...state
    if (!st) die(`the ${kind} file has no ${v.slice(0, -5)}`, 2);
    return stateName(st);
  }
  const x = dig(path);
  if ((x == null || x === '') && /(^|\.)line2$/.test(path)) return '';
  if (x == null || typeof x === 'object' || (kind === 'contact' && x === '')) die(`the ${kind} file has no ${v}`, 2);
  return String(x);
}
const stateName = (st) => US_STATES[String(st ?? '').trim().toUpperCase()] || String(st ?? '').trim();  // already a name, or not a US code: as it is
// --contact: the person's email, phone, name and delivery address, from a JSON file only the owning account can read
//   {"email": "...", "phone": "...", "name": "...", "address": {"line1", "line2", "city", "state", "postal_code", "country"}}
// (first_name and last_name are optional: they default to the name's first and last word). It is opened without following a
// symlink and checked on the open file (a regular file, the account's own, mode 0600 or tighter), so it can't be swapped between the check
// and the read. Its values are typed only through $contact.* tokens, masked in page text (MASK) and looked for in every frame (scan).
export function contactOf(file) {
  if (!file) return null;
  let fd; try { fd = openSync(file, FS.O_RDONLY | FS.O_NOFOLLOW); }
  catch (e) { die(e.code === 'ELOOP' ? `--contact: ${file} is a symlink: give the file itself` : '--contact: no file ' + file, 2); }
  let j;
  try {
    const st = fstatSync(fd);
    if (!st.isFile()) die(`--contact: ${file} is not a plain file`, 2);
    if (st.uid !== process.getuid()) die(`--contact: ${file} is not this user's own file`, 2);
    if (st.mode & 0o077) die(`--contact: ${file} can be read by others: chmod 600 it`, 2);
    try { j = JSON.parse(readFileSync(fd, 'utf8')); } catch { die('--contact: ' + file + ' is not JSON', 2); }
  } finally { try { closeSync(fd); } catch {} }
  if (!j || typeof j !== 'object' || Array.isArray(j)) die('--contact: ' + file + ' is not a JSON object', 2);
  const t = (x) => (x == null || typeof x === 'object' ? '' : String(x).trim());
  const ad = j.address && typeof j.address === 'object' ? j.address : {}, name = t(j.name), parts = name.split(/\s+/).filter(Boolean);
  return { email: t(j.email), phone: t(j.phone), name, first_name: t(j.first_name) || parts[0] || '',
    last_name: t(j.last_name) || (parts.length > 1 ? parts[parts.length - 1] : ''),
    address: { line1: t(ad.line1), line2: t(ad.line2), city: t(ad.city), state: t(ad.state), postal_code: t(ad.postal_code), country: t(ad.country) } };
}
// The words the mask and the log compare: lowercase, every run of anything but a-z, 0-9, @ and letters past Latin-1 one space,
// padded with a space each side (MASK's nm does the same in the page), so "A.Name@example.com" and "a name@example com" are one value
export const normw = (s) => ' ' + String(s ?? '').toLowerCase().replace(/[^a-z0-9@À-￿]+/g, ' ').trim() + ' ';
const digitsOf = (s) => String(s ?? '').replace(/\D/g, '');
// "77 Mission Rock St" -> "77 mission": the house number and the street's first word, what any rewording of the street keeps
export const streetKey = (line1) => { const w = normw(line1).trim().split(' '); return w.length >= 2 && /^\d+[a-z]?$/.test(w[0]) ? w[0] + ' ' + w[1] : ''; };
const nameToks = (n) => normw(n).trim().split(' ').filter((x) => x.length >= 3);
// The person's values to keep off the recording: from the contact file, and in a real run the card's billing name and address.
// words: normalized (normw, unpadded), 3+ characters; digits: the phone's last 7 digits.
export function privates(card, contact) {
  const ba = card?.billing_address || {}, ad = contact?.address || {};
  const vals = [contact?.email, contact?.name, contact?.first_name, contact?.last_name, ...nameToks(contact?.name),
    ad.line1, ad.line2, ad.city, ad.postal_code, ad.state && stateName(ad.state), streetKey(ad.line1),
    ba.name, ...nameToks(ba.name), ba.line1, ba.line2, ba.city, ba.postal_code, streetKey(ba.line1)];
  const words = vals.map((v) => normw(v).trim()).filter((w) => w.length >= 3);
  const p = digitsOf(contact?.phone), digits = p.length >= 7 ? [p.slice(-7)] : [];
  return { words: [...new Set(words)], digits };
}
// In the OCR: the email, the phone's digits, the name and the street show. The city, state and zip alone are too common to count:
// they are masked in the page but not looked for.
export function contactLeaks(text, contact, card) {
  if (!contact && !card?.billing_address) return false;
  const ba = card?.billing_address || {}, ad = contact?.address || {}, cmp = (s) => String(s ?? '').toLowerCase().replace(/[^a-z0-9@À-￿]/g, '');
  const email = cmp(contact?.email), phone = digitsOf(contact?.phone).slice(-7);
  // a name: its first and last word on one line, in any order ("Tester, Jane"), or the whole name run together
  const names = [];
  const addName = (full, first, last) => {
    const w = normw(full).trim().split(' ').filter(Boolean), f = normw(first || w[0]).trim(), l = normw(last || (w.length > 1 ? w[w.length - 1] : '')).trim();
    if (f.length >= 2 && l.length >= 2) names.push({ f: ' ' + f + ' ', l: ' ' + l + ' ', whole: cmp(full) });
  };
  if (contact) addName(contact.name, contact.first_name, contact.last_name);
  if (ba.name) addName(ba.name);
  // a street: "77 Mission" as words, however the rest is spelled; a line1 with no house number as a whole
  const streets = [];
  for (const l1 of [ad.line1, ba.line1]) {
    const k = streetKey(l1);
    if (k) { const [n, w] = k.split(' '); streets.push(new RegExp(`(^|[^a-z0-9])${n}\\s+${w}([^a-z0-9]|$)`)); }
    else if (normw(l1).trim().length >= 6) streets.push(normw(l1));
  }
  let run = '';
  for (const line of text.split('\n')) {
    const lc = line.toLowerCase(), lw = normw(line), lcmp = cmp(line);
    if (email.length >= 6 && lcmp.includes(email)) return true;
    if (phone.length === 7) {
      if (digitsOf(line).includes(phone)) return true;
      if (/^[\s\d().+-]*$/.test(line)) { run += digitsOf(line); if (run.includes(phone)) return true; } else run = '';
    }
    for (const n of names) if ((lw.includes(n.f) && lw.includes(n.l)) || (n.whole.length >= 6 && lcmp.includes(n.whole))) return true;
    for (const st of streets) if (typeof st === 'string' ? lw.includes(st) : st.test(lc)) return true;
  }
  return false;
}
// --dry: placeholders for the address tokens, never the person's; the card's own fields are never typed
const SECRET = /^\$card\.(number|cvc|exp|exp_month|exp_year)$/;
const DRY = { billing_address: { name: 'Dry Run', line1: '123 Test Street', line2: '', city: 'New York', state: 'NY', postal_code: '10001', country: 'US' } };
// A dry run types placeholders for who and where; from a --contact file it takes only the city, state, zip and country, so the
// checkout quotes the person's own shipping and tax without their name, street, email or phone ever going in.
const DRY_CONTACT = { email: 'dry-run@example.com', phone: '212 555 0100', name: 'Dry Run', first_name: 'Dry', last_name: 'Run',
  address: { line1: '123 Test Street', line2: '', city: 'New York', state: 'NY', postal_code: '10001', country: 'US' } };
const secrets = (card) => card ? [String(card.number).replace(/\D/g, ''), String(card.cvc || '')].filter((s) => s.length >= 3) : [];
let PRIV = [];  // regexes for the person's contact values, set by run: printed as [contact]
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
export function privRes(hide, phone) {
  const res = [...hide.words].sort((x, y) => y.length - x.length)
    .map((w) => new RegExp('(?<![a-z0-9\\u00c0-\\uffff])' + w.split(' ').map(esc).join('[^a-z0-9@\\u00c0-\\uffff]+') + '(?![a-z0-9\\u00c0-\\uffff])', 'gi'));
  const p = digitsOf(phone);
  for (const d of p.length >= 7 ? [p, p.slice(-7)] : []) res.push(new RegExp(d.split('').join('[\\s().+-]*'), 'g'));
  return res;
}
export function clean(s, card) {  // anything we print: no card-length digit run, no value of this card, no contact value
  s = String(s).replace(/\b(\d[ -]?){12,19}\b/g, '[digits]');
  for (const x of secrets(card)) s = s.split(x).join('[card]');
  for (const re of PRIV) s = s.replace(re, '[contact]');
  return s;
}
export const setPriv = (res) => { PRIV = res; };  // for the tests

// --- the OCR check ---
const luhn = (d) => { let s = 0; for (let i = 0; i < d.length; i++) { let n = +d[d.length - 1 - i]; if (i % 2) { n *= 2; if (n > 9) n -= 9; } s += n; } return s % 10 === 0; };
export function leaks(text, card) {
  const num = card ? String(card.number).replace(/\D/g, '') : '';
  const mine = (d) => num.length >= 13 && (d.includes(num.slice(0, 12)) || d.includes(num.slice(-12)));
  // A number wrapped over lines ("4242 4242" / "4242 4242"): lines of only digits are joined (blank lines don't break the run).
  // Any joined run holding 12 digits of this card counts; a Luhn-valid 13-19 digit run counts only if built from card-shaped groups
  // of 4-6 digits, so a phone number next to a zip code isn't taken for a card.
  let run = '', groups = '';
  const broken = () => { const r = run, g = groups; run = groups = ''; return mine(r) || (g.length >= 13 && g.length <= 19 && luhn(g)); };
  for (const line of text.split('\n')) {
    for (const m of line.matchAll(/\d(?:[ -]?\d){12,18}/g)) { const d = m[0].replace(/\D/g, ''); if (d.length >= 13 && d.length <= 19 && luhn(d)) return true; }
    if (mine(line.replace(/\D/g, ''))) return true;
    if (!line.trim()) continue;
    if (/^[\d\s-]+$/.test(line)) {
      run += line.replace(/\D/g, '');
      if (line.trim().split(/[\s-]+/).every((x) => /^\d{4,6}$/.test(x))) groups += line.replace(/\D/g, '');
      else { if (groups.length >= 13 && groups.length <= 19 && luhn(groups)) return true; groups = ''; }
      if (mine(run)) return true;
      continue;
    }
    if (broken()) return true;
  }
  return broken();
}
function ocr(img) {
  return new Promise((ok) => {
    // one thread each: several tesseracts with OpenMP threads of their own fight over the cores and run far slower
    const p = spawn('tesseract', [img, '-', '--psm', '11'], { stdio: ['ignore', 'pipe', 'ignore'], env: { ...process.env, OMP_THREAD_LIMIT: '1' } }); let out = '';
    p.stdout.on('data', (d) => { out += d; }); p.on('close', (c) => ok(c === 0 ? out : null)); p.on('error', () => ok(null));
  });
}
// Each frame is read twice: as it is, and scaled 3x with everything darker than near-white turned black, which reads the small and
// faint grey text (an 11px #aaa "Paid with 4242 ...") that tesseract misses on the plain frame.
const SHARP = "scale=iw*3:ih*3:flags=lanczos,format=gray,lutyuv=y='if(lt(val,235),0,255)'";
export const scanDirs = new Set();  // removed on exit, so a killed scan leaves no frames behind
async function scan(files, card, contact = null) {  // throws on a frame it can't read: the caller deletes what it can't vouch for
  const work = mkdtempSync(join(process.env.TMPDIR || tmpdir(), 'h2buyscan-')); scanDirs.add(work); const imgs = [];
  try {
    for (const [n, f] of files.entries()) {
      const dir = join(work, String(n)); mkdirSync(dir);
      const video = ['.webm', '.mp4', '.mkv', '.mov'].includes(extname(f).toLowerCase());
      // mpdecimate keeps every frame that differs visibly from the last kept one (small changes count: hi=256 per 8x8 block)
      const ff = (vf, pat) => execFileSync('ffmpeg', ['-v', 'error', '-i', f, '-vf', video ? 'mpdecimate=hi=64*4:lo=64*2:frac=0,' + vf : vf,
        ...(video ? ['-fps_mode', 'vfr'] : ['-frames:v', '1']), join(dir, pat)], { stdio: ['ignore', 'ignore', 'pipe'] });
      ff('format=rgb24', video ? 'a%05d.png' : 'a.png'); ff(SHARP, video ? 'b%05d.png' : 'b.png');
      for (const x of readdirSync(dir).sort()) imgs.push({ img: join(dir, x), from: f });
      if (!imgs.some((x) => x.from === f)) throw new Error('no frames in ' + basename(f));
    }
    const hits = new Set(), personal = new Set(); let i = 0, unread = 0;
    await Promise.all(Array.from({ length: Math.max(2, Math.min(8, availableParallelism() >> 1)) }, async () => {
      while (i < imgs.length) { const it = imgs[i++]; const t = await ocr(it.img); if (t == null) unread++; else { if (leaks(t, card)) hits.add(it.from); if (contactLeaks(t, contact, card)) personal.add(it.from); } }
    }));
    if (unread) throw new Error('tesseract could not read ' + unread + ' frames: install tesseract');
    return { frames: imgs.length / 2, hits: [...hits], contact: [...personal] };
  } finally { rmSync(work, { recursive: true, force: true }); scanDirs.delete(work); }
}
process.on('exit', () => { for (const d of scanDirs) try { rmSync(d, { recursive: true, force: true }); } catch {} });

// --- run ---
function arg(a, k) { const i = a.indexOf(k); return i >= 0 ? a[i + 1] : undefined; }
export function hostOK(url, allow) {
  let u; try { u = new URL(url); } catch { return false; }
  const h = u.hostname.toLowerCase(), base = allow.toLowerCase().replace(/^www\./, '');
  return u.protocol === 'https:' && (h === base || h.endsWith('.' + base));
}
export const cents = (s) => { const m = /^\$?(\d+)(?:\.(\d{1,2}))?$/.exec(String(s).trim().replace(/,/g, '')); return m ? +m[1] * 100 + +(m[2] || '0').padEnd(2, '0') : NaN; };

// A frame the person can see: every iframe element on its way up is visible and bigger than a dot. Hidden frames (an invisible
// captcha whose body is minified script, a tracking pixel) hold numbers next to words like "total" that aren't on screen.
async function shown(f) {
  for (let x = f; x.parentFrame(); x = x.parentFrame()) {
    try {
      const el = await x.frameElement(), box = await el.boundingBox();
      if (!box || box.width < 20 || box.height < 10 || !(await el.isVisible())) return false;
    } catch { return false; }
  }
  return true;
}
async function pageText(page) {  // every shown frame's text, so a total drawn in a payment iframe counts too
  const parts = [];
  for (const f of page.frames()) { try { if (await shown(f)) parts.push(await f.evaluate(() => document.body ? document.body.innerText : '')); } catch {} }
  return parts.join('\n');
}
export function shows(text, total) {  // "37.63", "$37.63", "37,63" ... the amount as a number on the page, not a part of a longer one
  const [d, c] = (cents(total) / 100).toFixed(2).split('.');
  const dd = Number(d).toLocaleString('en-US');
  return new RegExp(`(^|[^\\d.,])(${d}|${dd.replace(/,/g, '[,.\\s]?')})[.,]${c}(?!\\d)`).test(text);
}

// --- what the page says the order costs ---
// Money on the page: an amount with cents ("37.63", "1,037.63", "37,63") or with a currency mark ("$45", "45 EUR").
const CODES = 'USD|EUR|GBP|CAD|AUD|NZD|JPY|CNY|INR|CHF|SEK|NOK|DKK|PLN|MXN|BRL|SGD|HKD|KRW|ZAR';
const MONEY = new RegExp(`(?<![\\w.,])(?:(US\\$|[A-Z]{1,2}\\$|\\$|€|£|¥|₹|₩|\\b(?:${CODES}))\\s?)?(\\d{1,3}(?:[,.\\u00a0\\u202f]\\d{3})+(?:[.,]\\d{2})?|\\d+(?:[.,]\\d{2})?)(?![\\d.,]?\\d)(?:\\s?(€|£|\\b(?:${CODES})\\b|kr|zł))?`, 'g');
const MARK = { '$': ['usd', 'cad', 'aud', 'nzd', 'sgd', 'hkd', 'mxn'], 'us$': ['usd'], 'c$': ['cad'], 'ca$': ['cad'], 'a$': ['aud'], 'au$': ['aud'],
  'nz$': ['nzd'], 's$': ['sgd'], 'hk$': ['hkd'], 'mx$': ['mxn'], 'r$': ['brl'], '€': ['eur'], '£': ['gbp'], '¥': ['jpy', 'cny'], '₹': ['inr'],
  '₩': ['krw'], 'kr': ['sek', 'nok', 'dkk'], 'zł': ['pln'] };
export function money(text) {
  const out = [];
  for (const m of text.matchAll(MONEY)) {
    const mark = (m[1] || m[3] || '').toLowerCase(), n = m[2], dec = /[.,]\d{2}$/.test(n) && !/^\d{1,3}([,.]\d{3})+$/.test(n);
    if (!mark && !dec) continue;
    const whole = (dec ? n.slice(0, -3) : n).replace(/\D/g, ''), c = +(whole || '0') * 100 + (dec ? +n.slice(-2) : 0);
    out.push({ cents: c, cur: mark ? (MARK[mark] || [mark]) : null, at: m.index, end: m.index + m[0].length });
  }
  return out;
}
const TOTAL = /\b(total|amount due|balance due|due today|due now|you pay|to pay|pay|charged?)\b/i, NOTSUB = /sub-?\s?total/i;
// The amounts the page labels as a total (not a subtotal): an amount whose own label (the text before it on its line, after any
// earlier amount) says total, amount due, pay ...; a label line with no amount takes the first amount on the next line.
export function totals(text) {
  const found = []; let carry = false;
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    const ms = money(line); let from = 0;
    if (carry && ms.length) { found.push(ms[0]); from = ms[0].end; ms.shift(); }
    carry = false;
    for (const m of ms) { const label = line.slice(from, m.at); if (TOTAL.test(label) && !NOTSUB.test(label)) found.push(m); from = m.end; }
    const tail = line.slice(from); carry = TOTAL.test(tail) && !NOTSUB.test(tail) && !money(tail).length;
  }
  return found;
}
// The checkout's verdict, before the order button and after every step once the card is in: refuse a price in another currency, a
// labelled total above the approved amount, and (at the button) a page whose labelled totals don't include the stated one.
export function judge(text, { max, total = null, currency = 'usd' }) {
  const all = money(text), foreign = all.find((m) => m.cur && !m.cur.includes(currency));
  if (foreign) return `the page shows a price in another currency (${text.slice(foreign.at, foreign.end).trim()}), not ${currency.toUpperCase()}`;
  const t = totals(text), over = t.find((m) => m.cents > max);
  if (over) return `the page shows a total of ${(over.cents / 100).toFixed(2)}, more than the approved ${(max / 100).toFixed(2)}`;
  if (total == null) return null;
  if (t.length) return t.some((m) => m.cents === total) ? null : `the page's total is ${t.map((m) => (m.cents / 100).toFixed(2)).join(' / ')}, not ${(total / 100).toFixed(2)}`;
  // no labelled total on the page: the stated total must show, and no amount on the page may be above the approved one
  if (!shows(text, (total / 100).toFixed(2))) return `the page doesn't show the total ${(total / 100).toFixed(2)}`;
  const big = all.find((m) => m.cents > max);
  return big ? `the page shows ${(big.cents / 100).toFixed(2)}, more than the approved ${(max / 100).toFixed(2)}` : null;
}

class Stop extends Error { constructor(why, code = 4) { super(why); this.code = code; } }
const ACTIVE = ['click', 'fill', 'type', 'select', 'check', 'press', 'place'];
// a step that can submit the order: a click, a key, a choice; or text with a newline in it, which presses Enter
const presses = (kind, s) => ['click', 'select', 'check', 'press', 'place'].includes(kind) || (['fill', 'type'].includes(kind) && /[\r\n]/.test(String(s.value)));
async function run(a) {
  const stepsFile = arg(a, '--steps'), out = arg(a, '--out'), allow = arg(a, '--allow-host'), max = arg(a, '--max-total');
  const currency = (arg(a, '--currency') || 'usd').toLowerCase(), dry = a.includes('--dry');
  if (dry && a.includes('--card')) die('--dry takes no --card: a dry run never holds a card', 2);
  if (!stepsFile || !out || !allow || (!max && !dry)) die('run needs --steps, --out, --allow-host and --max-total', 2);
  let steps; try { steps = JSON.parse(readFileSync(stepsFile, 'utf8')); } catch (e) { die('--steps: ' + e.message, 2); }
  if (!Array.isArray(steps) || !steps.length) die('--steps must be a JSON list of steps', 2);
  const card = dry ? null : cardOf(arg(a, '--card')), file = contactOf(arg(a, '--contact')), maxC = max ? cents(max) : null;
  // a dry run types DRY_CONTACT with the file's city, state, zip and country, and watches only those (its placeholders are no secret)
  const where = file && { city: file.address.city, state: file.address.state, postal_code: file.address.postal_code, country: file.address.country };
  const contact = dry ? { ...DRY_CONTACT, address: { ...DRY_CONTACT.address, ...(where ? Object.fromEntries(Object.entries(where).filter(([, v]) => v)) : {}) } } : file;
  const seen = dry ? (where ? { address: where } : null) : file;
  const hide = privates(card, seen); PRIV = privRes(hide, seen?.phone);
  if (max && !(maxC > 0)) die('--max-total must be an amount', 2);
  if (dry && !max && steps.some((s) => s && typeof s === 'object' && 'place' in s)) die('a dry run with a place step needs --max-total', 2);
  const valOf = (v) => dry && typeof v === 'string' && v.startsWith('$card.') ? (SECRET.test(v) ? '' : resolve(v, DRY)) : resolve(v, card, contact);
  const KNOWN = ['goto', 'click', 'fill', 'type', 'select', 'check', 'press', 'wait', 'waitFor', 'expect', 'shot', 'place'];
  const usesCard = steps.some((s) => typeof s?.value === 'string' && s.value.startsWith('$card.'));
  steps.forEach((s, n) => {
    if (!s || typeof s !== 'object' || Array.isArray(s)) die(`step ${n + 1}: each step is an object`, 2);
    const k = KNOWN.filter((x) => x in s);
    if (k.length !== 1) die(`step ${n + 1}: needs exactly one of ${KNOWN.join(', ')}`, 2);
    if (s.goto && !hostOK(s.goto, allow)) die(`step ${n + 1}: ${clean(s.goto)} is not https on ${allow}, the approved merchant`, 2);
    if ('place' in s && !(cents(s.total ?? max) <= maxC)) die(`step ${n + 1}: total ${s.total} is more than the approved ${max}`, 2);
    if ('optional' in s && !['click', 'check'].includes(k[0])) die(`step ${n + 1}: only a click or a check can be optional`, 2);
    if ('shot' in s && !/^[\w-]{1,40}$/.test(s.shot)) die(`step ${n + 1}: shot names are letters, digits, - and _`, 2);
    if ('in' in s && !(typeof s.in === 'string' || (Array.isArray(s.in) && s.in.length && s.in.every((x) => typeof x === 'string'))))
      die(`step ${n + 1}: "in" is an iframe selector, or a list of them for nested frames`, 2);
    valOf(s.value);  // a missing card field fails here, before Chrome starts, not halfway through the checkout
    for (const v of [s.value, s.goto, s.expect]) if (typeof v === 'string' && /\d{12}/.test(v.replace(/[ -]/g, ''))) die(`step ${n + 1}: a card number in the steps file; use $card.number`, 2);
  });
  // One card, one purchase: once a run has typed the card and then acted on the page, this buy's card is spent; a second run could
  // place a second order, so it is refused. (A run that stopped before acting after the card can be run again.)
  const used = join(out, 'card-used.json');
  if (!dry && (usesCard || steps.some((s) => 'place' in s)) && existsSync(used)) {
    let u = {}; try { u = JSON.parse(readFileSync(used, 'utf8')); } catch {}
    if (u.after || u.placed) die('an earlier run of this buy already entered the card and went on from there (' + (u.placed ? 'it pressed the order button' : u.after + ' steps after the card') + ') at ' + u.at + ': one card, one purchase. Check the merchant for an order; if there is none, hive buy cancel and ask again', 2);
  }
  mkdirSync(out, { recursive: true, mode: 0o700 });
  const { chromium } = playwright();
  // Everything is recorded into a staging dir and moves into DIR only after the scan finds no card number; on any failure, a kill
  // (SIGINT, SIGTERM, SIGHUP) or a crash the staging dir is deleted, so nothing unscanned is ever left behind.
  const stage = mkdtempSync(join(out, '.stage-')), wipe = () => { try { rmSync(stage, { recursive: true, force: true }); } catch {} };
  process.on('exit', wipe);
  for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.on(sig, () => { wipe(); console.error('hive buy record: killed; the unscanned recording is deleted'); process.exit(1); });
  let browser, ctx, page;
  try {
    browser = await chromium.launch({ executablePath: chrome(), headless: true, handleSIGINT: false, handleSIGTERM: false, handleSIGHUP: false,
      args: ['--disable-features=AutofillServerCommunication'] });
    ctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, recordVideo: { dir: stage, size: { width: 1280, height: 800 } } });
    await ctx.addInitScript(MASK(hide));
    const fx = process.env.HIVE_BUY_RECORD_FIXTURE;
    if (fx) await ctx.route(/^https:\/\/[^/]+\.test\//, (r) => {
      const u = new URL(r.request().url()); const f = join(fx, u.hostname, u.pathname.endsWith('/') ? u.pathname + 'index.html' : u.pathname);
      return existsSync(f) ? r.fulfill({ path: f }) : r.fulfill({ status: 404, body: 'no fixture' });
    });
    page = await ctx.newPage();
  } catch (e) { try { await browser?.close(); } catch {} wipe(); die(clean(e.message.split('\n')[0], card)); }
  const popups = []; ctx.on('page', (p) => { if (p !== page) { popups.push(p); console.log('a popup opened: ' + clean(hostOf(p.url()), card)); } });
  let host = '';
  page.on('framenavigated', (f) => { if (f === page.mainFrame()) { const h = hostOf(f.url()); if (h !== host) { host = h; console.log('  now at ' + clean(h, card)); } } });
  const shots = [], cardFields = []; let code = 0, why = '', cardIn = null, placeCheck = null, survey = null;
  const mark = (x) => { if (dry) return; cardIn = { ...cardIn, ...x }; writeFileSync(used, JSON.stringify(cardIn) + '\n', { mode: 0o600 }); };
  const check = async (opts) => {  // the total check reads twice, 700 ms apart: a total that is still changing isn't trusted
    const r1 = judge(await pageText(page), opts); if (r1) return r1;
    await page.waitForTimeout(700); return judge(await pageText(page), opts);
  };
  try {
    for (const [n, s] of steps.entries()) {
      const ins = s.in == null ? [] : [].concat(s.in);
      const at = ins.reduce((f, sel) => f.frameLocator(sel), page), loc = (sel) => at.locator(sel).first();
      const label = Object.entries(s).map(([k, v]) => `${k}=${typeof v === 'string' && /^\$(card|contact)\./.test(v) ? v : clean(JSON.stringify(v), card)}`).join(' ');
      console.log(`step ${n + 1}: ${label}`);
      const kind = KNOWN.find((x) => x in s), val = s.value, isCard = typeof val === 'string' && val.startsWith('$card.');
      if (ACTIVE.includes(kind)) {
        // act only on the approved merchant's https page (its card iframes may be on any host); after a link or a redirect off it, stop
        if (!hostOK(page.url(), allow)) throw new Stop(`the page is now ${clean(hostOf(page.url()) || page.url(), card)}, not https on ${allow}: stopped before step ${n + 1}`);
        // with the card in, every press may be the one that buys: check the totals before it, not only at the place step
        if (cardIn && kind !== 'place' && presses(kind, s)) { const bad = await check({ max: maxC, currency }); if (bad) throw new Stop(`${bad}: stopped before step ${n + 1}, nothing pressed`); }
      }
      if (cardIn && presses(kind, s)) mark({ after: (cardIn.after || 0) + 1 });
      if (isCard && (kind === 'fill' || kind === 'type')) {
        const el = loc(s[kind]); await el.waitFor({ state: 'attached', timeout: 30000 });
        let masked = false;
        for (let i = 0; i < 20 && !masked; i++) {
          masked = await el.evaluate((e) => (e instanceof HTMLInputElement && e.type === 'password') ||
            ((e instanceof HTMLInputElement || e instanceof HTMLTextAreaElement || e.isContentEditable) && getComputedStyle(e).webkitTextSecurity === 'disc'));
          if (!masked) await page.waitForTimeout(100);
        }
        if (dry) {  // record what the field is; a field the mask misses is a finding here, where a real run would stop
          const what = await el.evaluate((e) => ({ tag: e.tagName.toLowerCase(), type: e.getAttribute('type') || '', autocomplete: e.getAttribute('autocomplete') || '' }));
          const fr = await el.evaluate(() => location.origin), top = new URL(page.url()).origin;
          cardFields.push({ step: n + 1, token: s.value, selector: clean(s[kind]), masked, ...what, frame: clean(fr), cross_origin: fr !== top });
          console.log(`  dry: ${s.value} field is ${masked ? 'masked' : 'NOT masked (a real run stops here)'}, a ${what.tag}${fr !== top ? ' in a cross-origin frame (' + clean(hostOf(fr)) + ')' : ''}`);
          if (SECRET.test(val)) { await page.waitForTimeout(300); continue; }  // the card's own fields: nothing typed
        } else if (!masked) throw new Stop(`step ${n + 1}: ${clean(s[kind], card)} is not a text field the mask covers, so the card value would show: nothing typed`);
        if (!cardIn) mark({ at: new Date().toISOString(), after: 0 });
      }
      if (s.optional && !(await loc(s[kind]).waitFor({ state: 'visible', timeout: +s.optional > 1 ? Math.min(+s.optional, 30000) : 5000 }).then(() => true, () => false))) {
        console.log('  optional: not on the page, skipped'); continue;
      }
      if ('goto' in s) await page.goto(s.goto, { waitUntil: 'domcontentloaded' });
      else if ('click' in s) await loc(s.click).click();
      else if ('fill' in s) await loc(s.fill).fill(valOf(val));
      else if ('type' in s) await loc(s.type).pressSequentially(valOf(val), { delay: 40 });
      else if ('select' in s) await loc(s.select).selectOption(valOf(val));
      else if ('check' in s) await loc(s.check).check();
      else if ('press' in s) await (s.on ? loc(s.on).press(s.press) : page.keyboard.press(s.press));
      else if ('wait' in s) await page.waitForTimeout(Math.min(+s.wait || 0, 60000));
      else if ('waitFor' in s) await loc(s.waitFor).waitFor({ timeout: 30000 });
      else if ('expect' in s) { if (!(await pageText(page)).includes(s.expect)) throw new Stop(`the page doesn't show ${JSON.stringify(s.expect)}`); }
      else if ('shot' in s) { const p = join(stage, s.shot + '.png'); await page.screenshot({ path: p, fullPage: true }); shots.push(p); }
      else if ('place' in s && dry) {  // read the totals, press nothing, and end the dry run here
        const text = await pageText(page), bad = await check({ max: maxC, total: cents(s.total ?? max), currency });
        const found = await loc(s.place).count() > 0;
        placeCheck = { would_press: !bad && found, why: bad || (found ? null : 'the order button is not on the page'), totals: totals(text).map((m) => (m.cents / 100).toFixed(2)), button_found: found };
        console.log('  dry: at the order button: ' + (bad ? 'a real run would stop: ' + bad : 'the total check passes') + '; nothing pressed');
        break;
      }
      else if ('place' in s) {
        // the button first: a selector that doesn't match stops here with nothing pressed, not on a click timeout after marking it placed
        if (!(await loc(s.place).waitFor({ state: 'visible', timeout: 20000 }).then(() => true, () => false)))
          throw new Stop(`the order button (${clean(s.place, card)}) is not on the page: nothing pressed, the order was not placed.` +
            (cardIn ? ' The card was typed, so it is used (one card, one purchase): hive buy cancel the buy, then ask again' : ''));
        const total = cents(s.total ?? max), bad = await check({ max: maxC, total, currency });
        if (bad) throw new Stop(bad + ': the order was not placed. If the cart came out different or higher, hive buy cancel and ask again');
        if (cardIn) mark({ placed: true });
        await loc(s.place).click();
        // the charge the page reports afterwards: above the approved amount is a breach to report, with the recording kept
        await page.waitForTimeout(1000);
        const after = judge(await pageText(page), { max: maxC, currency });
        if (after) throw new Stop('after the order button ' + after + '. The order may be charged more than approved: check the order and say so in the thread', 5);
      }
      await page.waitForTimeout(300);  // let each step reach a frame
    }
  } catch (e) {
    code = e instanceof Stop ? e.code : 4; why = clean(e instanceof Stop ? e.message : e.message.split('\n')[0], card);
    // a stop after the order button was pressed: the order may exist, and a cancel would only hide it
    if (cardIn?.placed && code === 4) why += '. The order button WAS pressed, so the order may be placed: do not cancel; check the merchant for the order, run hive buy done with the receipt or the recording, and tell the person';
  }
  if (dry) try {  // what the last page is made of: which frames are cross-origin, canvas-drawn fields, payment fields' autocomplete
    const top = new URL(page.url()).origin; survey = [];
    for (const f of page.frames()) {
      let o = ''; try { o = new URL(f.url()).origin; } catch {}
      const r = await f.evaluate(() => {
        const pay = /card|cc-|cvc|cvv|csc|expir|exp-|security.?code/i;
        return { canvases: document.querySelectorAll('canvas').length, fields: [...document.querySelectorAll('input, select, textarea')]
          .filter((e) => pay.test([e.name, e.id, e.getAttribute('autocomplete'), e.getAttribute('aria-label'), e.getAttribute('placeholder')].join(' ')))
          .map((e) => ({ tag: e.tagName.toLowerCase(), type: e.getAttribute('type') || '', name: e.name || '', id: e.id || '', autocomplete: e.getAttribute('autocomplete') || '' })) };
      }).catch(() => null);
      survey.push({ frame: clean(hostOf(f.url()) || f.url().slice(0, 40)), cross_origin: o !== top, ...(r || { unreadable: true }) });
    }
  } catch (e) { survey = [{ error: clean(e.message.split('\n')[0]) }]; }
  const files = [];
  try {
    await page.waitForTimeout(800);
    const vids = [page, ...popups].map((p) => p.video());
    await ctx.close(); await browser.close();
    for (const [i, v] of vids.entries()) if (v) { const f = join(stage, i ? `popup-${i}.webm` : 'checkout.webm'); renameSync(await v.path(), f); files.push(f); }
    files.push(...shots);
  } catch (e) { wipe(); die('the recording failed (' + clean(e.message.split('\n')[0], card) + '); nothing is kept' + (code ? '. Before that: ' + why : ''), 1); }
  let r;
  try { r = await scan(files, card, seen); } catch (e) { wipe(); die('the OCR check failed (' + clean(e.message.split('\n')[0], card) + '): the unscanned recording is deleted' + (code ? '. Before that: ' + why : ''), 1); }
  if (r.hits.length) {
    wipe();
    die(`a card number showed in ${r.hits.map((f) => basename(f)).join(', ')}: the recording and screenshots are deleted. Do not post any of it; say so in the thread.` + (code ? ' Before that: ' + why : ''), 3);
  }
  if (r.contact.length) {
    wipe();
    die(`the person's contact details (email, phone, name or street) showed in ${r.contact.map((f) => basename(f)).join(', ')}: the recording and screenshots are deleted. Do not post any of it; say so in the thread.` + (code ? ' Before that: ' + why : ''), 3);
  }
  const kept = files.map((f) => { const to = join(out, basename(f)); renameSync(f, to); return to; }); wipe();
  console.log(JSON.stringify({ recording: kept[0], popups: kept.filter((f) => basename(f).startsWith('popup-')), shots: kept.filter((f) => f.endsWith('.png')),
    frames_scanned: r.frames, card_number_seen: false, contact_seen: false, stopped: code ? why : null, ...(dry ? { dry: true, card_fields: cardFields, place_check: placeCheck, survey } : {}) }));
  if (code) die('stopped: ' + why, code);
}
const hostOf = (u) => { try { return new URL(u).host; } catch { return ''; } };

const a = process.argv.slice(2), main = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;  // importable, for the tests
if (!main) {}
else if (a[0] === 'run') await run(a.slice(1));
else if (a[0] === 'scan') {
  const card = cardOf(arg(a, '--card')), contact = contactOf(arg(a, '--contact'));
  const files = a.slice(1).filter((x, i, l) => !['--card', '--contact'].includes(x) && !['--card', '--contact'].includes(l[i - 1]));
  if (!files.length) die('scan FILE...', 2);
  for (const f of files) if (!existsSync(f) || !statSync(f).isFile()) die('no file ' + f, 2);
  let r; try { r = await scan(files, card, contact); } catch (e) { die(e.message); }
  console.log(JSON.stringify({ frames_scanned: r.frames, card_number_seen: r.hits.length > 0, in: r.hits.map((f) => basename(f)),
    contact_seen: r.contact.length > 0, contact_in: r.contact.map((f) => basename(f)) }));
  process.exit(r.hits.length || r.contact.length ? 3 : 0);
} else die('usage: buy_record.mjs run [--dry] --steps F --card F [--contact F] --out DIR --allow-host HOST --max-total N | scan [--card F] [--contact F] FILE...', 2);
