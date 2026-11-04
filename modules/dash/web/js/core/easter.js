// More easter eggs (the person, 11041609428b). Display only, nothing is sent.
// Konami code: hex rain. /party, /bees, /matrix (see run() below, called by the composer). Double-click an avatar: spin.
// Typing "hive" three times in a row in the composer: honeycomb behind the page. Logo clicked 3 times quickly: a swarm of bees chases the cursor for 5 s.
const $ = (t, css, txt) => { const e = document.createElement(t); if (css) e.style.cssText = css; if (txt) e.textContent = txt; return e; };
const FX = "position:fixed;pointer-events:none;z-index:99999;";
let styleEl;
function style(css) { if (!styleEl) { styleEl = $("style"); document.head.append(styleEl); } styleEl.textContent += css; }
style(`@keyframes ez-fall{to{transform:translateY(110vh) rotate(360deg)}}@keyframes ez-spin{to{transform:rotate(720deg) scale(1.3)}}
@keyframes ez-hue{to{filter:hue-rotate(360deg)}}
body.ez-hue{animation:ez-hue 1s linear infinite}.ez-spin{animation:ez-spin .8s ease-in-out}
@media (prefers-reduced-motion:reduce){body.ez-hue{animation-duration:6s}}`);
const later = (fn, ms) => setTimeout(fn, ms);
/** Items falling from the top: each is [char, colour]. */
function rain(chars, n, ms, css = "") {
  for (let i = 0; i < n; i++) {
    const e = $("div", FX + `top:-8vh;left:${Math.random() * 100}vw;font-size:${18 + Math.random() * 26}px;${css}animation:ez-fall ${ms * (.5 + Math.random() * .5)}ms linear ${Math.random() * ms * .4}ms forwards`, chars[i % chars.length]);
    document.body.append(e); later(() => e.remove(), ms * 1.6);
  }
}
function hex() { rain(["⬡", "⬢"], 60, 3500, "color:#f5b025;"); document.body.classList.add("ez-hue"); later(() => document.body.classList.remove("ez-hue"), 4000); }
function party() { rain(["🎉", "🎊", "✨", "🟣", "🟡", "🔵"], 70, 3000); document.body.classList.add("ez-hue"); later(() => document.body.classList.remove("ez-hue"), 4000); }
function bees() {
  for (let i = 0; i < 14; i++) {
    const y = 8 + Math.random() * 80, e = $("div", FX + `left:-40px;top:${y}vh;font-size:24px;transition:transform ${3 + Math.random() * 2}s ease-in-out`, "🐝");
    document.body.append(e); later(() => { e.style.transform = `translate(${innerWidth + 80}px,${(Math.random() - .5) * 160}px) scaleX(-1)`; }, 50 + i * 120);
    later(() => e.remove(), 7000);
  }
}
function matrix() {
  const c = $("canvas", FX + "inset:0;width:100vw;height:100vh;opacity:.85;background:#000"); c.width = innerWidth; c.height = innerHeight; document.body.append(c);
  const g = c.getContext("2d"), cols = Math.floor(c.width / 16), y = Array(cols).fill(0).map(() => Math.random() * -40);
  const t = setInterval(() => {
    g.fillStyle = "rgba(0,0,0,.08)"; g.fillRect(0, 0, c.width, c.height); g.fillStyle = "#0f0"; g.font = "16px monospace";
    y.forEach((v, i) => { g.fillText(String.fromCharCode(0x30A0 + Math.random() * 96), i * 16, v * 16); y[i] = v * 16 > c.height && Math.random() > .975 ? 0 : v + 1; });
  }, 50);
  later(() => { clearInterval(t); c.remove(); }, 5000);
}
const CMDS = { party, bees, matrix };
/** The composer calls this with the trimmed text; true if it was an egg (and so must not be sent). */
export function run(text) { const m = /^\/(party|bees|matrix)$/.exec(text); if (!m) return false; CMDS[m[1]](); return true; }

function honeycomb() {
  const cell = "<svg xmlns='http://www.w3.org/2000/svg' width='56' height='97'><path d='M28 2l26 15v30L28 62 2 47V17zM0 64l28 16v17M56 64L28 80' fill='none' stroke='%23f5b025' stroke-width='2'/></svg>";
  const e = $("div", FX + `inset:0;z-index:0;opacity:0;transition:opacity .6s;background:url("data:image/svg+xml,${cell.replace(/#/g, "%23")}")`);
  document.body.append(e); requestAnimationFrame(() => { e.style.opacity = ".35"; });
  later(() => { e.style.opacity = "0"; later(() => e.remove(), 700); }, 4000);
}
let mx = 0, my = 0;
function angryBee() {
  const end = performance.now() + 5000;
  const bs = Array.from({ length: 12 }, () => {
    const e = $("div", FX + "font-size:26px", "🐝"); document.body.append(e);
    return { e, x: Math.random() * innerWidth, y: Math.random() * innerHeight, k: .03 + Math.random() * .07, j: 6 + Math.random() * 16 };
  });
  const step = now => {
    if (now > end) { bs.forEach(b => b.e.remove()); return; }
    for (const b of bs) {
      b.x += (mx - b.x) * b.k + (Math.random() - .5) * b.j; b.y += (my - b.y) * b.k + (Math.random() - .5) * b.j;
      b.e.style.transform = `translate(${b.x - 13}px,${b.y - 13}px) scaleX(${mx < b.x ? 1 : -1})`;
    }
    requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}
let logoN = [];
/** Called on each logo click. */
export function logoClicks() { const t = Date.now(); logoN = logoN.filter(x => t - x < 1500); logoN.push(t); if (logoN.length >= 3) { logoN = []; angryBee(); } }

export function initEaster() {
  const K = "ArrowUp ArrowUp ArrowDown ArrowDown ArrowLeft ArrowRight ArrowLeft ArrowRight b a".split(" "); let ki = 0, typed = "";
  addEventListener("keydown", e => {
    ki = e.key.toLowerCase() === K[ki].toLowerCase() ? ki + 1 : (e.key === "ArrowUp" ? 1 : 0);
    if (ki === K.length) { ki = 0; hex(); }
  });
  addEventListener("input", e => {
    if (!(e.target.classList && e.target.classList.contains("cp-t"))) return;
    typed = /(?:hive[\s\S]*){3}$/i.test(e.target.value) && (e.target.value.match(/hive/gi) || []).length === 3 && /hive\W*$/i.test(e.target.value) ? "x" : "";
    if (typed) honeycomb();
  });
  addEventListener("mousemove", e => { mx = e.clientX; my = e.clientY; }, { passive: true });
  addEventListener("dblclick", e => { const a = e.target.closest && e.target.closest(".av"); if (!a) return; a.classList.remove("ez-spin"); void a.offsetWidth; a.classList.add("ez-spin"); later(() => a.classList.remove("ez-spin"), 900); });
}
