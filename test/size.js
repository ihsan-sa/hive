// Prints the size of each download in dist/ and fails if any is over the cap (default 250 MB).
const fs = require('fs'), path = require('path');
const dir = process.argv[2] || 'dist', cap = (+process.env.HIVE_SIZE_CAP_MB || 250) * 1024 * 1024;
const re = /\.(exe|dmg|zip|deb|AppImage)$/;
let files = [], bad = 0;
try { files = fs.readdirSync(dir).filter(f => re.test(f)); } catch { }
if (!files.length) { console.error('no downloads in ' + dir); process.exit(1); }
for (const f of files) {
  const n = fs.statSync(path.join(dir, f)).size, mb = (n / 1048576).toFixed(1);
  if (n > cap) { bad++; console.error(`FAIL size ${f} ${mb} MB over ${cap / 1048576} MB`); }
  else console.log(`ok size ${f} ${mb} MB`);
}
process.exit(bad ? 1 : 0);
