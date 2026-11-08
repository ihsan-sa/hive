// A project's card: what it needs from you first, what it did today, what comes next; the threads and agents behind it one click down.
// Flow opens it in the side panel from a goal's lane. It was the Work page's card; the page went (R5: the focus pages carry what it showed).
// Everything here already exists: needs_you.py's state rows (with the ladder's open rung from goals), the board's result posts
// (the first line of a post whose last line starts Done:/Waiting:/Blocked:), the threads' status bars and the live agent list.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { waitingOn } from "../core/needs.js";
import { threadChildren, threadTitle, displayText } from "../core/model.js";
import { ago, plural, mdPlain, short, DAY, now } from "../core/format.js";
import { gist } from "../core/gist.js";
import { threadHref, go } from "../core/router.js";
import { agentRef } from "./refs.js";
import { button } from "./kit.js";
import { sayTo } from "./composer.js";

const when = at => (at ? ago(Date.parse(at)) : "");
const RESULT = /^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*/i;
/** The result posts of the last day in these threads: {text, word, thread, t}, newest first. The first line says what was done; the last names how it ended. */
function today(threads) {
  const out = [], since = now() - DAY;
  for (const th of threads) {
    const d = S.threads.get(th); if (!d) continue;
    for (const p of d.posts) {
      if (p.t < since || p.from === "person") continue;
      const lines = String(p.text || "").split("\n").map(l => l.trim()).filter(Boolean);
      const m = lines.length > 1 && lines[lines.length - 1].match(RESULT); if (!m) continue;
      const plain = mdPlain(displayText(p)).split("\n").map(l => l.trim()).filter(Boolean);
      if (/^\(?this run ended without posting/i.test(plain[0] || "")) continue;   // the starter's relay of an unposted answer: nobody checked it
      const text = gist(plain.slice(0, -1), lines[lines.length - 1]);   // the plainer of the first line and the Done line; names fade
      if (text) out.push({ text: short(text, 150), word: m[1].toLowerCase(), thread: th, t: p.t, id: p.id });
    }
  }
  return out.sort((a, b) => b.t - a.t);
}
/** The status bar's second line, "Word: the rest", as its word and text. */
function stand(th) {
  const r = S.threadStatus[th]; if (!r || !r.text) return null;
  const l = r.text.split("\n").map(x => x.trim()).filter(Boolean), m = (l[1] || "").match(/^([A-Za-z][\w ]*?):\s*(.*)$/);
  return { about: l[0] || "", word: m ? m[1].toLowerCase() : "", line: m ? m[2] : l[1] || "", at: r.at ? r.at * 1e3 : 0 };
}
const rungText = r => String(r || "").split(/\s+check:/i)[0].trim();
const ordinal = key => { const m = String(key || "").match(/^(\d+)\.\s*/); return m ? m[1] : null; };

/** One project's reading of the data: everything a card or a row shows. */
function reading(s) {
  const threads = [s.thread, ...(s.thread ? threadChildren(s.thread) : [])].filter(Boolean);
  const running = [...S.running.entries()].filter(([, a]) => threads.includes(a.thread)).map(([n]) => n);
  const did = today(threads), st = s.thread ? stand(s.thread) : null;
  const asks = waitingOn().filter(w => threads.includes(w.th)).map(w => ({ thread: w.th, action: w.text, post: w.post, t: w.t }));   // the one list (core/needs.js), newest first
  const needs = asks.length;
  const blocked = st && /^(blocked|waiting|needs the person)$/.test(st.word);
  const state = running.length ? { key: "moving", tone: "good", shape: "dot", label: `moving, ${plural(running.length, "agent")} on it` }
    : needs ? { key: "needs", tone: "info", shape: "square", label: "needs you" }
    : blocked ? { key: "held", tone: "warn", shape: "half", label: st.word === "blocked" ? "blocked" : "waiting" }
    : did.length ? { key: "done", tone: "ended", shape: "dot", label: "quiet, moved today" }
    : { key: "quiet", tone: "quiet", shape: "ring", label: "quiet" };
  const total = (s.rungs_done || 0) + (s.rungs_left || 0);
  const next = s.rung ? { text: rungText(s.rung), step: total ? `step ${(s.rungs_done || 0) + 1} of ${total}` : "" } : total && !s.rungs_left ? { text: "The ladder is done: nothing is left on it.", step: `${total} steps done` } : null;
  return { threads, running, did, st, asks, needs, state, next };
}

const dot = st => h("span", { class: "st", "data-tone": st.tone, "data-live": st.key === "moving" ? "" : null, "aria-hidden": "true" });
const didLine = d => h("li", null, h("a", { href: threadHref(d.thread), title: `${threadTitle(d.thread)}, ${ago(d.t)}` }, d.text), d.word !== "done" ? h("span", { class: "meta wk-w" }, " · " + d.word) : null);

/** A focus or project card: what it needs from you first (the newest ask), what it did today, what comes next; the footer is its state and agents. */
export function card(s) {
  const r = reading(s), n = ordinal(s.key), did = r.did.slice(0, 3);
  const part = (key, label, body) => (body ? h("div", { class: "wk-p", "data-part": key }, h("dt", null, label), h("dd", null, body)) : null);
  const ask = r.asks[0], more = r.asks.length - 1;
  const decided = ask && ask.post && S.byId[ask.post] ? (String(S.byId[ask.post].text || "").match(/^\W*Decided:\s*(.+)$/im) || [])[1] : null;   // the same "Decided:" line Flow reads
  const who = r.running[0] || (s.thread && S.threadStatus[s.thread] && S.threadStatus[s.thread].from) || null;
  return h("article", { class: "card wk", "data-key": "wk:" + s.key, "data-state": r.state.key },
    h("div", { class: "wk-hd" }, dot(r.state), h("h3", { class: "wk-t", title: s.state ? `${r.state.label}. ${mdPlain(s.state)}${s.at ? " (" + when(s.at) + ")" : ""}` : r.state.label },
      n ? h("span", { class: "wk-n" }, n + " ") : null, s.thread ? h("a", { href: threadHref(s.thread) }, s.name) : s.name)),
    h("dl", { class: "wk-b" },
      part("needs", "Needs you", r.needs || s.open_asks ? [ask ? h("a", { href: threadHref(ask.thread) }, ask.action) : null,
        decided ? h("span", { class: "meta" }, " If you don't answer: " + short(decided, 120)) : null,
        more > 0 ? h("span", { class: "meta" }, ` · and ${more} more`) : null,
        s.open_asks ? h("span", { class: "meta" }, `${ask ? " · " : ""}${plural(s.open_asks, "open request")} of yours ${s.open_asks === 1 ? "is" : "are"} on it.`) : null] : null),
      part("today", "Today", did.length ? [h("ul", { class: "wk-l" }, did.map(didLine)), r.did.length > 3 ? h("p", { class: "meta" }, `and ${r.did.length - 3} more in the thread`) : null] : h("span", { class: "muted" }, "No step finished in the last day.")),
      part("next", r.next ? "Next" + (r.next.step ? " · " + r.next.step.replace(/^step /, "step ") : "") : "Next", r.next ? r.next.text : h("span", { class: "muted" }, "No ladder: the thread says what comes next."))),
    h("div", { class: "wk-ft meta" }, h("span", { class: "wk-state" }, r.state.label), r.running.length ? h("span", { class: "wk-who" }, r.running.map(a => agentRef(a))) : null,
      r.threads.length > 1 ? `+${r.threads.length - 1} helper ${r.threads.length > 2 ? "threads" : "thread"}` : null,
      s.thread && who ? button(`Say something to ${who}`, () => { sayTo(s.thread, who); go("comms", s.thread); }, { kind: "icon", size: "s", icon: "chat", cls: "wk-say" }) : null));
}
