// The guided tour: a floating window with a drawing of the deck. It lights a control (on the drawing and on the real deck), waits for it, and walks the dashboard.
// While it runs, only the control it asks for reaches the page. So a stray press does nothing, and the practice Kill window can only be cancelled.
import { go, parse } from "../core/router.js";
import { can } from "../core/store.js";
import { closePanel, panelOpen } from "../ui/panel.js";
import { canChange, D, disarm, runningAgents, setTour, tabs, watch } from "./control.js";
import { LED } from "./flx4.js";

const SEEN = "h2.deck.toured";
const svgNS = 'xmlns="http://www.w3.org/2000/svg"';

// ---- the drawing: a schematic of the FLX4, not to scale. Every control a step can ask for has a data-k. ----
const box = (k, x, y, w, h, label, r = 3) => `<g class="dt-c" data-k="${k}"><rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${r}"/>${label ? `<text x="${x + w / 2}" y="${y + h / 2 + 3}">${label}</text>` : ""}</g>`;
const dot = (k, cx, cy, r, label) => `<g class="dt-c" data-k="${k}"><circle cx="${cx}" cy="${cy}" r="${r}"/>${label ? `<text x="${cx}" y="${cy + 3}">${label}</text>` : ""}</g>`;
const dim = (x, y, label) => `<g class="dt-dim"><circle cx="${x}" cy="${y}" r="7"/><text x="${x}" y="${y + 17}">${label}</text></g>`;
function deckSide(s, x) {
  const pads = Array.from({ length: 8 }, (_, i) => box(`pad${s}${i}`, x + 62 + (i % 4) * 41, 228 + (i >> 2) * 32, 37, 28, String(i + 1))).join("");
  return box(`loopIn${s}`, x + 8, 10, 26, 16, "IN") + box(`loopOut${s}`, x + 37, 10, 26, 16, "OUT") + box(`reloop${s}`, x + 66, 10, 26, 16, "EXIT")
    + box(`callPrev${s}`, x + 101, 10, 22, 16, "◀") + box(`callNext${s}`, x + 126, 10, 22, 16, "▶")
    + box(`sync${s}`, x + 188, 10, 38, 16, "SYNC")
    + `<g class="dt-c" data-k="jog${s}"><circle cx="${x + 92}" cy="112" r="70"/><circle class="dt-in" cx="${x + 92}" cy="112" r="42"/><text x="${x + 92}" y="115">JOG</text></g>`
    + `<g class="dt-c" data-k="tempo${s}"><rect x="${x + 197}" y="44" width="18" height="136" rx="3"/><text x="${x + 206}" y="193">TEMPO</text></g>`
    + box(`shift${s}`, x + 8, 200, 44, 18, "SHIFT")
    + dot(`cue${s}`, x + 30, 243, 17, "CUE") + dot(`play${s}`, x + 30, 279, 16, "▶‖")
    + box("hotcue" + s, x + 62, 207, 37, 16, "HOT") + box("beatloop" + s, x + 103, 207, 37, 16, "LOOP") + box("beatjump" + s, x + 144, 207, 37, 16, "JUMP") + box("sampler" + s, x + 185, 207, 37, 16, "SMPL")
    + pads;
}
const DRAWING = `<svg ${svgNS} viewBox="0 0 660 302" class="dt-deck" role="img" aria-label="Drawing of the DDJ-FLX4 with the control to use lit">
  <rect class="dt-body" x="1" y="1" width="658" height="300" rx="10"/>
  ${deckSide("L", 6)}
  ${deckSide("R", 424)}
  ${box("load1", 241, 12, 38, 16, "LOAD 1")}<g class="dt-c" data-k="browse"><circle cx="300" cy="22" r="15"/><text x="300" y="47">BROWSE</text></g>${box("load2", 321, 12, 38, 16, "LOAD 2")}
  ${dim(258, 56, "HI")}${dim(258, 92, "MID")}${dim(258, 128, "LOW")}${dim(342, 56, "HI")}${dim(342, 92, "MID")}${dim(342, 128, "LOW")}
  ${box("headCueL", 246, 166, 28, 15, "CUE")}${box("headCueR", 326, 166, 28, 15, "CUE")}
  <g class="dt-dim"><rect x="255" y="188" width="10" height="66" rx="2"/><rect x="335" y="188" width="10" height="66" rx="2"/></g>
  <g class="dt-c" data-k="crossfader"><rect x="256" y="268" width="88" height="20" rx="3"/><rect class="dt-in" x="292" y="270" width="14" height="16" rx="2"/></g>
  <text class="dt-head" x="388" y="20">BEAT FX</text>
  ${box("fxSelect", 366, 28, 44, 16, "SELECT")}${box("fxOn", 366, 50, 44, 26, "ON/OFF")}
</svg>`;

