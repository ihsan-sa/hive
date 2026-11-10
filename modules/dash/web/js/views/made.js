// Files and links: the files and links agents gave in conversations someone started or wrote in, newest first. Read from the board: no endpoint of its own.
import { h } from "../core/dom.js";
import { S, SYS, OUTSIDE } from "../core/store.js";
import { threadTitle, displayText } from "../core/model.js";
import { ago } from "../core/format.js";
import { pageHeader, empty, icon, waiting } from "../ui/kit.js";
import { fileUrl, pageUrl, PAGE, TYPE, fsize } from "../ui/message.js";
import { openPreview, remember, kindOf, badgeOf, previewKey } from "../ui/preview.js";
import { inChatPlace, chatEscape, chatLeave } from "./comms.js";

const LINK = /https:\/\/[^\s<>()\[\]"'`]+/g;
const host = u => { try { return new URL(u).host; } catch (e) { return null; } };

/** [{key, name, url, kind, th, t, id, size, from}], newest first. A link given twice counts once, at its latest. */
export function madeThings() {
  const out = [], seen = new Set();
  for (const d of S.threads.values()) {
    if (!d.posts.some(p => p.from === "person")) continue;
    for (let i = d.posts.length - 1; i >= 0; i--) {
      const p = d.posts[i];
      if (OUTSIDE.has(p.from) || SYS.has(p.from)) continue;
      for (const f of p.files || []) out.push({ key: p.id + "/" + f.name, name: f.name, url: PAGE.test(f.name) ? pageUrl(p, f) : fileUrl(p, f), kind: PAGE.test(f.name) ? "page" : "file", th: d.name, t: p.t, id: p.id, size: f.size, from: p.from });
      for (const m of displayText(p).match(LINK) || []) {
        const url = m.replace(/[.,;:!?*_]+$/, ""), at = host(url);
        if (!at || seen.has(url)) continue;
        seen.add(url);
        out.push({ key: url, name: at, url, kind: "link", th: d.name, t: p.t });
      }
    }
  }
  return out.sort((a, b) => b.t - a.t);
}

// A row as in the design (HANDOFF "Inbox/Files"): the type badge, the name, when, and "Type · size · from the conversation". A file opens in the preview pane, as a
// file in a conversation does (review M2: it used to open in a raw new tab); a link opens its page in a new tab.
const word = (k, name) => (k === "image" ? "Picture" : TYPE[k] || (name.includes(".") ? badgeOf(name) + " file" : "File"));
function row(t, files) {
  const sub = (...xs) => h("span", { class: "lr-s" }, xs.filter(Boolean).join(" \u00b7 "));
  if (t.kind === "link") return h("a", { class: "lr mf-row", "data-key": t.key, "data-kind": "link", href: t.url, target: "_blank", rel: "noopener noreferrer", title: t.url },
    h("span", { class: "mf-i", "aria-hidden": "true" }, icon("link")), h("span", { class: "lr-t" }, h("span", null, t.name)), h("span", { class: "lr-x" }, ago(t.t)),
    sub("Link", "from " + threadTitle(t.th)));
  const k = kindOf(t.name), i = files.indexOf(t);
  return h("button", { type: "button", class: "lr mf-row", "data-key": t.key, "data-kind": k, "data-pv-key": t.key, "data-pv-on": previewKey() === t.key ? "" : null, title: "Open " + t.name + " beside the list",
    onclick: () => openPreview(files.map(f => remember({ id: f.id, name: f.name, size: f.size, from: f.from, t: f.t })), i) },
    h("span", { class: "mf-i", "aria-hidden": "true" }, badgeOf(t.name)), h("span", { class: "lr-t" }, h("span", null, t.name)), h("span", { class: "lr-x" }, ago(t.t)),
    sub(word(k, t.name), fsize(t.size), "from " + threadTitle(t.th)));
}

export default {
  id: "made", label: "Files and links", about: "files and links from your conversations",
  tab: () => !!S.app.app,
  leave: chatLeave, escape: chatEscape,
  render(ctx) {
    if (!S.live) return h("div", { class: "page" }, waiting(S.failed, "Reading…", "your files and links"));
    const things = madeThings(), files = things.filter(t => t.kind !== "link");
    // In Chat's own layout (comms.js inChatPlace), the way the design shows it: the rail beside it with Files lit in its foot.
    return inChatPlace(ctx, "made", h("div", { class: "page", "data-key": "made" },
      pageHeader({ title: "Files and links", lead: "Files and links from your conversations, newest first. To change one, say so in its conversation." }),
      things.length ? h("div", { class: "list mf-list" }, things.map(t => row(t, files)))
        : empty("When your Hive gives you a file or a link, you'll find it here too.", { title: "No files or links yet" })));
  },
};
