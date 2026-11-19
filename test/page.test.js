// Loads page/download.html's script and checks it picks the right button for each system's user-agent.
const fs = require('fs'), path = require('path'), vm = require('vm'), assert = require('assert');
const html = fs.readFileSync(path.join(__dirname, '..', 'page', 'download.html'), 'utf8');
for (const id of ['btn-win', 'btn-mac', 'btn-linux']) assert(html.includes(`id="${id}"`), id);
assert(/Run anyway/.test(html) && /Open Anyway/.test(html), 'help steps present');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function run(ua, platform) {
  const btns = ['win', 'mac', 'linux'].map(os => ({ dataset: { os }, className: 'btn' }));
  const note = { textContent: '' };
  const doc = { querySelectorAll: () => btns, getElementById: () => note };
  const ctx = { document: undefined, navigator: undefined };
  vm.createContext(ctx);
  vm.runInContext(script, ctx);
  const os = ctx.pick(doc, { userAgent: ua, platform });
  return { os, picked: btns.filter(b => b.className.includes('pick')).map(b => b.dataset.os), note: note.textContent };
}
const cases = [
  ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120', 'Win32', 'win'],
  ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605', 'MacIntel', 'mac'],
  ['Mozilla/5.0 (X11; Linux x86_64) Firefox/120', 'Linux x86_64', 'linux'],
];
for (const [ua, pf, want] of cases) {
  const r = run(ua, pf);
  assert.deepStrictEqual(r.picked, [want], `${want}: ${JSON.stringify(r)}`);
}
const phone = run('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)', 'iPhone');
assert.deepStrictEqual(phone.picked, []); assert(phone.note);
console.log('ok page: Windows, Mac and Linux each get their button; a phone gets none');

// The setup screen follows the system's colour scheme: every colour is a variable, and a light set is given for prefers-color-scheme: light.
{
  const setup = fs.readFileSync(path.join(__dirname, '..', 'setup.html'), 'utf8');
  const css = setup.match(/<style>([\s\S]*?)<\/style>/)[1];
  assert(/color-scheme:\s*dark light/.test(css), 'both schemes declared');
  const light = css.match(/prefers-color-scheme:\s*light\)\s*\{\s*:root\s*\{([^}]*)\}/);
  assert(light, 'a light set exists');
  const vars = n => new Set([...n.matchAll(/(--[a-z]+):/g)].map(x => x[1]));
  const dark = css.match(/:root\s*\{([^}]*)\}/)[1];
  assert.deepStrictEqual([...vars(light[1])].sort(), [...vars(dark)].sort(), 'light sets every colour the dark one does');
  const rest = css.replace(/button\.main[^\n]*/, '').replace(/:root\s*\{[^}]*\}/g, '').replace(/@media[^{]*\{\s*\}/g, '');
  assert(!/#(?:111|eee|c8c8c8|999|444|1c1c1c|2a2a2a|000|cfcfcf|555|fff)\b/i.test(rest), 'no fixed dark colours left outside the variables');
  console.log('ok scheme');
}

{
  const html = require('fs').readFileSync(require('path').join(__dirname, '../page/download.html'), 'utf8');
  assert(!/ihsan/.test(html), 'the page names the site by its real name');
  for (const f of ['install.sh', 'install.ps1', 'install-mac.sh']) assert(html.includes('https://hivelab.ca/' + f), f + ' line');
  console.log('ok page install lines');
}
