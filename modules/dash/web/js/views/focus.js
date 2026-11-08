// The Focus page (#focus/<id>, #focus/misc): one focus a level deeper than its home card. Its summary in full (Now, Lately),
// what waits on the person in it, the agents' to-do list, who is on it now and in the last day, its conversations, its files.
// A focus is an abstraction for the person (person-doc-2026-11-07): the rail's By focus view and the home's cards open it.
// Stats hide in simple mode. Misc is the unfiled conversations: a plain list, no summary, no to-do.
// It shows in the chat's layout (comms.js inChat), where a conversation would: the rail beside it, its focus marked there.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { threadTitle } from "../core/model.js";
import { ago, plural } from "../core/format.js";
import { href, go, threadHref } from "../core/router.js";
import { simpleOn, plainName } from "../core/simple.js";
import { button, empty, pageHeader, section } from "../ui/kit.js";
import { agentRef } from "../ui/refs.js";
import { needsHref } from "../core/needs.js";
import { loadFoci, fociFailed, focusById, threadsOf, needsOf, askedOf, agentsOf, stateOf, pills, threadLine, focusName, MISC, FOCI_EVERY, activityOf, shareOf, fractionWords, pct, usd } from "../core/foci.js";
import { archived } from "../ui/threadmeta.js";
import { draft } from "../ui/composer.js";
import { jump } from "../ui/message.js";
import { inChat, chatEscape, chatLeave } from "./comms.js";

