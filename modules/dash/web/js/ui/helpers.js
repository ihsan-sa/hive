// Helpers (subagents): what an agent starts inside its own Claude Code or Codex run. The board never sees one and nobody can message one,
// so a helper is always marked as one (the word, a harness mark, a tree line, lighter type) and never shares an agent's count.
// Design: designs/subagents-ui (NOTES.md), asked for by the person in thread 111919537629. CSS: css/helpers.css (.sa-).
import { h, s } from "../core/dom.js";
import { S } from "../core/store.js";
import { agentCost, modelName, sEnd } from "../core/model.js";
import { ago, dur, int, money, now, plural, stamp } from "../core/format.js";
import { agentHref, threadHref } from "../core/router.js";
import { button } from "./kit.js";
import { agentRef, copyButton, threadRef } from "./refs.js";

export const HX = { "claude-code": ["cc", "Claude Code"], codex: ["cx", "Codex"] };
const T = v => (v ? Date.parse(v) : null);
/** How long it ran, or has been running. */
export const took = r => { const a = T(r.start), b = r.state === "running" ? now() : T(r.end); return a && b && b >= a ? b - a : null; };
export const tookText = r => { const v = took(r); return v == null ? "–" : v < 1000 ? "<1s" : dur(v); };
export const hModel = r => (r.model ? modelName(r.model) : r.harness === "codex" ? "Codex" : "–");
export const modeWord = r => (r.mode === "foreground" ? "owner waits" : r.mode === "background" ? "background" : r.mode || "–");
const modeLong = r => (r.mode === "foreground" ? `${r.agent} waited for it to finish` : r.mode === "background" ? `${r.agent} kept working while it ran` : "not recorded");
export const costText = r => (r.usd != null ? money(r.usd) : r.harness === "codex" ? "–" : "–");
const costTitle = r => (r.usd == null && r.harness === "codex" ? "Codex doesn't report what a helper cost" : null);
export const helperKey = r => `${r.harness}:${r.id}`;
const STATE = { running: "running", done: "done", stopped: "stopped" };

/** The marks: its harness (cc, or cx with a dashed edge), the word "helper" (or "helper's helper"), and its state as a shape and a word. */
export const hx = r => { const [k, say] = HX[r.harness] || ["?", r.harness || "unknown"]; return h("span", { class: "sa-hx", "data-h": r.harness === "codex" ? "codex" : null, title: say }, k); };
export const helperTag = r => h("span", { class: "sa-tag" }, (r.depth || 1) > 1 ? "helper's helper" : "helper");
export const helperState = r => h("span", { class: "sa-st", "data-s": STATE[r.state] || "done", title: r.state === "stopped" ? "Its owner's session ended before it reported" : null }, STATE[r.state] || r.state || "–");

/** "· 14 helpers", to ride after an agent count. onClick makes the figure a button. Null at 0, so nothing changes when no helper runs. */
export function helperCount(n, { onClick, after } = {}) {
  if (!n) return null;
  const fig = [h("b", null, int(n)), n === 1 ? " helper" : " helpers"];
  return h("span", { class: "sa-count" }, onClick ? h("button", { type: "button", onclick: onClick }, fig) : fig, after || null);
}

// ---- Now: helper rows under the agent that owns them (or under "Still finishing for an agent that has ended") ----
/** Is the Now page showing helper rows? Remembered per device; on unless turned off. */
const SHOW_KEY = "now.helpers";
export const showHelpers = () => { try { return localStorage.getItem(SHOW_KEY) !== "0"; } catch (e) { return true; } };
export function showSwitch(onChange) {
  const box = h("input", { class: "switch", type: "checkbox", checked: showHelpers() || null, onchange: e => { try { localStorage.setItem(SHOW_KEY, e.target.checked ? "1" : "0"); } catch (x) { /* no storage */ } onChange(); } });
  return h("label", { class: "sa-show", title: "Helpers are what an agent starts inside its own run. You can't message one." }, box, "Show helpers");
}
/** "3 helpers": the chip an agent row keeps when helper rows are hidden. */
export const helperChip = n => (n ? h("span", { class: "sa-chip", title: plural(n, "helper") + " working for it" }, plural(n, "helper")) : null);

const OPEN = new Set();   // owners whose "N more helpers" is open, so a poll's redraw keeps it open
const byType = rows => { const m = new Map(); for (const r of rows) m.set(r.type || "helper", (m.get(r.type || "helper") || 0) + 1); return [...m].sort((a, b) => b[1] - a[1]).map(([k, v]) => `${v} ${k}`).join(", "); };
const chev = () => s("svg", { viewBox: "0 0 12 12", fill: "none", stroke: "currentColor", "stroke-width": 1.5, "aria-hidden": "true" }, s("path", { d: "M3 4.5l3 3 3-3" }));
const what = r => h("span", { class: "sa-what" }, hx(r), helperTag(r), h("span", { class: "sa-type" }, r.type || "helper"),
  h("a", { class: "sa-desc", href: "#", title: r.desc || null, onclick: e => { e.preventDefault(); openHelper(r); } }, r.desc || "(no task given)"));
