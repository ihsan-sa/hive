// Inbox: posts agents addressed to the person, newest first. In the app it sits under the Chat tab, opened from the button on Chat's home: Chat's home answers
// "what needs me?" from the same data. Kept as a page for the whole list and for marking read.
import { h } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { IB, pollInbox, markRead, onInbox } from "../core/inbox.js";
import { agentLabel, displayText, threadTitle } from "../core/model.js";
import { ago, firstSentence, plural } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { form } from "../templates.js";
import { button, empty, list, pageHeader, section } from "../ui/kit.js";

let update = null;
onInbox(() => update && update());
const mark = async body => { await markRead(body); if (update) update(); };
// The starter's notices repeat ("a7c31 waits for provider codex: limit at 96%", 150 of the latest 200): all of the same one are one row with a count. Agents' own messages are never folded.
const body = p => String(p.text || "").replace(/^\[source thread: [^\]]*\]\s*/, "");   // the starter's routing note isn't for reading
const sameNotice = p => p.from === "starter" && body(p).split("\n")[0].replace(/[0-9a-f]{5,}/g, "#").replace(/\d+/g, "N");
function folded(items) {   // items come newest first: a notice joins the row of its newest repeat, wherever that is, so two notices taking turns stay two rows
  const out = [], seen = new Map();
  for (const p of items) {
    const k = sameNotice(p), row = k && seen.get(k);
    if (row) { row.all.push(p); continue; }
    const r = { p, key: k, all: [p] };
    out.push(r); if (k) seen.set(k, r);
  }
  return out;
}

export default {
  id: "inbox", label: "Inbox", about: "every message agents sent you",
  tab: () => S.app.app && can("read"),
  badge: () => (!S.app.app && can("read") && IB.unread ? String(IB.unread) : null),
  enter(ctx) { update = ctx.update; if (S.app.app) pollInbox(); },
  leave() { update = null; },
  render() {
    if (!S.live) return h("div", { class: "page", "data-width": "narrow" }, S.failed ? empty("Couldn't read your messages. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    if (!S.app.app || !can("read")) return h("div", { class: "page", "data-width": "narrow" }, empty("The inbox isn't available on this connection.", { kind: "page", title: "Inbox" }));
    if (!IB.items) { pollInbox(); return h("div", { class: "page", "data-width": "narrow" }, empty("Reading…", { kind: "loading" })); }
    const w = can("post");
    return form({
      header: pageHeader({ title: "Inbox", hint: "Every message an agent sent you, newest first. Chat's home shows the ones that need you now.",
        actions: w && IB.unread ? button("Mark all read", () => mark({ all: true }), { size: "s" }) : null,
        after: IB.err ? h("p", { class: "fmsg", role: "alert", "data-tone": "bad" }, IB.err) : null }),
      sections: [section({ id: "inbox-list" }, list(folded(IB.items).map(({ p, all }) => { const unread = all.filter(x => !x.read).map(x => x.id); return {
        key: p.id, href: threadHref(p.thread),
        onClick: w && unread.length ? () => mark({ ids: unread }) : null,
        lead: unread.length ? h("span", { class: "inb-new", "data-tone": "info" }, "New") : null,
        title: threadTitle(p.thread), sub: agentLabel(p.from),
        meta: [ago(Date.parse(p.at)), all.length > 1 ? `${all.length} like this` : null, p.answered ? "you've replied since" : null, p.files ? plural(p.files, "file") : null].filter(Boolean).join(" · "),
        text: firstSentence(displayText({ text: body(p) })),
      }; }), { limit: innerWidth < 640 ? IB.shown || 12 : undefined, onMore: () => { IB.shown = (IB.shown || 12) + 20; update && update(); }, empty: "Nothing yet. When an agent needs you or finishes something you asked for, it shows here." }))],
    });
  },
};
