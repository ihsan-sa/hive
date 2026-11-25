// Loads page/download.html's script and checks it picks the right button for each system's user-agent.
const fs = require('fs'), path = require('path'), vm = require('vm'), assert = require('assert');
const html = fs.readFileSync(path.join(__dirname, '..', 'page', 'download.html'), 'utf8');
for (const id of ['btn-win', 'btn-mac', 'btn-mac-intel', 'btn-linux']) assert(html.includes(`id="${id}"`), id);
assert(/Run anyway/.test(html) && /Open Anyway/.test(html), 'help steps present');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function run(ua, platform, gpu = '') {
  const btns = [['win'], ['mac', 'arm64'], ['mac', 'x64'], ['linux']].map(([os, chip]) => ({ dataset: chip ? { os, chip } : { os }, className: 'btn' }));
  const note = { textContent: '' };
  const doc = { querySelectorAll: () => btns, getElementById: () => note };
  const ctx = { document: undefined, navigator: undefined };
  vm.createContext(ctx);
  vm.runInContext(script, ctx);
  const os = ctx.pick(doc, { userAgent: ua, platform, gpu });
  return { os, picked: btns.filter(b => b.className.includes('pick')).map(b => b.dataset.os + (b.dataset.chip ? '-' + b.dataset.chip : '')), note: note.textContent };
}
const cases = [
  ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120', 'Win32', 'win'],
  ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605', 'MacIntel', 'mac-arm64', 'Apple GPU'],
  ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120', 'MacIntel', 'mac-arm64', 'ANGLE (Apple, ANGLE Metal Renderer: Apple M2, Unspecified Version)'],
  ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120', 'MacIntel', 'mac-x64', 'ANGLE (Intel Inc., Intel(R) Iris(TM) Plus Graphics 655, OpenGL 4.1)'],
  ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120', 'MacIntel', 'mac-x64', 'AMD Radeon Pro 5500M OpenGL Engine'],
  ['Mozilla/5.0 (X11; Linux x86_64) Firefox/120', 'Linux x86_64', 'linux'],
];
for (const [ua, pf, want, gpu] of cases) {
  const r = run(ua, pf, gpu);
  assert.deepStrictEqual(r.picked, [want], `${want}: ${JSON.stringify(r)}`);
}
const phone = run('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)', 'iPhone');
assert.deepStrictEqual(phone.picked, []); assert(phone.note);
console.log('ok page: Windows, Mac (Apple chip or Intel) and Linux each get their button; a phone gets none');

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

{
  // The update notice opens the page at #update: it must say how to update on each system, in the ways the runners
  // proved (Windows: the new installer even with the app open; Mac: quit, drag over, Replace; Linux: apt over the old one).
  const html = require('fs').readFileSync(require('path').join(__dirname, '../page/download.html'), 'utf8');
  assert(/id="update"/.test(html), 'an #update part');
  const line = id => (html.match(new RegExp(`<li id="${id}">([\\s\\S]*?)</li>`)) || [])[1] || '';
  const w = line('update-win'), m = line('update-mac'), l = line('update-linux');
  assert(/Hive-Setup\.exe/.test(w) && /stay open/.test(w), 'Windows: run the new Hive-Setup.exe, the app may stay open');
  assert(/quit Hive/i.test(m) && /Applications/.test(m) && /Replace/.test(m), 'Mac: quit, drag into Applications, Replace');
  assert(/sudo apt install \.\/Hive-linux-amd64\.deb/.test(l) && /keeps working/.test(l), 'Linux: apt install over the old one');
  const main = require('fs').readFileSync(require('path').join(__dirname, '../main.js'), 'utf8');
  assert(/on\('click', \(\) => \{ external\(lib\.downloadPage\('update'\)\)/.test(main), 'the update notice opens #update');
  assert(/ipcMain\.on\('download'[^\n]*downloadPage\('update'\)/.test(main), "the setup screen's newer-app link opens #update");
  console.log('ok page update lines');
}

{
  // The .deb installs only with apt on an Intel or AMD PC: the Linux help must say so and send everyone else
  // (Fedora, Arch, ARM) to the install line, which installs with apt, dnf or pacman.
  const html = require('fs').readFileSync(require('path').join(__dirname, '../page/download.html'), 'utf8');
  const help = (html.match(/<details id="help-linux">([\s\S]*?)<\/details>/) || [])[1] || '';
  assert(/Ubuntu/.test(help) && /Debian/.test(help) && /apt/.test(help) && /Intel or AMD/.test(help), 'Linux help: who the .deb is for');
  assert(/Fedora/.test(help) && /Arch/.test(help) && /ARM/.test(help) && /curl -fsSL https:\/\/hivelab\.ca\/install\.sh \| sh/.test(help), 'Linux help: the install line for everyone else');
  // Double-click doesn't install a .deb everywhere (Ubuntu 23.10 and some later versions say there is no app for
  // "Debian package" files): the help must name that message and give the apt line for it.
  assert(/no app installed for "Debian package" files/.test(help) && /sudo apt install \.\/Hive-linux-amd64\.deb/.test(help), 'Linux help: what to do when double-click finds no app');
  console.log('ok page linux who');
}

{
  // The site's /app-version.json goes up with the downloads: it must name the version they carry (build-all.sh writes it).
  const fs = require('fs'), path = require('path');
  const site = JSON.parse(fs.readFileSync(path.join(__dirname, '../page/app-version.json'), 'utf8')).version;
  const pkg = require('../package.json').version;
  assert.strictEqual(site, pkg, `page/app-version.json says ${site}, package.json ${pkg}: run build-all.sh or fix the file`);
  console.log('ok app-version matches');
}

{
  // Every download the page links is a file a build makes (names from package.json's artifactName rules), and every
  // system's file is offered. The Mac had one link to "Hive-mac.dmg", which nothing builds: a person would have got a 404.
  const fs = require('fs'), path = require('path');
  const pkg = require('../package.json'), b = pkg.build;
  const made = new Set();
  const name = (tpl, os, arch, ext) => tpl.replace('${os}', os).replace('${arch}', arch).replace('${ext}', ext);
  for (const t of b.win.target) if (t.target === 'nsis') made.add(name(b.nsis.artifactName || b.artifactName, 'win', 'x64', 'exe'));
  for (const t of b.mac.target) for (const a of t.arch) made.add(name(b.artifactName, 'mac', a, t.target));
  for (const t of b.linux.target) for (const a of t.arch) made.add(name(b.artifactName, 'linux', t.target === 'deb' ? { x64: 'amd64' }[a] : { x64: 'x86_64' }[a], t.target));
  const html = fs.readFileSync(path.join(__dirname, '../page/download.html'), 'utf8');
  const links = [...html.matchAll(/href="downloads\/([^"]+)"/g)].map(m => m[1]);
  for (const l of links) assert(made.has(l), `the page links downloads/${l}, which no build makes (builds make: ${[...made].join(', ')})`);
  for (const f of made) if (!f.endsWith('.AppImage')) assert(links.includes(f), `the build makes ${f}, which the page does not offer`);
  console.log(`ok download names: ${links.join(', ')}`);
}
