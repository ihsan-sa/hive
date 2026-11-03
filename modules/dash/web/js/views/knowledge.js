// Knowledge: everything the Hive keeps, to browse, search and read: notes, modules, scripts, core, projects and post files.
// #knowledge/<home-relative path> opens a folder or a file, and <path>::<line> jumps to a line. The server decides what can be seen and
// redacts all text; this view only reads, except a project's goals.md, saved through /api/plan with the plans right. On a phone the list and a file take turns.
import { h } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { ago, plural, stamp } from "../core/format.js";
import { href, threadHref } from "../core/router.js";
import { threadTitle } from "../core/model.js";
import { button, empty, field, input, list } from "../ui/kit.js";
import { markdown } from "../ui/markdown.js";
import { markWords } from "../ui/message.js";

const K = { home: null, seen: new Map(), failed: new Map(), q: "", res: null, timer: 0, worktrees: false, limit: 300, source: false, edit: null, shown: null, memo: {}, mentions: { key: "", v: [] } };
const open = (path, line) => href("knowledge", line ? `${path}::${line}` : path);
const parse = arg => { const m = /^(.*)::(\d+)$/.exec(arg || ""); return m ? { path: m[1], line: +m[2] } : { path: arg || "", line: 0 }; };
const parent = p => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "");
const bytes = n => (n == null ? "" : n < 1024 ? n + " B" : n < 1048576 ? Math.round(n / 1024) + " KB" : (n / 1048576).toFixed(1) + " MB");

async function fetchPath(path, ctx) {
  try { const r = await jget("/api/kb/open?path=" + encodeURIComponent(path)); if (!r.path) throw Error("404"); K.seen.set(path, r); K.failed.delete(path); }
  catch (e) { if (e.message === "404" || !K.seen.has(path)) { K.seen.delete(path); K.failed.set(path, e.message); } }   // a blip while reading leaves the page as it was
  ctx.update();
}
async function loadHome(ctx) { try { K.home = await jget("/api/kb/home"); } catch (e) { K.home = K.home || {}; } ctx.update(); }
function refresh(ctx) {
  const { path } = parse(ctx.arg);
  if (!path) return loadHome(ctx);
  fetchPath(path, ctx);
  const up = parent(path), cur = K.seen.get(path);
  if (up && !K.seen.has(up) && (!cur || cur.type !== "dir")) fetchPath(up, ctx);
}
function search(ctx) {
  clearTimeout(K.timer);
  const q = K.q.trim();
  if (q.length < 2) { K.res = null; return; }
  K.timer = setTimeout(async () => {
    try { const r = await jget("/api/kb/search?q=" + encodeURIComponent(q)); if (K.q.trim() === q) K.res = r; }
    catch (e) { K.res = { q, names: [], hits: [], failed: e.message }; }
    ctx.update();
  }, 250);
}

