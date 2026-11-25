// Work tiers (core/tiers.py; the person, 111311199053): each focus, thread and timer sits in a tier; a tier can be paused and has a
// stop point. Settings > Work tiers changes them through /api/work-tiers; a focus card and a thread's bar show a chip from the same read.
// State is in plain words and never a usage figure (a9179's review, rung WT-1): Running, Paused until Thu 9:00, Off, Stopped: …
import { h, tr } from "../core/dom.js";
import { LOC } from "../core/format.js";
import { status as st } from "../core/status.js";
import { S, can, jget, post } from "../core/store.js";
import { focusById, focusName, focusOf } from "../core/foci.js";
import { threadTitle } from "../core/model.js";
import { clock, dayName, ago } from "../core/format.js";
import { section, block, blocks, button, field, fieldErr, input, invalid, statusLabel, waiting } from "./kit.js";
import { act, isBusy } from "./activity.js";
import { confirm } from "./dialog.js";

const T = { data: null, err: null, at: 0, p: null, say: null, name: "", rename: {}, open: {}, logOpen: false, sel: new Set(), last: null };
const READ_EVERY = 30e3;

/** Read /api/work-tiers (at most every 30 s unless forced); `done` redraws. */
export function loadTiers(done, force) {
  if (!force && (T.p || Date.now() - T.at < READ_EVERY)) return T.p;
  T.p = jget("/api/work-tiers").then(v => { T.data = v; T.err = null; }, e => { T.err = e.message; }).finally(() => { T.at = Date.now(); T.p = null; done && done(); });
  return T.p;
}

const tiers = () => (T.data && T.data.tiers) || [];
const OFF = "off";
const whenText = u => { const d = new Date(u * 1e3), days = (d - Date.now()) / 864e5; return days < 6 ? d.toLocaleDateString(LOC, { weekday: "short" }) + " " + clock(d) : dayName(d) + " " + clock(d); };
/** [status key, words]: a tier's state in plain words, no figures. */
export function tierState(t) {
  if (!t) return ["none", ""];
  if (String(t.id) === OFF || t.state === "off") return ["killed", "Off"];
  const p = t.paused;
  if (p && p.until) return p.until * 1e3 > Date.now() ? ["paused", "Paused until " + whenText(p.until)] : ["inuse", "Running"];
  if (p) return ["paused", "Paused until you resume it"];
  if (/stop point/.test(t.state || "")) return ["atstop", "Stopped: reached its weekly limit"];
  return ["inuse", "Running"];
}
const STATE_HINT = { inuse: "new work in this tier can start", paused: "nothing new in this tier starts until the pause ends; running work finishes", atstop: "the week's usage is past this tier's stop point; it runs again when the week resets", killed: "this tier never starts anything" };
const stopText = t => String(t.id) === OFF ? "Nothing in it runs." : t.stop == null ? "Runs until your accounts reach their own limits." : `Starts nothing new past ${t.stop}% of the week's usage.`;

/** The tier a focus or thread sits in, as the page shows it: thread placement (exact, else the longest glob), else its focus, else the default. */
// fnmatch.fnmatchcase, as core/tiers.py matches: * any run, ? one character, [seq] and [!seq] one of (or none of) a set
const isGlob = g => /[*?[]/.test(g);
const glob = g => { let r = "", i = 0;
  while (i < g.length) { const c = g[i++];
    if (c === "*") r += ".*"; else if (c === "?") r += ".";
    else if (c === "[") { let j = i; if (g[j] === "!") j++; if (g[j] === "]") j++; while (j < g.length && g[j] !== "]") j++;
      if (j >= g.length) r += "\\["; else { let seq = g.slice(i, j).replace(/[\\\]]/g, "\\$&"); if (seq[0] === "!") seq = "^" + seq.slice(1); else if (seq[0] === "^") seq = "\\" + seq; r += "[" + seq + "]"; i = j + 1; } }
    else r += c.replace(/[.+^${}()|\\\]\/]/g, "\\$&"); }
  try { return new RegExp("^" + r + "$", "s"); } catch { return /^(?!)/; } };
