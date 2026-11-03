// Agent card: what one running agent is doing and whether it needs you.
// Hierarchy: name and state, then what it's doing now (the line you read), then what it was asked and last said,
// then one quiet line of what it runs on. On a phone it folds to name, state and the now line; tap to open.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { agentState, agentCard as cardRows } from "../core/model.js";
import { dur, firstSentence, now } from "../core/format.js";
import { status } from "../core/status.js";
import { killButton } from "../actions.js";
import { statusDot } from "./kit.js";
import { agentRef, threadRef } from "./refs.js";

export function agentCard(a, { open, onToggle } = {}) {
  const st = agentState(a), S_ = status(st.key), task = S.byId[a.post], pend = S.pending[a.name];
  const rows = cardRows(a.name);
  return h("article", { class: "ac", "data-key": a.name, "data-tone": S_.tone, "data-attn": st.key === "stuck" ? "" : null, "data-open": open ? "" : null, "aria-label": `${a.name}, ${st.text}` },
    h("button", { type: "button", class: "ac-tap", "aria-expanded": String(!!open), "aria-label": `${open ? "Fold" : "Open"} ${a.name}`, onclick: () => onToggle && onToggle(a.name) }),
    h("header", { class: "ac-h" },
      agentRef(a.name, { copy: true }),
      threadRef(a.thread),
      h("span", { class: "ac-st", title: S_.hint }, st.text)),
    h("p", { class: "ac-now clamp-2" }, a.doing || "Starting"),
    h("dl", { class: "ac-dl" },
      h("dt", null, "Asked"), h("dd", { class: "clamp-2", title: task ? task.text : null }, a.from ? a.from + ": " : "", firstSentence(task && task.text)),
      a.said ? [h("dt", null, "Said"), h("dd", { class: "clamp-2", title: a.said }, firstSentence(a.said))] : null),
    h("footer", { class: "ac-f" },
      rows.map(r => h("span", { title: `${r.k}: ${r.v}${r.hint ? " · " + r.hint : ""}` }, r.short)),
      h("span", { title: "running for" }, "up " + dur(now() - a.since * 1e3)),
      pend ? h("span", { "data-tone": "info" }, `${pend.posts} more post${pend.posts > 1 ? "s" : ""} queued`) : null,
      a.alive ? killButton(a.name) : null));
}
