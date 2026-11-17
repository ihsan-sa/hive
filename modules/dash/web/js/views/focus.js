// The Focus page (#focus/<id>, #focus/misc): one focus a level deeper than its home card. Its summary in full (Now, Lately),
// what waits on the person in it, the agents' to-do list, its conversations, who is on it now and in the last day, and its numbers.
// A focus is an abstraction for the person (person-doc-2026-11-07): the rail's By focus view and the home's cards open it.
// Stats hide in simple mode. Misc is the unfiled conversations: a plain list, no summary, no to-do.
// It shows in the chat's layout (comms.js inChat), where a conversation would: the rail beside it, its focus marked there.
import { h } from "../core/dom.js";
import { LOC } from "../core/format.js";
import { S, can, post } from "../core/store.js";
import { threadTitle, agentOn } from "../core/model.js";
import { plural, dur, ago } from "../core/format.js";
import { href, go, threadHref } from "../core/router.js";
import { simpleOn, plainName } from "../core/simple.js";
import { button, empty, statusWord, section, waiting } from "../ui/kit.js";
import { agentRef } from "../ui/refs.js";
import { openPreview, fileHref } from "../ui/preview.js";
import { loadFoci, fociFailed, focusById, focusOf, threadsOf, needsOf, askedOf, yoursN, agentsOf, stateOf, pills, plainPills, needSource, scrub, focusName, MISC, FOCI_EVERY, activityOf, shareOf, fractionWords, pct, usd, leadOf, pageSince, newSince, hiddenAsks, markSeen } from "../core/foci.js";
import { waitingLater } from "../core/needs.js";
import { archived } from "../ui/threadmeta.js";
import { draft, answer } from "../ui/composer.js";
import { inChat, chatEscape, chatLeave, answerer, summaryWait, sumLines, lineText, flat, liftIn, updatedAt, rowStand, newMark, leadWord } from "./comms.js";
import { quickIs, laterButtons, setLater } from "../ui/quick.js";
import { when, exact } from "../ui/when.js";
import { yoursPosts, yoursText } from "../core/yours.js";
import { railSay } from "../core/foci.js";
import { jump } from "../ui/message.js";
import { openQuick } from "../ui/quick.js";
import { uploadFile } from "../core/upload.js";

const idOf = arg => (arg === "misc" ? null : arg);
const say = t => (simpleOn() ? plainPills(t) : pills(t));
const FP = { more: false, yours: 3, mine: false, sent: [], brief: null, typed: {} };   // typed: the steer box's unsent words per focus, kept for the session (WCAG 3.3.7)   // sent: what the steer box sent from this page, with its state   // the Quiet and archived conversations, shown on asking (this page only)

/** Start a new conversation already tagged with this focus: the composer carries the tag and posts it with the first message. */
export function newIn(id) { draft(null).focus = id || ""; go("comms", "~new"); }

// The page, v3 (designs/ui-redesign-v3/V3-HANDOFF.md 3b): a header with the state and aim, then a main column (Needs you first, Now,
// To do, Lately, Conversations) and a side column (Agents, Activity). Every line full length: the card clamps, the page doesn't.
const head = (title, note, more) => h("div", { class: "fp-h" }, h("h2", null, title), note ? h("span", { class: "sec-n" }, note) : null, more || null);
const sec = (key, cls, title, note, ...kids) => h("section", { class: ["fp-sec", cls], "data-key": "fp-" + key, "aria-label": title, tabindex: "-1" }, head(title, note), ...kids);

