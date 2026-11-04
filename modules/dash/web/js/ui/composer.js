// The composer: write a message, attach files (button, paste or drop), name an agent with @, reply, send.
// Up and Down walk the person's past messages like a shell's history; Tab finishes what they've started typing from them.
// App mode only (can("post")). Everything lives here, outside the render, so polls never touch what the person is doing:
// - a draft per thread (text, reply, chosen recipient, name), kept in localStorage, so it survives leaving and reloading;
// - OUT, messages on their way: they show at once as "Sending…", settle when the board has them, and a failed one keeps
//   its text and offers Retry, Edit and Discard. The board is append-only, so nothing here suggests unsending a posted one.
import { uploadFile } from "../core/upload.js";
import { h } from "../core/dom.js";
import { S, jget, post, poll, loadApp } from "../core/store.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { go } from "../core/router.js";
import { isAgent, agentStatus, threadPeople, partner, displayText, typedText } from "../core/model.js";
import { short, mdPlain, now } from "../core/format.js";
import { button, input, statusDot } from "./kit.js";

const CP = { update: null, th: undefined, focus: false, ac: null, menu: false, drag: false, sig: "", hist: null };
const refresh = () => CP.update && CP.update();
const IMGNAME = /\.(png|jpe?g|gif|webp)$/i;
const size = n => (n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");

// ---- drafts ----
const D = new Map();
const dkey = th => "draft:" + (th || "(new)");
export function draft(th) {
  const k = dkey(th); let d = D.get(k);
  if (!d) {
    let v = {}; try { v = JSON.parse(localStorage.getItem(k) || "{}"); } catch (e) {}
    d = { th: th || null, text: v.text || "", replyId: v.reply || null, reply: null, to: v.to || null, name: v.name || "", naming: !!v.name, files: [], err: "" };
    D.set(k, d);
  }
  if (!d.reply && d.replyId && S.byId[d.replyId]) d.reply = S.byId[d.replyId];
  return d;
}
function keep(d) {
  const k = dkey(d.th), rid = d.reply ? d.reply.id : d.replyId;
  try { if (d.text || rid || d.to || d.name) localStorage.setItem(k, JSON.stringify({ text: d.text, reply: rid, to: d.to, name: d.name })); else localStorage.removeItem(k); } catch (e) {}
}
const field = th => document.querySelector(`.cp[data-key="${CSS.escape("cp:" + (th || "(new)"))}"] .cp-t`);

/** Start a reply to a post from anywhere: opens its thread and puts the cursor in the composer. */
export function replyTo(p) {
  const d = draft(p.th); d.reply = p; d.replyId = p.id; d.to = null; keep(d);
  CP.focus = true; go("comms", p.th); refresh();
}
/** Esc: close the @ list or the To menu, else cancel a reply. True if it did something. */
export function cancelReply() {
  if (CP.ac) { CP.ac = null; refresh(); return true; }
  if (CP.menu) { CP.menu = false; refresh(); return true; }
  const d = CP.th !== undefined && D.get(dkey(CP.th));
  if (!d || !(d.reply || d.replyId)) return false;
  d.reply = null; d.replyId = null; keep(d); refresh(); return true;
}
export const focusComposer = () => { CP.focus = true; refresh(); };
/** Add quoted text to a thread's draft and put the cursor there. It only adds text: the person still presses Send. */
export function quoteInto(th, text) {
  const d = draft(th), f = field(th), had = f ? f.value : d.text;
  d.text = had + (had && !had.endsWith("\n\n") ? (had.endsWith("\n") ? "\n" : "\n\n") : "") + text;
  if (f) f.value = d.text;
  keep(d); CP.focus = true; refresh();
}

// ---- who it goes to ----
// The server's rule (person_post in dash.py, /api/target): a plain message wakes whoever posted last in the thread.
// The person thinks of it as the agent they've been talking to, so that is the default here, and when the two differ
// the message is sent with that name in front. Typing @name, replying, or choosing in the To menu overrides it.
const TG = { key: null, v: {} };
function serverWho(th) {
  const k = th + "|" + S.ver;
  if (TG.key !== k) { TG.key = k; jget("/api/target?thread=" + encodeURIComponent(th)).then(t => { TG.v[th] = t; refresh(); }).catch(() => {}); }
  return TG.v[th] || null;
}
function named(text) {
  for (const m of text.matchAll(/(?:^|\s)@([\w.-]+)/g)) if (m[1] === "new" || isAgent(m[1])) return m[1];
  return null;
}
/** {who: name | "new" | null, why: reply | named | chosen | partner | last | first, server: who the server would wake} */
export function recipient(th) {
  if (!th) return { who: "new", why: "first" };
  const d = draft(th), s = serverWho(th), sv = s && s.who;
  if (d.reply) return { who: isAgent(d.reply.from) ? d.reply.from : sv, why: "reply", server: sv };
  const m = named(d.text); if (m) return { who: m, why: "named", server: sv };
  if (d.to) return { who: d.to, why: "chosen", server: sv };
  const p = partner(th);
  if (p) return { who: p, why: sv && sv !== p ? "partner" : "last", server: sv };
  // The target is fetched when the board changes, and a run starting doesn't change the board: an agent already running here still counts.
  const run = !sv && S.live && S.live.agents.find(a => a.thread === th && a.alive && a.name);
  return { who: sv || (run ? run.name : "new"), why: sv || run ? "last" : "first", server: sv };
}
function wire(th, d, r) {
  let t = d.text.trim() ? d.text : "Attached: " + d.files.map(f => f.name).join(", ");
  if (!th || r.why === "reply" || r.why === "named" || r.why === "first") return t;
  if (r.who === "new") return "@new " + t;
  if (r.who && r.who !== r.server) return "@" + r.who + " " + t;
  return t;
}

// ---- messages on their way ----
export const OUT = [];
let lid = 0;
function prune() {
  for (let i = OUT.length - 1; i >= 0; i--) if (OUT[i].id && S.byId[OUT[i].id]) { for (const f of OUT.splice(i, 1)[0].files) if (f.url) URL.revokeObjectURL(f.url); }
}
/** Messages for one thread (or null: new work) not on the board yet. */
export function outFor(th) { prune(); return OUT.filter(x => x.th === (th || null)); }
export const outSig = th => outFor(th).map(x => x.lid + x.state + (x.uploading ? "u" : "")).join();

async function deliver(it) {
  it.state = "sending"; it.err = ""; refresh();
  try {
    if (!it.refs) { it.uploading = it.files.length > 0; refresh(); const refs = []; for (const f of it.files) refs.push(await uploadFile(f.file)); it.refs = refs; it.uploading = false; }
    const res = await post("/api/post", { text: it.wire, thread: it.th || it.name || null, reply_to: it.reply ? it.reply.id : null, files: it.refs, simple: simpleOn() });
    it.id = res.id; it.state = "sent";
    if (!it.th && res.thread) { it.th = res.thread; go("comms", res.thread); loadApp(); }
    poll();
  } catch (e) { it.state = "failed"; it.err = simpleOn() && !/sign in/.test(e.message) ? "This didn't send. Check your connection, then try again." : e.message; it.uploading = false; }
  refresh();
}
function queue(th, d, r) {
  const it = { lid: ++lid, th: th || null, text: d.text, files: d.files, reply: d.reply, to: th ? r.who : null, name: th ? null : d.name.trim() || null, wire: wire(th, d, r), state: "sending", err: "", t: now() };
  OUT.push(it); deliver(it);
  return it;
}
function send(th) {
  const d = draft(th);
  if (!d.text.trim() && !d.files.length) return;
  queue(th, d, recipient(th));
  Object.assign(d, { text: "", files: [], reply: null, replyId: null, to: null, name: "", naming: false, err: "" }); keep(d);
  CP.ac = null; CP.menu = false; CP.hist = null;
  const el = field(th); if (el) { el.value = ""; el.focus(); }   // morph leaves a focused field alone, so clear it here
  refresh();
}
/** Send a short message for the person from a button (Carry on, Answer). Shows like any other. */
export function say(th, text, to) {
  const r = to ? { who: to, why: "chosen", server: (TG.v[th] || {}).who } : recipient(th);
  queue(th, { text, files: [], reply: null, name: "" }, r); refresh();
}
export const actions = {
  retry: it => deliver(it),
  edit: it => {
    const d = draft(it.th);
    d.text = d.text.trim() ? d.text + "\n\n" + it.text : it.text; d.files = d.files.concat(it.files); d.reply ||= it.reply; keep(d);
    OUT.splice(OUT.indexOf(it), 1); CP.focus = true; const el = field(it.th); if (el) el.value = d.text; refresh();
  },
  discard: it => { OUT.splice(OUT.indexOf(it), 1); for (const f of it.files) if (f.url) URL.revokeObjectURL(f.url); refresh(); },
};

// ---- files ----
function addFiles(th, list) {
  const d = draft(th); d.err = "";
  for (let f of list) {
    if (f.size > 100 * 1048576) { d.err = `${f.name} is over 100 MB, so it can't be attached.`; continue; }
    if (/^image\.\w+$/i.test(f.name)) { const t = new Date(), p = n => String(n).padStart(2, "0"); f = new File([f], `pasted-${p(t.getHours())}${p(t.getMinutes())}${p(t.getSeconds())}.${f.name.split(".").pop()}`, { type: f.type }); }
    d.files.push({ file: f, name: f.name, size: f.size, url: IMGNAME.test(f.name) ? URL.createObjectURL(f) : null });
  }
  refresh();
}
const hasFiles = e => e.dataTransfer && [...(e.dataTransfer.types || [])].includes("Files");
/** Props for the element files can be dropped on (the whole conversation). */
export function dropZone(th) {
  return {
    "data-drag": CP.drag ? "" : null,
    ondragover: e => { if (!hasFiles(e)) return; e.preventDefault(); if (!CP.drag) { CP.drag = true; refresh(); } },
    ondragleave: (e, n) => { if (CP.drag && !n.contains(e.relatedTarget)) { CP.drag = false; refresh(); } },
    ondrop: e => { if (!hasFiles(e)) return; e.preventDefault(); CP.drag = false; addFiles(th, e.dataTransfer.files); CP.focus = true; refresh(); },
  };
}

// ---- @ names ----
function acNames(th, q) {
  const seen = new Set(), out = [], add = n => { if (n && !seen.has(n) && n.startsWith(q)) { seen.add(n); out.push(n); } };
  if (th) for (const n of threadPeople(th)) if (isAgent(n)) add(n);
  for (const n of S.running.keys()) add(n);
  for (let i = S.sessions.length - 1; i >= 0 && out.length < 8; i--) add(S.sessions[i].name);
  add("new");
  return out.slice(0, 8);
}
function acCheck(el, th) {
  const before = el.value.slice(0, el.selectionStart), m = simpleOn() ? null : before.match(/(?:^|\s)@([\w.-]*)$/);
  const items = m ? acNames(th, m[1].toLowerCase()) : [];
  const next = items.length && !(items.length === 1 && items[0] === m[1]) ? { q: m[1], at: el.selectionStart - m[1].length - 1, items, i: 0 } : null;
  if (JSON.stringify(next && next.items) !== JSON.stringify(CP.ac && CP.ac.items)) { CP.ac = next; return true; }
  if (next) CP.ac.at = next.at, CP.ac.q = next.q;
  return false;
}
function acPick(th, n) {
  const el = field(th), a = CP.ac; if (!el || !a) return;
  const d = draft(th), v = el.value.slice(0, a.at) + "@" + n + " " + el.value.slice(a.at + 1 + a.q.length);
  el.value = v; d.text = v; keep(d);
  const c = a.at + n.length + 2; el.focus(); el.setSelectionRange(c, c);
  CP.ac = null; refresh();
}
// ---- past messages ----
/** What the person has sent, from every thread and from any door, newest first, each text once. Messages still on their way count: Up right after Enter brings back that one. */
function pastSent() {
  const out = [], seen = new Set(), add = t => { t = t.trim(); if (t && !seen.has(t)) { seen.add(t); out.push(t); } };
  for (let i = OUT.length - 1; i >= 0; i--) if (!OUT[i].id || !S.byId[OUT[i].id]) add(OUT[i].text);
  for (let i = S.posts.length - 1; i >= 0 && out.length < 500; i--) if (S.posts[i].from === "person") { const t = typedText(S.posts[i]); if (!/^Attached: /.test(t)) add(t); }
  return out;
}
function recallTo(th, n, text) {
  const d = draft(th); d.text = text; keep(d);
  n.value = text; n.setSelectionRange(text.length, text.length); refresh();   // morph leaves a focused field alone
}
/** Still on what Up, Down or Tab put there: not edited, and the caret not moved into it (then the arrows are the caret's again). */
const walking = n => { const w = CP.hist; return !!w && n.value === (w.i < 0 ? w.was : w.list[w.i]) && n.selectionStart === n.value.length && n.selectionEnd === n.value.length; };
/** Up, Down: true if it took the key. Up starts only from an empty box, so it never takes the place of a draft. */
function histStep(th, n, up) {
  if (!walking(n)) {
    if (!up || n.value.trim()) return false;
    const list = pastSent(); if (!list.length) return false;
    CP.hist = { list, i: -1, was: n.value, tab: false };
  }
  const w = CP.hist;
  if (up ? w.i >= w.list.length - 1 : w.i < 0) return true;
  w.i += up ? 1 : -1;
  recallTo(th, n, w.i < 0 ? w.was : w.list[w.i]);
  if (w.i < 0) CP.hist = null;
  return true;
}
/** Tab: the newest past message that starts with what's typed; Tab again, the one before. False with nothing to offer, so Tab still leaves the box. */
function histTab(th, n) {
  if (walking(n) && CP.hist.tab) { const w = CP.hist; w.i = (w.i + 1) % w.list.length; recallTo(th, n, w.list[w.i]); return true; }
  const typed = n.value, low = typed.toLowerCase();
  if (!typed.trim() || n.selectionStart !== typed.length || n.selectionEnd !== typed.length) return false;
  const list = pastSent().filter(t => t.length > typed.length && t.toLowerCase().startsWith(low));
  if (!list.length) return false;
  CP.hist = { list, i: 0, was: typed, tab: true };
  recallTo(th, n, list[0]);
  return true;
}
const stateWords = n => (n === "new" ? "starts a new agent" : agentStatus(n).label || "");

// ---- pieces ----
function toChip(th, r) {
  const n = r.who, label = n === "new" ? "a new agent" : n || "…";
  const why = { reply: "you're replying to it", named: "named in your message", chosen: "you chose it" }[r.why];
  return h("button", { type: "button", class: "cp-to", "aria-haspopup": "listbox", "aria-expanded": String(CP.menu), title: "Who this goes to. Change it here, or type @ and a name.",
    onclick: () => { CP.menu = !CP.menu; refresh(); } },
    h("span", { class: "meta" }, "To"), n && n !== "new" ? statusDot(agentStatus(n).key, { live: S.running.has(n) }) : null, h("b", null, label),
    why ? h("span", { class: "meta" }, why) : null, h("span", { "aria-hidden": "true", class: "meta" }, "▾"));
}
function toMenu(th, r) {
  const d = draft(th), names = threadPeople(th).filter(isAgent), pick = n => { d.to = n; keep(d); CP.menu = false; refresh(); field(th)?.focus(); };
  return h("div", { class: "cp-menu", role: "listbox", "aria-label": "Send to", "data-key": "menu" },
    names.map(n => h("button", { type: "button", role: "option", class: "cp-opt", "aria-selected": String(r.who === n), "data-key": n, onclick: () => pick(n) },
      statusDot(agentStatus(n).key, { live: S.running.has(n) }), h("b", null, n), h("span", { class: "meta" }, stateWords(n)),
      n === partner(th) ? h("span", { class: "meta" }, "· you've been talking to it") : null)),
    h("button", { type: "button", role: "option", class: "cp-opt", "aria-selected": String(r.who === "new"), "data-key": "new", onclick: () => pick("new") },
      statusDot("none"), h("b", null, "A new agent"), h("span", { class: "meta" }, "starts fresh, with this conversation to read")),
    h("p", { class: "cp-menu-n" }, r.why === "partner"
      ? `${r.server} wrote here last, so a message with no name would wake it. You've been talking to ${r.who}, so Hive sends it there.`
      : "Typing @ and a name in your message sends it to that agent instead."));
}
function replyChip(d) {
  const p = d.reply;
  return h("div", { class: "cp-reply", "data-key": "reply" },
    h("span", { class: "meta" }, "Replying to"), h("b", null, p.from === "person" ? "yourself" : simpleOn() ? hiveName() : p.from),
    h("span", { class: "cp-reply-x" }, short(mdPlain(displayText(p)), 120)),
    h("button", { type: "button", class: "cp-x", title: "Cancel the reply (Esc)", "aria-label": "Cancel the reply", onclick: () => { d.reply = null; d.replyId = null; keep(d); refresh(); } }, "×"));
}
function filesRow(th, d) {
  return h("div", { class: "cp-files", "data-key": "files" }, d.files.map((f, i) => h("span", { class: "cp-file", "data-key": i + f.name, "data-img": f.url ? "" : null },
    f.url ? h("img", { src: f.url, alt: "" }) : h("b", null, (f.name.split(".").pop() || "file").slice(0, 4).toUpperCase()),
    h("span", { class: "cp-fn" }, h("span", null, f.name), h("span", { class: "meta" }, size(f.size))),
    h("button", { type: "button", class: "cp-x", title: "Remove " + f.name, "aria-label": "Remove " + f.name, onclick: () => { const [x] = d.files.splice(i, 1); if (x.url) URL.revokeObjectURL(x.url); refresh(); } }, "×"))));
}

/** composer({thread, update, place}): thread null is new work. place: thread | home | start (only changes the wording). */
export function composer({ thread, update, place = "thread", autofocus }) {
  CP.update = update;
  const th = thread || null;
  if (CP.th !== th) { CP.th = th; CP.ac = null; CP.menu = false; CP.hist = null; }
  const d = draft(th), r = recipient(th), k = "cp:" + (th || "(new)"), empty = !d.text.trim() && !d.files.length;
  if (CP.focus || autofocus === true) { CP.focus = false; queueMicrotask(() => { const t = field(th); if (t && document.activeElement !== t) { t.focus(); t.setSelectionRange(t.value.length, t.value.length); } }); }
  CP.sig = (named(d.text) || "") + "|" + empty;
  const ph = !th ? "Say what you want done, in your own words" : simpleOn() ? "Message" : r.who === "new" ? "Message a new agent" : r.who ? `Message ${r.who}` : "Message";
  return h("div", { class: "cp", "data-key": k, "data-place": place, ...(th ? {} : dropZone(null)) },  // a thread's composer is inside its conversation's zone
    d.reply ? replyChip(d) : null,
    d.err ? h("p", { class: "cp-err", role: "alert", "data-key": "err" }, d.err) : null,
    h("div", { class: "cp-box", "data-key": "box" },
      CP.ac ? h("div", { class: "cp-ac", role: "listbox", "aria-label": "Names", "data-key": "ac" }, CP.ac.items.map((n, i) => h("button", { type: "button", role: "option", class: "cp-opt", "data-key": n, "aria-selected": String(i === CP.ac.i), onclick: () => acPick(th, n) },
        n === "new" ? statusDot("none") : statusDot(agentStatus(n).key, { live: S.running.has(n) }), h("b", null, "@" + n), h("span", { class: "meta" }, stateWords(n))))) : null,
      d.files.length ? filesRow(th, d) : null,
      !th && d.naming ? h("div", { class: "cp-name-row", "data-key": "name" }, input({ class: "input cp-name", placeholder: "A short name, like fix-contact-form", "aria-label": "Name for this job", value: d.name, oninput: (e, n) => { d.name = n.value; keep(d); } })) : null,
      h("textarea", { class: "cp-t", "data-key": "t", rows: 1, placeholder: ph, "aria-label": th ? "Message" : "What do you want done?", value: d.text, enterkeyhint: "send",
        oninput: (e, n) => {
          d.text = n.value; keep(d); CP.hist = null;
          const moved = acCheck(n, th), sig = (named(d.text) || "") + "|" + (!d.text.trim() && !d.files.length);
          if (moved || sig !== CP.sig) refresh();
        },
        onkeydown: (e, n) => {
          const a = CP.ac;
          if (a && (e.key === "ArrowDown" || e.key === "ArrowUp")) { e.preventDefault(); a.i = (a.i + (e.key === "ArrowDown" ? 1 : a.items.length - 1)) % a.items.length; refresh(); return; }
          if (a && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) { e.preventDefault(); acPick(th, a.items[a.i]); return; }
          if (!e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey && !e.isComposing) {
            if ((e.key === "ArrowUp" || e.key === "ArrowDown") && histStep(th, n, e.key === "ArrowUp")) { e.preventDefault(); return; }
            if (e.key === "Tab" && histTab(th, n)) { e.preventDefault(); return; }
          }
          if (e.key === "Escape") { if (cancelReply()) { e.stopPropagation(); e.preventDefault(); } return; }
          if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(th); }
        },
        onpaste: e => { const fs = e.clipboardData && [...e.clipboardData.files]; if (fs && fs.length) { e.preventDefault(); addFiles(th, fs); } } }),
      h("div", { class: "cp-bar", "data-key": "bar" },
        simpleOn() ? null : th ? toChip(th, r) : d.naming ? h("span", { class: "meta cp-new" }, "A new agent starts on this") : button("Name it", () => { d.naming = true; refresh(); }, { kind: "quiet", size: "s", title: "Give this job a short name (optional). Without one it's called by its first words." }),
        h("input", { type: "file", multiple: true, hidden: true, class: "cp-pick", onchange: e => { addFiles(th, e.target.files); e.target.value = ""; } }),
        button("Attach", (e, b) => b.parentNode.querySelector(".cp-pick").click(), { kind: "quiet", size: "s", title: "Attach files. You can also paste or drop them here." }),
        h("span", { class: "cp-hint meta" }, "Enter to send · Shift+Enter for a new line · ↑ past messages · Tab completes"),
        button(place === "thread" ? "Send" : "Start", () => send(th), { kind: "primary", disabled: empty, cls: "cp-send" }))),
    CP.menu && th ? toMenu(th, r) : null);
}
