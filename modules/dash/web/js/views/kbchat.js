// "Ask the Hive" on the Knowledge page: a composer that starts an agent on its own thread (kb-ask-*) and shows that thread right here.
// The agent is told to find files in the knowledge base and give their paths in backticks; every such path in a reply becomes a
// file chip that opens the file in Knowledge, and the reply's files are listed once under it. Backend unchanged: an ordinary post,
// an agent and a thread. Redesign 2026-11-04 (kb-ui): states on .kb-ask[data-state], chips, a files list, a textarea composer,
// and a collapsed one-line form for file pages (kbChat(ctx, { compact: true })).
import { h } from "../core/dom.js";
import { S, post, can } from "../core/store.js";
import { markdown } from "../ui/markdown.js";
import { button, icon } from "../ui/kit.js";
import { href, threadHref } from "../core/router.js";
import { clock } from "../core/format.js";

const BRIEF = "Knowledge-page question from the person (answer them on this thread; they read it inside the Knowledge page). Use `kb find WORDS` and the files under ~ to find what they ask for. Reply briefly. Name every file you point to by its home-relative path in backticks, like `kb/notes.md` or `projects/x/goals.md`, because each backticked path becomes a link that opens it in the knowledge base. Question: ";
const Q = { th: null, text: "", busy: false, err: "", open: false, sent: 0 };
const PATH = /^~?\/?((?:[\w.@+-]+\/)*[\w.@+-]+\.[A-Za-z0-9]{1,8}|(?:[\w.@+-]+\/)+[\w.@+-]+\/?)(?:::(\d+))?$/;

/** A file chip: icon, the name, and the folder it is in (muted, cut from the left when long). */
function chip(path, line, cur) {
  const p = path.replace(/\/$/, ""), dir = p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "", isDir = path.endsWith("/") || !/\.[A-Za-z0-9]{1,8}$/.test(p);
  const to = line ? `${p}::${line}` : p;
  return h("a", { class: "kb-ask-link", onclick: () => { Q.open = false; }, href: href("knowledge", to), title: "~/" + p + (line ? ":" + line : ""), "aria-current": cur === p ? "true" : null },
    icon(isDir ? "folder" : "file"),
    h("span", { class: "kb-ask-n" }, p.split("/").pop() + (line ? ":" + line : "")),
    dir ? h("span", { class: "kb-ask-d" }, "‎" + dir) : null);   // LRM keeps the rtl ellipsis from flipping slashes
}
/** Turn backticked paths into chips; return the node and the files found, in order, once each. */
function linked(node, cur) {
  const found = new Map();
  for (const c of node.querySelectorAll("code")) {
    if (c.closest("pre") || c.closest("a")) continue;
    const m = PATH.exec(c.textContent.trim().replace(/^~\/(?=\.)/, "~/")); if (!m || !/[\/.]/.test(m[1])) continue;
    c.replaceWith(chip(m[1], m[2], cur));
    const k = m[1].replace(/\/$/, ""); if (!found.has(k)) found.set(k, m[2] || null);
  }
  return { node, found };
}
function filesList(found) {
  if (found.size < 2) return null;   // one file: the chip in the text is enough
  return h("nav", { class: "kb-ask-files", "aria-label": "Files in this answer" },
    h("h3", null, `${found.size} files in this answer`),
    [...found].map(([p, line]) => h("a", { class: "kb-ask-file", onclick: () => { Q.open = false; }, href: href("knowledge", line ? `${p}::${line}` : p), "data-key": "f:" + p },
      icon(/\.[A-Za-z0-9]{1,8}$/.test(p) ? "file" : "folder"),
      h("b", null, p.split("/").pop(), h("small", null, p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "")),
      h("span", null, "Open"))));
}
async function send(ctx) {
  const q = Q.text.trim(); if (!q || Q.busy) return;
  Q.busy = true; Q.err = ""; ctx.update();
  try {
    const th = Q.th || "kb-ask-" + Date.now().toString(36);
    await post("/api/post", { text: Q.th ? q : "@new " + BRIEF + q, thread: th, files: [], model: Q.th ? null : "sonnet" });
    Q.th = th; Q.text = ""; Q.sent = Date.now(); Q.open = true;
    const el = document.querySelector(".kb-ask-in"); if (el) el.value = "";
  } catch (e) { Q.err = e.message; }
  Q.busy = false; ctx.update();
}
const secs = t => { const s = Math.max(0, Math.round((Date.now() - t) / 1000)); return s < 60 ? s + "s" : Math.floor(s / 60) + "m " + (s % 60) + "s"; };

