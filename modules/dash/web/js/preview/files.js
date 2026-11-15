// The Files list in the preview panel: every file this conversation shows (attachments and file links, in page order), then
// a project's folder you browse. A click opens the file in the same preview; All files in its header comes back here, to the
// folder you were in. Folders come from the Knowledge API (/api/kb/open, under the "files" right), so a project file is read
// the way Knowledge reads it: redacted text, images as images, nothing run.
import { h } from "../core/dom.js";
import { can, said } from "../core/store.js";
import { button, icon } from "../ui/kit.js";
import { threadFiles, kindOf, badgeOf, filesClosedByHand } from "../ui/preview.js";
import { fix } from "../core/format.js";

const KEY = "pv.dir", ROOT = "projects";
let dir = null;   // the folder shown, kept between openings
try { dir = localStorage.getItem(KEY); } catch (e) {}
if (!dir || !(dir === ROOT || dir.startsWith(ROOT + "/"))) dir = ROOT;

const fsize = n => (n == null ? "" : n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(n < 10240 ? 1 : 0) + " kB" : fix((n / 1048576), 1) + " MB");
const who = f => (f.from ? (f.from === "person" ? "you" : f.from) : "");

function row(name, sub, badge, kind, onclick, cls) {
  return h("button", { type: "button", class: "pf-row" + (cls ? " " + cls : ""), title: name, onclick },
    h("b", { class: "pv-badge", "data-kind": kind }, badge),
    h("span", { class: "pf-n" }, h("span", { class: "pf-fn" }, name), sub ? h("span", { class: "meta" }, sub) : null));
}

export function drawFiles(head, body, { open, close }) {
  head.replaceChildren(h("div", { class: "pv-t" },
    icon("folder", { cls: "pf-ic" }),
    h("div", { class: "pv-n" }, h("span", { class: "pv-fn" }, "Files")),
    button("Close (Esc)", () => { filesClosedByHand(); close(); }, { kind: "icon", icon: "close", size: "s", cls: "pv-x" })));
  const mine = threadFiles();
  // Like ChatGPT's Outputs and Sources: what agents made, then what you sent. Prev/Next still walks them all in page order.
  const one = (f, i) => row(f.name, [fsize(f.size), who(f) && "from " + who(f)].filter(Boolean).join(" · "), badgeOf(f.name), kindOf(f.name), () => open(mine, i));
  const out = [], src = [];
  mine.forEach((f, i) => (f.from === "person" ? src : out).push(one(f, i)));
  const group = (t, rows) => rows.length ? [h("h3", { class: "pf-h" }, t, h("span", { class: "pf-c" }, String(rows.length))), ...rows] : [];
  const here = h("section", { class: "pf-sec", "aria-label": "In this conversation" },
    ...(mine.length ? [...group("Outputs", out), ...group("Sources", src)]
      : [h("h3", { class: "pf-h" }, "In this conversation"), h("p", { class: "meta pf-none" }, "No files here yet. Attach one, or ask an agent to.")]));
  const proj = can("files") ? h("section", { class: "pf-sec", "aria-label": "Project files" }, h("h3", { class: "pf-h" }, "Project files"), h("div", { class: "pf-dir" })) : null;
  body.replaceChildren(h("div", { class: "pf" }, here, proj));
  body.scrollTop = 0;
  if (proj) browse(proj.lastChild, dir, open);
}

async function browse(box, path, open) {
  box.replaceChildren(h("p", { class: "meta pf-none" }, "Loading…"));
  let d;
  try {
    const r = await fetch("/api/kb/open?path=" + encodeURIComponent(path), { cache: "no-store" });
    if (r.status === 404 && path !== ROOT) return browse(box, ROOT, open);   // the remembered folder went away
    if (r.status === 403) return box.replaceChildren(h("p", { class: "meta pf-none" }, "This sign-in can't read project files."));
    if (!r.ok) throw new Error(said(r.status));
    d = await r.json();
  } catch (e) {
    return box.replaceChildren(h("p", { class: "meta pf-none" }, "Couldn't list this folder: " + (e && e.message || e)));
  }
  if (!box.isConnected) return;
  dir = path; try { localStorage.setItem(KEY, path); } catch (e) {}
  const parts = path.split("/"), go = p => () => browse(box, p, open);
  const crumbs = h("nav", { class: "pf-crumbs", "aria-label": "Folder" }, parts.map((p, i) => {
    const to = parts.slice(0, i + 1).join("/"), last = i === parts.length - 1;
    return [i ? h("span", { class: "pf-sep", "aria-hidden": "true" }, "/") : null,
      last ? h("span", { class: "pf-cur", "aria-current": "page" }, i ? p : "Projects")
        : h("button", { type: "button", class: "pf-crumb", onclick: go(to) }, i ? p : "Projects")];
  }));
  const ents = (d.entries || []).filter(e => !e.worktree);
  const files = ents.filter(e => !e.dir).map(e => ({ kb: e.path, name: e.name, size: e.size, t: e.t }));
  const rows = ents.map(e => e.dir
    ? row(e.name, e.title || "", "dir", "dir", go(e.path), "pf-d")
    : row(e.name, [fsize(e.size), e.title].filter(Boolean).join(" · "), badgeOf(e.name), kindOf(e.name), () => open(files, files.findIndex(f => f.kb === e.path))));
  // At the top there can be dozens of projects: show a few, the rest on request.
  const cap = path === ROOT && rows.length > 6 && !box.dataset.all;
  const list = h("div", { class: "pf-list" }, cap ? rows.slice(0, 6) : rows);
  if (cap) list.append(h("button", { type: "button", class: "pf-more", onclick: () => { box.dataset.all = "1"; browse(box, path, open); } }, "Show all " + rows.length + " projects"));
  box.replaceChildren(crumbs, rows.length ? list : h("p", { class: "meta pf-none" }, "This folder is empty."),
    ...(d.more ? [h("p", { class: "meta pf-none" }, "More files are here than the list shows; Knowledge has them all.")] : []));
}
