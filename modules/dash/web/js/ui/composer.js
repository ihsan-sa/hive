// The composer: write a post, attach files, reply. App mode only (can("post")). All state lives here, so a draft
// survives polls and leaving the view; the fields are never rebuilt under the person's hands.
import { h } from "../core/dom.js";
import { S, jget, post, poll } from "../core/store.js";
import { go } from "../core/router.js";
import { button, input } from "./kit.js";

const CP = { text: "", name: "", files: [], reply: null, note: "", err: "", busy: false, key: null, target: null, focus: false, update: null };
const size = n => (n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");
const refresh = () => CP.update && CP.update();

/** Start a reply to a post from anywhere: opens its thread and puts the cursor in the composer. */
export function replyTo(p) {
  CP.reply = p; CP.note = ""; CP.err = ""; CP.focus = true;
  go("comms", p.th); refresh();
}
export function cancelReply() { if (!CP.reply) return false; CP.reply = null; refresh(); return true; }

// Who a post here would reach. Asked again when the board or log moves.
function target(th) {
  const k = th + "|" + S.ver; if (CP.key === k) return; CP.key = k;
  jget("/api/target?thread=" + encodeURIComponent(th)).then(t => { CP.target = { th, ...t }; refresh(); }).catch(() => {});
}
function targetLine(th) {
  if (CP.reply) return [`Replying to ${CP.reply.from}. `, button("Cancel", cancelReply, { kind: "quiet", size: "s" })];
  if (CP.err) return h("span", { "data-tone": "bad", style: "color:var(--ci)", role: "alert" }, CP.err);
  if (CP.note) return CP.note;
  if (!th) return "New work: a fresh agent starts on this.";
  target(th);
  const t = CP.target; if (!t || t.th !== th) return " ";
  return (!t.who ? "No agent has worked here yet, so this starts a new one." : t.running ? `Goes to ${t.who}, running. It sees this after its current step.` : `Wakes ${t.who}, the last agent here.`) + " Naming another agent with @ sends it to them instead.";
}

async function upload(f) {
  if (f.size > 100 * 1048576) throw Error(f.name + " is over 100 MB");
  CP.note = `Uploading ${f.name}…`; refresh();
  const r = await fetch("/api/upload?name=" + encodeURIComponent(f.name), { method: "POST", headers: { "X-Hive-Dash": "1", "Content-Type": "application/octet-stream" }, body: f });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw Error(j.error || "upload failed: " + r.status);
  return j.ref;
}
async function send(thread) {
  if (CP.busy || (!CP.text.trim() && !CP.files.length)) return;
  const th = CP.reply ? CP.reply.th : thread || CP.name.trim() || null;
  CP.busy = true; CP.err = ""; refresh();
  try {
    const refs = [];
    for (const f of CP.files) refs.push(await upload(f));
    const res = await post("/api/post", { text: CP.text, thread: th, reply_to: CP.reply ? CP.reply.id : null, files: refs });
    CP.text = ""; CP.name = ""; CP.files = []; CP.reply = null; CP.key = null;
    for (const el of document.querySelectorAll(".cp-t,.cp-name")) el.value = "";   // morph leaves a focused field alone
    const said = "Sent" + (res.said ? ": " + res.said : "") + ".";
    CP.note = said;
    setTimeout(() => { if (CP.note === said) { CP.note = ""; refresh(); } }, 6000);
    if (res.thread && res.thread !== thread) go("comms", res.thread);
    poll();
  } catch (e) { CP.note = ""; CP.err = `Not sent: ${e.message}. Your text is still here.`; }
  finally { CP.busy = false; refresh(); }
}

/** composer({thread, update}): thread may be null (new work). update re-renders the view. */
export function composer({ thread, update }) {
  CP.update = update;
  if (CP.focus) { CP.focus = false; queueMicrotask(() => { const t = document.querySelector(".cp-t"); if (t) t.focus(); }); }
  return h("div", { class: "cp", "data-key": "composer" },
    h("div", { class: "cp-to meta", "data-key": "to", role: "status" }, targetLine(thread)),
    CP.files.length ? h("div", { class: "cp-files", "data-key": "files" }, CP.files.map((f, i) => h("span", { class: "cp-file", "data-key": i + f.name },
      `${f.name} (${size(f.size)})`,
      h("button", { type: "button", class: "copy", style: "opacity:1", title: "Remove " + f.name, "aria-label": "Remove " + f.name, onclick: () => { CP.files.splice(i, 1); refresh(); } }, "×")))) : null,
    !thread && !CP.reply ? input({ class: "input cp-name", "data-key": "name", placeholder: "Name this job (optional, like fix-login)", "aria-label": "Job name", value: CP.name, oninput: e => { CP.name = e.target.value; } }) : null,
    h("div", { class: "cp-row", "data-key": "row" },
      h("textarea", { class: "input cp-t", rows: 2, placeholder: thread || CP.reply ? "Message" : "Say what you want done", "aria-label": "Message", value: CP.text,
        oninput: e => { CP.text = e.target.value; },
        onkeydown: e => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(thread); } } }),
      h("input", { type: "file", multiple: true, hidden: true, class: "cp-pick", onchange: e => { CP.files.push(...e.target.files); e.target.value = ""; refresh(); } }),
      button("Attach", (e, b) => b.parentNode.querySelector(".cp-pick").click(), { title: "Attach files" }),
      button("Send", () => send(thread), { kind: "primary", disabled: CP.busy })));
}
