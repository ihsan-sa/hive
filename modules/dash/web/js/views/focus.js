// The Focus page (#focus/<id>, #focus/misc): one focus a level deeper than its home card. Its summary in full (Now, Lately),
// what waits on the person in it, the agents' to-do list, its conversations, who is on it now and in the last day, and its numbers.
// A focus is an abstraction for the person (person-doc-2026-11-07): the rail's By focus view and the home's cards open it.
// Stats hide in simple mode. Misc is the unfiled conversations: a plain list, no summary, no to-do.
// It shows in the chat's layout (comms.js inChat), where a conversation would: the rail beside it, its focus marked there.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { threadTitle, agentOn } from "../core/model.js";
import { ago, plural } from "../core/format.js";
import { href, go, threadHref } from "../core/router.js";
import { simpleOn, plainName } from "../core/simple.js";
import { button, empty, pageHeader, section } from "../ui/kit.js";
import { agentRef } from "../ui/refs.js";
import { needsHref } from "../core/needs.js";
import { loadFoci, fociFailed, focusById, threadsOf, needsOf, askedOf, agentsOf, stateOf, pills, plainPills, needSource, scrub, threadLine, focusName, MISC, FOCI_EVERY, activityOf, shareOf, fractionWords, pct, usd } from "../core/foci.js";
import { archived } from "../ui/threadmeta.js";
import { draft } from "../ui/composer.js";
import { inChat, chatEscape, chatLeave } from "./comms.js";

const idOf = arg => (arg === "misc" ? null : arg);
const say = t => (simpleOn() ? plainPills(t) : pills(t));
const bullet = t => h("li", null, say(t));
const bullets = (rows, none) => rows && rows.length ? h("ul", { class: "fp-ul" }, rows.map(bullet)) : h("p", { class: "meta" }, none);

/** Start a new conversation already tagged with this focus: the composer carries the tag and posts it with the first message. */
export function newIn(id) { draft(null).focus = id || ""; go("comms", "~new"); }

const SENT = { needs: "Something here waits on you.", moving: "Agents are on it now.", waiting: "Agents have been here lately; nothing waits on you.", quiet: "Nothing has moved here this week." };

function threadRow(r) {
  const line = threadLine(r.th), d = S.threads.get(r.th);
  return h("li", { class: "lr", "data-key": "th:" + r.th }, h("a", { class: "lr-in", href: threadHref(r.th) },
    h("div", { class: "lr-t" }, h("b", null, threadTitle(r.th)), d ? h("span", { class: "meta" }, ago(d.last)) : null),
    line ? h("div", { class: "lr-x clamp-2" }, say(line)) : null));
}
const todoList = (rows, id) => h("ul", { class: "fp-todo" }, rows.map(c => h("li", { "data-done": c.done ? "" : null }, h("span", { class: "fp-box", "aria-hidden": "true" }, c.done ? "✓" : ""), h("span", null, say(c.text)), c.focus && c.focus !== id ? h("span", { class: "meta" }, focusName(c.focus)) : null)));
const threadList = rows => h("ul", { class: "list", "data-dense": "" }, rows.map(threadRow));

/** The focus's share of the hive's work and cost, with the numbers the home card leaves out: one line per window, the same bars. */
function activitySec(id) {
  const a = activityOf(id); if (!a) return null;
  const bar = (win, what) => { const d = shareOf(a, win, what); return h("span", { class: "fc-g-t fp-g-t", role: "img", "aria-label": `${what}: ${pct(d)}` }, h("i", { class: "fc-g-d", style: `width:${(d * 100).toFixed(1)}%` })); };
  const line = (win, label) => {
    const b = a[win] || {}, t = a.total[win] || {}, w = shareOf(a, win, "work"), c = shareOf(a, win, "cost");
    // A cost that rounds to nothing is left out, words and bar: "$0.00" told the person nothing (phone walk, finding 7). Work is said alone then.
    const paid = usd(b.cost || 0) !== usd(0), all = usd(t.cost || 0) !== usd(0) ? `, ${usd(t.cost || 0)}` : "";
    const text = simpleOn()
      ? `${label}: ${fractionWords(w)} of what your Hive did was here${paid ? `, and ${fractionWords(c)} of what it cost` : ""}.`
      : paid ? `${label}: ${plural(b.posts || 0, "message")}, ${usd(b.cost || 0)}; ${pct(w)} of the hive's work and ${pct(c)} of its cost (${plural(t.posts || 0, "message")}${all} in all).`   // the person's words (R6 review, finding 13): messages, not runs and posts
      : `${label}: ${plural(b.posts || 0, "message")}; ${pct(w)} of the hive's work (${plural(t.posts || 0, "message")}${all} in all).`;
    return h("li", null, h("span", null, text), h("span", { class: "fp-g" }, bar(win, "work"), paid ? bar(win, "cost") : null));
  };
  const anyPaid = ["day", "week"].some(w => usd((a[w] || {}).cost || 0) !== usd(0));
  return section({ title: "Activity", note: simpleOn() ? null : anyPaid ? "the bars: work, then cost, as a share of the hive's" : "the bar: work, as a share of the hive's" }, h("ul", { class: "fp-act" }, line("day", "Last day"), line("week", "Last week")));
}

