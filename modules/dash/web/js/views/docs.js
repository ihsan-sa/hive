// Documents: the library's PDFs, opened in the dashboard to comment on (the person, 11211810943e: "open the pdf of my resume in preview
// mode and then comment on it and then the agent in the chat just like edits and updates the pdf and files in the library at the same
// time"). #docs lists them; #docs/<id> opens one in ui/docviewer.js. Owner-only (`files` right), like Files and Mail.
import { h } from "../core/dom.js";
import { can, jget } from "../core/store.js";
import { href } from "../core/router.js";
import { ago } from "../core/format.js";
import { empty, list, pageHeader, needFull } from "../ui/kit.js";
import { icon } from "../ui/icons.js";
import { chatPlace } from "./comms.js";
import { viewer, poll as pollDoc, leave as leaveDoc } from "../ui/docviewer.js";

const D = { list: null, err: null, asked: false, q: "" };
const loadList = async ctx => { try { D.list = await jget("/api/docs"); D.err = null; } catch (e) { D.err = e.message; } ctx.update(); };

function listPage(ctx) {
  if (!D.list && !D.asked) { D.asked = true; loadList(ctx); }
  if (!D.list) return h("div", { class: "page", "data-width": "narrow", "data-key": "docs" }, pageHeader({ title: "Documents" }),
    D.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, "Couldn't read the library: " + D.err) : empty("Reading…", { kind: "loading" }));
  const q = D.q.trim().toLowerCase(), docs = D.list.docs.filter(d => !q || (d.title + " " + d.id).toLowerCase().includes(q));
  const rows = docs.map(d => ({
    key: d.id, lead: icon("doc"), title: d.title,
    sub: `${d.id} · revision ${d.rev === "next" ? "new, being filed" : d.rev}` + (d.drafts ? ` · ${d.drafts} not sent` : "") + (d.open ? ` · ${d.open} with the agent` : ""),
    meta: ago(d.at * 1000), href: href("docs", d.id),
  }));
  return h("div", { class: "page", "data-width": "narrow", "data-key": "docs" },
    pageHeader({ title: "Documents", lead: "The library's PDFs. Open one to comment on it like in the library: select text or draw a box. An agent edits the file and files the new revision, and it shows up here." }),
    h("input", { class: "input dv-find", type: "search", placeholder: "Find a document", "aria-label": "Find a document", "data-key": "find", value: D.q,
      oninput: e => { D.q = e.target.value; ctx.update(); } }),
    list(rows, { empty: q ? "No document matches." : "The library has no PDFs yet.", limit: D.more ? 0 : 30, onMore: () => { D.more = true; ctx.update(); } }));
}

export default chatPlace("docs", {
  id: "docs", label: "Documents", tab: () => can("files"),
  poll: { every: 15000, run: ctx => { if (ctx.arg) pollDoc(); else loadList(ctx); } },
  leave: () => { leaveDoc(); D.asked = false; },
  render(ctx) {
    if (!can("files")) return needFull("Documents", "Documents");
    if (!ctx.arg) return listPage(ctx);
    return h("div", { class: "page dvp", "data-key": "doc:" + ctx.arg },
      h("a", { class: "cm-back dv-back", href: href("docs") }, "‹ Documents"),
      viewer(ctx.arg, ctx));
  },
});