// Paths in a note become links inside this page. Only home-relative paths under the shelves do; the server still judges each one.
const ROOTS = "kb|modules|core|projects|investigations|archive";
const IN_TEXT = new RegExp(`\\[([^\\]\\n]+)\\]\\(([^)\\s]+)\\)|~/((?:${ROOTS}|tools|files)(?:/[\\w.-]+)*)`, "g");
const IN_CODE = new RegExp(`^(?:~/)?((?:${ROOTS}|tools|files)(?:/[\\w.-]+)*)/?$|^(${ROOTS})(/[\\w.-]+)+$`);
function relative(from, to) {
  if (/^[a-z]+:|^#/i.test(to)) return null;
  to = to.replace(/[#?].*$/, "");
  if (to.startsWith("~/")) to = to.slice(2);
  else if (to.startsWith("/")) return null;
  else to = `${parent(from)}/${to}`;
  const out = [];
  for (const part of to.split("/")) { if (part === "..") { if (!out.length) return null; out.pop(); } else if (part && part !== ".") out.push(part); }
  return new RegExp(`^(${ROOTS}|tools|files)$`).test(out[0] || "") ? out.join("/") : null;
}
function linkPaths(root, from) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT), nodes = [];
  while (walker.nextNode()) if (!walker.currentNode.parentElement.closest("a,pre")) nodes.push(walker.currentNode);
  for (const n of nodes) {
    const text = n.nodeValue;
    if (n.parentElement.tagName === "CODE") {
      const m = IN_CODE.exec(text.trim());
      if (m) { const p = (m[1] || text.trim()).replace(/\/$/, ""); n.replaceWith(h("a", { href: open(p) }, text)); }
      continue;
    }
    let at = 0, out = null;
    for (const m of text.matchAll(IN_TEXT)) {
      const p = m[3] ? m[3].replace(/\.$/, "") : relative(from, m[2]);
      if (!p) continue;
      out = out || [];
      out.push(text.slice(at, m.index), h("a", { href: open(p) }, m[1] || `~/${p}`));
      at = m.index + m[0].length - (m[3] && m[3].endsWith(".") ? 1 : 0);
    }
    if (out) n.replaceWith(...out, text.slice(at));
  }
  return root;
}
// Bodies can be half a megabyte: built once per version, and left alone between refreshes.
function once(sig, build) { if (K.memo.sig !== sig) K.memo = { sig, node: build() }; return K.memo.node; }
// The node can only live in one place: while it's on the page, the new tree gets an empty twin, and morph keeps the one that's there.
// If morph used the twin after all (the layout around it changed), the node moves into it.
function own(cls, sig, build) {
  const node = once(sig, build), box = h("div", { class: cls, "data-own": "", "data-sig": sig });
  if (!node.isConnected) box.append(node);
  else queueMicrotask(() => { if (box.isConnected && !box.firstChild && K.memo.node === node) box.append(node); });
  return box;
}

function codeBody(text, line) {
  const n = (text.match(/\n/g) || []).length + (text.endsWith("\n") || !text ? 0 : 1);
  return h("div", { class: "kb-code" },
    line ? h("div", { class: "kb-code-at", style: `top:calc(var(--s2) + ${line - 1} * 1.5em)` }) : null,
    h("pre", { class: "kb-gutter", "aria-hidden": "true" }, Array.from({ length: Math.max(n, 1) }, (_, i) => i + 1).join("\n")),
    h("pre", { class: "kb-src" }, text));
}
const mdBody = (text, from) => linkPaths(markdown(text), from);
function docBody(d, line) {
  const asCode = line || K.source || d.kind === "text" || d.type === "text";
  const sig = `${d.path}|${d.t}|${asCode ? "code" + line : "md"}`;
  return own(asCode ? "kb-codewrap" : "kb-body", sig, () => (asCode ? codeBody(d.text, line) : mdBody(d.text, d.path)));
}

function mentions(path) {
  const key = path + "|" + S.posts.length;
  if (K.mentions.key !== key) {
    const found = [];
    for (let i = S.posts.length - 1; i >= 0 && found.length < 6; i--) if ((S.posts[i].text || "").includes(path)) found.push(S.posts[i]);
    K.mentions = { key, v: found };
  }
  return K.mentions.v;
}
function related(d) {
  const posts = mentions(d.path), linked = d.linked || [];
  if (!posts.length && !linked.length) return null;
  return h("div", { class: "kb-rel" },
    linked.length ? h("section", { class: "sec", "data-key": "linked" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Named in"), h("span", { class: "sec-n" }, plural(linked.length, "note"))),
      h("div", { class: "kb-links" }, linked.map(p => h("a", { class: "kb-link", href: open(p), "data-key": p }, h("b", null, p.split("/").pop()), h("span", { class: "meta" }, p))))) : null,
    posts.length ? h("section", { class: "sec", "data-key": "threads" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Threads"), h("span", { class: "sec-n" }, "latest")),
      h("div", { class: "kb-links" }, posts.map(p => h("a", { class: "kb-link", href: threadHref(p.thread), "data-key": p.id }, h("b", null, threadTitle(p.thread)), h("span", { class: "meta" }, p.from))))) : null);
}

// A project's plan. The text to edit comes unredacted from /api/plans, so a save can't write "[hidden]" over a real line.
const projectOf = path => { const m = /^projects\/([^/]+)(\/goals\.md)?$/.exec(path); return m ? m[1] : null; };
async function startEdit(name, ctx) {
  try {
    const all = await jget("/api/plans"), p = all.find(x => x.name === name) || {};
    K.edit = { at: parse(ctx.arg).path, name, isNew: false, text: p.text || "", version: p.version || null, say: null };
  } catch (e) { K.edit = { at: parse(ctx.arg).path, name, text: "", failed: e.message }; }
  ctx.update();
}
async function saveEdit(start, ctx) {
  const e = K.edit, name = e.isNew ? e.name.trim() : e.name;
  const wasNew = e.isNew;
  try {
    const r = await post("/api/plan", { name, text: e.text, version: e.version, start });
    Object.assign(e, { name, version: r.version, isNew: false, say: start ? "Saved, and an agent is starting on it." : "Saved. Nothing was started.", tone: "good" });
    K.seen.delete(`projects/${name}`); K.seen.delete(`projects/${name}/goals.md`); K.seen.delete("projects");
    if (wasNew) { e.at = `projects/${name}`; location.hash = open(e.at); return; }   // the new project's own page, with the editor still open
    refresh(ctx);
  } catch (err) { e.say = err.message; e.tone = "bad"; ctx.update(); }
}
function editor(ctx) {
  const e = K.edit;
  if (e.failed) return empty(e.failed, { kind: "error" });
  return h("section", { class: "kb-edit", "data-key": "edit" },
    h("h2", { class: "kb-t" }, e.isNew ? "New project" : `Plan for ${e.name}`),
    e.isNew ? field("Name", input({ value: e.name, autocomplete: "off", oninput: (ev, n) => { e.name = n.value; } })) : null,
    h("textarea", { class: "input", rows: 20, "aria-label": "Plan", value: e.text, oninput: (ev, n) => { e.text = n.value; } }),
    h("div", { class: "frow" }, button("Save", () => saveEdit(false, ctx), { kind: "primary" }), button("Save and start an agent", () => saveEdit(true, ctx)),
      button("Close", () => { K.edit = null; ctx.update(); }, { kind: "quiet" }),
      e.say ? h("p", { class: "fmsg", "data-tone": e.tone, role: "status" }, e.say) : null));
}

function crumbs(path) {
  const parts = path ? path.split("/") : [];
  return h("nav", { class: "kb-crumbs", "aria-label": "Where you are" },
    h("a", { href: href("knowledge"), "data-key": "home" }, "Knowledge"),
    parts.flatMap((p, i) => [h("span", { "aria-hidden": "true", "data-key": "s" + i }, "/"),
      i === parts.length - 1 ? h("span", { "aria-current": "page", "data-key": "c" + i }, p) : h("a", { href: open(parts.slice(0, i + 1).join("/")), "data-key": "c" + i }, p)]));
}

function searchRows() {
  const r = K.res, ws = K.q.toLowerCase().split(/\s+/).filter(Boolean);
  const names = list(r.names.map(n => ({ key: "n:" + n.path, href: open(n.path), title: markWords(n.path.split("/").pop(), ws), text: parent(n.path), meta: ago(n.t * 1e3).replace(" ago", "") })), { dense: true, limit: 30 });
  const hits = list(r.hits.map(x => ({ key: `h:${x.path}:${x.line}`, href: open(x.path, x.line), title: x.path, text: markWords(x.text.trim(), ws), meta: String(x.line) })), { dense: true, limit: 100 });
  return [
    r.failed ? empty(r.failed, { kind: "error" }) : null,
    r.busy ? h("p", { class: "kb-sub", "data-key": "busy" }, "Other searches are running. These are names only.") : null,
    r.names.length ? h("h2", { class: "kb-sub", "data-key": "nh" }, "File names") : null, r.names.length ? names : null,
    r.hits.length ? h("h2", { class: "kb-sub", "data-key": "hh" }, r.more ? "Inside files, first matches" : "Inside files") : null, r.hits.length ? hits : null,
    !r.names.length && !r.hits.length && !r.failed ? empty("Nothing found.") : null,
  ];
}
function folderRows(dir, cur, ctx) {
  const all = dir.entries || [], tree = K.worktrees ? all : all.filter(e => !e.worktree), hidden = all.length - all.filter(e => !e.worktree).length;
  return [
    list(tree.map(e => ({ key: e.path, href: open(e.path), current: cur === e.path, title: e.name + (e.dir ? "/" : ""), text: e.title || (e.worktree ? "worktree" : ""), meta: e.dir ? ago(e.t * 1e3).replace(" ago", "") : bytes(e.size) })),
      { empty: "Nothing to show here.", dense: true, limit: K.limit, onMore: () => { K.limit += 500; ctx.update(); } }),
    hidden ? button(K.worktrees ? "Hide worktrees" : `Show ${plural(hidden, "worktree")}`, () => { K.worktrees = !K.worktrees; ctx.update(); }, { kind: "quiet", size: "s", cls: "kb-more" }) : null,
    dir.more ? h("p", { class: "kb-sub", "data-key": "cut" }, `Showing the first ${all.length}.`) : null,
  ];
}
function changedRows() {
  return list((K.home.recent || []).map(f => ({ key: f.path, href: open(f.path), title: f.title || f.name, text: parent(f.path), meta: ago(f.t * 1e3).replace(" ago", "") })), { empty: "Nothing yet." });
}
function sideBody(path, ctx) {
  if (K.q.trim().length >= 2) return K.res ? searchRows() : empty("Searching…", { kind: "loading" });
  if (!path) {
    if (!K.home) return empty("Reading…", { kind: "loading" });
    return [list((K.home.shelves || []).map(s => ({ key: s.path, href: open(s.path), title: s.label, text: s.about, meta: String(s.n) })), { empty: "Nothing here yet." }),
      h("div", { class: "kb-phone", "data-key": "recent" }, h("h2", { class: "kb-sub" }, "Changed lately"), changedRows())];
  }
  const cur = K.seen.get(path), dir = cur && cur.type === "dir" ? cur : K.seen.get(parent(path));
  if (dir) return folderRows(dir, path, ctx);
  if (K.failed.has(path) && parent(path)) return null;
  return empty("Reading…", { kind: "loading" });
}

function fileHead(d, ctx) {
  const name = projectOf(d.path), isMd = d.type === "md";
  return h("header", { class: "kb-h" },
    h("a", { class: "btn kb-back", href: open(parent(d.path)), "data-kind": "quiet", "data-size": "s" }, "‹ ", parent(d.path).split("/").pop() || "Knowledge"),
    h("h1", { class: "kb-t" }, d.name),
    h("p", { class: "meta" }, h("code", null, "~/" + d.path), d.size != null ? ` · ${bytes(d.size)}` : "", " · changed ", h("span", { title: stamp(d.t * 1e3) }, ago(d.t * 1e3))),
    isMd || (name && can("plans")) ? h("div", { class: "frow" },
      isMd ? button(K.source ? "Show formatted" : "Show source", () => { K.source = !K.source; ctx.update(); }, { kind: "quiet", size: "s" }) : null,
      name && can("plans") ? button("Edit plan", () => startEdit(name, ctx), { size: "s" }) : null) : null);
}
function fileView(d, line, ctx) {
  const box = h("article", { class: "kb-note kb-file", "data-key": "file:" + d.path }, fileHead(d, ctx),
    d.truncated ? h("p", { class: "meta", "data-key": "cut" }, `Showing the first ${bytes(d.text.length)} of ${bytes(d.size)}.`) : null,
    d.type === "md" || d.type === "text" ? docBody(d, line) : null,
    d.type === "image" ? h("img", { class: "kb-img", src: "/api/kb/raw?path=" + encodeURIComponent(d.path), alt: d.name }) : null,
    d.type === "pdf" ? h("p", { "data-key": "pdf" }, button("Download PDF", null, { href: "/api/kb/raw?path=" + encodeURIComponent(d.path) })) : null,
    d.type === "binary" ? empty("This is a binary file, so it stays on the machine and isn't shown here.") : null,
    related(d));
  return box;
}
function folderView(d, ctx) {
  const name = projectOf(d.path), plans = can("plans");
  return h("article", { class: "kb-note", "data-key": "dir:" + d.path },
    h("header", { class: "kb-h" }, h("h1", { class: "kb-t" }, d.name), h("p", { class: "meta" }, h("code", null, "~/" + d.path), ` · ${plural((d.entries || []).length, "item")}`),
      plans && (name || d.path === "projects") ? h("div", { class: "frow" },
        name ? button(d.doc && d.doc.name === "goals.md" ? "Edit plan" : "Write a plan", () => startEdit(name, ctx), { size: "s" }) : null,
        d.path === "projects" ? button("New project", () => { K.edit = { at: "projects", name: "", isNew: true, text: "", version: null }; ctx.update(); }, { size: "s" }) : null) : null),
    d.doc ? h("section", { class: "kb-doc", "data-key": "doc:" + d.doc.path }, h("a", { class: "meta", href: open(d.doc.path) }, d.doc.name), docBody(d.doc, 0))
      : empty("This folder has no README. Pick a file from the list."));
}

export default {
  id: "knowledge", label: "Knowledge", about: "notes, modules, scripts and files", tab: () => !!S.app.app && can("files"),
  enter(ctx) { if (can("files")) refresh(ctx); },
  poll: { every: 30000, run(ctx) { if (can("files")) refresh(ctx); } },
  render(ctx) {
    if (!can("files")) return h("div", { class: "page" }, empty("Reading files needs full mode here. Sign in again if your hour is up.", { kind: "page", title: "Knowledge" }));
    const { path, line } = parse(ctx.arg), d = path ? K.seen.get(path) : null, isFile = !!d && d.type !== "dir", editing = K.edit && K.edit.at === path;
    const side = h("nav", { class: "kb-side", "aria-label": "Files" },
      h("div", { class: "kb-top" },
        crumbs(path),
        input({ type: "search", value: K.q, placeholder: "Search names and text", "aria-label": "Search the knowledge base", autocomplete: "off", oninput: (e, n) => { K.q = n.value; search(ctx); ctx.update(); } })),
      h("div", { class: "kb-list" }, sideBody(path, ctx)));
    const main = h("div", { class: "kb-main", "data-key": "main" },
      editing ? h("div", { class: "kb-note" }, editor(ctx))
        : !path ? h("div", { class: "kb-note", "data-key": "home" }, h("header", { class: "kb-h" }, h("h1", { class: "kb-t" }, "Knowledge"), h("p", { class: "meta" }, "What the Hive has written down and built. Secrets are hidden, and nothing here can be changed except project plans.")),
          h("section", { class: "sec" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Changed lately")), K.home ? changedRows() : empty("Reading…", { kind: "loading" })))
        : K.failed.has(path) ? h("div", { class: "kb-note", "data-key": "gone" }, h("a", { class: "btn kb-back", href: open(parent(path)), "data-kind": "quiet", "data-size": "s" }, "‹ Back"), empty(K.failed.get(path) === "404" ? `~/${path} isn't here, or isn't shown here. Dotfiles and anything named like a key stay hidden.` : `Couldn't read ~/${path}.`))
        : !d ? h("div", { class: "kb-none", "data-key": "wait" }, empty("Reading…", { kind: "loading" }))
        : isFile ? fileView(d, line, ctx) : folderView(d, ctx));
    const top = document.getElementById("top");
    const want = `${path}::${line}`;
    if (d && K.shown !== want) {
      K.shown = want;
      requestAnimationFrame(() => {
        const box = document.querySelector(".kb-main"), mark = box && box.querySelector(".kb-code-at");
        if (box) { box.scrollTop = 0; if (mark) mark.scrollIntoView({ block: "center" }); }
        const pane = document.querySelector(`.kb-list [aria-current]`); if (pane && pane.scrollIntoView) pane.scrollIntoView({ block: "nearest" });
      });
    }
    return h("div", { class: "kb", "data-key": "kb", "data-in": isFile || editing || (path && K.failed.has(path)) ? "" : null, style: `--top-h:${top ? top.offsetHeight : 47}px` }, side, main);
  },
};