/** One running helper as a row. dev: the developer table's cells (who, what it is doing, time); else the app table's (who, mode, model, calls, time). */
function kid(r, dev) {
  const calls = r.calls ? `${plural(r.calls, "call")} so far` : "starting";
  const line = [hModel(r), modeWord(r), r.calls ? plural(r.calls, "call") : null].filter(Boolean).join(" · ");
  return h("li", { class: "sa-kid", "data-depth": (r.depth || 1) > 1 ? "2" : null, "data-key": helperKey(r) }, what(r),
    dev ? h("span", { class: "sa-k-model sa-meta", title: calls }, line) : [h("span", { class: "sa-k-mode sa-meta" }, modeWord(r)), h("span", { class: "sa-k-model sa-meta" }, hModel(r)), h("span", { class: "sa-k-st sa-meta" }, calls)],
    h("span", { class: "sa-k-line" }, line), h("span", { class: "sa-time" }, tookText(r)), h("span", { class: "sa-k-end" }));
}
/** An owner's running helpers, three shown and the rest behind "N more helpers · 5 general-purpose, 1 web-fetch". */
export function helperKids(owner, rows, { dev, update } = {}) {
  if (!rows || !rows.length) return null;
  const open = OPEN.has(owner) || rows.length <= 4, shown = open ? rows : rows.slice(0, 3), rest = rows.slice(3);
  return h("ul", { class: "sa-kids", "aria-label": `${plural(rows.length, "helper")} working for ${owner}` }, shown.map(r => kid(r, dev)),
    rows.length > 4 ? h("li", null, h("button", { class: "sa-more", type: "button", "aria-expanded": String(open), onclick: () => { open ? OPEN.delete(owner) : OPEN.add(owner); update && update(); } },
      chev(), open ? "Show fewer helpers" : `${plural(rest.length, "more helper")} · ${byType(rest)}`)) : null);
}
/** When an agent's last session ended (ms), or null if it never did or isn't known. */
export const ownerEnded = n => { const x = S.lastSess && S.lastSess[n]; return x && x.end ? sEnd(x) : null; };
/** Running helpers whose owner is not running: their own quiet group, since nobody waits on their reports. */
export function orphanGroup(groups, { dev, update } = {}) {
  if (!groups.length) return null;
  const n = groups.reduce((a, g) => a + g.rows.length, 0);
  return h("section", { class: "sa-orph", "data-key": "sa-orph" }, h("h2", { class: "sa-orph-h" }, "Still finishing for an agent that has ended ",
    h("span", null, `${plural(n, "helper")} · ${n === 1 ? "it ends" : "they end"} on its own, and ${n === 1 ? "its report goes" : "their reports go"} nowhere`)),
    groups.map(g => { const seen = ownerEnded(g.owner);
      return [h("p", { class: "sa-owner", "data-key": "own-" + g.owner }, agentRef(g.owner), h("span", { class: "sa-meta" }, seen ? "ended " + ago(seen) : "ended"), g.rows[0].agent_thread ? threadRef(g.rows[0].agent_thread) : null),
        h("div", { class: "sa-orph-l" }, helperKids(g.owner, g.rows, { dev, update }))]; }));
}

// ---- An agent's page: its helpers, newest first ----
const LI_HEAD = ["Status", "Type", "Task", "Model", "Mode", "Took", "Calls", "Cost"];
const LIST_OPEN = new Set();
export function helperList(owner, rows, { update } = {}) {
  const open = LIST_OPEN.has(owner) || rows.length <= 8, shown = open ? rows : rows.slice(0, 6), rest = rows.slice(6);
  const restUsd = rest.reduce((a, r) => a + (r.usd || 0), 0);
  return h("div", null, h("div", { class: "sa-list", role: "table", "aria-label": "Helpers" },
    h("div", { class: "sa-li", "data-head": "", role: "row" }, LI_HEAD.map((l, i) => h("span", { role: "columnheader", class: i >= 5 ? "num" : null }, l))),
    shown.map(r => h("div", { class: "sa-li", role: "row", "data-depth": (r.depth || 1) > 1 ? "2" : null, "data-key": helperKey(r) },
      h("span", { class: "sa-l-st", role: "cell" }, helperState(r)),
      h("span", { class: "sa-l-type", role: "cell" }, h("span", { class: "sa-who" }, hx(r), h("span", { class: "sa-type" }, r.type || "helper"))),
      h("span", { class: "sa-task", role: "cell" }, (r.depth || 1) > 1 ? helperTag(r) : null, h("a", { href: "#", class: "sa-desc", title: r.desc || null, onclick: e => { e.preventDefault(); openHelper(r); } }, r.desc || "(no task given)")),
      h("span", { class: "sa-l-line", role: "cell" }, [r.type, hModel(r), modeWord(r), tookText(r)].filter(Boolean).join(" · ")),
      h("span", { class: "sa-l-model sa-meta", role: "cell" }, hModel(r)), h("span", { class: "sa-l-mode sa-meta", role: "cell" }, modeWord(r)),
      h("span", { class: "sa-l-took num", role: "cell" }, tookText(r)), h("span", { class: "sa-l-calls num", role: "cell" }, r.calls != null ? int(r.calls) : "–"),
      h("span", { class: "sa-l-cost num", role: "cell", title: costTitle(r) }, costText(r))))),
    rows.length > 8 ? h("div", { class: "lr-more" }, button(open ? "Show fewer" : `Show ${rest.length} more` + (restUsd ? ` · ${money(restUsd)}` : ""), () => { open ? LIST_OPEN.delete(owner) : LIST_OPEN.add(owner); update && update(); }, { kind: "quiet", size: "s" })) : null);
}

