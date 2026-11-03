// The DDJ-FLX4 as a remote for this page. Web MIDI in the browser: nothing on the server changes and nothing is installed.
// Two levels. Normal: look around; a press can only follow a link, choose a view option or close something, so nothing in the hive can change.
// Full control: armed on purpose (hold SHIFT + BEAT FX ON/OFF), drops by itself. Adds kill, tier, pause and every button on the page.
// What the server allows (can()) still decides: full control unlocks the deck, not the server. Each change goes through the same confirm window as the mouse.
import { h } from "../core/dom.js";
import { S, can, subscribe } from "../core/store.js";
import { agentStatus, agentThread, fresh7 as weekly, threadTitle } from "../core/model.js";
import { go, parse } from "../core/router.js";
import { askKill, askPause, askTier } from "../actions.js";
import { closePanel, openPanel, panelOpen } from "../ui/panel.js";
import { singleOpen, toggleSingle, leaveSingle, sendSingle } from "./single.js";
import { decoder, HELLO, isFlx4, KEEP_ALIVE, LED, PAGES, vuMsg } from "./flx4.js";

const ARM_HOLD = 1500, CONFIRM_HOLD = 1200, ARM_IDLE = 10 * 60e3;
export const D = {
  state: "off",      // off | asking | waiting | on | refused | none
  say: "", input: null, output: null, access: null, sysex: false,
  armed: false, armedAt: 0, last: 0, log: [], touched: null, pageOf: { 1: "hotcue", 2: "hotcue" }, tourLeds: null,
};
const decode = decoder(), lit = new Map(), watchers = new Set(), logWatchers = new Set();
let views = [], sel = null, selKey = null, keepTimer = 0, ledTimer = 0, blink = false, blinkTimer = 0, holdTimer = 0, idleTimer = 0, jogs = { 1: 0, 2: 0 }, raf = 0, hudTimer = 0, unsub = null;

export const supported = () => !!navigator.requestMIDIAccess;
export const setViews = v => { views = v; };
export const tabs = () => shownViews();
/** The tour sees each message first and can keep it from the page: { before(e) -> false to drop, after(e) }. */
let tour = null;
export const setTour = t => { tour = t; lights(); };
/** watch: connection and full control changed. watchLog: any message came, up to hundreds a second from a jog, so throttle what you do. */
export const watch = fn => (watchers.add(fn), () => watchers.delete(fn));
export const watchLog = fn => (logWatchers.add(fn), () => logWatchers.delete(fn));
const changed = () => { for (const f of watchers) f(); };
const shownViews = () => views.filter(v => (typeof v.tab === "function" ? v.tab() : v.tab));
/** Single screen, for the deck's EXIT button. display: where its messages go while it's open; leaving: what EXIT does on a TV page opened by its address. */
let display = null, leaving = null;
export const setDisplay = (fn, out) => { display = fn; leaving = out; };
const curView = () => views.find(v => v.id === parse().id);
const dialog = () => document.querySelector("dialog[open]");

// ---- the small text that says what a control just did ----
const hud = h("div", { class: "deck-hud", role: "status" }), cursor = h("div", { class: "deck-cursor", "aria-hidden": "true" });
function say(text, { tone, keep } = {}) {
  hud.textContent = text; tone ? hud.setAttribute("data-tone", tone) : hud.removeAttribute("data-tone");
  (dialog() || document.body).append(hud); hud.hidden = false;
  clearTimeout(hudTimer); if (!keep) hudTimer = setTimeout(() => { hud.hidden = true; }, 2200);
}
const short = t => { t = (t || "").replace(/\s+/g, " ").trim(); return t.length > 60 ? t.slice(0, 59) + "…" : t; };