/** opts.compact: a file page. Without a thread it renders nothing; with one, a one-line bar that opens the thread in place.
    opts.cur: the path open in Knowledge, so its chip is marked. */
export function kbChat(ctx, { compact = false, cur = "" } = {}) {
  if (!can("post")) return null;
  if (compact && !Q.th) return null;
  const d = Q.th && S.threads.get(Q.th);
  const posts = d ? d.posts : [];
  const running = !!(d && S.live && S.live.agents.some(a => a.thread === Q.th && a.alive));
  const last = posts[posts.length - 1], pending = Q.th && (!last || last.from === "person");
  const state = Q.err ? "error" : Q.busy ? "sending" : !Q.th ? "idle" : pending ? "working" : "answered";
  const answers = posts.filter(p => p.from !== "person"), nfiles = new Set();
  const log = Q.th ? h("div", { class: "kb-ask-log", "data-key": "log", "aria-live": "polite" },
    posts.map(p => {
      if (p.from === "person") return h("div", { class: "kb-ask-m", "data-who": "me", "data-key": p.id },
        h("p", null, String(p.text).replace(/^[^]*?Knowledge-page question from the person[^]*?Question: /, "")));
      const { node, found } = linked(markdown(p.text), cur); found.forEach((_, k) => nfiles.add(k));
      node.classList.add("kb-ask-body");
      return h("div", { class: "kb-ask-m", "data-who": "agent", "data-key": p.id },
        h("div", { class: "kb-ask-by" }, h("span", { class: "kb-ask-av" }, icon("hive")), h("b", null, p.from), h("time", null, clock(p.t))),
        node, filesList(found));
    }),
    pending ? h("div", { class: "kb-ask-wait", "data-key": "wait" },
      h("span", { class: "kb-ask-av" }, icon("hive")),
      h("span", { class: "kb-ask-shim" }, running ? "Searching the knowledge base" : "Starting an agent"),
      Q.sent ? h("span", { class: "meta" }, secs(Q.sent)) : null) : null) : null;
  const acts = state === "answered" ? h("div", { class: "kb-ask-acts", "data-key": "acts" },
    button("New question", () => { Q.th = null; Q.open = false; ctx.update(); requestAnimationFrame(() => document.querySelector(".kb-ask-in")?.focus()); }, { size: "s", kind: "quiet", icon: "plus" }),
    button("Open as a conversation", null, { size: "s", kind: "quiet", href: threadHref(Q.th) })) : null;
  const ta = h("textarea", { class: "kb-ask-in", rows: "1", value: Q.text, placeholder: Q.th ? "Ask a follow-up…" : "Ask the Hive to find the notes and files on…", "aria-label": "Ask the Hive",
    oninput: (e, n) => { Q.text = n.value; n.form.querySelector(".kb-ask-send").disabled = !n.value.trim() || Q.busy; },
    onkeydown: (e, n) => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(ctx); } } });
  const head = h("h2", { class: "kb-ask-h", "data-key": "h", onclick: compact ? () => { Q.open = !Q.open; ctx.update(); } : null },
    icon("hive"), "Ask the Hive",
    h("span", { class: "meta" }, state === "working" ? "· working" : state === "answered" ? `· answered${nfiles.size ? " · " + nfiles.size + " files" : ""}` : ""),
    compact ? button(Q.open ? "Hide" : "Show", null, { size: "s", kind: "quiet", cls: "kb-ask-x" })
      : Q.th ? button("Close", () => { Q.th = null; ctx.update(); }, { kind: "icon", icon: "close", size: "s", cls: "kb-ask-x" }) : null);
  return h("section", { class: "kb-ask", "data-key": "ask", "data-state": state, "data-collapsed": compact && !Q.open ? "" : null },
    head, log, acts,
    h("form", { class: "kb-ask-f", onsubmit: e => { e.preventDefault(); send(ctx); } }, ta,
      h("span", { class: "kb-ask-tip" }, icon("hive"), " An agent searches the notes and links the files it finds"),
      h("button", { type: "submit", class: "kb-ask-send", "aria-label": "Ask", title: "Ask (Enter)", disabled: !Q.text.trim() || Q.busy }, icon("arrow-up"))),
    Q.err ? h("p", { class: "kb-ask-err", role: "alert" }, "Couldn't send: " + Q.err) : null);
}
