// The Focus page (#focus/<id>, #focus/misc): one focus a level deeper than its home card. Its summary in full (Now, Lately),
// what waits on the person in it, the agents' to-do list, its conversations, who is on it now and in the last day, and its numbers.
// A focus is an abstraction for the person (person-doc-2026-11-07): the rail's By focus view and the home's cards open it.
// Stats hide in simple mode. Misc is the unfiled conversations: a plain list, no summary, no to-do.
// It shows in the chat's layout (comms.js inChat), where a conversation would: the rail beside it, its focus marked there.
import { h } from "../core/dom.js";
import { S, can, post } from "../core/store.js";
import { threadTitle, agentOn } from "../core/model.js";
import { plural, dur, ago } from "../core/format.js";
import { href, go, threadHref } from "../core/router.js";
import { simpleOn, plainName } from "../core/simple.js";
import { button, empty, statusWord, section } from "../ui/kit.js";
import { agentRef } from "../ui/refs.js";
import { loadFoci, fociFailed, focusById, focusOf, threadsOf, needsOf, askedOf, agentsOf, stateOf, pills, plainPills, needSource, scrub, threadLine, focusName, MISC, FOCI_EVERY, activityOf, shareOf, fractionWords, pct, usd } from "../core/foci.js";
import { waitingLater } from "../core/needs.js";
import { archived } from "../ui/threadmeta.js";
import { draft, answer } from "../ui/composer.js";
import { inChat, chatEscape, chatLeave, answerer, summaryWait, sumLines, lineText, flat, liftIn, updatedAt, rowDot } from "./comms.js";
import { quickIs, laterButtons, setLater } from "../ui/quick.js";
import { when } from "../ui/when.js";
import { yoursPosts, yoursText } from "../core/yours.js";
import { railSay } from "../core/foci.js";
import { jump } from "../ui/message.js";
import { openQuick } from "../ui/quick.js";

const idOf = arg => (arg === "misc" ? null : arg);
const say = t => (simpleOn() ? plainPills(t) : pills(t));
const FP = { more: false, yours: 3, mine: false, sent: [] };   // sent: what the steer box sent from this page, with its state   // the Quiet and archived conversations, shown on asking (this page only)

/** Start a new conversation already tagged with this focus: the composer carries the tag and posts it with the first message. */
export function newIn(id) { draft(null).focus = id || ""; go("comms", "~new"); }

// The page, v3 (designs/ui-redesign-v3/V3-HANDOFF.md 3b): a header with the state and aim, then a main column (Needs you first, Now,
// To do, Lately, Conversations) and a side column (Agents, Activity). Every line full length: the card clamps, the page doesn't.
const head = (title, note, more) => h("div", { class: "fp-h" }, h("h2", null, title), note ? h("span", { class: "sec-n" }, note) : null, more || null);
const sec = (key, cls, title, note, ...kids) => h("section", { class: ["fp-sec", cls], "data-key": "fp-" + key, "aria-label": title }, head(title, note), ...kids);

function threadRow(r) {
  const line = threadLine(r.th), d = S.threads.get(r.th), dot = rowDot(r.th);
  return h("li", { class: "lr", "data-key": "th:" + r.th }, h("a", { class: "lr-in", href: threadHref(r.th) },
    h("span", { class: "lr-t" }, dot ? h("span", { class: "st", "data-tone": dot.tone, title: dot.word }) : null, h("b", null, threadTitle(r.th)), d ? when(d.last, "t") : null),
    line ? h("span", { class: "lr-x clamp-1" }, say(line)) : null));
}
// A box the person can tick (the person, 110812372f5e): it marks the item done for the agents (the focus check done command), an untick reopens it.
function tick(c, ctx) {
  const was = c.done, wok = c.ok; c.done = was ? null : Date.now() / 1e3; c.ok = null; ctx.update();
  post("/api/focus", { check: c.id, done: !was }).then(() => loadFoci(true), () => { c.done = was; c.ok = wok; }).then(() => ctx.update());
}
const box = (c, ctx) => ctx && can("post") ? h("input", { type: "checkbox", class: "fp-box", checked: !!c.done, "aria-label": (c.done ? "Reopen: " : "Mark done: ") + c.text, onchange: () => tick(c, ctx) })
  : h("span", { class: "fp-box", "aria-hidden": "true" }, c.done ? "✓" : "");