export function tierOfFocus(fid) {
  const ts = tiers(); return ts.find(t => ((t.holds || {}).focus || []).includes(fid)) || ts.find(t => t.default) || null;
}
/** The thread placement that decides a conversation's tier, as [placement, tier], or null when it follows its focus. */
function placementOf(th) {
  const pl = tiers().flatMap(t => ((t.holds || {}).thread || []).map(g => [g, t]));
  return pl.find(([g]) => g === th) || pl.filter(([g]) => isGlob(g) && glob(g).test(th)).sort((a, b) => b[0].length - a[0].length)[0] || null;
}
export function tierOfThread(th, fid) {
  const hit = placementOf(th);
  return hit ? hit[1] : fid ? tierOfFocus(fid) : tiers().find(t => t.default) || null;
}

/** A small chip: the tier's name, and its state when that isn't Running. `title` says what it means. */
export function tierChip(t, { key } = {}) {
  if (!t) return null;
  const [k, words] = tierState(t), run = words === "Running";
  return h("span", { class: "chip tier-chip", "data-key": key || "tier", "data-tone": run ? null : st(k).tone, title: `In the ${t.name} work tier: ${words}. Change it in Settings, Work tiers.` },
    h("a", { href: "#settings/work-tiers" }, t.name), run ? null : h("span", { class: "tier-chip-s" }, " · " + words));
}
/** Worth a chip: in a tier other than the default, or a tier that isn't running (a9179's review, 11131156d47e). */
const notable = t => t && (!t.default || tierState(t)[1] !== "Running");
/** On a focus card, only when its tier is notable. */
export const focusTierChip = fid => { const t = T.data ? tierOfFocus(fid) : null; return notable(t) ? tierChip(t, { key: "tier-" + fid }) : null; };
/** On a thread's bar: when its tier isn't running, or it sits apart from its focus (that is news even in the default tier). */
export function threadTierChip(th, fid) {
  if (!T.data) return null;
  const t = tierOfThread(th, fid), f = fid ? tierOfFocus(fid) : null;
  if (!t) return null;
  const apart = f ? t.id !== f.id : !t.default, held = tierState(t)[1] !== "Running";
  return apart || held ? tierChip(t, { key: "tier-thread" }) : null;
}

/** For the Settings nav dot: "Extra paused" when any tier is paused or stopped, else null. */
export function pausedTiers() {
  const held = tiers().filter(t => String(t.id) !== OFF && ["paused", "atstop"].includes(tierState(t)[0]));
  return held.length ? held.map(t => t.name).join(", ") + (held.length === 1 ? " is paused" : " are paused") : null;
}

// ---- Settings > Work tiers ----
// saved: runs only once the change is in (a new tier's or a rename's typed name is cleared then, not when the press starts, so a refusal
// keeps it in the box). A second press while the first is on its way is dropped by act, and leaves the tiers as they are.
async function change(op, body, done, what, saved) {
  T.say = null;
  try { const r = await act("tiers-" + op, what || "Changing work tiers", () => post("/api/work-tiers", { op, ...body }), done); if (r === false) return; T.data = r; T.at = Date.now(); T.say = what ? { text: what, tone: "good" } : null; if (saved) saved(); }
  catch (e) { T.say = { text: `Nothing changed: ${e.message}`, tone: "bad" }; }
  done();
}

// Pause choices: a time it lifts by itself, or until resumed.
const nextAt = (days, hour) => { const d = new Date(); d.setDate(d.getDate() + days); d.setHours(hour, 0, 0, 0); return d.getTime() / 1e3; };
const nextMonday = () => { const d = new Date(), n = ((8 - d.getDay()) % 7) || 7; return nextAt(n, 9); };
const PAUSES = [["3h", "For 3 hours", () => Math.round(Date.now() / 1e3 + 3 * 3600)], ["tom", "Until tomorrow 9:00", () => nextAt(1, 9)], ["mon", "Until Monday 9:00", nextMonday], ["none", "Until I resume it", () => null]];
const STOPS = [["none", "No stop of its own"], ...[95, 90, 85, 80, 70, 60, 50, 40, 30].map(v => [String(v), `Stop at ${v}% of the week`])];

