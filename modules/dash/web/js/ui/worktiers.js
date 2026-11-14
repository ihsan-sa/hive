// Work tiers (core/tiers.py; the person, 111311199053): each focus, thread and timer sits in a tier; a tier can be paused and has a
// stop point. Settings > Work tiers changes them through /api/work-tiers; a focus card and a thread's bar show a chip from the same read.
// State is in plain words and never a usage figure (a9179's review, rung WT-1): Running, Paused until Thu 9:00, Off, Stopped: …
import { h } from "../core/dom.js";
import { LOC } from "../core/format.js";
import { status as st } from "../core/status.js";
import { S, can, jget, post } from "../core/store.js";
import { focusById, focusName } from "../core/foci.js";
import { clock, dayName, ago } from "../core/format.js";
import { section, block, blocks, button, field, input, statusLabel, waiting } from "./kit.js";
import { act, isBusy } from "./activity.js";
import { confirm } from "./dialog.js";

const T = { data: null, err: null, at: 0, p: null, say: null, name: "", rename: {}, open: {}, logOpen: false };
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
const glob = g => new RegExp("^" + g.replace(/[.+^${}()|\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".") + "$");
export function tierOfFocus(fid) {
  const ts = tiers(); return ts.find(t => ((t.holds || {}).focus || []).includes(fid)) || ts.find(t => t.default) || null;
}
export function tierOfThread(th, fid) {
  const ts = tiers(), pl = ts.flatMap(t => ((t.holds || {}).thread || []).map(g => [g, t]));
  const hit = pl.find(([g]) => g === th) || pl.filter(([g]) => /[*?]/.test(g) && glob(g).test(th)).sort((a, b) => b[0].length - a[0].length)[0];
  return hit ? hit[1] : fid ? tierOfFocus(fid) : ts.find(t => t.default) || null;
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
async function change(op, body, done, what) {
  T.say = null;
  try { T.data = await act("tiers-" + op, what || "Changing work tiers", () => post("/api/work-tiers", { op, ...body }), done); T.at = Date.now(); T.say = what ? { text: what, tone: "good" } : null; }
  catch (e) { T.say = { text: `Nothing changed: ${e.message}`, tone: "bad" }; }
  done();
}

// Pause choices: a time it lifts by itself, or until resumed.
const nextAt = (days, hour) => { const d = new Date(); d.setDate(d.getDate() + days); d.setHours(hour, 0, 0, 0); return d.getTime() / 1e3; };
const nextMonday = () => { const d = new Date(), n = ((8 - d.getDay()) % 7) || 7; return nextAt(n, 9); };
const PAUSES = [["3h", "For 3 hours", () => Math.round(Date.now() / 1e3 + 3 * 3600)], ["tom", "Until tomorrow 9:00", () => nextAt(1, 9)], ["mon", "Until Monday 9:00", nextMonday], ["none", "Until I resume it", () => null]];
const STOPS = [["none", "No stop of its own"], ...[95, 90, 85, 80, 70, 60, 50, 40, 30].map(v => [String(v), `Stop at ${v}% of the week`])];

const KIND = { focus: "Focus", thread: "Thread", job: "Timer" };
function itemRow(kind, x, t, done) {
  const name = kind === "focus" ? focusName(x) : x, label = kind === "focus" && !focusById(x) ? x : name, key = `${kind}:${x}`;
  return h("li", { class: "lr tier-it", "data-key": key },
    h("div", { class: "lr-in frow fsplit" },
      h("span", { class: "tier-it-n" }, h("span", { class: "muted" }, (kind === "thread" && x.includes("*") ? "Threads" : KIND[kind]) + " "), h("b", null, label)),
      h("span", { class: "frow-end" }, h("select", { class: "input", "aria-label": `Move ${label} to`, title: `Move ${label} to another tier`, disabled: isBusy("tiers-set") || null,
        onchange: e => { const to = e.target.value; if (to === "_unset") return change("unset", { what: key }, done, `${label} follows ${kind === "thread" ? "its focus" : "the default tier"} now.`); const tt = tiers().find(z => String(z.id) === to); change("set", { what: key, tier: to }, done, `Moved ${label} to ${tt ? tt.name : to}.`); } },
        h("option", { value: "", selected: "" }, "Move to…"),
        tiers().filter(z => z.id !== t.id).map(z => h("option", { value: String(z.id) }, z.name)),
        kind !== "focus" ? h("option", { value: "_unset" }, kind === "thread" ? "Follow its focus" : "Take out of tiers") : null))));
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
    items.length ? h("ul", { class: "list tier-items" }, items) : h("p", { class: "field-h" }, t.default ? "Every focus you don't place goes here." : "Nothing is in this tier. Move a focus here from its own tier."),
    id === OFF ? null : h("details", { class: "tier-more", "data-key": "more", open: T.open[id] ? "" : null, ontoggle: e => { T.open[id] = e.target.open; } }, h("summary", null, "Stop point, name and more"),
    h("div", { class: "frow" },
      h("select", { class: "input", "aria-label": `Stop point for ${t.name}`, title: "Past this share of the week's usage, nothing new in this tier starts", onchange: e => change("stop", { tier: id, pct: e.target.value }, done, `${t.name}: stop point saved.`) },
        STOPS.map(([v, l]) => h("option", { value: v, selected: (t.stop == null ? "none" : String(t.stop)) === v ? "" : null }, l))),
      t.default ? null : button("Make default", () => change("default", { tier: id }, done, `Work with no tier now goes to ${t.name}.`), { size: "s", kind: "quiet" })),
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); const v = (T.rename[id] || "").trim(); if (v && v !== t.name) change("rename", { tier: id, name: v }, () => { delete T.rename[id]; done(); }, `Renamed to ${v}.`); } },
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
  const body = !T.data ? waiting(T.err, "Reading…", "the tiers")
    : [h("p", { class: "tier-intro" }, "Every focus, thread and timer sits in a tier. Pause a tier and nothing new in it starts; what is already running finishes. A pause with an end lifts by itself."),
      T.say ? h("p", { class: "fmsg", "data-tone": T.say.tone, role: "status" }, T.say.text) : null,
      blocks(...tiers().map(t => tierBlock(t, done)), logBlock()),
      h("form", { class: "frow", "data-key": "tier-new", onsubmit: e => { e.preventDefault(); const v = T.name.trim(); if (!v) { T.say = { text: "Give the new tier a name first.", tone: "bad" }; return done(); } change("new", { name: v }, () => { T.name = ""; done(); }, `Added ${v}.`); } },
        h("div", { class: "frow-grow", "data-size": "s" }, field("Add a tier", input({ placeholder: "a name, like Weekend", value: T.name, maxlength: 40, oninput: e => { T.name = e.target.value; } }))),
        button("Add", null, { type: "submit", busy: isBusy("tiers-new") })),
      h("p", { class: "field-h" }, "Upkeep (backups, guards, the watchdog) is never paused.")];
  return section({ title: "Work tiers", note: "what may start new work, and until when", level: 1 }, body);
}