// The person's tick waits for an agent (the person, 1108133400fb): it stays in the list, ticked, "an agent will check it", until
// an agent confirms it (the focus check confirm command) or reopens it with a why. Confirmed and agent-done steps sit last, struck through.
const finished = c => c.done && (c.ok || c.ok === undefined);   // ok undefined: a server from before the confirm step
const todoNote = c => c.done && !finished(c) ? h("span", { class: "fp-tnote" }, "ticked by you · an agent will check it")
  : !c.done && c.why ? h("span", { class: "fp-tnote" }, "reopened: " + c.why) : null;
const todoList = (rows, id, ctx) => h("ul", { class: "fp-todo" }, rows.map(c => h("li", { "data-key": "c:" + (c.id || c.text), "data-done": finished(c) ? "" : null, "data-ticked": c.done && !finished(c) ? "" : null }, box(c, ctx),
  h("span", null, say(c.text), c.focus && c.focus !== id ? h("span", { class: "meta" }, " · ", focusName(c.focus)) : null, todoNote(c)), c.done ? when(c.done * 1e3, "t", "ticked") : h("span"))));
const todoSec = (checks, id, ctx) => {
  const open = checks.filter(c => !finished(c)), fin = checks.filter(finished).sort((a, b) => b.done - a.done);
  return [open.length ? null : h("p", { class: "fp-calm" }, "Everything on the list is done."), todoList([...open, ...fin], id, ctx)];   // done ones inline, struck through, last
};

const tl = (kind, lines) => h("ol", { class: "fp-tl" }, lines.map((l, i) => h("li", { class: "fp-ev", "data-kind": kind, "data-key": kind + ":" + l.text.slice(0, 80) }, when(l.at, "t") || h("span"), h("span", { class: "fp-ev-x" }, lineText(l.text)))));

/** One waiting item on the page: the whole row a button that opens the quick-answer panel; a plain row where this address can't post. */
function askRow(w, src, ctx) {
  const open = answerer(w), lf = liftIn(w.text), th = lf.th || (w.src !== "summary" && S.threads.has(w.th) ? w.th : null);
  const d = w.post && S.threads.get(w.th), p = d ? d.posts.find(x => x.id === w.post) : null, by = (w.st && w.st.who) || (p && p.from) || null;
  const body = [h("span", { class: "fp-ask-x" }, flat(lineText(lf.text))),
    h("span", { class: "fp-ask-m" }, h("span", null, th ? threadTitle(th) : src), by && by !== "person" ? (simpleOn() ? h("span", null, "· " + plainName(by)) : h("span", null, "· ", flat(agentRef(by, { pill: true })))) : null, w.t ? h("span", null, "· ", when(w.t, "t")) : null),
  ];
  const key = w.key || w.th + ":" + (w.post || w.src);
  if (!open) return h("li", { "data-key": key }, h("div", { class: "fp-ask" }, body));
  return h("li", { "data-key": key, class: "fp-ask-li" }, h("button", { type: "button", class: "fp-ask", "aria-haspopup": "dialog", "aria-expanded": String(quickIs(w)), title: "Answer: " + w.text, onclick: open }, body),
    h("div", { class: "fp-ask-b" }, laterButtons(w), h("button", { type: "button", class: "btn cm-need-a", "data-size": "s", "aria-haspopup": "dialog", onclick: open }, "Answer")));
}