const KIND = { focus: "Focus", thread: "Thread", job: "Timer" };
// Moving many at once (the person, 11160903a362): tick rows (Shift picks a run of them) and move them from the bar at the top, or drag a row
// (or, when it is ticked, every ticked row) onto another tier. A batch is one POST per item in turn, as the server takes one change a call.
const splitKey = k => { const i = k.indexOf(":"); return [k.slice(0, i), k.slice(i + 1)]; };
const labelOf = k => { const [kind, x] = splitKey(k); return kind === "focus" ? (focusById(x) ? focusName(x) : x) : x; };
const holderOf = k => { const [kind, x] = splitKey(k); return tiers().find(t => ((t.holds || {})[kind] || []).includes(x)) || null; };
const allKeys = () => tiers().flatMap(t => ["focus", "thread", "job"].flatMap(kind => ((t.holds || {})[kind] || []).map(x => `${kind}:${x}`)));
async function moveMany(keys, to, done) {
  const un = to === "_unset", tt = tiers().find(z => String(z.id) === to), dest = tt ? tt.name : to;
  const todo = keys.filter(k => un ? !k.startsWith("focus:") : String((holderOf(k) || {}).id) !== to);
  if (!todo.length) return;
  const one = todo.length === 1 ? labelOf(todo[0]) : null, what = one ? (un ? `${one} follows ${todo[0].startsWith("thread:") ? "its focus" : "the default tier"} now.` : `Moved ${one} to ${dest}.`)
    : un ? `${todo.length} now follow their focus or the default tier.` : `Moved ${todo.length} to ${dest}.`;
  let n = 0; T.say = null;
  try {
    const r = await act("tiers-set", one ? what : `Moving ${todo.length}`, async () => { let last; for (const k of todo) { last = await post("/api/work-tiers", un ? { op: "unset", what: k } : { op: "set", what: k, tier: to }); n++; } return last; }, done, what);
    if (r === false) return;
    T.data = r; T.at = Date.now(); T.say = { text: what, tone: "good" }; todo.forEach(k => T.sel.delete(k));
  } catch (e) { T.say = { text: n ? `Moved ${n} of ${todo.length}, then stopped: ${e.message}` : `Nothing changed: ${e.message}`, tone: "bad" }; loadTiers(done, true); }
  done();
}
const TD = { keys: null, el: null }, TD_TYPE = "text/x-h2-tier";
const dropMark = el => { if (TD.el && TD.el !== el) TD.el.removeAttribute("data-drop"); TD.el = el; };
const isTier = e => TD.keys && e.dataTransfer && [...(e.dataTransfer.types || [])].includes(TD_TYPE);
const dropZone = (id, done) => ({
  ondragover: (e, el) => { if (!isTier(e)) return; e.preventDefault(); e.dataTransfer.dropEffect = "move"; dropMark(el); el.setAttribute("data-drop", "in"); },
  ondragleave: (e, el) => { if (!el.contains(e.relatedTarget)) { el.removeAttribute("data-drop"); if (TD.el === el) TD.el = null; } },
  ondrop: (e, el) => { if (!isTier(e)) return; e.preventDefault(); const keys = TD.keys; TD.keys = null; dropMark(null); el.removeAttribute("data-drop"); moveMany(keys, id, done); } });

