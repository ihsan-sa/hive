// References to agents and threads. Real links (#agent/<name>, #comms/<thread>), so they open in a new tab,
// show in the status bar, and work by keyboard. Status is shape + colour + the label in the tooltip.
import { h, copyText } from "../core/dom.js";
import { agentBrief, agentLabel, agentStatus, isAgent, isRunning, isWarden, threadTitle, tierOf } from "../core/model.js";
import { agentHref, threadHref } from "../core/router.js";
import { status, tier } from "../core/status.js";
import { statusDot, wardenMark } from "./kit.js";
import { openPanel } from "./panel.js";

/** A ref's click: plain clicks open where the old page opened them; ctrl, shift and middle clicks stay ordinary links. */
export function openRef(e, kind, name) {
  if (e.button || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
  e.preventDefault(); e.stopPropagation();
  openPanel(kind, name);
}

/** agentRef(name, {copy, state}) — state: also show the status word. */
export function agentRef(n, { copy, state, href, dot = true, pill } = {}) {
  if (!n) return "?";
  const s = agentStatus(n), agent = isAgent(n), warden = isWarden(n);
  const a = h("a", { class: pill ? "ref pill" : "ref", "data-kind": "agent", "data-name": n, href: href || (agent ? agentHref(n) : threadHref("@" + n)), title: `${n}${warden ? ", a warden" : ""}${s.label ? `: ${s.label}` : ""}`, onclick: !href && agent ? e => openRef(e, "agent", n) : null },
    dot ? statusDot(s.key, { live: isRunning(n) }) : null, h("span", { class: "ref-n" }, agentLabel(n)), warden ? wardenMark() : null,
    state && s.label && s.key !== "none" ? h("span", { class: "sw", "data-tone": null, style: "font-weight:400" }, status(s.key).label) : null);
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
    onclick: (e, b) => { e.preventDefault(); e.stopPropagation(); copyText(text).then(() => { b.textContent = "✓"; setTimeout(() => (b.textContent = "⧉"), 900); }, (er) => { b.title = er.message; b.textContent = "!"; setTimeout(() => (b.textContent = "⧉"), 1800); }); } }, "⧉");
}

/** agentAvatar(name) — the agent mark: a soft square with the name's two middle letters (a7c31 → 7c) and its status shape; a link to the agent. For stacks (.avs). */
export function agentAvatar(n, { size } = {}) {
  const s = agentStatus(n), agent = isAgent(n), label = `${n}${s.label ? ", " + status(s.key).label.toLowerCase() : ""}`;
  return h("a", { class: "av", "data-name": n, "data-size": size || null, href: agent ? agentHref(n) : threadHref("@" + n), title: label, "aria-label": label, onclick: agent ? e => openRef(e, "agent", n) : null },
    h("span", null, mono(n)), s.key !== "none" ? statusDot(s.key, { live: isRunning(n) }) : null);
}
// An agent's two marks: a8779 → 87 (every agent starts with a letter), other names their first two letters.
const mono = n => /^[a-z][0-9a-f]{4,}$/.test(n) ? n.slice(1, 3) : String(n).slice(0, 2);
/** A stack of avatars, at most max, then +N. Hovering it (or tabbing into it) opens a list of every name with its status, each a link. */
export function avatars(names, { max = 4, size } = {}) {
  if (!names.length) return null;
  const running = names.filter(n => isRunning(n)).length;
  return h("span", { class: "avs-w", tabindex: "0", "aria-label": `${names.length} agents here; show the list` },
    h("span", { class: "avs" }, names.slice(0, max).map(n => agentAvatar(n, { size })), names.length > max ? h("span", { class: "av", "data-size": size || null }, "+" + (names.length - max)) : null),
    h("div", { class: "avs-pop", role: "group", "aria-label": "Agents here" }, h("div", { class: "avs-pop-l" },
      h("div", { class: "avs-pop-h meta", style: "--i:0" }, `${names.length} ${names.length === 1 ? "agent" : "agents"}` + (running ? ` · ${running} running` : "")),
      [...names].sort((a, b) => isRunning(b) - isRunning(a)).map((n, i) => h("div", { class: "avs-pop-r", "data-key": n, style: `--i:${Math.min(i + 1, 12)}` }, pill(n))))));
}
// A pill in the list: name, state, then model and cost so far (the person, 2026-11-07).
function pill(n) {
  const a = agentRef(n, { state: true }), b = isAgent(n) ? agentBrief(n) : "";
  if (b) a.append(h("span", { class: "sw avs-pop-m" }, b));
  return a;
}
