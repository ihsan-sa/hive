// Inbox: posts agents addressed to the person, newest first. In the app it sits under the Chat tab, opened from the button on Chat's home: Chat's home answers
// "what needs me?" from the same data. Kept as a page for the whole list and for marking read.
import { h } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { IB, pollInbox, markRead, onInbox } from "../core/inbox.js";
import { displayText, threadTitle } from "../core/model.js";
import { ago, firstSentence, plural } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { metrics } from "../templates.js";
import { button, empty, list, pageHeader, section } from "../ui/kit.js";

let update = null;
onInbox(() => update && update());
const mark = async body => { await markRead(body); if (update) update(); };

export default {
  id: "inbox", label: "Inbox", about: "every message agents sent you",
  tab: () => S.app.app && can("read"),
  badge: () => (!S.app.app && can("read") && IB.unread ? String(IB.unread) : null),
  enter(ctx) { update = ctx.update; if (S.app.app) pollInbox(); },
  leave() { update = null; },
  render() {
    if (!S.live) return h("div", { class: "page" }, S.failed ? empty("Couldn't read your messages. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    if (!S.app.app || !can("read")) return h("div", { class: "page" }, empty("The inbox isn't available on this connection.", { kind: "page", title: "Inbox" }));
    if (!IB.items) { pollInbox(); return h("div", { class: "page" }, empty("Reading…", { kind: "loading" })); }
    const w = can("post");
    return metrics({
      header: pageHeader({ title: "Inbox", lead: "Every message an agent sent you, newest first. Chat's home shows the ones that need you now.",
        actions: w && IB.unread ? button("Mark all read", () => mark({ all: true }), { size: "s" }) : null,
        after: IB.err ? h("p", { class: "meta", role: "alert", "data-tone": "bad", style: "color:var(--ci)" }, IB.err) : null }),
      sections: [section({}, list(IB.items.map(p => ({
        key: p.id, href: threadHref(p.thread),
        onClick: w && !p.read ? () => mark({ ids: [p.id] }) : null,
        lead: p.read ? null : h("span", { class: "inb-new", "data-tone": "info" }, "New"),
        title: threadTitle(p.thread), sub: p.from,
        meta: [ago(Date.parse(p.at)), p.answered ? "you've replied since" : null, p.files ? plural(p.files, "file") : null].filter(Boolean).join(" · "),
        text: firstSentence(displayText({ text: p.text })),
      })), { empty: "Nothing yet. When an agent needs you or finishes something you asked for, it shows here." }))],
    });
  },
};