// A conversation on the focus page: its mark, title and time, and under them where it stands in the word and the line the top of its chat leads with
// (comms.js rowStand; HA-11 review: this row said "paused", "waiting" and "failed, not fixed" where the chat said Stopped, Waiting on others and Blocked).
// The line is the stand's own words. In the app and simple looks they name each agent already (model.js labelCodes), so nothing here makes a pill: pills()
// took the code out of a path too, and "tmp/a9e03/out.csv" read "tmp/an agent/out.csv" on this page alone (test/drive_stand_words.mjs). The developer
// look keeps its codes, as links. Ids the person cannot read are out of the line already (comms.js standSay), and pills() takes them out there.
function threadRow(r) {
  const d = S.threads.get(r.th), x = rowStand(r.th), s = x.s;
  return h("li", { class: "lr", "data-key": "th:" + r.th }, h("a", { class: "lr-in", href: threadHref(r.th) },
    h("span", { class: "lr-t" }, x.mark, h("b", null, threadTitle(r.th)), x.fresh, d ? when(d.last, "t") : null),
    s ? h("span", { class: "lr-x clamp-1", title: x.line }, x.word, s.why ? [" \u00b7 ", s.why] : null, s.text ? [" \u00b7 ", S.app && S.app.app ? s.text : pills(s.text)] : null) : null));
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
  h("span", null, say(scrub(c.done ? c.text.replace(/^For you:\s*/i, "") : c.text)), c.focus && c.focus !== id ? h("span", { class: "meta" }, " · ", focusName(c.focus)) : null, todoNote(c)), c.done ? when(c.done * 1e3, "t", "ticked") : h("span"))));
const todoSec = (checks, id, ctx) => {
  const open = checks.filter(c => !finished(c)), fin = checks.filter(finished).sort((a, b) => b.done - a.done);
  return [open.length ? null : h("p", { class: "fp-calm" }, "Everything on the list is done."), todoList([...open, ...fin], id, ctx)];   // done ones inline, struck through, last
};

// A line written since the person last looked at this page is marked (core/foci.js pageSince): a bar on the row, "New:" for a screen reader.
const tl = (kind, lines, since) => h("ol", { class: "fp-tl" }, lines.map(l => { const nw = newSince(l.at, since);
  return h("li", { class: "fp-ev", "data-kind": kind, "data-new": nw ? "" : null, "data-key": kind + ":" + l.text.slice(0, 80) }, when(l.at, "t") || h("span"), h("span", { class: "fp-ev-x" }, nw ? newMark() : null, lineText(l.text))); }));