// ---- connecting ----
export async function connect() {
  if (!supported()) { D.state = "none"; D.say = "This browser has no Web MIDI. Use Chrome or Edge on the laptop."; return changed(); }
  D.state = "asking"; D.say = ""; changed();
  try {
    try { D.access = await navigator.requestMIDIAccess({ sysex: true }); D.sysex = true; }
    catch (e) { D.access = await navigator.requestMIDIAccess({ sysex: false }); D.sysex = false; }
  } catch (e) { D.state = "refused"; D.say = "The browser refused MIDI. Allow it from the icon at the left of the address bar, then try again."; return changed(); }
  D.access.onstatechange = () => bind();
  bind();
}
async function bind() {
  const find = m => [...m.values()].find(p => isFlx4(p.name) && p.state !== "disconnected");
  const inp = find(D.access.inputs), out = find(D.access.outputs);
  if (inp === D.input && out === D.output) return;
  if (D.input) D.input.onmidimessage = null;
  D.input = inp || null; D.output = out || null;
  if (!inp) {
    if (D.armed) disarm("the deck was unplugged");
    stopLoops(); lit.clear(); D.state = "waiting";
    const others = [...D.access.inputs.values()].map(p => p.name);
    D.say = others.length ? `Other MIDI devices here (${others.join(", ")}), but no DDJ-FLX4. Plug it in over USB.` : "Plug the DDJ-FLX4 in over USB and turn it on.";
    return changed();
  }
  try { await inp.open(); if (out) await out.open(); }
  catch (e) { D.input = D.output = null; D.state = "waiting"; D.say = "The deck is busy. Close rekordbox, Serato or anything else using it, then unplug and replug."; return changed(); }
  inp.onmidimessage = onMessage;
  D.state = "on"; D.say = "";
  startLoops(); send(...HELLO); say("DDJ-FLX4 connected");
  changed();
}
/** On load, connect without asking if the browser already allows it. Otherwise wait for the button on the Deck page. */
export async function resume() {
  if (!supported() || !navigator.permissions) return;
  try { const p = await navigator.permissions.query({ name: "midi", sysex: true }); if (p.state === "granted") connect(); } catch (e) { /* this browser can't say: wait for the button */ }
}
function send(...msgs) { if (D.output) for (const m of msgs) try { D.output.send(m); } catch (e) { /* unplugged: statechange follows */ } }
function startLoops() {
  stopLoops();
  document.body.append(cursor);
  if (D.sysex) keepTimer = setInterval(() => { if (!document.hidden) send(KEEP_ALIVE); }, 200);
  blinkTimer = setInterval(() => { blink = !blink; lights(); }, 450);
  ledTimer = setInterval(lights, 1000);
  unsub = subscribe(lights);
  requestAnimationFrame(follow);
}
function stopLoops() { clearInterval(keepTimer); clearInterval(ledTimer); clearInterval(blinkTimer); if (unsub) unsub(); unsub = null; cursor.remove(); }
addEventListener("pagehide", () => { for (const [k, v] of lit) if (v) send([k >> 8, k & 255, 0]); send(vuMsg(1, 0), vuMsg(2, 0)); });

// ---- full control ----
export const canChange = () => ["stop", "tier", "limits", "post"].some(can);
export function arm() {
  if (!canChange()) return say("This connection can't change anything, so there's nothing to unlock", { tone: "warn" });
  D.armed = true; D.armedAt = Date.now(); D.last = Date.now();
  clearInterval(idleTimer); idleTimer = setInterval(() => { if (Date.now() - D.last > ARM_IDLE) disarm("10 minutes without a touch"); }, 15e3);
  say("FULL CONTROL on. Tap BEAT FX ON/OFF to leave", { tone: "bad" }); lights(); changed();
}
export function disarm(why) { D.armed = false; clearInterval(idleTimer); dropSelIfUnsafe(); say("Full control off" + (why ? `: ${why}` : ""), {}); lights(); changed(); }

