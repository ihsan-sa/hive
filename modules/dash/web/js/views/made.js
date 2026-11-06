// Files and links: the files and links agents gave in conversations someone started or wrote in, newest first. Read from the board: no endpoint of its own.
import { h } from "../core/dom.js";
import { S, SYS, OUTSIDE } from "../core/store.js";
import { threadTitle, displayText } from "../core/model.js";
import { ago } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { pageHeader, empty } from "../ui/kit.js";
import { fileUrl, pageUrl, PAGE } from "../ui/message.js";

const LINK = /https:\/\/[^\s<>()\[\]"'`]+/g;
const host = u => { try { return new URL(u).host; } catch (e) { return null; } };

/** [{key, name, url, kind, th, t}], newest first. A link given twice counts once, at its latest. */
export function madeThings() {
  const out = [], seen = new Set();
  for (const d of S.threads.values()) {
    if (!d.posts.some(p => p.from === "person")) continue;
    for (let i = d.posts.length - 1; i >= 0; i--) {
      const p = d.posts[i];
      if (OUTSIDE.has(p.from) || SYS.has(p.from)) continue;
      for (const f of p.files || []) out.push({ key: p.id + "/" + f.name, name: f.name, url: PAGE.test(f.name) ? pageUrl(p, f) : fileUrl(p, f), kind: PAGE.test(f.name) ? "page" : "file", th: d.name, t: p.t });
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

export default {
  id: "made", label: "Files and links", about: "files and links from your conversations",
  tab: () => !!S.app.app,
  render() {
    if (!S.live) return h("div", { class: "page" }, empty("Reading…", { kind: "loading" }));
    const things = madeThings();
    return h("div", { class: "page", "data-width": "narrow", "data-key": "made" },
      pageHeader({ title: "Files and links", lead: "Files and links from your conversations, newest first." }),
      things.length ? h("ul", { class: "list" }, things.map(t => h("li", { class: "lr", "data-key": t.key },
        h("a", { class: "lr-in", href: t.url, target: "_blank", rel: "noopener noreferrer" },
          h("div", { class: "lr-t" }, h("b", { title: t.name }, t.name), h("span", { class: "muted" }, t.kind), h("span", { class: "meta" }, ago(t.t))),
          h("div", { class: "lr-x clamp-2" }, "From ", threadTitle(t.th))))))
        : empty("Nothing yet. When your Hive gives you a file or a link, you'll find it here too.", { title: "Nothing made yet" }),
      things.length ? h("p", { class: "meta" }, "To change something, say so in ", h("a", { href: threadHref(things[0].th) }, "the conversation"), ".") : null);
  },
};