// ---- steps ----
const here = () => parse().id;
const padIndex = id => tabs().findIndex(t => t.id === id);
const leftPad = i => e => e.c === "pad" && e.deck === 1 && !e.shift && e.i === i && e.mode === "hotcue";
const press = (c, deck) => e => e.c === c && (deck == null || e.deck === deck) && e.on;
const jog = deck => e => e.c === "jog" && e.deck === deck;
const count = n => { let k = 0; return { reset() { k = 0; }, hit: () => ++k >= n }; };
const viewPad = (id, title, text) => ({ title, text: () => `Press left pad ${padIndex(id) + 1}. ${text}`, show: () => [`padL${padIndex(id)}`], wantFn: () => leftPad(padIndex(id)), skip: () => padIndex(id) < 0 || padIndex(id) > 7 });
const STEPS = [
  { title: "A tour of the deck", text: "It tells you what to press and waits for you. Only the control it asks for works while it runs, so nothing can go wrong. Press PLAY on the left deck to start, or CUE to skip it.", show: ["playL", "cueL"], want: e => e.on && (e.c === "play" || e.c === "cue"), own: e => { if (e.c === "cue") end(); else next(); } },
  { at: "now", ...viewPad("now", "The left pads are your views", "That's Now: every agent running and what it's doing. The lit pad is the view you're on.") },
  { title: "Next view", text: "Press ▶ (CUE/LOOP CALL) at the top of the left deck. It steps to the next view.", show: ["callNextL"], want: press("callNext", 1) },
  { title: "Previous view", text: "Press ◀ next to it to step back.", show: ["callPrevL"], want: press("callPrev", 1) },
  viewPad("comms", "Comms", "That's the board: every thread and post."),
  { title: "Scroll", text: "Spin the left jog wheel. Jogs scroll: the left one the page or left pane. The top of the wheel goes slower than its side ring.", show: ["jogL"], want: jog(1), n: 6 },
  { at: "now", title: "The cursor", text: "Back on Now. Turn BROWSE, the knob at the top of the mixer. A frame walks over what you can open.", show: ["browse"], want: e => e.c === "browse", n: 3 },
  { title: "Open it", text: "Press BROWSE down. It opens what the frame is on. An agent opens in the side panel.", show: ["browse"], want: press("browsePress") },
  { title: "Back", text: "Press CUE. It's always Back: closes the panel, then drops the frame, then goes to the previous page.", show: ["cueL", "cueR"], want: press("cue") },
  { at: "now", title: "The right pads are running agents", text: () => `Press right pad 1: ${(runningAgents()[0] || {}).name || "the oldest agent"}, the oldest running. Lit means running, blinking means stuck.`, show: ["padR0"], want: e => e.c === "pad" && e.deck === 2 && !e.shift && e.i === 0 && e.mode === "hotcue", skip: () => !runningAgents().length },
  { title: "Read along", text: "Spin the right jog wheel. It scrolls the side panel when one is open.", show: ["jogR"], want: jog(2), n: 6, skip: () => !panelOpen() && !runningAgents().length },
  { title: "Close it", text: "Press CUE to close the panel.", show: ["cueL", "cueR"], want: press("cue"), skip: () => !panelOpen() },
  { at: "now", title: "Top of the page", text: "Spin the left jog down a bit, then press PLAY: it jumps back to the top.", show: ["playL", "jogL"], want: e => jog(1)(e) || press("play")(e), done: e => e.c === "play" },
  viewPad("mesh", "Mesh", "Who works with whom. On this view the jogs and crossfader do more."),
  { title: "Replay", text: "Slide the crossfader. On Mesh it scrubs back through the day.", show: ["crossfader"], want: e => e.c === "crossfader", n: 6 },
  { title: "Back to live", text: "Slide the crossfader all the way right. Far right is live.", show: ["crossfader"], want: e => e.c === "crossfader", done: e => e.v > .97 },
  viewPad("timeline", "Timeline", "Who ran when."),
  { title: "Pan the timeline", text: "Spin the left jog. Here it pans through time, and the right jog zooms.", show: ["jogL"], want: jog(1), n: 6 },
  { title: "Follow now", text: "Press PLAY: the timeline follows the present again.", show: ["playL", "playR"], want: press("play") },
  { at: "now", title: "Page size", text: "Move the right TEMPO slider. It sizes the whole page, with the middle at 100%. The tour puts the size back after this step.", show: ["tempoR"], want: e => e.c === "tempo" && e.deck === 2, n: 8, leave: () => { document.documentElement.style.zoom = ""; } },
  { title: "Full control", text: "So far nothing could change. To unlock changes, hold SHIFT and press BEAT FX ON/OFF, and keep holding for 1.5 seconds. It turns itself off after 10 minutes untouched.", show: ["shiftL", "shiftR", "fxOn"], want: e => e.c === "fxOn", ready: () => D.armed, skip: () => !canChange() },
  { title: "Practice: a Kill window", text: () => `Hold SHIFT and press right pad 1. It opens the Kill window for ${(runningAgents()[0] || {}).name || "that agent"}. Nothing gets killed: the tour lets you do nothing here but cancel.`, show: ["shiftL", "shiftR", "padR0"], want: e => e.c === "pad" && e.deck === 2 && e.shift && e.i === 0 && e.mode === "hotcue", skip: () => !D.armed || !can("stop") || !runningAgents().length },
  { title: "Cancel it", text: "Press CUE to cancel. Outside the tour, holding PLAY for 1.2 s is what confirms a change from the deck. Everything goes through a window like this one.", show: ["cueL", "cueR"], want: press("cue"), skip: () => !document.querySelector("dialog[open]") },
  { title: "Leave full control", text: "Tap BEAT FX ON/OFF. Back to look-only.", show: ["fxOn"], want: e => e.c === "fxOn" && e.on, ready: () => !D.armed, skip: () => !D.armed },
  { title: "That's the deck", text: "The Deck page, the link at the top right, lists every control, including LOOP IN (kill the picked agent) and LOOP OUT (set its tier). Press PLAY to finish.", show: ["playL"], want: e => e.on && e.c === "play", own: () => end() },
];