function pick(key, e, el, done) {
  const on = e.target.checked, ks = [...el.closest(".tier-items").querySelectorAll("li.tier-it")].map(li => li.getAttribute("data-key"));
  const a = ks.indexOf(T.last), b = ks.indexOf(key);
  (e.shiftKey && a >= 0 && b >= 0 ? ks.slice(Math.min(a, b), Math.max(a, b) + 1) : [key]).forEach(k => on ? T.sel.add(k) : T.sel.delete(k));
  T.last = key; done();
}
function itemRow(kind, x, t, done) {
  const name = kind === "focus" ? focusName(x) : x, label = kind === "focus" && !focusById(x) ? x : name, key = `${kind}:${x}`, on = T.sel.has(key);
  return h("li", { class: "lr tier-it", "data-key": key, "data-on": on ? "" : null, draggable: "true", title: "Drag onto another tier to move it",
      ondragstart: e => { if (e.target.closest && e.target.closest("select,input,button")) return; TD.keys = on ? [...T.sel] : [key]; e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData(TD_TYPE, TD.keys.join("\n")); e.dataTransfer.setData("text/plain", TD.keys.map(labelOf).join(", ")); },
      ondragend: () => { TD.keys = null; dropMark(null); } },
    h("div", { class: "lr-in frow fsplit" },
      h("label", { class: "tier-it-n" }, h("input", { type: "checkbox", class: "tier-pick", checked: on, "aria-label": `Select ${label}`, onclick: (e, el) => pick(key, e, el, done) }),
        h("span", null, h("span", { class: "muted" }, (kind === "thread" && x.includes("*") ? "Threads" : KIND[kind]) + " "), h("b", null, label))),
      h("span", { class: "frow-end" }, h("select", { class: "input", "aria-label": `Move ${label} to`, title: `Move ${label} to another tier`, disabled: isBusy("tiers-set") || null,
        onchange: e => moveMany([key], e.target.value, done) },
        h("option", { value: "", selected: "" }, "Move to…"),
        tiers().filter(z => z.id !== t.id).map(z => h("option", { value: String(z.id) }, z.name)),
        kind !== "focus" ? h("option", { value: "_unset" }, kind === "thread" ? "Follow its focus" : "Take out of tiers") : null))));
}
/** The bar over the tiers while rows are ticked: how many, where to move them, clear. */
function selBar(done) {
  const keys = [...T.sel], n = keys.length; if (!n) return null;
  const anyFocus = keys.some(k => k.startsWith("focus:"));
  return h("div", { class: "tier-bar", "data-key": "tier-bar", role: "region", "aria-label": "Selected items" },
    h("b", { "aria-live": "polite" }, `${n} selected`),
    h("select", { class: "input", "aria-label": "Move selected to", disabled: isBusy("tiers-set") || null, onchange: e => { const v = e.target.value; if (v) moveMany(keys, v, done); } },
      h("option", { value: "", selected: "" }, "Move selected to…"),
      tiers().map(z => h("option", { value: String(z.id) }, z.name)),
      anyFocus ? null : h("option", { value: "_unset" }, "Follow their focus or the default")),
    button("Clear", () => { T.sel.clear(); T.last = null; done(); }, { size: "s", kind: "quiet" }));
}

function tierBlock(t, done) {
  const id = String(t.id), [k, words] = tierState(t), holds = t.holds || {}, n = ["focus", "thread", "job"].reduce((a, x) => a + (holds[x] || []).length, 0);
  const paused = !!t.paused && id !== OFF && k === "paused";
  const pauseMenu = id === OFF ? null : paused
    ? button("Resume", () => change("resume", { tier: id }, done, `${t.name} runs again.`), { size: "s", busy: isBusy("tiers-resume") })
    : h("select", { class: "input", "aria-label": `Pause ${t.name}`, title: `Pause ${t.name}: nothing new in it starts; what is running finishes`, disabled: isBusy("tiers-pause") || null,
      onchange: e => { const c = PAUSES.find(p => p[0] === e.target.value); if (!c) return; const u = c[2](); change("pause", { tier: id, until: u }, done, `${t.name} is paused ${u ? "until " + whenText(u) : "until you resume it"}.`); } },
      h("option", { value: "", selected: "" }, "Pause…"), PAUSES.map(([v, l]) => h("option", { value: v }, l)));
  const items = ["focus", "thread", "job"].flatMap(kind => (holds[kind] || []).slice().sort((a, b) => (kind === "focus" ? focusName(a).localeCompare(focusName(b)) : a.localeCompare(b))).map(x => itemRow(kind, x, t, done)));
  const rn = T.rename[id] ?? t.name;
  return block({ title: t.name + (t.default ? " (default)" : ""), sub: stopText(t), state: statusLabel(k, words, { title: STATE_HINT[k] }), key: "tier-" + id, actions: pauseMenu },
    h("div", { class: "tier-drop", "data-key": "drop", ...dropZone(id, done) },
      items.length ? h("ul", { class: "list tier-items" }, items) : h("p", { class: "field-h" }, t.default ? "Every focus you don't place goes here." : "Nothing is in this tier. Drag something here, or move it from its own tier.")),
    id === OFF ? null : h("details", { class: "tier-more", "data-key": "more", open: T.open[id] ? "" : null, ontoggle: e => { T.open[id] = e.target.open; } }, h("summary", null, "Stop point, name and more"),
    h("div", { class: "frow" },
      h("select", { class: "input", "aria-label": `Stop point for ${t.name}`, title: "Past this share of the week's usage, nothing new in this tier starts", onchange: e => change("stop", { tier: id, pct: e.target.value }, done, `${t.name}: stop point saved.`) },
        STOPS.map(([v, l]) => h("option", { value: v, selected: (t.stop == null ? "none" : String(t.stop)) === v ? "" : null }, l))),
      t.default ? null : button("Make default", () => change("default", { tier: id }, done, `Work with no tier now goes to ${t.name}.`), { size: "s", kind: "quiet" })),
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); const v = (T.rename[id] || "").trim(); if (v && v !== t.name) change("rename", { tier: id, name: v }, done, `Renamed to ${v}.`, () => { delete T.rename[id]; }); } },
      h("div", { class: "frow-grow", "data-size": "s" }, field("Name", input({ value: rn, maxlength: 40, "aria-label": `Name of ${t.name}`, oninput: e => { T.rename[id] = e.target.value; } }))),
      button("Rename", null, { type: "submit", size: "s", kind: "quiet" }),
      t.default ? null : button("Remove tier", () => { confirm({ title: `Remove ${t.name}?`, body: ["What it holds moves to the default tier."], ok: "Remove", kind: "danger", onOk: () => change("drop", { tier: id }, done, `Removed ${t.name}.`) }); }, { size: "s", kind: "danger" }))));
}