/** Later (the person, 1108133400fb): what they put off here, out of the home and its count, each with Bring back. */
function laterSec(id, ctx) {
  const ls = waitingLater().filter(w => (w.focus || focusOf(w.th) || null) === (id || null) || (w.later && w.later.focus === id));
  if (!ls.length) return null;
  return section({ title: `Later · ${ls.length}`, key: "fp-later:" + (id || "misc"), fold: true },
    h("ul", { class: "fp-later" }, ls.map(w => h("li", { class: "fp-later-i", "data-key": "l:" + (w.key || w.th + (w.post || "")) },
      h("span", { class: "fp-ask-x" }, flat(lineText(w.text))), w.later && w.later.at ? when(w.later.at * 1e3, "t") : null,
      can("post") ? button("Bring back", () => { setLater(w, "back"); ctx.update(); }, { size: "s", kind: "quiet" }) : null))));
}

/** Yours here (the person, 11081533a572): the Yours page cut to this focus, what was said to the person in its conversations (and,
 *  on asking, what they said), newest first. Reply opens the conversation in the side panel at that message, to answer from here. */
export const replyWait = (p, id) => ({ th: p.th, post: p.id, text: yoursText(p), t: p.t, src: "reply", focus: id || null, key: "r:" + p.id });
/** Steer the whole focus: a message to its own conversation, for whoever keeps it (the same path as answering the summary's asks). */
function steer(f, el, ctx) {
  const text = el.value.trim(); if (!text) return;
  FP.sent.push({ f: f.id, it: answer(f.thread || f.id, text, { to: f.id }) }); el.value = ""; ctx.update();
}
const sentWord = it => (it.state === "failed" ? "Couldn't send" : it.state === "sent" ? "Sent" : "Sending…");
const STEER_SAYS = "Goes to the focus's own conversation, for whoever keeps it. To steer one piece of work, reply to it below.";
// The page's one primary action. It stays under For you here, right above Conversations, as the person asked (11081916c2cb, 11081917dced), not first as the Nov 8 handoff §3.2 drew it;
// and it keeps its own box: the chat composer is one per page and sends to a thread's agent, not to the focus.
function steerBox(f, ctx) {
  if (f.misc || !can("post")) return null;
  const mine = FP.sent.filter(x => x.f === f.id && !(x.it.state === "sent" && x.it.id && S.byId[x.it.id]));
  return h("div", { class: "fp-steer", "data-key": "fp-steer" },
    mine.length ? h("ul", { class: "fp-steer-o", "aria-live": "polite" }, mine.map(({ it }) => h("li", { "data-key": "o" + it.lid, "data-state": it.state },
      h("span", null, it.text), h("span", { class: "meta" }, sentWord(it))))) : null,
    h("textarea", { class: "input fp-steer-in", rows: 2, placeholder: "Message the whole focus…", "aria-label": "Message the whole focus", title: STEER_SAYS,
      onkeydown: (e, n) => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !matchMedia("(max-width: 699px)").matches) { e.preventDefault(); steer(f, n || e.target, ctx); } } }),
    h("div", { class: "fp-steer-a" },
      button("Send", e => steer(f, e.target.closest(".fp-steer").querySelector(".fp-steer-in"), ctx), { kind: "primary", size: "s", cls: "fp-steer-s" })));
}
function yoursSec(f, ctx) {
  const id = f.misc ? null : f.id;
  const all = yoursPosts().filter(p => (focusOf(p.th) || null) === id);
  const rows = FP.mine ? all : all.filter(p => p.from !== "person");
  if (!all.length && f.misc) return null;
  const shown = rows.slice(0, FP.yours), more = rows.length - shown.length;
  const who = p => (p.from === "person" ? h("b", null, "You") : simpleOn() ? h("b", null, plainName(p.from)) : agentRef(p.from, { pill: true }));
  const row = p => {
    const w = replyWait(p, id), open = e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jump(p.id); };
    return h("li", { class: "fp-yr-i", "data-key": "y:" + p.id, "data-who": p.from === "person" ? "me" : null, "data-hover-acts": can("post") ? "" : null },
      h("a", { class: "fp-yr-in", href: threadHref(p.th), onclick: open, title: "Open it in the conversation, at that message" },
        h("span", { class: "fp-yr-m" }, who(p), h("span", { class: "fp-yr-th" }, threadTitle(p.th)), when(p.t, "t")),
        h("span", { class: "fp-yr-x" }, yoursText(p) ? railSay(yoursText(p), 420) : "(no text)")),
      can("post") ? h("span", { class: "row-acts" }, h("button", { type: "button", class: "btn fp-yr-r", "data-size": "s", "aria-haspopup": "dialog", "aria-expanded": String(quickIs(w)), title: "Reply here, beside the page",
        onclick: (e, n) => { e.preventDefault(); openQuick(w, n || e.target); } }, "Reply")) : null);
  };
  const tog = all.length ? h("label", { class: "fp-yr-mine meta" }, h("input", { type: "checkbox", role: "switch", class: "switch", checked: FP.mine, onchange: (e, n) => { FP.mine = n.checked; FP.yours = 3; ctx.update(); } }), "With what you said") : null;
  return h("section", { class: "fp-sec fp-yr", "data-key": "fp-yr", "aria-label": "For you here" }, head("For you here", rows.length ? String(rows.length) : null, tog),
    rows.length ? h("ul", { class: "fp-yr-l" }, shown.map(row)) : h("p", { class: "fp-calm" }, "Nothing was said to you in this focus yet."),
    more ? h("p", { class: "fp-more" }, button(`Show ${Math.min(more, 10)} more`, () => { FP.yours += 10; ctx.update(); }, { kind: "quiet", size: "s" })) : null,
    steerBox(f, ctx));
}