// ---- what a touch was, on the drawing ----
const side = d => (d === 2 ? "R" : "L");
function keyOf(e) {
  switch (e.c) {
    case "pad": return `pad${side(e.deck)}${e.i}`;
    case "padMode": return e.mode + side(e.deck);
    case "jog": case "jogTouch": return "jog" + side(e.deck);
    case "browse": case "browsePress": return "browse";
    case "load": return "load" + e.deck;
    case "crossfader": case "fxOn": case "fxSelect": return e.c;
    case "tempo": case "play": case "cue": case "shift": case "sync": case "headCue": case "loopIn": case "loopOut": case "reloop": case "callPrev": case "callNext": return e.c + side(e.deck);
    default: return null;
  }
}
const NAME = { pad: "a pad", padMode: "a pad-mode button", jog: "a jog wheel", browse: "BROWSE", browsePress: "BROWSE", load: "LOAD", crossfader: "the crossfader", fxOn: "BEAT FX ON/OFF", fxSelect: "BEAT FX SELECT", tempo: "a TEMPO slider", play: "PLAY", cue: "CUE", sync: "BEAT SYNC", headCue: "the headphone CUE", loopIn: "LOOP IN", loopOut: "LOOP OUT", reloop: "EXIT", callPrev: "◀", callNext: "▶", fader: "a channel fader", trim: "a TRIM knob", eqHi: "an EQ knob", eqMid: "an EQ knob", eqLow: "an EQ knob", filter: "a CFX knob", headMix: "the headphone mix", fxLevel: "the FX level knob", beatPrev: "BEAT ◀", beatNext: "BEAT ▶", fxChannel: "the FX channel switch" };
const ledsFor = k => {
  if (k === "fxOn") return LED.fxOn();
  const p = k.match(/^pad([LR])(\d)$/); if (p) return LED.pad(p[1] === "R" ? 2 : 1, "hotcue", +p[2]);
  const m = k.match(/^(.+)([LR])$/);
  return m && LED[m[1]] ? LED[m[1]](m[2] === "R" ? 2 : 1) : null;
};

// ---- running it ----
let win = null, at = -1, hits = 0, moving = 0, hintTimer = 0, unwatch = null, placer = 0, armedByTour = false;
const step = () => STEPS[at];
const val = x => (typeof x === "function" ? x() : x);
const wantOf = s => (s.wantFn ? s.wantFn() : s.want);
export const touring = () => !!win;

function paint() {
  const s = step(); if (!win || !s) return;
  const shown = STEPS.filter(x => !x.skip || x === s || !x.skip()), n = shown.indexOf(s) + 1;
  win.querySelector(".dt-n").textContent = `${Math.max(1, n)} of ${shown.length}`;
  win.querySelector(".dt-t").textContent = s.title;
  win.querySelector(".dt-p").textContent = val(s.text);
  hint("");
  const show = new Set(val(s.show));
  for (const g of win.querySelectorAll(".dt-c")) g.toggleAttribute("data-want", show.has(g.dataset.k));
  D.tourLeds = new Set([...show].map(ledsFor).filter(Boolean).map(([st, note]) => st << 8 | note));
}
function hint(text) {
  if (!win) return;
  const p = win.querySelector(".dt-h"); p.textContent = text; p.hidden = !text;
  clearTimeout(hintTimer); if (text) hintTimer = setTimeout(() => { p.hidden = true; }, 2600);
}
function flash(k) {
  const g = k && win.querySelector(`.dt-c[data-k="${k}"]`); if (!g) return;
  g.setAttribute("data-hit", ""); clearTimeout(g._t); g._t = setTimeout(() => g.removeAttribute("data-hit"), 160);
}
function goTo(i, dir = 1) {
  const old = step(); if (old && old.leave) old.leave();
  clearTimeout(moving); moving = 0; hits = 0;
  while (STEPS[i] && STEPS[i].skip && STEPS[i].skip()) i += dir;
  if (!STEPS[i]) { if (dir > 0) return end(); i = 0; }
  at = i;
  const s = step();
  if (s.at && here() !== s.at && !document.querySelector("dialog[open]")) { if (panelOpen()) closePanel(); go(s.at); }
  paint();
}
const next = () => goTo(at + 1);
const advanceSoon = () => { if (moving) return; moving = setTimeout(() => { moving = 0; next(); }, 450); };   // long enough to see what the press did

