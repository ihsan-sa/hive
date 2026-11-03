// References to agents and threads. Real links (#agent/<name>, #comms/<thread>), so they open in a new tab,
// show in the status bar, and work by keyboard. Status is shape + colour + the label in the tooltip.
import { h, copyText } from "../core/dom.js";
import { agentStatus, isAgent, isRunning, threadTitle, tierOf } from "../core/model.js";
import { agentHref, threadHref } from "../core/router.js";
import { status, tier } from "../core/status.js";
import { statusDot } from "./kit.js";
import { openPanel } from "./panel.js";

/** A ref's click: plain clicks open where the old page opened them; ctrl, shift and middle clicks stay ordinary links. */
export function openRef(e, kind, name) {
  if (e.button || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
  e.preventDefault(); e.stopPropagation();
  openPanel(kind, name);
}

/** agentRef(name, {copy, state}) — state: also show the status word. */
export function agentRef(n, { copy, state, href } = {}) {
  if (!n) return "?";
  const s = agentStatus(n), agent = isAgent(n);
  const a = h("a", { class: "ref", "data-kind": "agent", href: href || (agent ? agentHref(n) : threadHref("@" + n)), title: s.label ? `${n}: ${s.label}` : n, onclick: !href && agent ? e => openRef(e, "agent", n) : null },
    statusDot(s.key, { live: isRunning(n) }), h("span", { class: "ref-n" }, n),
    state && s.label && s.key !== "none" ? h("span", { class: "sl", "data-tone": null, style: "font-weight:400" }, status(s.key).label) : null);
  if (!copy || !agent) return a;
  return h("span", { class: "ref-wrap" }, a, copyButton("@" + n));
}

/** threadRef(thread) — the small square is its priority tier. */
export function threadRef(th, { strong } = {}) {
  if (!th) return null;
  const t = tier(tierOf(th));
  return h("a", { class: "ref", "data-kind": strong ? null : "thread", href: threadHref(th), title: `${th} · ${t.label}` },
    h("span", { class: "ref-sw", "data-tone": t.tone, "aria-hidden": "true" }), h("span", { class: "ref-n" }, threadTitle(th)));
}

export function copyButton(text) {
  return h("button", { type: "button", class: "copy", title: `Copy ${text}`, "aria-label": `Copy ${text}`,
    onclick: (e, b) => { e.preventDefault(); e.stopPropagation(); copyText(text).then(() => { b.textContent = "✓"; setTimeout(() => (b.textContent = "⧉"), 900); }); } }, "⧉");
}
