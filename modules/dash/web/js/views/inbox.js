// Inbox: posts agents addressed to the person, newest first. App mode only. Shares the door's read marks
// (~/.hive/delivered/person). It polls while the page is open anywhere, since the tab carries the unread count.
import { h } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { ago, firstSentence, plural } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { metrics } from "../templates.js";
import { button, empty, list, pageHeader, section } from "../ui/kit.js";

const IB = { items: null, unread: 0, sig: "", err: "", asked: false };
let update = null;

async function pollInbox() {
  if (!S.app.app || !can("read") || document.hidden) return;
  try {
    const v = await jget("/api/inbox"), sig = JSON.stringify(v);
    if (sig === IB.sig) return;
    IB.sig = sig; IB.items = v.items; IB.unread = v.unread;
    if (update) update();
  } catch (e) {}
}
setInterval(pollInbox, 3000);

async function mark(body) {
  IB.err = "";
  try { await post("/api/inbox-read", body); }
  catch (e) { IB.err = "Couldn't mark it read: " + e.message + "."; }
  IB.sig = ""; await pollInbox(); if (update) update();
}

export default {
  id: "inbox", label: "Inbox", tab: () => S.app.app && can("read"),
  badge: () => (S.app.app && can("read") && IB.unread ? String(IB.unread) : null),
  enter(ctx) { update = ctx.update; if (S.app.app) pollInbox(); },
  leave() { update = null; },
  render() {
    if (!S.live) return h("div", { class: "page" }, empty("Reading…", { kind: "loading" }));
    if (!S.app.app || !can("read")) return h("div", { class: "page" }, empty("The inbox isn't available on this connection.", { kind: "page", title: "Inbox" }));
    if (!IB.items) { if (!IB.asked) { IB.asked = true; pollInbox(); } return h("div", { class: "page" }, empty("Reading…", { kind: "loading" })); }
    const w = can("post");
    return metrics({
      header: pageHeader({ title: "For you", lead: "Posts agents addressed to you, newest first.",
        actions: w && IB.unread ? button("Mark all read", () => mark({ all: true }), { size: "s" }) : null,
        after: IB.err ? h("p", { class: "meta", role: "alert", "data-tone": "bad", style: "color:var(--ci)" }, IB.err) : null }),
      sections: [section({}, list(IB.items.map(p => ({
        key: p.id, href: threadHref(p.thread),
        onClick: w && !p.read ? () => mark({ ids: [p.id] }) : null,
        lead: p.read ? null : h("span", { class: "inb-new", "data-tone": "info" }, "New"),
        title: p.from, sub: p.thread,
        meta: [ago(Date.parse(p.at)), p.answered ? "you've replied since" : null, p.files ? plural(p.files, "file") : null].filter(Boolean).join(" · "),
        text: firstSentence(p.text),
      })), { empty: "Nothing yet. When an agent needs you or finishes something you asked for, it shows here." }))],
    });
  },
};