/** Runs before the page gets a message. false keeps it from the page. */
function before(e) {
  flash(keyOf(e));
  if (e.c === "shift" || e.c === "jogTouch" || e.c === "padMode" || e.on === false) return true;   // a release must always land, or a hold would keep running
  const s = step(); if (!s) return true;
  if (moving) return false;
  if (!wantOf(s)(e)) {
    if (e.c === "pad" && e.mode !== "hotcue") hint("The pads are on another mode. Press HOT CUE above them.");
    else hint(`That was ${NAME[e.c] || e.c}. The lit control is the one to use.`);
    return false;
  }
  if (s.own) { s.own(e); return false; }
  return true;
}
/** Runs after the page got it. */
function after(e) {
  const s = step(); if (!s || moving || e.on === false) return;
  if (!wantOf(s)(e) || s.ready) return;
  if (s.done) { if (s.done(e)) advanceSoon(); return; }
  if (++hits >= (s.n || 1)) advanceSoon();
}
const checkReady = () => { const s = step(); if (win && s && s.ready && s.ready() && !moving) advanceSoon(); };

const button = (text, cls, fn) => { const b = document.createElement("button"); b.type = "button"; b.textContent = text; b.className = cls; b.addEventListener("click", fn); return b; };
function drag(handle) {
  handle.addEventListener("pointerdown", ev => {
    if (ev.target.closest("button")) return;
    const r = win.getBoundingClientRect(), z = parseFloat(document.documentElement.style.zoom) || 1, dx = ev.clientX - r.left, dy = ev.clientY - r.top;
    handle.setPointerCapture(ev.pointerId);
    const mv = m => { win.style.left = (m.clientX - dx) / z + "px"; win.style.top = (m.clientY - dy) / z + "px"; win.style.right = win.style.bottom = "auto"; };
    const up = () => { handle.removeEventListener("pointermove", mv); handle.removeEventListener("pointerup", up); };
    handle.addEventListener("pointermove", mv); handle.addEventListener("pointerup", up);
  });
}
/** A modal window sits above everything on the page, so the tour rides inside it while one is open. */
const place = () => { if (!win) return; const home = document.querySelector("dialog[open]") || document.body; if (win.parentNode !== home) home.append(win); checkReady(); };

export function start() {
  if (win || D.state !== "on") return;
  try { localStorage.setItem(SEEN, "1"); } catch (e) { /* private window: the tour may offer itself again */ }
  win = document.createElement("div"); win.className = "deck-tour"; win.setAttribute("role", "dialog"); win.setAttribute("aria-label", "Deck tour");
  win.innerHTML = `<div class="dt-top"><b>Deck tour</b><span class="dt-n meta"></span><span class="dt-btns"></span></div>${DRAWING}<h3 class="dt-t"></h3><p class="dt-p"></p><p class="dt-h" hidden></p>`;
  win.querySelector(".dt-btns").append(button("Back", "quiet", () => goTo(Math.max(0, at - 1), -1)), button("Skip step", "quiet", next), button("End", "quiet", end));
  drag(win.querySelector(".dt-top"));
  armedByTour = D.armed;
  setTour({ before, after });
  unwatch = watch(checkReady);
  placer = setInterval(place, 150); place();
  goTo(0);
}
export function end() {
  if (!win) return;
  const s = step(); if (s && s.leave) s.leave();
  clearTimeout(moving); moving = 0; clearTimeout(hintTimer); clearInterval(placer);
  setTour(null); D.tourLeds = null; if (unwatch) unwatch(); unwatch = null;
  win.remove(); win = null; at = -1;
  if (D.armed && !armedByTour) disarm();   // leave the deck as it was found
}
/** The first time the deck is connected in this browser, offer the tour. */
export function offer() {
  watch(() => { if (D.state !== "on" || win) return; let seen = "1"; try { seen = localStorage.getItem(SEEN); } catch (e) { /* no storage: don't nag */ } if (!seen) start(); });
}