function page(ctx) {
  const id = idOf(ctx.arg), f = id ? focusById(id) : MISC;
  if (!f) return h("div", { class: "page fp" }, S.foci ? empty(`No focus called ${ctx.arg}. It may have been merged or renamed.`, { kind: "empty" }) : empty("Reading…", { kind: "loading" }), h("p", null, h("a", { href: href("comms") }, "Back to Chat")));
  const st = stateOf(id), rows = threadsOf(id), active = rows.filter(r => r.active && !archived(r.th)), quiet = rows.filter(r => !r.active && !archived(r.th)), arch = rows.filter(r => archived(r.th));
  const ag = agentsOf(id), needs = needsOf(id), live = needs.filter(w => w.src !== "summary"), asked = askedOf(id), sum = f.summary, checks = f.checks || [], open = checks.filter(c => !c.done), done = checks.filter(c => c.done);
  // Agents as the chat's pills, each with its own one line (`hive me`), never bare codes; past a handful, a count that opens the Agents page.
  const who = n => (simpleOn() ? h("b", null, plainName(n)) : agentRef(n, { dot: false }));
  const onLine = n => { const o = agentOn(n), t = o && scrub(o.text); return t ? h("span", { class: "meta" }, " ", say(t)) : null; };
  const agRows = (label, names) => names.length > 6
    ? h("p", null, h("a", { href: href("agents") }, `${plural(names.length, "agent")} ${label === "Now" ? "are on this now" : "have worked on this in the last day"}`))
    : h("div", null, h("p", { class: "meta" }, label), h("ul", { class: "fp-ul" }, names.map(n => h("li", null, who(n), onLine(n)))));
  const meta = simpleOn() ? null : [plural(rows.length, "conversation"), ag.now.length ? `${ag.now.length} working now` : null, needs.length ? `${needs.length} wait on you` : null, sum ? "summary " + ago(sum.at * 1e3) : "no summary yet"];
  const actions = [button("New conversation", () => newIn(id), { kind: "primary", size: "s", icon: "new" }),
    !simpleOn() ? button("Flow", null, { href: href("flow"), kind: "quiet", size: "s", title: "The Flow board: every conversation's state at a glance" }) : null,
    needs.length && needsHref() ? button("Needs you", null, { href: needsHref(), kind: "quiet", size: "s" }) : null];
  return h("div", { class: "page fp", "data-key": "fp:" + (id || "misc") },
    pageHeader({ title: focusName(f), lead: f.misc ? "Conversations not filed under a focus." : f.aim || SENT[st.key], state: { text: st.word, tone: st.tone }, meta, actions, size: "l",
      after: fociFailed() ? h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, "Couldn't refresh this focus; this is what was read last.") : null }),
    f.misc ? null : section({ title: "Now", note: sum ? ["from ", who(sum.from), " " + ago(sum.at * 1e3)] : null }, bullets(sum && sum.now, sum ? "Nothing listed as moving." : "No summary yet: the agents on this focus keep one here as they work.")),
    // Needs you: first the decisions the agents put to the person in the summary (what the home card shows), then every open
    // ask in the focus's conversations from the one list, quieter: the page is a level deeper than the card and may say it all.
    section({ title: "Needs you", note: live.length && asked.length ? plural(live.length, "open ask") + " below" : null }, asked.length ? bullets(asked) : null,
      live.length ? [asked.length ? h("p", { class: "meta fp-also" }, "Also open in its conversations") : null, h("ul", { class: "list", "data-dense": "" }, live.map(w => h("li", { class: "lr", "data-key": "nd:" + w.th }, h("a", { class: "lr-in", href: threadHref(w.th) },
        h("div", { class: "lr-t" }, h("b", null, needSource(w)), h("span", { class: "meta" }, ago(w.t))), w.text ? h("div", { class: "lr-x clamp-2" }, say(w.text)) : null))))] : null,
      !asked.length && !needs.length ? h("p", { class: "meta" }, "Nothing waits on you here.") : null),
    // To do sits between Needs you and Lately (the design's order): the checklist agents keep with the focus command, open first, ticked ones folded.
    // Present on every focus, one line when empty, so the person sees the list exists. Misc has none.
    f.misc ? null : section({ title: "To do", note: checks.length ? `${done.length} of ${checks.length} done` : null },
      open.length ? todoList(open, id) : h("p", { class: "meta" }, checks.length ? "Everything on the list is done." : "Nothing on the to-do list here."),
      done.length ? section({ title: `Done (${done.length})`, fold: true, key: "fp-done" }, todoList(done, id)) : null),
    f.misc || !sum || !(sum.lately || []).length ? null : section({ title: "Lately" }, bullets(sum.lately)),
    section({ title: "Conversations", note: active.length ? `${active.length} active this week` : null },
      active.length ? threadList(active) : h("p", { class: "meta" }, "Nothing active this week."),
      quiet.length ? section({ title: `Quiet (${quiet.length})`, fold: true, key: "fp-quiet" }, threadList(quiet)) : null,
      arch.length ? section({ title: `Archived (${arch.length})`, fold: true, key: "fp-arch" }, threadList(arch)) : null),
    // Who and how much come after what: Agents, then the numbers (Activity), as the design orders them. The kb notes filed under a focus
    // are not listed here (the design names no such part, and their file names do not read as titles); the Knowledge tab has them.
    section({ title: "Agents" }, ag.now.length || ag.day.length
      ? h("div", { class: "fp-ag" }, ag.now.length ? agRows("Now", ag.now) : null, ag.day.length ? agRows("Last day", ag.day) : null)
      : h("p", { class: "meta" }, "No agent has been on this in the last day.")),
    activitySec(id),
    (f.links || []).length ? h("p", { class: "meta fp-rel" }, "Related: ", f.links.map((id, i) => [i ? ", " : null, h("a", { href: href("focus", id) }, focusName(id))])) : null);
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