/** The focus's share of the hive's work and cost: a labelled bar per window, then the plain numbers. Simple view: words, no bars. */
function shareSec(id) {
  const a = activityOf(id); if (!a) return null;
  const WIN = [["day", "Last day"], ["week", "Last week"]];
  const paid = win => usd((a[win] || {}).cost || 0) !== usd(0);
  const anyPaid = WIN.some(([w]) => paid(w));
  const bar = (label, d, what) => h("span", { class: "fp-bar", role: "img", "aria-label": `${label}: ${pct(d)} of the hive's ${what}` }, h("i", { style: `width:${(d * 100).toFixed(1)}%` }));
  const rows = what => WIN.map(([win, label]) => { const d = shareOf(a, win, what);
    return h("div", { class: "fp-share-r", "data-key": what + win }, h("span", null, label), h("b", null, simpleOn() ? fractionWords(d) : pct(d)), simpleOn() ? null : bar(label, d, what)); });
  const nums = WIN.map(([win, label]) => { const b = a[win] || {};
    return `${plural(b.posts || 0, "message")}${paid(win) ? ` and ${usd(b.cost || 0)}` : ""} ${label === "Last day" ? "in the last day" : "in the last week"}`; }).join("; ");
  return h("section", { class: "fp-sec", "data-key": "fp-act", "aria-label": "Activity" }, head("Activity"),
    h("div", { class: "fp-share" },
      h("p", { class: "fp-by" }, "Share of the hive's work"), ...rows("work"),
      anyPaid ? [h("p", { class: "fp-by" }, "Share of the hive's cost"), ...rows("cost")] : null,
      simpleOn() ? null : h("p", { class: "fp-share-n" }, nums + ".")));
}