function logBlock() {
  const log = (T.data && T.data.log) || [];
  return block({ title: "Recent changes", sub: log.length ? `last ${log.length}` : "none yet", key: "tier-log", fold: true, open: T.logOpen, onToggle: o => { T.logOpen = o; } },
    log.length ? h("ul", { class: "plain tier-log" }, log.map((e, i) => h("li", { "data-key": String(i) }, h("span", null, e.text), h("span", { class: "muted" }, ` · ${e.who === "you" ? "you" : "an agent you asked"}, ${ago(e.at * 1e3)}`)))) : h("p", { class: "field-h" }, "Changes you make here show up in this list."));
}

export function tiersSection(ctx) {
  if (!can("tier")) return null;
  const done = ctx.update;
  if (T.data && T.sel.size) { const have = new Set(allKeys()); [...T.sel].forEach(k => { if (!have.has(k)) T.sel.delete(k); }); }
  const body = !T.data ? waiting(T.err, "Reading…", "the tiers")
    : [h("p", { class: "tier-intro" }, "Every focus, thread and timer sits in a tier. Pause a tier and nothing new in it starts; what is already running finishes. A pause with an end lifts by itself. To move several, tick them, or drag a row onto another tier."),
      selBar(done),
      T.say ? h("p", { class: "fmsg", "data-tone": T.say.tone, role: "status" }, T.say.text) : null,
      blocks(...tiers().map(t => tierBlock(t, done)), logBlock()),
      // An empty name is said beside the box (WCAG 3.3.1, drive_app_errors), and typing a name clears it.
      h("form", { class: "frow", "data-key": "tier-new", onsubmit: e => { e.preventDefault(); if (isBusy("tiers-new")) return; const v = T.name.trim(); if (!v) { T.bad = "Give the new tier a name first, like Weekend."; return done(); } T.bad = ""; change("new", { name: v }, done, `Added ${v}.`, () => { T.name = ""; }); } },
        h("div", { class: "frow-grow", "data-size": "s" }, field("Add a tier", input({ placeholder: "a name, like Weekend", value: T.name, maxlength: 40, ...invalid("err-tname", T.bad), oninput: e => { T.name = e.target.value; if (T.bad && T.name.trim()) { T.bad = ""; done(); } } })), fieldErr("err-tname", T.bad)),
        button("Add", null, { type: "submit", busy: isBusy("tiers-new") })),
      h("p", { class: "field-h" }, "Upkeep (backups, guards, the watchdog) is never paused.")];
  return section({ title: "Work tiers", note: "what may start new work, and until when", level: 1 }, body);
}