// ---- One helper: a drawer (a sheet on a phone). Not a page: a helper only makes sense beside its owner. ----
export function openHelper(r) {
  document.querySelector("dialog.sa-drawer")?.close();
  const ownUsd = agentCost(r.agent);
  const dl = rows => h("dl", { class: "sa-dl" }, rows.filter(Boolean).map(([k, v, small]) => [h("dt", null, k), h("dd", null, v, small ? h("small", null, small) : null)]));
  const dlg = h("dialog", { class: "sa-drawer", "aria-label": "Helper: " + (r.desc || r.type || "") },
    h("div", { class: "sa-drawer-b" }, hx(r), helperTag(r), r.type || "helper", button("Close", () => dlg.close(), { kind: "quiet", size: "s" })),
    h("div", { class: "sa-drawer-in" },
      h("div", null, h("h2", null, r.desc || "(no task given)"), h("p", { class: "sa-line" }, helperState(r), h("span", { class: "meta" }, r.state === "running" ? `started ${ago(T(r.start))}` : r.end ? `ended ${ago(T(r.end))}` : ""))),
      h("div", { class: "sa-cant" }, s("svg", { viewBox: "0 0 16 16", fill: "none", stroke: "currentColor", "stroke-width": 1.5, "aria-hidden": "true" }, s("circle", { cx: 8, cy: 8, r: 6.5 }), s("path", { d: "M8 7v4M8 4.8v.2" })),
        h("p", { style: "margin:0" }, `You can't message a helper. It works for ${r.agent}, inside ${r.agent}'s own run, and reports only to it. To change what it does, write to ${r.agent}.`),
        h("div", { class: "btn-row" }, button("Open " + r.agent, () => dlg.close(), { size: "s", href: agentHref(r.agent) }), r.agent_thread ? button("Open conversation", () => dlg.close(), { size: "s", kind: "quiet", href: threadHref(r.agent_thread) }) : null)),
      h("div", null, h("h3", { class: "sa-h3" }, "Where it came from"), dl([
        ["Agent", agentRef(r.agent)],
        (r.depth || 1) > 1 ? ["Started by", "another helper of " + r.agent] : null,
        r.agent_thread ? [S.app.app ? "Conversation" : "Thread", threadRef(r.agent_thread)] : null])),
      h("div", null, h("h3", { class: "sa-h3" }, "The run"), dl([
        ["Harness", (HX[r.harness] || [0, r.harness])[1]],
        ["Type", r.type || "–"],
        ["Model", hModel(r), r.model && hModel(r) !== r.model ? r.model : null],
        ["Mode", modeWord(r), modeLong(r)],
        ["Started", r.start ? stamp(T(r.start)) : "–"],
        ["Ended", r.state === "running" ? "still running" : r.end ? stamp(T(r.end)) : "–", r.state === "stopped" ? "its owner's session ended before it reported" : null],
        ["Took", tookText(r)],
        ["Work", [r.calls != null ? plural(r.calls, "model call") : null, r.tools != null ? plural(r.tools, "tool call") : null, r.out_tokens ? int(r.out_tokens) + " output tokens" : null].filter(Boolean).join(", ") || "–"],
        ["Cost", h("span", { title: costTitle(r) }, costText(r)), r.usd != null ? `at API list prices; counted inside ${r.agent}'s ${ownUsd ? money(ownUsd) : "cost"}` : costTitle(r)]])),
      r.file ? h("div", null, h("h3", { class: "sa-h3" }, "Transcript"), h("code", { class: "sa-path" }, r.file),
        h("p", { class: "meta" }, "Helpers don't stream to the board; this file is its whole run."), copyButton(r.file)) : null));
  dlg.addEventListener("close", () => dlg.remove());
  dlg.addEventListener("click", e => { if (e.target === dlg) dlg.close(); });   // a click on the backdrop
  document.body.append(dlg); dlg.showModal();
}
