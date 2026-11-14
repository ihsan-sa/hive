// Knowledge: everything the Hive keeps, to browse, search and read: notes, modules, scripts, core, projects and post files.
// #knowledge/<home-relative path> opens a folder or a file, and <path>::<line> jumps to a line. The server decides what can be seen and
// redacts all text; this view only reads, except a project's goals.md, saved through /api/plan with the plans right. On a phone the list and a file take turns.
import { h } from "../core/dom.js";
import { S, can, getPref, jget, post, setPref } from "../core/store.js";
import { ago, plural, stamp } from "../core/format.js";
import { href, threadHref } from "../core/router.js";
import { labelCodes, threadTitle } from "../core/model.js";
import { button, empty, skeleton, waiting, field, input, list, needFull, copyPress } from "../ui/kit.js";
import { icon } from "../ui/icons.js";
import { markdown } from "../ui/markdown.js";
import { markWords } from "../ui/message.js";
import { kbChat } from "./kbchat.js";

const K = { map: null, mapAt: 0, topics: new Set(), home: null, seen: new Map(), failed: new Map(), q: "", res: null, timer: 0, worktrees: false, limit: 300, source: false, edit: null, shown: null, memo: {}, mentions: { key: "", v: [] } };
const open = (path, line) => href("knowledge", line ? `${path}::${line}` : path);
const parse = arg => { const m = /^(.*)::(\d+)$/.exec(arg || ""); return m ? { path: m[1], line: +m[2] } : { path: arg || "", line: 0 }; };
const parent = p => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "");
const bytes = n => (n == null ? "" : n < 1024 ? n + " B" : n < 1048576 ? Math.round(n / 1024) + " KB" : (n / 1048576).toFixed(1) + " MB");