// ---- Pause / Resume on one conversation or focus (friction-redesign, ad748 11250102f453: "one obvious Pause / Resume on a thread and on a
// focus ... say what pausing does"). Built on the work tiers above, so the starter honours it: a held tier starts no new run.
// A focus that has a tier to itself pauses that tier, so it can lift by itself at a time; anything else moves into the Off tier until resumed,
// and Resume puts it back where it was (core/tiers.py keeps where it stood on the Off tier; without that, a thread follows its focus again and a focus goes to the default).
// The words say only what the starter does: a held tier starts no new run. It doesn't stop one already running (no signal is sent), so they
// don't promise "agents stop" (review a937f, 11250223e6cf).
export const PAUSE_SAYS = "Nothing new starts. An agent already at work carries on until it stops. A message you write still starts one.";
// Where a thing stood before Pause moved it into Off: core/tiers.py keeps it on the Off tier ("was"), so Resume on any device puts it back
// (a0ad9's review, 112503361aba). This browser's own note is kept only for a core too old to say.
const WAS_KEY = "hive.pause.was";
const offTier = () => tiers().find(t => String(t.id) === OFF);
const wasFor = k => { const o = offTier(), w = o && o.was; return w && typeof w === "object" ? w[k] : wasOf()[k]; };
const wasOf = () => { try { return JSON.parse(localStorage.getItem(WAS_KEY) || "{}") || {}; } catch { return {}; } };
const setWas = (k, v) => { try { const m = wasOf(); if (v == null) delete m[k]; else m[k] = v; localStorage.setItem(WAS_KEY, JSON.stringify(m)); } catch { /* private mode: Resume falls back */ } };
const keyName = k => { const [kind, x] = splitKey(k); return kind === "focus" ? (focusById(x) ? focusName(x) : x) : kind === "thread" ? (isGlob(x) ? `conversations ${x}` : (threadTitle(x) || x)) : `${tr("the timer")} ${x}`; };   // tr: the line it goes in is translated whole, its names are not (product-test nightly 2026-11-25)

/** Where a focus or conversation stands for Pause / Resume, or null before the tiers are read (or without the right to change them).
 *  held: nothing new in it starts now. by: the placement that holds it ("thread:x", "focus:y", or "tier" when its tier itself is paused).
 *  alone: a focus with a tier to itself, so a pause there can end at a set time. others: what else a tier Resume would start. */
export function pauseOf(kind, id, fid) {
  if (!T.data || !can("tier")) return null;
  const t = kind === "focus" ? tierOfFocus(id) : tierOfThread(id, fid); if (!t) return null;
  const k = tierState(t)[0], off = String(t.id) === OFF, holds = t.holds || {}, me = `${kind}:${id}`;
  const own = (holds[kind] || []).includes(id);
  const others = ["focus", "thread", "job"].flatMap(x => (holds[x] || []).map(y => `${x}:${y}`)).filter(x => x !== me);
  const held = off || k === "paused";
  // what holds it: its own place in Off, its focus's place in Off, a pattern such as "1125*" in Off ("elsewhere": Resume opens Settings), or a paused tier
  const by = !held ? null : off ? (own ? me : kind === "thread" && fid && tierOfFocus(fid) === t ? `focus:${fid}` : "elsewhere") : "tier";
  return { t, held, by, own, off, until: !off && t.paused && t.paused.until ? t.paused.until : null, alone: kind === "focus" && own && !others.length && !t.default && !off, others: by === "tier" ? others : [] };
}

/** "Paused" or "Paused until Thu 9:00", for a chip beside the name. */
export const pausedWords = p => p && p.held ? (p.until ? "Paused until " + whenText(p.until) : "Paused") : "";