/** In short (core/foci.js leadOf): where the focus stands against its aim, in the agents' one or two sentences. First on the page, before any list. */
function shortSec(lead, since) {
  if (!lead) return null;
  const nw = newSince(lead.since, since);
  return h("section", { class: "fp-sec fp-short", "data-key": "fp-short", "data-new": nw ? "" : null, "aria-label": "In short" },
    head("In short", null, h("span", { class: "fp-by meta" }, lead.from ? ["by ", simpleOn() ? plainName(lead.from) : agentRef(lead.from, { pill: true }), " \u00b7 "] : null, when(lead.at, "t"))),
    h("p", { class: "fp-short-x" }, nw ? newMark() : null, lineText(lead.body, { topic: false })));
}
/** A part of the since line takes the reader to its section and puts the keyboard there: Lately sits under the conversations, a long way down a busy page. */
const toSec = (key, text) => h("button", { type: "button", class: "linkbtn fp-jump", "data-to": key, onclick: (e, el) => {
  const s = el.closest(".fp")?.querySelector(`[data-key="fp-${key}"]`); if (!s) return;   // el: the button (a delegated handler's event has the document as currentTarget)
  s.focus({ preventScroll: true }); s.scrollIntoView({ block: "start", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" }); } }, text);
/** What changed on the page since the person last looked, in one line, so a page they know says so and a page with news says where it is.
 *  Nothing on a first visit (since null): a page of marks says nothing. n: {lead, now, lately, asks, convs}. */
function sinceLine(since, n) {
  if (!since) return null;
  const parts = [n.lead ? "the In short" : null, n.asks ? toSec("needs", plural(n.asks, "thing") + " for you") : null, n.now ? toSec("now", `${n.now} in Now`) : null, n.lately ? toSec("lately", `${n.lately} in Lately`) : null].filter(Boolean);
  const convs = n.convs ? [toSec("convs", plural(n.convs, "conversation")), ` ${n.convs === 1 ? "has" : "have"} new messages`] : null;
  // One sentence with the time in it, said once: "New since you last looked 22h ago: 2 in Now; 3 conversations have new messages."
  const at = h("time", { datetime: new Date(since).toISOString(), title: "You last looked " + exact(since) }, ago(since));
  const list = parts.flatMap((x, i) => i ? [", ", x] : [x]);
  const said = parts.length ? ["New since you last looked ", at, ": ", list, convs ? ["; ", convs] : null, "."]
    : convs ? ["Nothing new in the summary since you last looked ", at, "; ", convs, "."] : ["Nothing new here since you last looked ", at, "."];
  return h("p", { class: "fp-since", "data-key": "fp-since", "data-new": parts.length ? "" : null }, said);
}

/** One waiting item on the page: the whole row a button that opens the quick-answer panel; a plain row where this address can't post. */
function askRow(w, src, ctx, since) {
  const open = answerer(w), lf = liftIn(w.text), th = lf.th || (w.src !== "summary" && S.threads.has(w.th) ? w.th : null);
  const d = w.post && S.threads.get(w.th), p = d ? d.posts.find(x => x.id === w.post) : null, by = (w.st && w.st.who) || (p && p.from) || null;
  const body = [h("span", { class: "fp-ask-x" }, flat(lineText(lf.text))),
    h("span", { class: "fp-ask-m" }, h("span", null, th ? threadTitle(th) : src), by && by !== "person" ? (simpleOn() ? h("span", null, "· " + plainName(by)) : h("span", null, "· ", flat(agentRef(by, { pill: true })))) : null, w.t ? h("span", null, "· ", when(w.t, "t")) : null),
  ];
  const key = w.key || w.th + ":" + (w.post || w.src), nw = newSince(w.t, since) ? "" : null;
  if (nw === "") body[0].prepend(newMark());
  if (!open) return h("li", { "data-key": key, "data-new": nw }, h("div", { class: "fp-ask" }, body));
  return h("li", { "data-key": key, class: "fp-ask-li", "data-new": nw }, h("button", { type: "button", class: "fp-ask", "aria-haspopup": "dialog", "aria-expanded": String(quickIs(w)), title: "Answer: " + w.text, onclick: open }, body),
    h("div", { class: "fp-ask-b" }, laterButtons(w), h("button", { type: "button", class: "btn cm-need-a", "data-size": "s", "aria-haspopup": "dialog", onclick: open }, "Answer")));
}

/** Later (the person, 1108133400fb): what they put off here, out of the home and its count, each with Bring back. */
function laterSec(id, ctx) {
  const ls = [...waitingLater().filter(w => (w.focus || focusOf(w.th) || null) === (id || null) || (w.later && w.later.focus === id)), ...hiddenAsks(focusById(id), { later: true })];
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
  FP.sent.push({ f: f.id, it: answer(f.thread || f.id, text, { to: f.id }) }); el.value = ""; delete FP.typed[f.id]; ctx.update();
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
    h("textarea", { class: "input fp-steer-in", rows: 2, placeholder: "Message the whole focus…", "aria-label": "Message the whole focus", title: STEER_SAYS, value: FP.typed[f.id] || "", oninput: (e, n) => { FP.typed[f.id] = (n || e.target).value; },
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
  const answered = p => p.from !== "person" && S.posts.some(q => q.th === p.th && q.from === "person" && q.t > p.t);   // SU-3: an answered ask is not still waiting on the person
  const row = p => {
    const w = replyWait(p, id), open = e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jump(p.id); };
    return h("li", { class: "fp-yr-i", "data-key": "y:" + p.id, "data-who": p.from === "person" ? "me" : null, "data-hover-acts": can("post") ? "" : null },
      h("a", { class: "fp-yr-in", href: threadHref(p.th), onclick: open, title: "Open it in the conversation, at that message" },
        h("span", { class: "fp-yr-m" }, who(p), h("span", { class: "fp-yr-th" }, threadTitle(p.th)), when(p.t, "t")),
        h("span", { class: "fp-yr-x" }, yoursText(p) ? railSay(yoursText(p), 420) : "(no text)")),
      answered(p) ? h("span", { class: "meta fp-yr-ans" }, "answered") : can("post") ? h("span", { class: "row-acts" }, h("button", { type: "button", class: "btn fp-yr-r", "data-size": "s", "aria-haspopup": "dialog", "aria-expanded": String(quickIs(w)), title: "Reply here, beside the page",
        onclick: (e, n) => { e.preventDefault(); openQuick(w, n || e.target); } }, "Reply")) : null);
  };
  const tog = all.length ? h("label", { class: "fp-yr-mine meta" }, h("input", { type: "checkbox", role: "switch", class: "switch", checked: FP.mine, onchange: (e, n) => { FP.mine = n.checked; FP.yours = 3; ctx.update(); } }), "With what you said") : null;
  // HA-17: the number beside the heading is what waits on the person in this focus, the same count as its home card and list row (foci.js
  // yoursN); a message said to them with no reply after it is history, not a wait, so those are counted on a line of their own under it.
  const n = yoursN(id), unans = rows.filter(p => p.from !== "person" && !answered(p)).length;
  return h("section", { class: "fp-sec fp-yr", "data-key": "fp-yr", "aria-label": "For you here" }, head("For you here", n ? String(n) : null, tog),
    unans ? h("p", { class: "meta fp-yr-un", "data-key": "fp-yr-un" }, `${plural(unans, "message")} to you with no reply after ${unans === 1 ? "it" : "them"}`) : null,
    rows.length ? h("ul", { class: "fp-yr-l" }, shown.map(row)) : h("p", { class: "fp-calm" }, "Nothing was said to you in this focus yet."),
    more ? h("p", { class: "fp-more" }, button(`Show ${Math.min(more, 10)} more`, () => { FP.yours += 10; ctx.update(); }, { kind: "quiet", size: "s" })) : null,
    steerBox(f, ctx));
}

// Instructions for chats here (E37): the group's own instructions and files, which every agent started in one of its
// conversations is given (the focus brief command). Anyone can read them here; a full sign-in edits them.
const fileName = p => String(p).split("/").pop();
async function saveBrief(f, ctx) {
  const B = FP.brief; if (!B || B.busy) return;
  B.busy = true; B.err = ""; ctx.update();
  try {
    const refs = []; for (const x of B.add) refs.push(await uploadFile(x));
    await post("/api/focus", { brief: { focus: f.id, text: B.text, files: refs, drop: B.drop } });
    FP.brief = null; await loadFoci(true);
  } catch (e) { B.busy = false; B.err = "Couldn't save: " + (e && e.message || "the server didn't answer") + "."; }
  ctx.update();
}
/** A group file as a link that opens it in the preview beside the page (served from the group's own folder, E37b); a new tab on a modified click. */
function groupFile(f, files, i) {
  const items = files.map(x => ({ id: "groups/" + f.id, name: fileName(x) }));
  return h("a", { class: "fp-brief-l", href: fileHref(items[i]), target: "_blank", rel: "noopener",
    onclick: e => { if (e.metaKey || e.ctrlKey || e.shiftKey || e.button) return; e.preventDefault(); openPreview(items, i); } }, items[i].name);
}

function briefSec(f, ctx) {
  if (f.misc) return null;
  const b = f.brief || { text: "", files: [] }, mine = can("config");
  const B = FP.brief && FP.brief.f === f.id ? FP.brief : null;
  const SAYS = "Every new conversation in this focus is given these, and so is an agent that comes back to one.";
  if (!B) {
    if (!b.text && !(b.files || []).length && !mine) return null;
    return h("section", { class: "fp-sec fp-brief", "data-key": "fp-brief", "aria-label": "Instructions for chats here" }, head("Instructions for chats here", null,
        mine ? button(b.text || (b.files || []).length ? "Edit" : "Add", () => { FP.brief = { f: f.id, text: b.text || "", add: [], drop: [], err: "", busy: false }; ctx.update(); }, { kind: "quiet", size: "s", cls: "fp-brief-e" }) : null),
      b.text ? h("p", { class: "fp-brief-x" }, b.text) : h("p", { class: "fp-calm" }, "None yet. " + SAYS),
      (b.files || []).length ? h("ul", { class: "fp-brief-f" }, b.files.map((x, i) => h("li", { "data-key": "bf:" + x }, groupFile(f, b.files, i)))) : null);
  }
  const files = (b.files || []).filter(x => !B.drop.includes(fileName(x)));
  return h("section", { class: "fp-sec fp-brief", "data-key": "fp-brief", "aria-label": "Instructions for chats here" }, head("Instructions for chats here"),
    h("p", { class: "meta" }, SAYS),
    h("textarea", { class: "input fp-brief-in", rows: 4, maxlength: 4000, placeholder: "For example: reply in Spanish; keep answers short.", "aria-label": "Instructions for chats in this focus",
      value: B.text, oninput: (e, n) => { B.text = (n || e.target).value; } }),
    h("ul", { class: "fp-brief-f" }, files.map(x => h("li", { "data-key": "bf:" + x }, fileName(x), " ", button("Remove", () => { B.drop.push(fileName(x)); ctx.update(); }, { kind: "quiet", size: "s" }))),
      B.add.map((x, i) => h("li", { "data-key": "ba:" + i + x.name }, x.name, " (new) ", button("Remove", () => { B.add.splice(i, 1); ctx.update(); }, { kind: "quiet", size: "s" })))),
    h("label", { class: "btn fp-brief-add", "data-size": "s", "data-kind": "quiet" }, "Add a file", h("input", { type: "file", multiple: true, hidden: true, class: "fp-brief-file", onchange: (e, n) => { B.add.push(...(n || e.target).files); ctx.update(); } })),
    B.err ? h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, B.err) : null,
    h("div", { class: "fp-steer-a" }, button("Cancel", () => { FP.brief = null; ctx.update(); }, { kind: "quiet", size: "s" }),
      button(B.busy ? "Saving…" : "Save", () => saveBrief(f, ctx), { kind: "primary", size: "s", cls: "fp-brief-s" })));
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
  if (!f) return h("div", { class: "page fp" }, S.foci && !S.foci.failed ? empty(`No focus called ${ctx.arg}. It may have been merged or renamed.`, { kind: "empty" }) : waiting(fociFailed(), "Reading…", "the focuses"), h("p", null, h("a", { href: href("comms") }, "Back to Chat")));
  const st = stateOf(id), rows = threadsOf(id), active = rows.filter(r => r.active && !archived(r.th)), quiet = rows.filter(r => !r.active && !archived(r.th)), arch = rows.filter(r => archived(r.th));
  const ag = agentsOf(id), needs = needsOf(id), live = needs.filter(w => w.src !== "summary"), asked = askedOf(id), sum = f.summary, checks = f.checks || [], done = checks.filter(c => finished(c));
  const now = sumLines(sum, "now"), lately = sumLines(sum, "lately"), up = updatedAt(f, rows), name = focusName(f);
  // In short and what is new to the person (core/foci.js): the lead's state is the page's word, as on the card; the Now lines that read as
  // theirs to do (the server's `hidden`) list under Needs you after the asks the agents listed, so the page never says nothing waits above one.
  const lead = f.misc ? null : leadOf(f), since = f.misc ? null : pageSince(f), hid = f.misc ? [] : hiddenAsks(f);
  const waits = [...asked.map((t, i) => summaryWait(f, i, t)), ...hid, ...live];
  const fresh = { lead: lead && newSince(lead.since, since) ? 1 : 0, now: now.filter(l => newSince(l.at, since)).length, lately: lately.filter(l => newSince(l.at, since)).length, asks: waits.filter(w => newSince(w.t, since)).length,
    convs: since ? rows.filter(r => { const d = S.threads.get(r.th); return d && d.last > since && d.posts.some(p => p.t > since && p.from !== "person"); }).length : 0 };
  // Agents as the chat's pills, each with how long it has been on and its own one line (the me command), never bare codes.
  const pill = n => (simpleOn() ? h("b", null, plainName(n)) : agentRef(n, { pill: true }));
  const agRow = n => { const o = agentOn(n), t = o && scrub(o.text), s = S.lastSess[n];
    return h("li", { "data-key": "ag:" + n }, h("span", null, pill(n), s && s.start && !s.end ? h("span", { class: "meta t", title: "Started " + new Date(s.start).toLocaleString(LOC) }, " for " + dur(Date.now() - s.start)) : null),
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
        f.misc ? null : h("div", { class: "fp-state" }, lead && lead.state ? leadWord(lead, since, st.key === "moving" || st.moving) : statusWord(st.tone, st.word), ag.now.length && !simpleOn() ? h("span", { class: "fc-ag" }, ag.now.slice(0, 4).map(n => agentRef(n, { pill: true }))) : null),
        h("h1", { class: "fp-title" }, name),
        h("p", { class: "fp-aim" }, f.misc ? "Conversations not filed under a focus." : f.aim || null),
        simpleOn() ? null : h("p", { class: "fp-meta" }, meta)),
      actions),
    fociFailed() ? h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, "Couldn't refresh this focus; this is what was read last. ", button("Try now", () => loadFoci(true).then(() => ctx.update()), { kind: "quiet", size: "s" })) : null,
    h("div", { class: "fp-grid" },
      // The main column (the Nov 8 handoff §3.3, the box kept where the person put it): Needs you, Now, To do, For you here and the box, Conversations, Lately.
      h("div", { class: "fp-main" },
        shortSec(lead, since),
        f.misc ? null : sinceLine(since, fresh),
        waits.length ? h("section", { class: "fp-sec fp-needs", "data-key": "fp-needs", "aria-label": "Needs you", tabindex: "-1" }, head("Needs you", String(waits.length)),
          h("ul", null, waits.map(w => askRow(w, w.hidden ? "Reads as yours, from Now" : w.src === "summary" ? "From the summary" : needSource(w), ctx, since))))
          : f.misc ? null : h("p", { class: "fp-calm", "data-key": "fp-calm" }, h("span", { class: "st", "data-tone": "quiet", "aria-hidden": "true" }), "Nothing waits on you here."),
        laterSec(id, ctx),
        f.misc ? null : h("section", { class: "fp-sec", "data-key": "fp-now", "aria-label": "Now", tabindex: "-1" }, head("Now", null, sum && sum.from ? h("span", { class: "fp-by meta" }, "by ", simpleOn() ? plainName(sum.from) : agentRef(sum.from, { pill: true }), sum.at ? ` \u00b7 ${ago(sum.at * 1e3)}` : null) : null),
          now.length ? tl("now", now, since) : h("p", { class: "fp-calm" }, sum ? "Nothing listed as moving." : "No summary yet. The agents on this focus write one as they work; until then, its conversations below show what runs.")),
        f.misc ? null : sec("todo", null, "To do", checks.length ? (allDone ? null : `${checks.filter(finished).length} of ${checks.length} done`) : null,
          checks.length ? todoSec(checks, id, ctx) : h("p", { class: "fp-calm" }, "Nothing on the to-do list here.")),
        yoursSec(f, ctx),
        sec("convs", "fp-convs", "Conversations", active.length ? `${active.length} active this week` : null,
          active.length ? h("ul", { class: "list", "data-dense": "" }, active.map(threadRow)) : h("p", { class: "fp-calm" }, "Nothing active this week."),
          extra ? [FP.more ? h("ul", { class: "list", "data-dense": "", "data-key": "fp-quiet" }, [...quiet, ...arch].map(threadRow)) : null,
            h("p", { class: "fp-more" }, h("button", { type: "button", class: "btn", "data-kind": "quiet", "data-size": "s", "aria-expanded": String(FP.more), onclick: () => { FP.more = !FP.more; ctx.update(); } },
              FP.more ? "Show only the active ones" : [quiet.length ? `Quiet (${quiet.length})` : null, quiet.length && arch.length ? " and " : null, arch.length ? `${quiet.length ? "archived" : "Archived"} (${arch.length})` : null]))] : null),
        f.misc || !lately.length ? null : sec("lately", null, "Lately", null, tl("lately", lately, since))),
      f.misc ? null : h("aside", { class: "fp-side", "aria-label": "Who and how much" },
        briefSec(f, ctx),
        agents,
        shareSec(id),
        (f.links || []).length ? h("section", { class: "fp-sec", "data-key": "fp-rel", "aria-label": "Related" }, head("Related"), h("p", { class: "fp-rel" }, f.links.map(l => h("a", { href: href("focus", l) }, focusName(l))))) : null)));
}

export default {
  id: "focus", label: "Focus", tab: false,
  // Arriving is a look (core/foci.js markSeen): the marks count from the look before this visit, and the focus's home card counts as read from now.
  enter(ctx) { if (!ctx.arg) { location.hash = "#comms"; return; } const id = idOf(ctx.arg); if (id) markSeen(id, true); loadFoci(true).then(() => ctx.update()); },
  leave: chatLeave, escape: chatEscape,
  poll: { every: FOCI_EVERY, async run(ctx) { await loadFoci(); ctx.update(); } },
  render(ctx) {
    if (!S.live) return h("div", { class: "page" }, S.failed ? empty("Couldn't read the conversations. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    return inChat(ctx, idOf(ctx.arg), page(ctx));
  },
};
