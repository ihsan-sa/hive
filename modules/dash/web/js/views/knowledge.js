// Knowledge: the short Markdown notes agents write as they learn (~/.hive/kb), how they link, and how they changed.
// Not built yet on the server: GET /api/knowledge returns {dir, notes:[{path, title, by, thread, t, links, text, history}]}
// (fixtures/knowledge.json is the shape). #knowledge/<path> opens one note. On a phone the list and a note take turns.
import { h } from "../core/dom.js";
import { S, built, jget } from "../core/store.js";
import { ago, stamp, plural, short, mdPlain } from "../core/format.js";
import { href, threadHref } from "../core/router.js";
import { threadTitle } from "../core/model.js";
import { empty, input, list } from "../ui/kit.js";
import { agentRef } from "../ui/refs.js";
import { markdown } from "../ui/markdown.js";
import { markWords } from "../ui/message.js";

const K = { v: null, failed: false, q: "", sort: "recent" };
async function load(ctx) {
  try { K.v = await jget("/api/knowledge"); K.failed = false; } catch (e) { K.failed = true; }
  ctx.update();
}
const words = s => s.toLowerCase().split(/\s+/).filter(Boolean);
const folder = p => (p.includes("/") ? p.split("/")[0] : "");

function noteRows(notes, cur) {
  const ws = words(K.q);
  const hits = ws.length ? notes.filter(n => ws.every(w => (n.title + " " + n.path + " " + n.text).toLowerCase().includes(w))) : notes;
  const sorted = [...hits].sort((a, b) => (K.sort === "recent" ? b.t - a.t : a.path.localeCompare(b.path)));
  return sorted.map(n => ({ key: n.path, href: href("knowledge", n.path), current: cur && cur.path === n.path, title: ws.length ? markWords(n.title, ws) : n.title,
    meta: ago(n.t).replace(" ago", ""), text: short(mdPlain(n.text).replace(/^#.*\n/, "").replace(/\s+/g, " "), 90) }));
}

function noteView(n, all) {
  const by = new Map(all.map(x => [x.path, x])), back = all.filter(x => x.links.includes(n.path));
  const link = p => { const x = by.get(p); return h("a", { class: "kb-link", href: href("knowledge", p), "data-key": p }, h("b", null, x ? x.title : p), h("span", { class: "meta" }, p)); };
  return h("article", { class: "kb-note", "data-key": "note:" + n.path },
    h("a", { class: "btn kb-back", href: href("knowledge"), "data-kind": "quiet", "data-size": "s" }, "‹ Notes"),
    h("header", { class: "kb-h" },
      h("h1", { class: "kb-t" }, n.title),
      h("p", { class: "meta" }, h("code", null, (K.v.dir || "") + "/" + n.path), ` · changed ${ago(n.t)} by `, agentRef(n.by), " in ", h("a", { href: threadHref(n.thread) }, threadTitle(n.thread)))),
    h("div", { class: "kb-body" }, markdown(n.text.replace(/^# .*\n+/, ""))),
    h("div", { class: "kb-rel" },
      h("section", { class: "sec" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Links to"), h("span", { class: "sec-n" }, plural(n.links.length, "note"))),
        n.links.length ? h("div", { class: "kb-links" }, n.links.map(link)) : empty("Nothing.")),
      h("section", { class: "sec" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Linked from"), h("span", { class: "sec-n" }, plural(back.length, "note"))),
        back.length ? h("div", { class: "kb-links" }, back.map(x => link(x.path))) : empty("Nothing links here yet."))),
    h("section", { class: "sec kb-hist" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "How it changed"), h("span", { class: "sec-n" }, "newest first")),
      h("ol", { class: "kb-tl" }, n.history.map((c, i) => h("li", { "data-key": String(c.t) + i },
        h("time", { class: "meta num", title: stamp(c.t * 1e3) }, ago(c.t * 1e3)),
        h("span", null, c.change), h("span", { class: "meta" }, agentRef(c.by), " in ", h("a", { href: threadHref(c.thread) }, threadTitle(c.thread))))))));
}

export default {
  id: "knowledge", label: "Knowledge", about: "what the hive has learned", tab: () => !!S.app.app && built("knowledge"),
  enter(ctx) { if (!K.v) load(ctx); },
  render(ctx) {
    if (!K.v) return h("div", { class: "page" }, K.failed ? empty("Couldn't read the notes.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    const notes = (K.v.notes || []).map(n => ({ ...n, t: n.t * 1e3 })), cur = ctx.arg ? notes.find(n => n.path === ctx.arg) : null;
    const recent = [...notes].sort((a, b) => b.t - a.t)[0];
    const top = document.getElementById("top");
    return h("div", { class: "kb", "data-key": "kb", "data-in": cur ? "" : null, style: `--top-h:${top ? top.offsetHeight : 47}px` },
      h("nav", { class: "kb-side", "aria-label": "Notes" },
        h("div", { class: "kb-top" },
          h("div", { class: "kb-title" }, h("h1", null, "What the hive knows"), h("span", { class: "nb", title: "Designed, not built yet: the server needs GET /api/knowledge" }, "not built yet")),
          h("p", { class: "meta" }, `${plural(notes.length, "note")} the agents wrote as they worked. They read them before starting, so they remember what you told them.`),
          input({ type: "search", placeholder: "Search notes", "aria-label": "Search notes", value: K.q, oninput: e => { K.q = e.target.value; ctx.update(); } }),
          h("div", { class: "seg", role: "group", "aria-label": "Order" }, [["recent", "Recent"], ["path", "By folder"]].map(([k, l]) => h("button", { type: "button", "aria-pressed": String(K.sort === k), onclick: () => { K.sort = k; ctx.update(); } }, l)))),
        h("div", { class: "kb-list" }, list(noteRows(notes, cur), { dense: true, empty: K.q ? "No note has those words." : "No notes yet. Agents write them as they learn things worth keeping." }))),
      h("section", { class: "kb-main", "aria-label": "Note" },
        cur ? noteView(cur, notes) : notes.length ? h("div", { class: "kb-none" }, empty(`Pick a note. The newest is ${recent.title}, changed ${ago(recent.t)}.`)) : empty("Nothing yet.")));
  },
};