/** Open Pause (with its "until" choices where the tier allows it) or Resume for one focus or conversation. done redraws the page. */
export function askPause(kind, id, fid, done) {
  const p = pauseOf(kind, id, fid); if (!p) return;
  const me = `${kind}:${id}`, name = keyName(me), redraw = () => done && done();
  // act logs it under "What's happening"; a refusal throws, so a dialog shows it beside its buttons and keeps itself open.
  const run = (op, body, what) => act("tiers-" + op, what, () => post("/api/work-tiers", { op, ...body }), redraw, what).then(r => { if (r && r.tiers) { T.data = r; T.at = Date.now(); } redraw(); });
  const now = f => f().catch(() => redraw());
  if (p.held) {
    if (p.by === "tier") {
      const also = p.others.map(keyName);
      const go = () => run("resume", { tier: String(p.t.id) }, `${name} runs again.`);
      // the default tier also holds everything with no tier of its own, which no list names (review a937f)
      if (p.t.default) also.push(tr("every conversation and focus with no work tier of its own"));
      if (!also.length) return now(go);
      return confirm({ title: `Resume ${name}?`, ok: "Resume", body: [`It is paused with ${also.length === 1 ? `one other` : `${also.length} others`} that share its work tier; resuming starts them too: ${also.slice(0, 6).join(", ")}${also.length > 6 ? ` and ${also.length - 6} more` : ""}.`], onOk: go });
    }
    if (p.by === "elsewhere") { location.hash = "#settings/work-tiers"; return; }
    // held by its own place in Off (or its focus's): back where it was
    const k = p.by, was = wasFor(k), back = was != null && tiers().some(z => String(z.id) === String(was)) ? String(was) : null;
    const what = `${keyName(k)} runs again.`;
    const go = () => (back ? run("set", { what: k, tier: back }, what) : run("unset", { what: k }, what)).then(r => { setWas(k, null); return r; });
    if (k === me) return now(go);
    return confirm({ title: `Resume ${keyName(k)}?`, ok: "Resume", body: [`This conversation is paused with its focus, ${keyName(k)}. Resuming starts the focus and its conversations again.`], onOk: go });
  }
  // Pause. A focus alone in its tier can end at a time; otherwise it lasts until Resume.
  let pick = "none";
  // a conversation of this focus placed in a tier of its own follows that tier, not the focus (core/tiers.py tier_of): say which keep going
  // That covers a pattern too: "fw-*" placed in tier B keeps fw-bench running while its focus pauses (a0ad9's review, 112503361aba).
  const apart = kind !== "focus" ? [] : [...new Set([
    ...tiers().flatMap(t => String(t.id) === OFF ? [] : ((t.holds || {}).thread || []).filter(x => !isGlob(x) && focusOf(x) === id)),
    ...[...S.threads.keys()].filter(th => focusOf(th) === id).map(placementOf).filter(hit => hit && String(hit[1].id) !== OFF).map(hit => hit[0])])].map(x => keyName("thread:" + x));
  const opt = ([v, l]) => h("label", { class: "opt" }, h("input", { type: "radio", name: "pausepick", value: v, checked: v === pick || null, onchange: () => { pick = v; } }), h("b", null, l));
  confirm({
    title: `Pause ${name}?`, ok: "Pause",
    body: [h("p", { class: "pause-says" }, PAUSE_SAYS),
      p.alone ? h("div", { class: "pause-until", role: "radiogroup", "aria-label": "How long" }, PAUSES.map(opt))
        : h("p", { class: "meta" }, "It stays paused until you press Resume here."),
      apart.length ? h("p", { class: "meta pause-apart" }, `${apart.length === 1 ? `One conversation in it has` : `${apart.length} conversations in it have`} a work tier of ${apart.length === 1 ? "its" : "their"} own and keep${apart.length === 1 ? "s" : ""} going: ${apart.slice(0, 4).join(", ")}${apart.length > 4 ? ` and ${apart.length - 4} more` : ""}. Pause ${apart.length === 1 ? "it" : "them"} from ${apart.length === 1 ? "its" : "their"} own menu.`) : null,
      h("p", { class: "meta" }, "To see or change every pause, open Settings, Work tiers.")],
    onOk: () => {
      if (p.alone) { const c = PAUSES.find(x => x[0] === pick), u = c ? c[2]() : null; return run("pause", { tier: String(p.t.id), until: u }, `${name} is paused ${u ? "until " + whenText(u) : "until you resume it"}.`); }
      if (p.own) setWas(me, String(p.t.id)); else setWas(me, null);
      return run("set", { what: me, tier: OFF }, `${name} is paused until you resume it.`);
    },
  });
}

/** The visible pair while paused: a "Paused…" chip and Resume. null while it runs. */
export function pausedStrip(kind, id, fid, done) {
  const p = pauseOf(kind, id, fid); if (!p || !p.held) return null;
  return h("span", { class: "pause-strip", "data-key": "pause-" + kind, role: "group", "aria-label": pausedWords(p) },
    h("span", { class: "chip tier-chip", "data-tone": st("paused").tone, title: PAUSE_SAYS }, pausedWords(p)),
    button("Resume", e => { if (e && e.stopPropagation) e.stopPropagation(); askPause(kind, id, fid, done); }, { size: "s", kind: "quiet", icon: "play", cls: "pause-resume", busy: isBusy("tiers-resume") || isBusy("tiers-set") || isBusy("tiers-unset") }));
}