async function fetchPath(path, ctx) {
  try { const r = await jget("/api/kb/open?path=" + encodeURIComponent(path)); if (!r.path) throw Error("404"); K.seen.set(path, r); K.failed.delete(path); }
  catch (e) { if (e.status === 404 || e.message === "404" || !K.seen.has(path)) { K.seen.delete(path); K.failed.set(path, e.status === 404 || e.message === "404" ? "404" : e.message); } }   // a blip while reading leaves the page as it was
  ctx.update();
}
async function loadHome(ctx) { try { K.home = await jget("/api/kb/home"); } catch (e) { K.home = K.home || { failed: true }; } ctx.update(); }
// The notes map: every note under ~/kb with its topic, size, age and links. The server keeps it a minute; so does this page.
async function loadMap(ctx) {
  if (Date.now() - K.mapAt < 60000) return;
  K.mapAt = Date.now();
  try { const m = await jget("/api/kb/map"); K.map = { ...m, notes: m.notes || [], by: new Map((m.notes || []).map(n => [n.path, n])) }; } catch (e) { K.mapAt = 0; K.map = K.map || { notes: [], sources: [], by: new Map(), failed: e.message }; }
  ctx.update();
}
// Recently opened, on this device only.
const RECENT = "kb.recent";
function remember(d) {
  const was = getPref(RECENT, []).filter(r => r.path !== d.path);
  const title = d.type === "md" ? (K.map && K.map.by.get(d.path)?.title) || d.name : d.name;
  try { setPref(RECENT, [{ path: d.path, title, at: Date.now() }, ...was].slice(0, 8)); } catch (e) { /* storage full or off: nothing to keep */ }
}
function refresh(ctx) {
  const { path } = parse(ctx.arg);
  if (!path || path.startsWith("kb/")) loadMap(ctx);
  if (!path) return loadHome(ctx);
  fetchPath(path, ctx);
  const up = parent(path), cur = K.seen.get(path);
  if (up && !K.seen.has(up) && (!cur || cur.type !== "dir")) fetchPath(up, ctx);
  for (let a = up; a; a = parent(a)) if (a !== up && !K.seen.has(a)) fetchPath(a, ctx);   // the sidebar tree shows every folder above the open one
}
// One search box: it finds file names and searches inside them (ranked passages and lines).
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
const noteLink = p => { const n = K.map && K.map.by.get(p); return h("a", { class: "kb-link", href: open(p), "data-key": p }, h("b", null, (n && n.title) || p.split("/").pop()), h("span", { class: "meta" }, p)); };
function related(d) {
  const posts = mentions(d.path), note = K.map && K.map.by.get(d.path), out = note ? note.links : [], back = note ? note.back : [];
  const linked = (d.linked || []).filter(p => !back.includes(p));
  if (!posts.length && !linked.length && !out.length && !back.length) return null;
  return h("div", { class: "kb-rel" },
    out.length ? h("section", { class: "sec", "data-key": "out" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Links to"), h("span", { class: "sec-n" }, plural(out.length, "note"))), h("div", { class: "kb-links" }, out.map(noteLink))) : null,
    back.length ? h("section", { class: "sec", "data-key": "back" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Linked from"), h("span", { class: "sec-n" }, plural(back.length, "note"))), h("div", { class: "kb-links" }, back.map(noteLink))) : null,
    linked.length ? h("section", { class: "sec", "data-key": "linked" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Named in"), h("span", { class: "sec-n" }, plural(linked.length, "note"))),
      h("div", { class: "kb-links" }, linked.map(p => h("a", { class: "kb-link", href: open(p), "data-key": p }, h("b", null, p.split("/").pop()), h("span", { class: "meta" }, p))))) : null,
    posts.length ? h("section", { class: "sec", "data-key": "threads" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, S.app.app ? "Conversations" : "Threads"), h("span", { class: "sec-n" }, "latest")),
      h("div", { class: "kb-links" }, posts.map(p => h("a", { class: "kb-link", href: threadHref(p.thread), "data-key": p.id }, h("b", null, threadTitle(p.thread)), h("span", { class: "meta" }, p.from))))) : null);
}

// A project's plan. The text to edit comes unredacted from /api/plans, so a save can't write "[hidden]" over a real line.
const projectOf = path => { const m = /^projects\/([^/]+)(\/goals\.md)?$/.exec(path); return m ? m[1] : null; };
async function startEdit(name, ctx) {
  try {
    const all = await jget("/api/plans"), p = all.find(x => x.name === name) || {};
    K.edit = { at: parse(ctx.arg).path, name, isNew: false, text: p.text || "", version: p.version || null, say: null };
  } catch (e) { K.edit = { at: parse(ctx.arg).path, name, text: "", failed: `Couldn't open this to edit: ${e.message}` }; }
  ctx.update();
}
async function saveEdit(start, ctx) {
  const e = K.edit, name = e.isNew ? e.name.trim() : e.name;
  const wasNew = e.isNew;
  if (e.saving) return;   // a double click would post twice, and with "Save and start" start a second agent
  if (!name) { e.say = "Give the project a name first."; e.tone = "bad"; ctx.update(); return; }
  e.saving = true; e.say = ""; ctx.update();
  try {
    const r = await post("/api/plan", { name, text: e.text, version: e.version, start });
    Object.assign(e, { name, version: r.version, isNew: false, say: start ? "Saved, and an agent is starting on it." : "Saved. Nothing was started.", tone: "good" });
    K.seen.delete(`projects/${name}`); K.seen.delete(`projects/${name}/goals.md`); K.seen.delete("projects");
    if (wasNew) { e.at = `projects/${name}`; location.hash = open(e.at); return; }   // the new project's own page, with the editor still open
    refresh(ctx);
  } catch (err) { e.say = err.message; e.tone = "bad"; ctx.update(); } finally { e.saving = false; }
}
function editor(ctx) {
  const e = K.edit;
  if (e.failed) return empty(e.failed, { kind: "error" });
  return h("section", { class: "kb-edit", "data-key": "edit" },
    h("h2", { class: "kb-t" }, e.isNew ? "New project" : `Plan for ${e.name}`),
    e.isNew ? field("Name", input({ value: e.name, autocomplete: "off", oninput: (ev, n) => { e.name = n.value; } })) : null,
    h("textarea", { class: "input", rows: 20, "aria-label": "Plan", value: e.text, oninput: (ev, n) => { e.text = n.value; } }),
    h("div", { class: "frow" }, button("Save", () => saveEdit(false, ctx), { kind: "primary", busy: e.saving }), button("Save and start an agent", () => saveEdit(true, ctx), { disabled: e.saving }),
      button("Close", () => { K.edit = null; ctx.update(); }, { kind: "quiet" }),
      e.say ? h("p", { class: "fmsg", "data-tone": e.tone, role: "status" }, e.say) : null));
}

function crumbs(path) {
  const parts = path ? path.split("/") : [];
  if (!parts.length) return null;   // at the top the page's own title says where you are (the person, 2026-11-04, post 110401017e29)
  return h("nav", { class: "kb-crumbs", "aria-label": "Where you are" },
    h("a", { href: href("knowledge"), "data-key": "home" }, "Knowledge"),
    parts.flatMap((p, i) => [h("span", { "aria-hidden": "true", "data-key": "s" + i }, "/"),
      i === parts.length - 1 ? h("span", { "aria-current": "page", "data-key": "c" + i }, p) : h("a", { href: open(parts.slice(0, i + 1).join("/")), "data-key": "c" + i }, p)]));
}

const words = () => (K.res && K.res.ranked && K.res.ranked.words) || K.q.toLowerCase().split(/\s+/).filter(Boolean);
const passageHref = x => (x.thread ? threadHref(x.thread) : open(x.path, x.line));
const passageTitle = x => (x.thread ? threadTitle(x.thread) : x.title || x.path.split("/").pop());
const passagePlace = x => (x.thread ? "board" : x.source === "kb" ? parent(x.path).replace(/^kb\/?/, "notes/").replace(/\/$/, "") || "notes" : parent(x.path));
const passages = () => (K.res && K.res.ranked && K.res.ranked.passages) || [];
// Ranked matches as full cards for the main pane: the snippet shows why each one came up.
function resultsView() {
  const r = K.res, ws = words(), best = passages();
  if (!r) return h("div", { class: "kb-note", "data-key": "results" }, empty("Searching…", { kind: "loading" }));
  return h("div", { class: "kb-note", "data-key": "results" },
    h("header", { class: "kb-h" }, h("h1", { class: "kb-t" }, `Results for “${K.q.trim()}”`),
      h("p", { class: "meta" }, r.ranked ? `${plural(best.length, "best match", "best matches")} of ${plural(r.ranked.total, "passage")}${S.app.app ? "" : ", ranked the way agents' kb find ranks them"}` : "Plain matches: the ranked index isn't ready here.",
        r.ranked && r.ranked.spelling ? ` · also tried ${r.ranked.spelling}` : "")),
    best.length ? h("section", { class: "sec", "data-key": "best" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Best matches")),
      h("ol", { class: "kb-hits" }, best.map(x => h("li", { "data-key": `${x.path}:${x.line}` }, h("a", { class: "kb-hit", href: passageHref(x) },
        h("span", { class: "kb-hit-t" }, h("b", null, markWords(passageTitle(x), ws)), h("span", { class: "kb-src-tag", "data-src": x.source }, x.source)),
        h("span", { class: "kb-hit-x" }, markWords(x.text, ws)),
        h("span", { class: "meta" }, x.thread ? `post ${x.post}` : `~/${x.path}${x.line ? ":" + x.line : ""}`, x.date ? ` · ${x.date}` : "")))))) : null,
    r.names.length || r.hits.length ? h("section", { class: "sec", "data-key": "plain" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Exact names and lines")),
      h("div", { class: "kb-plain" }, plainRows(r, ws))) : null,
    !best.length && !r.names.length && !r.hits.length && !r.failed ? empty("Nothing found. Fewer or other words often help.") : null);
}
function plainRows(r, ws) {
  const names = list(r.names.map(n => ({ key: "n:" + n.path, href: open(n.path), title: markWords(n.path.split("/").pop(), ws), text: parent(n.path), meta: ago(n.t * 1e3).replace(" ago", "") })), { dense: true, limit: 30 });
  const hits = list(r.hits.map(x => ({ key: `h:${x.path}:${x.line}`, href: open(x.path, x.line), title: x.path, text: markWords(x.text.trim(), ws), meta: String(x.line) })), { dense: true, limit: 100 });
  return [r.names.length ? h("h2", { class: "kb-sub", "data-key": "nh" }, "File names") : null, r.names.length ? names : null,
    r.hits.length ? h("h2", { class: "kb-sub", "data-key": "hh" }, r.more ? "Inside files, first matches" : "Inside files") : null, r.hits.length ? hits : null];
}
function searchRows() {
  const r = K.res, ws = words(), best = passages();
  const top = list(best.map(x => ({ key: `r:${x.path}:${x.line}`, href: passageHref(x), title: markWords(passageTitle(x), ws), text: markWords(x.text, ws), meta: passagePlace(x) })), { dense: true, limit: 12 });
  return [
    r.failed ? empty(r.failed, { kind: "error" }) : null,
    r.busy ? h("p", { class: "kb-sub", "data-key": "busy" }, "Other searches are running. These are names only.") : null,
    best.length ? h("h2", { class: "kb-sub", "data-key": "bh" }, "Best matches") : null, best.length ? top : null,
    ...plainRows(r, ws),
    !best.length && !r.names.length && !r.hits.length && !r.failed ? empty("Nothing found.") : null,
  ];
}
function folderRows(dir, cur, ctx) {
  const all = dir.entries || [], hidden = all.length - all.filter(e => !e.worktree).length;
  // Nested tree: from the top folder above the open page, each folder on the way down opens in place.
  const here = dir.path || parent(cur), top = here.split("/")[0], root = K.seen.get(top);
  const rows = [];
  const walk = (d, depth) => (d.entries || []).filter(e => K.worktrees || !e.worktree).forEach(e => {
    const onPath = e.dir && (cur === e.path || cur.startsWith(e.path + "/")) || e.dir && here === e.path;
    rows.push({ key: e.path, href: open(e.path), current: cur === e.path, depth, lead: icon(e.dir ? "folder" : "file"), title: e.name + (e.dir ? "/" : ""), text: e.title || (e.worktree ? "worktree" : ""), meta: e.dir ? age(e.t) : bytes(e.size) });
    const kids = onPath && K.seen.get(e.path);
    if (kids && kids.type === "dir") walk(kids, depth + 1);
  });
  walk(root && root.type === "dir" && top !== here ? root : dir, 0);
  return [
    list(rows, { empty: "Nothing to show here.", dense: true, limit: K.limit, onMore: () => { K.limit += 500; ctx.update(); } }),
    hidden ? button(K.worktrees ? "Hide worktrees" : `Show ${plural(hidden, "worktree")}`, () => { K.worktrees = !K.worktrees; ctx.update(); }, { kind: "quiet", size: "s", cls: "kb-more" }) : null,
    dir.more ? h("p", { class: "kb-sub", "data-key": "cut" }, `Showing the first ${all.length}.`) : null,
  ];
}
const age = t => ago(t * 1e3).replace(" ago", "");
function recentRows() {
  const rows = getPref(RECENT, []);
  return rows.length ? list(rows.map(r => ({ key: "o:" + r.path, href: open(r.path), lead: icon("file"), title: r.title, text: parent(r.path), meta: ago(r.at).replace(" ago", "") })), { dense: true }) : null;
}
const TOPIC = { general: "Other notes", history: "History of the hives", _dropped: "Dropped" };
const topicName = (t, folder) => TOPIC[t] || (folder ? t + "/" : (t[0].toUpperCase() + t.slice(1)).replace(/-+$/, "") + " notes");
// Notes grouped by topic (their folder, or a name prefix two or more share). Each row: a bar for its size, shaded for how recent it is.
function mapView(ctx) {
  const m = K.map;
  if (!m) return skeleton("topics");
  if (!m.notes.length) return m.failed ? empty(`Couldn't read the notes: ${m.failed}`, { kind: "error" }) : empty("No notes yet.");
  const groups = new Map(), now = Date.now() / 1e3, big = Math.log(Math.max(...m.notes.map(n => n.size), 2));
  for (const n of m.notes) { if (!groups.has(n.topic)) groups.set(n.topic, []); groups.get(n.topic).push(n); }
  const order = [...groups].sort((a, b) => (a[0] === "general") - (b[0] === "general") || b[1].length - a[1].length || a[0].localeCompare(b[0]));
  return h("div", { class: "kb-map" }, order.map(([t, ns]) => {
    ns.sort((a, b) => b.t - a.t);
    const all = K.topics.has(t), shown = all ? ns : ns.slice(0, 6), folder = ns[0].path.split("/").length > 2;
    return h("section", { class: "kb-topic", "data-key": "t:" + t },
      h("header", { class: "kb-topic-h" }, folder ? h("a", { href: open("kb/" + t) }, topicName(t, true)) : h("span", null, topicName(t)), h("span", { class: "meta" }, `${ns.length} · ${age(ns[0].t)}`)),
      h("ul", { class: "kb-notes" }, shown.map(n => {
        const days = (now - n.t) / 86400, fresh = days < 2 ? 3 : days < 7 ? 2 : days < 30 ? 1 : 0;
        return h("li", { "data-key": n.path }, h("a", { class: "kb-nr", "data-fresh": days < 2 ? "" : null, href: open(n.path), title: `~/${n.path} · ${bytes(n.size)} · changed ${age(n.t)} ago` + (n.back.length ? ` · linked from ${n.back.length}` : "") },
          h("span", { class: "kb-nr-t" }, labelCodes(n.title) || n.path.split("/").pop()),
          h("span", { class: "meta" }, age(n.t))));
      })),
      ns.length > 6 ? button(all ? "Show fewer" : `Show all ${ns.length}`, () => { all ? K.topics.delete(t) : K.topics.add(t); ctx.update(); }, { kind: "quiet", size: "s", cls: "kb-more" }) : null);
  }));
}
function hubRows() {
  const hubs = (K.map ? K.map.notes : []).filter(n => n.back.length + n.links.length >= 2).sort((a, b) => b.back.length - a.back.length || b.links.length - a.links.length).slice(0, 8);
  return hubs.length ? list(hubs.map(n => ({ key: "hub:" + n.path, href: open(n.path), title: n.title || n.path, text: parent(n.path), meta: `${plural(n.back.length, "note")} link here · links to ${n.links.length}` })), { dense: true }) : null;
}
const SOURCE = { kb: "Your notes", board: "Board posts", projects: "Projects", modules: "Module notes", core: "Core", docs: "Old docs", dispatch: "Dispatch", slack: "Slack", journals: "Journals", boards: "Old boards", mail: "Mail", box: "Box" };
function sourcesView() {
  const ss = (K.map && K.map.sources) || [];
  if (!ss.length) return null;
  return h("ul", { class: "kb-cover" }, ss.map(s => h("li", { "data-key": s.source }, h("b", null, s.files.toLocaleString()), h("span", null, SOURCE[s.source] || s.source))));
}
// A card names its place in words, not as a folder path (punch list item 4): kb/ is "Your notes", kb/history/ is "History of the hives".
const placeName = p => { const d = parent(p), t = d.split("/").pop(); if (!d || d === "kb") return "Your notes"; return TOPIC[t] || (t[0].toUpperCase() + t.slice(1)).replace(/[-_]+/g, " "); };
// R7: before anything is picked, the notes changed last as cards: folder · when, the title, and how it sits among the others.
// TODO(server): /api/kb/map has no author or first line yet; "who" and the first line join the card once it sends them (n.by, n.first).
function latelyCards() {
  if (!K.map) return h("section", { class: "sec kb-lately", "data-key": "lately" }, skeleton("cards"));
  const ns = K.map && !K.map.failed ? [...K.map.notes].sort((a, b) => b.t - a.t).slice(0, 6) : [];
  if (!ns.length) return null;
  const week = K.map.notes.filter(n => Date.now() / 1e3 - n.t < 7 * 86400).length;
  return h("section", { class: "sec kb-lately", "data-key": "lately" },
    h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Lately learned"), h("span", { class: "sec-n" }, week ? `${plural(week, "note")} changed this week` : "")),
    h("div", { class: "kb-cards" }, ns.map(n => h("a", { class: "card kb-card", href: open(n.path), "data-key": n.path, title: "~/" + n.path },
      h("span", { class: "meta" }, [placeName(n.path), age(n.t) + " ago", n.by].filter(Boolean).join(" · ")),
      h("b", { class: "kb-card-t" }, labelCodes(n.title) || n.path.split("/").pop()),
      h("span", { class: "kb-card-x" }, n.first || (n.back.length ? `${plural(n.back.length, "note")} link here` : "No other note links here yet"))))));
}
// The home page below its title: what you opened, the map, the hubs, what changed, what search covers. On a phone it sits under the shelves.
const homeSections = ctx => [
  latelyCards(),
  recentRows() ? h("section", { class: "sec", "data-key": "opened" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Recently opened"), h("span", { class: "sec-n" }, "on this device")), recentRows()) : null,
  h("section", { class: "sec", "data-key": "map" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Notes by topic"), h("span", { class: "sec-n" }, K.map && !K.map.failed ? plural(K.map.notes.length, "note") : ""), button("Over time", null, { kind: "quiet", size: "s", icon: "clock", href: href("kbtime"), cls: "kb-time", title: "How the notes grew, linked and changed, from the backup history" })), mapView(ctx)),
  hubRows() ? h("section", { class: "sec", "data-key": "hubs" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Most connected notes"), h("span", { class: "sec-n" }, "how many notes link to each, and it to others")), hubRows()) : null,
  h("section", { class: "sec", "data-key": "changed" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Changed lately")), K.home ? changedRows() : skeleton(3)),
  sourcesView() ? h("section", { class: "sec", "data-key": "cover" }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "What search covers"), h("span", { class: "sec-n" }, "files and posts in the index")), sourcesView()) : null];
// The app look lists what the Hive wrote down, not its own workings: the core and modules folders, which the developer look keeps. A user's own code under projects/ stays listed.
const hiveCode = p => !!S.app.app && /^(core|modules)(\/|$)/.test(p);
function changedRows() {
  if (K.home.failed) return empty("Couldn't read what changed lately. It keeps trying.", { kind: "error" });
  return list((K.home.recent || []).filter(f => !hiveCode(f.path)).map(f => ({ key: f.path, href: open(f.path), title: f.title || f.name, text: parent(f.path), meta: ago(f.t * 1e3).replace(" ago", "") })), { empty: "Nothing yet." });
}
function sideBody(path, ctx) {
  if (!path) {
    if (!K.home || K.home.failed) return waiting(K.home && K.home.failed, "Reading…", "your notes", "side");
    return [list((K.home.shelves || []).filter(s => !hiveCode(s.path)).map(s => ({ key: s.path, href: open(s.path), lead: icon("folder"), title: s.label, text: s.about, meta: String(s.n) })), { empty: "Nothing here yet." }),
      h("div", { class: "kb-phone kb-home", "data-key": "recent" }, homeSections(ctx))];
  }
  const cur = K.seen.get(path), dir = cur && cur.type === "dir" ? cur : K.seen.get(parent(path));
  if (dir) return folderRows(dir, path, ctx);
  if (K.failed.has(path) && parent(path)) return null;
  return empty("Reading…", { kind: "loading" });
}

function fileHead(d, ctx) {
  const name = projectOf(d.path), isMd = d.type === "md";
  return h("header", { class: "kb-h" },
    h("a", { class: "btn kb-back", href: open(parent(d.path)), "data-kind": "quiet", "data-size": "s" }, icon("chevron-left"), parent(d.path).split("/").pop() || "Knowledge"),
    h("div", { class: "kb-hcrumbs" }, crumbs(d.path)),
    h("h1", { class: "kb-t" }, d.name),
    h("p", { class: "meta" }, h("code", null, "~/" + d.path), d.size != null ? ` · ${bytes(d.size)}` : "", " · changed ", h("span", { title: stamp(d.t * 1e3) }, ago(d.t * 1e3))),
    h("div", { class: "frow" },
      button("All notes", null, { kind: "quiet", size: "s", icon: "chevron-left", href: href("knowledge"), cls: "kb-all" }),
      button("Copy path", (e, b) => copyPress(b, "~/" + d.path), { kind: "quiet", size: "s" }),
      d.path.startsWith("kb/") && isMd ? button("History", null, { kind: "quiet", size: "s", href: href("kbtime", d.path), title: "How this note changed, from the backup history" }) : null,
      isMd ? button(K.source ? "Formatted" : "Source", () => { K.source = !K.source; ctx.update(); }, { kind: "quiet", size: "s" }) : null,
      name && can("plans") ? button("Edit plan", () => startEdit(name, ctx), { size: "s" }) : null));
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

// "/" or ⌘K / Ctrl+K goes to the search box. Only while the Knowledge page is up and nothing is being typed into.
addEventListener("keydown", e => {
  if (!document.querySelector(".kb") || e.altKey) return;
  const k = (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k", slash = e.key === "/" && !e.metaKey && !e.ctrlKey;
  if (!k && !slash) return;
  const t = e.target, typing = t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));
  if (slash && typing) return;
  const box = [...document.querySelectorAll("input[data-kb-all]")].find(i => i.offsetParent);
  if (box) { e.preventDefault(); box.focus(); box.select(); }
});
export default {
  title: a => { const { path } = parse(a); if (!path) return null; let p = path; try { p = decodeURIComponent(path); } catch (e) {} return p.replace(/\/+$/, "").split("/").pop(); },   // the tab's title (app.js where): the file or folder open, by its path
  id: "knowledge", label: "Knowledge", about: "notes, modules, scripts and files", tab: () => !!S.app.app && can("files"),
  enter(ctx) { if (can("files")) refresh(ctx); },
  poll: { every: 30000, run(ctx) { if (can("files")) refresh(ctx); } },
  render(ctx) {
    if (!can("files")) return needFull("Reading your files", "Knowledge");
    const { path, line } = parse(ctx.arg), d = path ? K.seen.get(path) : null, isFile = !!d && d.type !== "dir", editing = K.edit && K.edit.at === path;
    const find = (kbd = "/") => h("label", { class: "kb-q" }, icon("search"), input({ type: "search", value: K.q, placeholder: "Search everything: notes, files, posts", "aria-label": "Search the knowledge base", "data-kb-all": "", autocomplete: "off", oninput: (e, n) => { K.q = n.value; K.qPath = path; search(ctx); ctx.update(); } }), kbd ? h("kbd", null, kbd) : null);   // the phone has no "/" key to show
    const searching = K.q.trim().length >= 2 && K.qPath === path;
    const side = h("nav", { class: "kb-side", "aria-label": "Files" },
      h("div", { class: "kb-top" },
        crumbs(path)),
      h("div", { class: "kb-phone kb-find", "data-key": "pfind" }, find("")),
      !path && !searching && matchMedia("(max-width: 640px)").matches ? h("div", { class: "kb-phone kb-ask-p", "data-key": "pask" }, kbChat(ctx)) : null,
      searching ? h("div", { class: "kb-phone kb-phone-results", "data-key": "pres" }, resultsView()) : null,
      h("div", { class: "kb-list", "data-hide-phone": searching ? "" : null }, sideBody(path, ctx)));
    const box = h("div", { class: "kb-find kb-wide", "data-key": "find" }, find());
    const main = h("div", { class: "kb-main", "data-key": "main" }, editing ? null : box,
      editing ? h("div", { class: "kb-note" }, editor(ctx))
        : K.q.trim().length >= 2 && K.qPath === path ? resultsView()
        : !path ? h("div", { class: "kb-note", "data-key": "home" }, h("header", { class: "kb-h kb-home-h" }, h("h1", { class: "kb-t" }, "Knowledge"), h("p", { class: "meta" }, "What the Hive has written down and built. Read-only, except project plans.")),
          kbChat(ctx), homeSections(ctx))
        : K.failed.has(path) ? h("div", { class: "kb-note", "data-key": "gone" }, h("a", { class: "btn kb-back", href: open(parent(path)), "data-kind": "quiet", "data-size": "s" }, icon("chevron-left"), parent(path).split("/").pop() || "Knowledge"), empty(K.failed.get(path) === "404" ? `~/${path} isn't here, or isn't shown here. Dotfiles and anything named like a key stay hidden.` : `Couldn't read ~/${path}.`))
        : !d ? h("div", { class: "kb-none", "data-key": "wait" }, empty("Reading…", { kind: "loading" }))
        : [kbChat(ctx, { compact: true, cur: path }), isFile ? fileView(d, line, ctx) : folderView(d, ctx)]);
    const want = `${path}::${line}`;
    if (d && K.shown !== want) {
      K.shown = want;
      if (isFile) remember(d);
      requestAnimationFrame(() => {
        const box = document.querySelector(".kb-main"), mark = box && box.querySelector(".kb-code-at");
        if (box) { box.scrollTop = 0; if (mark) mark.scrollIntoView({ block: "center" }); }
        const pane = document.querySelector(`.kb-list [aria-current]`); if (pane && pane.scrollIntoView) pane.scrollIntoView({ block: "nearest" });
      });
    }
    return h("div", { class: "kb", "data-key": "kb", "data-in": isFile || editing || (path && K.failed.has(path)) ? "" : null }, side, main);
  },
};
