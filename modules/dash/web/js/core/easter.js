// More easter eggs (the person, 11041609428b). Display only, nothing is sent.
// Konami code: hex rain. /party, /bees, /matrix, /rave (see run() below, called by the composer). Double-click an avatar: spin.
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
// /rave (the person, 110423406aef): dark theme, the page spins and pulses, neon lasers and strobes, and the Axel F hook
// (Crazy Frog style) synthesised with Web Audio: the recording can't be shipped and the CSP loads nothing from elsewhere.
// 15 s; click or Esc stops it. Opt-in by typing it, so it ignores reduced motion (the first cut honoured it and didn't spin).
let raving = null;
const AXEL = (() => {   // [midi, sixteenths]; 0 = rest. F minor, 117 bpm-ish.
  const r = [[65,4],[68,3],[65,2],[65,1],[70,2],[65,2],[63,2],[65,4],[72,3],[65,2],[65,1],[73,2],[72,2],[68,2],
    [65,2],[72,2],[77,2],[65,1],[63,2],[63,1],[60,2],[67,2],[65,6],[0,4]];
  return r;
})();
function axelF(ac, out) {
  const s16 = 60 / 132 / 4; let t = ac.currentTime + .05, stop = false;
  const note = (m, d, at) => {
    const f = 440 * 2 ** ((m - 69) / 12), o = ac.createOscillator(), o2 = ac.createOscillator(), g = ac.createGain();
    o.type = "square"; o2.type = "sawtooth"; o.frequency.value = f; o2.frequency.value = f * 1.005;
    o.frequency.setValueAtTime(f * 1.06, at); o.frequency.exponentialRampToValueAtTime(f, at + .03);   // the "ring-ding" chirp
    g.gain.setValueAtTime(0, at); g.gain.linearRampToValueAtTime(.16, at + .01); g.gain.exponentialRampToValueAtTime(.001, at + d * .95);
    o.connect(g); o2.connect(g); g.connect(out); o.start(at); o2.start(at); o.stop(at + d); o2.stop(at + d);
  };
  const kick = at => { const o = ac.createOscillator(), g = ac.createGain(); o.frequency.setValueAtTime(140, at); o.frequency.exponentialRampToValueAtTime(40, at + .12);
    g.gain.setValueAtTime(.5, at); g.gain.exponentialRampToValueAtTime(.001, at + .15); o.connect(g); g.connect(out); o.start(at); o.stop(at + .16); };
  const bar = () => { const len = AXEL.reduce((n, x) => n + x[1], 0);
    for (let i = 0; i < len; i += 4) kick(t + i * s16);
    for (const [m, d] of AXEL) { if (m) note(m, d * s16, t); t += d * s16; } };
  const loop = () => { if (stop) return; while (t < ac.currentTime + 2) bar(); setTimeout(loop, 500); };
  loop(); return () => { stop = true; };
}
function rave() {
  if (raving) return;
  const root = document.documentElement, oldTheme = root.dataset.theme;
  root.dataset.theme = "dark";
  style(`@keyframes ez-rspin{0%{transform:rotate(0) scale(1)}25%{transform:rotate(90deg) scale(.75)}50%{transform:rotate(180deg) scale(1.05)}75%{transform:rotate(270deg) scale(.75)}100%{transform:rotate(360deg) scale(1)}}
@keyframes ez-rhue{0%{filter:hue-rotate(0) saturate(3) contrast(1.3)}100%{filter:hue-rotate(360deg) saturate(3) contrast(1.3)}}
html.ez-rave{overflow:hidden;background:#000}html.ez-rave body{overflow:hidden;transform-origin:50vw 50vh;animation:ez-rspin 3s linear infinite,ez-rhue .6s linear infinite!important}`);
  root.classList.add("ez-rave");
  // overlays sit on <html>, outside the spinning body, so the lasers stay put while the page turns under them
  const strobe = $("div", FX + "inset:0;background:#fff;opacity:0"), c = $("canvas", FX + "inset:0;width:100vw;height:100vh;mix-blend-mode:screen");
  c.width = innerWidth; c.height = innerHeight; root.append(c, strobe);
  const g = c.getContext("2d"), cols = ["#ff00ff", "#00ffff", "#39ff14", "#ff073a", "#fff01f", "#bc13fe", "#00f0ff"];
  let ac = null, mute = () => {};
  try { ac = new (window.AudioContext || window.webkitAudioContext)(); const m = ac.createGain(); m.gain.value = .7; m.connect(ac.destination); ac.resume(); mute = axelF(ac, m); } catch (e) { /* no audio: still rave */ }
  const t0 = performance.now(); let raf = 0, last = 0;
  const frame = now => {
    const t = (now - t0) / 1000, W = c.width, H = c.height;
    g.globalCompositeOperation = "source-over"; g.fillStyle = "rgba(0,0,0,.25)"; g.fillRect(0, 0, W, H);   // trails
    g.globalCompositeOperation = "lighter"; g.lineCap = "round";
    const origins = [[0, 0], [W, 0], [0, H], [W, H], [W / 2, H], [W / 2, 0]];
    origins.forEach(([ox, oy], k) => {
      for (let i = 0; i < 6; i++) {
        const base = Math.atan2(H / 2 - oy, W / 2 - ox), a = base + Math.sin(t * (1.5 + k * .4) + i * .9) * .9;
        const col = cols[(i + k + Math.floor(t * 4)) % cols.length], x2 = ox + Math.cos(a) * (W + H), y2 = oy + Math.sin(a) * (W + H);
        g.strokeStyle = col; g.shadowColor = col; g.shadowBlur = 30; g.lineWidth = 6;
        g.beginPath(); g.moveTo(ox, oy); g.lineTo(x2, y2); g.stroke();
        g.strokeStyle = "#fff"; g.shadowBlur = 0; g.lineWidth = 1.5; g.beginPath(); g.moveTo(ox, oy); g.lineTo(x2, y2); g.stroke();   // hot core
      }
    });
    const pulse = .5 + .5 * Math.sin(t * 2 * Math.PI * 2.2);   // glow pool in the middle on the beat
    const rg = g.createRadialGradient(W / 2, H / 2, 0, W / 2, H / 2, Math.max(W, H) * .6);
    rg.addColorStop(0, cols[Math.floor(t * 2) % cols.length] + Math.round(pulse * 120).toString(16).padStart(2, "0")); rg.addColorStop(1, "#0000");
    g.fillStyle = rg; g.fillRect(0, 0, W, H);
    if (now - last > 70) { last = now; const on = strobe.style.opacity === "0";
      strobe.style.background = Math.random() < .45 ? "#fff" : cols[(Math.random() * cols.length) | 0]; strobe.style.opacity = on ? (.55 + Math.random() * .4) : "0"; }
    raf = requestAnimationFrame(frame);
  };
  raf = requestAnimationFrame(frame);
  const stop = () => {
    cancelAnimationFrame(raf); root.classList.remove("ez-rave"); strobe.remove(); c.remove(); mute(); if (ac) ac.close();
    if (oldTheme) root.dataset.theme = oldTheme; else delete root.dataset.theme;
    removeEventListener("keydown", onKey, true); removeEventListener("click", stop, true); clearTimeout(tm); raving = null;
  };
  const onKey = e => { if (e.key === "Escape") stop(); };
  addEventListener("keydown", onKey, true); later(() => addEventListener("click", stop, true), 300);
  const tm = setTimeout(stop, 15000); raving = stop;
}
const CMDS = { party, bees, matrix, rave };
/** The composer calls this with the trimmed text; true if it was an egg (and so must not be sent). */
export function run(text) { const m = /^\/(party|bees|matrix|rave)$/.exec(text); if (!m) return false; CMDS[m[1]](); return true; }

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
export function logoClicks() { const t = Date.now(); logoN = logoN.filter(x => t - x < 1500); logoN.push(t); if (logoN.length >= 3) { logoN = []; angryBee(); return true; } return false; }

export function initEaster() {
  const hr = () => { if (location.hash === "#rave") { history.replaceState(null, "", location.pathname + location.search); rave(); } }; addEventListener("hashchange", hr); later(hr, 500);
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