function page(ctx) {
  const id = idOf(ctx.arg), f = id ? focusById(id) : MISC;
  if (!f) return h("div", { class: "page fp" }, S.foci ? empty(`No focus called ${ctx.arg}. It may have been merged or renamed.`, { kind: "empty" }) : empty("Reading…", { kind: "loading" }), h("p", null, h("a", { href: href("comms") }, "Back to Chat")));
  const st = stateOf(id), rows = threadsOf(id), active = rows.filter(r => r.active && !archived(r.th)), quiet = rows.filter(r => !r.active && !archived(r.th)), arch = rows.filter(r => archived(r.th));
  const ag = agentsOf(id), needs = needsOf(id), live = needs.filter(w => w.src !== "summary"), asked = askedOf(id), sum = f.summary, checks = f.checks || [], done = checks.filter(c => finished(c));
  const now = sumLines(sum, "now"), lately = sumLines(sum, "lately"), up = updatedAt(f, rows), name = focusName(f);
  const waits = [...asked.map((t, i) => summaryWait(f, i, t)), ...live];
  // Agents as the chat's pills, each with how long it has been on and its own one line (the me command), never bare codes.
  const pill = n => (simpleOn() ? h("b", null, plainName(n)) : agentRef(n, { pill: true }));
  const agRow = n => { const o = agentOn(n), t = o && scrub(o.text), s = S.lastSess[n];
    return h("li", { "data-key": "ag:" + n }, h("span", null, pill(n), s && s.start && !s.end ? h("span", { class: "meta t", title: "Started " + new Date(s.start).toLocaleString() }, " for " + dur(Date.now() - s.start)) : null),
      t ? h("span", { class: "fp-ag-x" }, say(t)) : null); };
  const agents = ag.now.length || ag.day.length ? h("section", { class: "fp-sec", "data-key": "fp-ag", "aria-label": "Agents" },
    head("Agents", [ag.now.length ? `${ag.now.length} now` : null, ag.day.length ? `${ag.day.length} in the last day` : null].filter(Boolean).join(", ")),
    ag.now.length ? (ag.now.length > 6 ? h("p", null, h("a", { class: "fp-agn", href: href("agents") }, `${plural(ag.now.length, "agent")} are on this now`)) : h("ul", { class: "fp-ag" }, ag.now.map(agRow))) : h("p", { class: "fp-calm" }, "No agent is on it right now."),
    ag.day.length ? [h("p", { class: "fp-by" }, "In the last day"), h("p", { class: "fp-rel" }, ag.day.slice(0, 12).map(pill), ag.day.length > 12 ? h("a", { href: href("agents") }, `+${ag.day.length - 12}`) : null)] : null)
    : h("section", { class: "fp-sec", "data-key": "fp-ag", "aria-label": "Agents" }, head("Agents"), h("p", { class: "fp-calm" }, "No agent has been on this in the last day."));
  const meta = [h("span", null, plural(rows.length, "conversation")), ag.now.length ? h("span", null, `${ag.now.length} working now`) : null, up ? h("span", null, when(up, "t", "updated")) : null];
  // Send in the box below is the page's primary; New conversation is the plain button, Flow an icon (the Nov 8 handoff §3.1).
  const actions = h("div", { class: "fp-a" }, can("post") ? button("New conversation", () => newIn(id), { size: "s", icon: "new" }) : null,
    !simpleOn() ? button("This focus in Flow", null, { href: href("flow"), kind: "icon", size: "s", icon: "bands", cls: "fp-flow" }) : null);
  const allDone = checks.length && done.length === checks.length;
  const extra = quiet.length + arch.length;
  return h("div", { class: "page fp", "data-key": "fp:" + (id || "misc") },
    h("header", { class: "fp-hd" },
      h("div", { class: "fp-hd-t" },
        f.misc ? null : h("div", { class: "fp-state" }, statusWord(st.tone, st.word), ag.now.length && !simpleOn() ? h("span", { class: "fc-ag" }, ag.now.slice(0, 4).map(n => agentRef(n, { pill: true }))) : null),
        h("h1", { class: "fp-title" }, name),
        h("p", { class: "fp-aim" }, f.misc ? "Conversations not filed under a focus." : f.aim || null),
        simpleOn() ? null : h("p", { class: "fp-meta" }, meta)),
      actions),
    fociFailed() ? h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, "Couldn't refresh this focus; this is what was read last. ", button("Try now", () => loadFoci(true).then(() => ctx.update()), { kind: "quiet", size: "s" })) : null,
    h("div", { class: "fp-grid" },
      // The main column (the Nov 8 handoff §3.3, the box kept where the person put it): Needs you, Now, To do, For you here and the box, Conversations, Lately.
      h("div", { class: "fp-main" },
        waits.length ? h("section", { class: "fp-sec fp-needs", "data-key": "fp-needs", "aria-label": "Needs you" }, head("Needs you", String(waits.length)),
          h("ul", null, waits.map(w => askRow(w, w.src === "summary" ? "From the summary" : needSource(w), ctx))))
          : f.misc ? null : h("p", { class: "fp-calm", "data-key": "fp-calm" }, h("span", { class: "st", "data-tone": "quiet", "aria-hidden": "true" }), "Nothing waits on you here."),
        laterSec(id, ctx),
        f.misc ? null : h("section", { class: "fp-sec", "data-key": "fp-now", "aria-label": "Now" }, head("Now", null, sum && sum.from ? h("span", { class: "fp-by meta" }, "by ", simpleOn() ? plainName(sum.from) : agentRef(sum.from, { pill: true }), sum.at ? ` \u00b7 ${ago(sum.at * 1e3)}` : null) : null),
          now.length ? tl("now", now) : h("p", { class: "fp-calm" }, sum ? "Nothing listed as moving." : "No summary yet. The agents on this focus write one as they work; until then, its conversations below show what runs.")),
        f.misc ? null : sec("todo", null, "To do", checks.length ? (allDone ? null : `${checks.filter(finished).length} of ${checks.length} done`) : null,
          checks.length ? todoSec(checks, id, ctx) : h("p", { class: "fp-calm" }, "Nothing on the to-do list here.")),
        yoursSec(f, ctx),
        sec("convs", "fp-convs", "Conversations", active.length ? `${active.length} active this week` : null,
          active.length ? h("ul", { class: "list", "data-dense": "" }, active.map(threadRow)) : h("p", { class: "fp-calm" }, "Nothing active this week."),
          extra ? [FP.more ? h("ul", { class: "list", "data-dense": "", "data-key": "fp-quiet" }, [...quiet, ...arch].map(threadRow)) : null,
            h("p", { class: "fp-more" }, h("button", { type: "button", class: "btn", "data-kind": "quiet", "data-size": "s", "aria-expanded": String(FP.more), onclick: () => { FP.more = !FP.more; ctx.update(); } },
              FP.more ? "Show only the active ones" : [quiet.length ? `Quiet (${quiet.length})` : null, quiet.length && arch.length ? " and " : null, arch.length ? `${quiet.length ? "archived" : "Archived"} (${arch.length})` : null]))] : null),
        f.misc || !lately.length ? null : sec("lately", null, "Lately", null, tl("lately", lately))),
      f.misc ? null : h("aside", { class: "fp-side", "aria-label": "Who and how much" },
        agents,
        shareSec(id),
        (f.links || []).length ? h("section", { class: "fp-sec", "data-key": "fp-rel", "aria-label": "Related" }, head("Related"), h("p", { class: "fp-rel" }, f.links.map(l => h("a", { href: href("focus", l) }, focusName(l))))) : null)));
}

export default {
  id: "focus", label: "Focus", tab: false,
  enter(ctx) { if (!ctx.arg) { location.hash = "#comms"; return; } loadFoci(true).then(() => ctx.update()); },
  leave: chatLeave, escape: chatEscape,
  poll: { every: FOCI_EVERY, async run(ctx) { await loadFoci(); ctx.update(); } },
  render(ctx) {
    if (!S.live) return h("div", { class: "page" }, S.failed ? empty("Couldn't read the conversations. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    return inChat(ctx, idOf(ctx.arg), page(ctx));
  },
};