// ---- the cursor: BROWSE walks what's on the page. Marked by a frame outside the page, so morph can't lose it. ----
const LOOK = 'a[href^="#"], .seg button, summary, [role="tab"]';
const FULL = LOOK + ', button, a.btn, input[type="checkbox"], input[type="radio"]';
const notHere = e => e.closest(".copy, .deck-page-raw");
function targets() {
  const root = panelOpen() ? document.querySelector(".panel") : document.getElementById("main");
  if (!root) return [];
  return [...root.querySelectorAll(D.armed ? FULL : LOOK)].filter(e => { if (notHere(e) || e.disabled) return false; const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
}
const keyOf = e => e.getAttribute("href") || e.getAttribute("data-key") || e.getAttribute("aria-label") || e.textContent.trim().slice(0, 40);
function resolve(list) {
  if (sel && sel.isConnected && list.includes(sel)) return sel;
  if (selKey) { const again = list.find(e => keyOf(e) === selKey); if (again) return (sel = again); }
  sel = null; return null;
}
function dropSelIfUnsafe() { if (sel && !sel.matches(LOOK)) { sel = null; selKey = null; } }
function move(d) {
  const list = targets();
  if (!list.length) return say("Nothing to pick on this page");
  const cur = resolve(list), i = cur ? list.indexOf(cur) : -1, next = list[((i < 0 ? (d > 0 ? -1 : 0) : i) + d + list.length * 8) % list.length];
  sel = next; selKey = keyOf(next);
  next.scrollIntoView({ block: "nearest" });
  say(short(next.getAttribute("aria-label") || next.textContent || selKey));
}
function follow() {
  if (D.state !== "on") return;
  requestAnimationFrame(follow);
  const v = jogs[1], w = jogs[2]; jogs = { 1: 0, 2: 0 };
  if (v) scrollBy_(1, v); if (w) scrollBy_(2, w);
  const on = sel && sel.isConnected && sel.offsetParent !== null && !dialog() ? sel.getBoundingClientRect() : null;
  cursor.hidden = !on;
  if (on) { const z = parseFloat(document.documentElement.style.zoom) || 1, st = cursor.style, p = 3; // TEMPO zooms the root, which scales the frame's own numbers too
    st.left = on.left / z - p + "px"; st.top = on.top / z - p + "px"; st.width = on.width / z + 2 * p + "px"; st.height = on.height / z + 2 * p + "px"; }
  cursor.toggleAttribute("data-armed", D.armed);
}
/** What the cursor is on, as far as the hive is concerned. */
function picked() {
  const v = curView(), own = v && v.deck && v.deck.picked && v.deck.picked();
  if (own) return own;
  const list = targets(), e = resolve(list), to = e && e.getAttribute("href");
  const m = to && /^#(agent|comms)\/(.+)$/.exec(to);
  if (m) return m[1] === "agent" ? { agent: decodeURIComponent(m[2]) } : { thread: decodeURIComponent(m[2]) };
  const ref = e && e.closest("[data-key]"), key = ref && ref.getAttribute("data-key");
  if (key && S.running.has(key)) return { agent: key };
  return v && v.id === "agent" && parse().arg ? { agent: parse().arg } : v && v.id === "comms" && parse().arg ? { thread: parse().arg } : {};
}

// ---- jogs scroll. Left: the leftmost thing that scrolls. Right: the side panel, else the rightmost. Same thing if the page has one. ----
function scrollers() {
  const main = document.getElementById("main"); if (!main) return [];
  const out = [];
  for (const e of main.querySelectorAll("*")) { if (e.clientHeight < 80 || e.scrollHeight <= e.clientHeight + 4) continue; const o = getComputedStyle(e).overflowY; if (o === "auto" || o === "scroll") out.push(e); if (out.length > 12) break; }
  return out.sort((a, b) => a.getBoundingClientRect().left - b.getBoundingClientRect().left);
}
function scrollBy_(deck, px) {
  const panel = panelOpen() && document.querySelector(".panel");
  if (panel && deck === 2) return panel.scrollBy(0, px);
  // A page that scrolls is the left jog's. An inner pane gets a jog only where it's the thing to move, and hands on at its end.
  const list = scrollers(), root = document.documentElement, pageMoves = root.scrollHeight > innerHeight + 4;
  const one = deck === 1 ? (pageMoves ? null : list[0]) : list[list.length - 1];
  if (one) { const was = one.scrollTop; one.scrollBy(0, px); if (one.scrollTop !== was || !pageMoves) return; }
  scrollBy(0, px);
}

// ---- hold to confirm ----
function hold(ms, text, done) {
  cancelHold(); say(text, { tone: "bad", keep: true });
  holdTimer = setTimeout(() => { holdTimer = 0; hud.hidden = true; done(); }, ms);
}
function cancelHold() { if (holdTimer) { clearTimeout(holdTimer); holdTimer = 0; hud.hidden = true; return true; } return false; }
function confirmDialog(dlg) {
  const code = dlg.querySelector('input[aria-label^="Type "]'), yes = dlg.querySelector('button[type="submit"]'), form = dlg.querySelector("form");
  if (!yes || !form) return;
  if (code) { code.value = code.placeholder; code.dispatchEvent(new Event("input", { bubbles: true })); }
  if (!yes.disabled) form.requestSubmit(yes);
}

// ---- a window is open: the deck talks only to it, nothing happens behind it ----
function inDialog(dlg, e) {
  if (e.c === "browse") {
    const list = [...dlg.querySelectorAll('input[type="radio"], input[type="checkbox"], button')].filter(x => !x.disabled);
    if (!list.length) return;
    const i = list.indexOf(document.activeElement), n = list[(i + (e.d > 0 ? 1 : -1) + list.length) % list.length]; n.focus();
    return say(short(n.closest("label") ? n.closest("label").textContent : n.textContent), { keep: true });
  }
  if (e.c === "browsePress" && e.on) { if (document.activeElement && dlg.contains(document.activeElement) && document.activeElement.type !== "submit") document.activeElement.click(); return; }
  if (e.c === "cue" && e.on) return dlg.close();
  if (e.c === "play") {
    if (!e.on) { if (cancelHold()) say("Not confirmed"); return; }
    if (!D.armed) return say("Confirming from the deck needs full control", { tone: "warn" });
    return hold(CONFIRM_HOLD, "Keep holding PLAY to confirm…", () => confirmDialog(dlg));
  }
}

// ---- what each control does ----
const need = (c, what) => { if (!D.armed) { say(`${what} needs full control: hold SHIFT + BEAT FX ON/OFF`, { tone: "warn" }); return false; } if (!can(c)) { say(`The server doesn't allow ${what.toLowerCase()} here`, { tone: "warn" }); return false; } return true; };
export const runningAgents = () => [...((S.live && S.live.agents) || [])].sort((a, b) => (a.since || 0) - (b.since || 0) || (a.name < b.name ? -1 : 1));
const LEAVES = new Set(["cue", "pad", "load", "callPrev", "callNext", "loopIn", "loopOut"]);   // these act on the dashboard, so they come back to it first
function act(e) {
  if (e.c === "reloop") { if (e.on) { if (display) leaving(); else toggleSingle(say); } return; }
  if (display) return display(e, say);
  if (singleOpen() && e.c !== "fxOn") { if (!LEAVES.has(e.c)) return sendSingle(e, say); leaveSingle(); if (e.c === "cue") return; }
  const v = curView(), deck = (v && v.deck) || {};
  switch (e.c) {
    case "browse": if (deck.browse && deck.browse(e.d)) return; return move(e.d * (e.shift ? 5 : 1));
    case "browsePress": {
      if (!e.on) return;
      if (e.shift) return back();
      if (deck.open && deck.open()) return;
      const list = targets(), t = resolve(list);
      if (!t) return say("Turn BROWSE to pick something first");
      return t.click();
    }
    case "load": {
      if (!e.on) return;
      const p = picked();
      if (e.deck === 1) { if (p.agent) return go("agent", p.agent); if (p.thread) return go("comms", p.thread); return say("Turn BROWSE to an agent or a thread first"); }
      const th = p.thread || (p.agent && agentThread(p.agent));
      return th ? go("comms", th) : say("Turn BROWSE to an agent or a thread first");
    }
    case "cue": if (e.on) back(); return;
    case "callPrev": case "callNext": {
      if (!e.on) return;
      const tabs = shownViews(), i = tabs.findIndex(t => t.id === parse().id), n = tabs[(i + (e.c === "callNext" ? 1 : -1) + tabs.length) % tabs.length];
      if (panelOpen()) closePanel();
      return go(n.id);
    }
    case "padMode": if (e.on) { for (const m of PAGES) if (D.pageOf[e.deck] === m) send([...LED.padMode(e.deck, m), 0]); if (PAGES.includes(e.mode)) D.pageOf[e.deck] = e.mode; lights(); } return;
    case "pad": {
      if (!e.on) return;
      if (PAGES.includes(e.mode)) D.pageOf[e.deck] = e.mode;
      const page = Math.max(0, PAGES.indexOf(e.mode)), at = page * 8 + e.i;
      if (e.deck === 1) {
        if (!e.shift) { const t = shownViews()[at]; if (!t) return say(`No view on pad ${e.i + 1}`); if (panelOpen()) closePanel(); go(t.id); return say(t.label); }
        const l = ((S.usage && S.usage.logins) || [])[at];
        if (!l) return say(`No account on pad ${e.i + 1}`);
        if (need("limits", (l.paused ? "Resuming " : "Pausing ") + l.name)) askPause(S.usage, l);
        return;
      }
      const a = runningAgents()[at];
      if (!a) return say(`No running agent on pad ${e.i + 1}`);
      if (!e.shift) { openPanel("agent", a.name); return say(a.name + (a.thread ? ` · ${threadTitle(a.thread)}` : "")); }
      if (need("stop", "Killing " + a.name)) askKill(a.name);
      return;
    }
    case "loopIn": { if (!e.on) return; const p = picked(); if (!p.agent) return say("Turn BROWSE to an agent first"); if (need("stop", "Killing " + p.agent)) askKill(p.agent); return; }
    case "loopOut": { if (!e.on) return; const p = picked(), th = p.thread || (p.agent && agentThread(p.agent)); if (!th) return say("Turn BROWSE to a thread first"); if (need("tier", "Setting a tier")) askTier(th); return; }
    case "fxOn":
      if (!e.on) { if (cancelHold()) say("Full control stays off"); return; }   // SHIFT may come up first, so the release can arrive as either note
      if (e.shift) {
        if (D.armed) return disarm();
        return hold(ARM_HOLD, "Keep holding for full control…", arm);
      }
      if (D.armed) disarm();
      return;
    case "jog": {
      const k = (e.part === "side" ? 8 : 3) * (e.shift ? 6 : 1);
      if (deck.jog && deck.jog(e.deck, e.d, e.shift)) return;
      jogs[e.deck] += e.d * k; return;
    }
    case "play": if (e.on && !(deck.play && deck.play())) { scrollTo(0, 0); for (const s of scrollers()) s.scrollTop = 0; } return;
    case "sync": if (e.on && deck.fit) deck.fit(); return;
    case "headCue": if (e.on && deck.toggle) deck.toggle(); return;
    case "fxSelect": if (e.on) document.querySelector(".theme")?.click(); return;
    case "tempo":
      if (e.deck === 1) { if (deck.window) deck.window(e.v); return; }
      document.documentElement.style.zoom = String(Math.round(Math.pow(2, (e.v - .5) * 1.4) * 100) / 100); return;
    case "crossfader": if (deck.scrub) deck.scrub(e.v); return;
  }
}
function back() {
  if (panelOpen()) return closePanel();
  if (sel) { sel = null; selKey = null; return; }
  history.back();
}

function onMessage(m) {
  if (document.hidden) return;   // another tab on this deck may be the one in view
  const [st, d1, d2] = m.data;
  if (st >= 0xF0) return;
  const e = decode(st, d1, d2 ?? 0);
  const now = Date.now();
  D.log.unshift({ t: now, hex: [st, d1, d2 ?? 0].map(x => x.toString(16).padStart(2, "0")).join(" "), name: e ? describe(e) : "not mapped" });
  D.log.length = Math.min(D.log.length, 30);
  if (e) { D.last = now; D.touched = e.c; }
  if (e) {
    const dlg = dialog();
    try { if (!tour || tour.before(e) !== false) { if (dlg) inDialog(dlg, e); else act(e); if (tour) tour.after(e); } }
    catch (x) { say("Deck: " + x.message, { tone: "bad" }); console.error(x); }
    if (e.on !== undefined) lights();
  }
  for (const f of logWatchers) f();
}
const describe = e => [e.deck ? `deck ${e.deck}` : "", e.c, e.i != null ? `pad ${e.i + 1} (${e.mode})` : "", e.shift ? "+shift" : "", e.d != null ? (e.d > 0 ? "+" : "") + e.d : "", e.v != null ? e.v.toFixed(3) : "", e.on === false ? "up" : ""].filter(Boolean).join(" ");

// ---- lights: the deck shows the hive. Only changes are sent. ----
function led(status, note, on) { const k = status << 8 | note; if (D.tourLeds && D.tourLeds.has(k)) on = blink; const v = on ? 0x7F : 0; if (lit.get(k) !== v) { lit.set(k, v); send([status, note, v]); } }
function lights() {
  if (D.state !== "on" || !D.output || document.hidden) return;
  const page = d => Math.max(0, PAGES.indexOf(D.pageOf[d]));
  const tabs = shownViews(), ids = parse().id, agents = runningAgents();
  for (let i = 0; i < 8; i++) {
    const t = tabs[page(1) * 8 + i], a = agents[page(2) * 8 + i];
    led(...LED.pad(1, D.pageOf[1], i), t && t.id === ids);
    const st = a && agentStatus(a.name).key;
    led(...LED.pad(2, D.pageOf[2], i), a && (st === "stuck" ? blink : true));
  }
  for (const d of [1, 2]) { led(...LED.padMode(d, D.pageOf[d]), true); led(...LED.play(d), true); led(...LED.cue(d), panelOpen() || !!dialog()); led(...LED.loopIn(d), D.armed); led(...LED.loopOut(d), D.armed); led(...LED.reloop(d), !!display || singleOpen()); }
  led(...LED.fxOn(), D.armed && (blink || Date.now() - D.armedAt > 3000));
  const n = agents.length, l = ((S.usage && S.usage.logins) || []).filter(x => !x.paused).map(x => weekly(x)).filter(x => x != null);
  const vu = [Math.min(1, n / 24), l.length ? Math.min(...l) / 100 : 0];
  for (const d of [1, 2]) { const m = vuMsg(d, vu[d - 1]), k = m[0] << 8 | m[1]; if (lit.get(k) !== m[2]) { lit.set(k, m[2]); send(m); } }
}

/** The map the Deck page shows. [control key, name, always, full control only] */
export const MAP = [
  ["browse", "BROWSE, turn", "Move the cursor over links and view options. With SHIFT: 5 at a time.", "Also lands on every button on the page"],
  ["browsePress", "BROWSE, press", "Open what the cursor is on. An agent opens in the side panel. With SHIFT: back.", "Presses the button under the cursor; anything that changes the hive opens its confirm window"],
  ["load", "LOAD 1 / LOAD 2", "Open the picked agent or thread as a full page / open its thread in Comms", ""],
  ["pad", "Left pads", "Views, in tab order. Other pad-mode buttons are more pages.", "SHIFT + pad: pause or resume that account"],
  ["pad", "Right pads", "Running agents, oldest first: opens the side panel. Lit: running. Blinking: stuck. Other pad-mode buttons: agents 9 to 32.", "SHIFT + pad: kill that agent"],
  ["jog", "Jog wheels", "Scroll. Left: the page, or the left pane where the page itself doesn't scroll. Right: the side panel, else the right pane, else the page. Side ring and SHIFT go faster. Mesh: left scrubs replay, right zooms. Timeline: left pans, right zooms.", ""],
  ["reloop", "RELOOP/EXIT", "Single screen in full screen, and back. The browser wants one click for full screen unless it already fills the screen (F11). There: TEMPO right sizes, BROWSE walks the links, jogs and crossfader work the Timeline and Mesh tiles, pads and ◀ ▶ come back to the dashboard.", ""],
  ["cue", "CUE", "Back: closes the panel, drops the cursor, then the browser's Back. In a window: Cancel.", ""],
  ["callPrev", "CUE/LOOP CALL ◀ ▶", "Previous / next view", ""],
  ["play", "PLAY", "Top of the page. Mesh: replay on and off. Timeline: follow now.", "In a window: hold 1.2 s to confirm, code filled in for you"],
  ["sync", "BEAT SYNC", "Mesh and Timeline: fit", ""],
  ["headCue", "Headphone CUE", "Mesh and Timeline: active only, on and off", ""],
  ["tempo", "TEMPO sliders", "Left: time window on Mesh and Timeline. Right: page size, middle is 100%.", ""],
  ["crossfader", "Crossfader", "Mesh: replay position. Far right is live.", ""],
  ["fxSelect", "BEAT FX SELECT", "Colour theme", ""],
  ["loopIn", "LOOP IN", "", "Kill the picked agent (confirm window)"],
  ["loopOut", "LOOP OUT", "", "Change the picked thread's tier (confirm window)"],
  ["fxOn", "BEAT FX ON/OFF", "SHIFT + hold 1.5 s: full control on", "Tap: full control off. It also drops after 10 min untouched, on unplug and on reload."],
];