const idOf = arg => (arg === "misc" ? null : arg);
const bullet = t => h("li", null, simpleOn() ? t.replace(/(^|[\s(])@([A-Za-z0-9_-]+)/g, (m, a, n) => a + plainName(n)) : pills(t));
const bullets = (rows, none) => rows && rows.length ? h("ul", { class: "fp-ul" }, rows.map(bullet)) : h("p", { class: "meta" }, none);

/** Start a new conversation already tagged with this focus: the composer carries the tag and posts it with the first message. */
export function newIn(id) { draft(null).focus = id || ""; go("comms", "~new"); }

const SENT = { needs: "Something here waits on you.", moving: "Agents are on it now.", waiting: "Agents have been here lately; nothing waits on you.", quiet: "Nothing has moved here this week." };

function threadRow(r) {
  const line = threadLine(r.th), d = S.threads.get(r.th);
  return h("li", { class: "lr", "data-key": "th:" + r.th }, h("a", { class: "lr-in", href: threadHref(r.th) },
    h("div", { class: "lr-t" }, h("b", null, threadTitle(r.th)), d ? h("span", { class: "meta" }, ago(d.last)) : null),
    line ? h("div", { class: "lr-x clamp-2" }, line) : null));
}
const threadList = rows => h("ul", { class: "list", "data-dense": "" }, rows.map(threadRow));

/** The focus's share of the hive's work and cost, with the numbers the home card leaves out: one line per window, the same bars. */
function activitySec(id) {
  const a = activityOf(id); if (!a) return null;
  const bar = (win, what) => { const d = shareOf(a, win, what); return h("span", { class: "fc-g-t fp-g-t", role: "img", "aria-label": `${what}: ${pct(d)}` }, h("i", { class: "fc-g-d", style: `width:${(d * 100).toFixed(1)}%` })); };
  const line = (win, label) => {
    const b = a[win] || {}, t = a.total[win] || {}, w = shareOf(a, win, "work"), c = shareOf(a, win, "cost");
    const text = simpleOn()
      ? `${label}: ${fractionWords(w)} of what your Hive did was here, and ${fractionWords(c)} of what it cost.`
      : `${label}: ${plural(b.runs || 0, "run")}, ${plural(b.posts || 0, "post")}, ${usd(b.cost || 0)}; ${pct(w)} of the hive's work and ${pct(c)} of its cost (${plural(t.runs || 0, "run")}, ${usd(t.cost || 0)} in all).`;
    return h("li", null, h("span", null, text), h("span", { class: "fp-g" }, bar(win, "work"), bar(win, "cost")));
  };
  return section({ title: "Activity", note: simpleOn() ? null : "the bars: work, then cost, as a share of the hive's" }, h("ul", { class: "fp-act" }, line("day", "Last day"), line("week", "Last week")));
}

function page(ctx) {
  const id = idOf(ctx.arg), f = id ? focusById(id) : MISC;
  if (!f) return h("div", { class: "page fp" }, S.foci ? empty(`No focus called ${ctx.arg}. It may have been merged or renamed.`, { kind: "empty" }) : empty("Reading…", { kind: "loading" }), h("p", null, h("a", { href: href("comms") }, "Back to Chat")));
  const st = stateOf(id), rows = threadsOf(id), active = rows.filter(r => r.active && !archived(r.th)), quiet = rows.filter(r => !r.active && !archived(r.th)), arch = rows.filter(r => archived(r.th));
  const ag = agentsOf(id), needs = needsOf(id), asked = askedOf(id), sum = f.summary, checks = f.checks || [], open = checks.filter(c => !c.done), done = checks.filter(c => c.done);
  const refs = names => names.map(n => agentRef(n, { dot: false }));
  const meta = simpleOn() ? null : [plural(rows.length, "conversation"), ag.now.length ? `${ag.now.length} working now` : null, needs.length ? `${needs.length} wait on you` : null, sum ? "summary " + ago(sum.at * 1e3) : "no summary yet"];
  const actions = [button("New conversation", () => newIn(id), { kind: "primary", size: "s", icon: "new" }),
    !simpleOn() ? button("Flow", null, { href: href("flow"), kind: "quiet", size: "s", title: "The Flow board: every conversation's state at a glance" }) : null,
    needs.length && needsHref() ? button("Needs you", null, { href: needsHref(), kind: "quiet", size: "s" }) : null];
  return h("div", { class: "page fp", "data-key": "fp:" + (id || "misc") },
    pageHeader({ title: focusName(f), lead: f.misc ? "Conversations not filed under a focus." : f.aim || SENT[st.key], state: { text: st.word, tone: st.tone }, meta, actions, size: "l",
      after: fociFailed() ? h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, "Couldn't refresh this focus; this is what was read last.") : null }),
    f.misc ? null : section({ title: "Now", note: sum ? `from ${plainName(sum.from)} ${ago(sum.at * 1e3)}` : null }, bullets(sum && sum.now, sum ? "Nothing listed as moving." : "No summary yet: the agents on this focus keep one here as they work.")),
    f.misc || !sum || !(sum.lately || []).length ? null : section({ title: "Lately" }, bullets(sum.lately)),
    // Needs you: first the decisions the agents put to the person in the summary (what the home card shows), then every open
    // ask in the focus's conversations from the one list, quieter: the page is a level deeper than the card and may say it all.
    section({ title: "Needs you", note: needs.length && asked.length ? plural(needs.length, "open ask") + " below" : null }, asked.length ? bullets(asked) : null,
      needs.length ? [asked.length ? h("p", { class: "meta fp-also" }, "Also open in its conversations") : null, h("ul", { class: "list", "data-dense": "" }, needs.map(w => h("li", { class: "lr", "data-key": "nd:" + w.th }, h("a", { class: "lr-in", href: threadHref(w.th) },
        h("div", { class: "lr-t" }, h("b", null, threadTitle(w.th)), h("span", { class: "meta" }, ago(w.t))), w.text ? h("div", { class: "lr-x clamp-2" }, w.text) : null))))] : null,
      !asked.length && !needs.length ? h("p", { class: "meta" }, "Nothing waits on you here.") : null),
    f.misc || !checks.length ? null : section({ title: "To do", note: `${done.length} of ${checks.length} done` },
      h("ul", { class: "fp-todo" }, [...open, ...done].map(c => h("li", { "data-done": c.done ? "" : null }, h("span", { class: "fp-box", "aria-hidden": "true" }, c.done ? "✓" : ""), h("span", null, c.text), c.focus && c.focus !== id ? h("span", { class: "meta" }, c.focus) : null)))),
    activitySec(id),
    section({ title: "Agents" }, ag.now.length || ag.day.length
      ? h("div", { class: "fp-ag" }, ag.now.length ? h("p", null, h("span", { class: "meta" }, "Now "), refs(ag.now)) : null, ag.day.length ? h("p", null, h("span", { class: "meta" }, "Last day "), refs(ag.day)) : null)
      : h("p", { class: "meta" }, "No agent has been on this in the last day.")),
    section({ title: "Conversations", note: active.length ? `${active.length} active this week` : null },
      active.length ? threadList(active) : h("p", { class: "meta" }, "Nothing active this week."),
      quiet.length ? section({ title: `Quiet (${quiet.length})`, fold: true, key: "fp-quiet" }, threadList(quiet)) : null,
      arch.length ? section({ title: `Archived (${arch.length})`, fold: true, key: "fp-arch" }, threadList(arch)) : null),
    (f.files || []).length ? section({ title: "Filed here", note: simpleOn() ? null : "kb notes agents filed under this focus when they closed an ask" }, h("ul", { class: "fp-links" }, f.files.map(x => h("li", null,
      h("a", { class: "ref", "data-kind": "kbfile", "data-pv-kb": x.path, "data-pv-key": "kb:" + x.path, href: "#knowledge/" + encodeURIComponent(x.path), title: "Open " + x.path + " (live)" }, x.path),
      x.post && S.byId[x.post] ? [" ", h("a", { class: "meta", href: threadHref(S.byId[x.post].th), title: "The post it came from", onclick: e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jump(x.post); } }, "from the post")] : null)))) : null,
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
