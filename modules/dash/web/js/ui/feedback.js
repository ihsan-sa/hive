// Feedback from anywhere in the app: a shortcut or More > Send feedback, then a screenshot, boxes drawn on it, a comment, Send.
// The picture comes from the browser's own screen capture, so nothing is loaded from elsewhere, and the browser asks first. Where it can't
// (a plain http address, most phones, or a No) the person can paste or choose an image, or send the comment alone.
// It goes to this Hive's own board, in the thread "feedback", like any message. There's no route to the makers yet, so the dialog says so.
import { h } from "../core/dom.js";
import { S, can, post, setupDone } from "../core/store.js";
import { threadHref } from "../core/router.js";
import { button } from "./kit.js";

const MAC = /Mac|iPhone|iPad/.test(navigator.platform || "");
export const FEEDBACK_KEY = MAC ? "⌥⇧S" : "Alt+Shift+S";
const BOX = "#e5484d";   // the same on any picture, whatever the theme
const FB = { open: false, comment: "" };
export const canFeedback = () => !!S.app.app && can("post") && setupDone();
addEventListener("keydown", e => {
  if (!e.altKey || !e.shiftKey || e.ctrlKey || e.metaKey || e.code !== "KeyS" || e.repeat || !canFeedback()) return;
  e.preventDefault(); openFeedback();
});

const pause = ms => new Promise(r => setTimeout(r, ms));
function toCanvas(src, w, hh) { const c = document.createElement("canvas"); c.width = w; c.height = hh; c.getContext("2d").drawImage(src, 0, 0, w, hh); return c; }

/** One frame of what the person chooses to share, then the capture stops at once. {pic: canvas} or {why: a sentence}. */
async function grab() {
  const md = navigator.mediaDevices;
  if (!md || !md.getDisplayMedia) return { why: "This browser can't take a screenshot at this address." };
  let stream;
  try { stream = await md.getDisplayMedia({ video: { displaySurface: "browser" }, audio: false, preferCurrentTab: true, selfBrowserSurface: "include" }); }
  catch (e) { return { why: e.name === "NotAllowedError" ? "No screenshot taken." : "The screenshot didn't work: " + e.message }; }
  try {
    const v = document.createElement("video"); v.muted = true; v.playsInline = true; v.srcObject = stream;
    await v.play();
    await pause(300);   // the browser's "sharing" bar moves the page as it appears
    if (!v.videoWidth) return { why: "The screenshot came back empty." };
    return { pic: toCanvas(v, v.videoWidth, v.videoHeight) };
  } catch (e) { return { why: "The screenshot didn't work: " + e.message }; }
  finally { for (const t of stream.getTracks()) t.stop(); }
}
async function upload(f) {
  const r = await fetch("/api/upload?name=" + encodeURIComponent(f.name), { method: "POST", headers: { "X-Hive-Dash": "1", "Content-Type": "application/octet-stream" }, body: f });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw Error(j.error || "the upload failed (" + r.status + ")");
  return j.ref;
}

/** Open the dialog: takes the picture first, so the dialog isn't in it. Must start from a key press or click: browsers require that. */
export async function openFeedback() {
  if (FB.open) return; FB.open = true;
  let pic = null, boxes = [], drag = null, only = false, away = false;
  const g = await grab(); pic = g.pic || null;
  const view = h("canvas", { class: "fb-c", "aria-label": "The screenshot. Drag to draw a box." });
  const stage = h("div", { class: "viewer-s fb-s" });
  const say = h("p", { class: "meta", role: "alert", "data-tone": "bad", style: "color:var(--ci)", hidden: true });
  const text = h("textarea", { class: "input fb-t", "aria-label": "Your comment", placeholder: "What's wrong, or what would you change?" });
  text.value = FB.comment; text.addEventListener("input", () => { FB.comment = text.value; });
  const pick = h("input", { type: "file", accept: "image/*", hidden: true });
  const undo = button("Undo box", () => { boxes.pop(); paint(); sync(); }, { size: "s", kind: "quiet" });
  const onlyBox = h("input", { type: "checkbox" }); onlyBox.addEventListener("change", () => { only = onlyBox.checked; });
  const onlyL = h("label", { class: "meta" }, onlyBox, " Send only the boxed part");
  const send = button("Send", () => go(), { kind: "primary", cls: "fb-send" });
  const again = button("Retake", () => take(), { size: "s", kind: "quiet" });
  const dlg = h("dialog", { class: "viewer fb", "aria-label": "Send feedback" },
    h("div", { class: "viewer-b" }, h("b", { class: "viewer-c" }, "Send feedback"), again, undo, onlyL, button("Close", () => dlg.close(), { size: "s" })),
    stage,
    h("div", { class: "fb-f" }, text, say, h("div", { class: "fb-r" },
      h("span", { class: "meta" }, "This goes to your own Hive, in the conversation called feedback, where an agent reads it. It isn't sent to the makers of Hive."), send)),
    pick);

  function drawBoxes(ctx, list, dx = 0, dy = 0) {
    ctx.strokeStyle = BOX; ctx.lineWidth = Math.max(2, pic.width / 500);
    for (const b of list) ctx.strokeRect(b.x - dx, b.y - dy, b.w, b.h);
  }
  function paint() {
    if (!pic) return;
    view.width = pic.width; view.height = pic.height;
    const c = view.getContext("2d"); c.drawImage(pic, 0, 0);
    drawBoxes(c, drag ? [...boxes, drag] : boxes);
  }
  function sync() {
    undo.hidden = !boxes.length; onlyL.hidden = !boxes.length; again.hidden = !pic;
    if (!boxes.length) { only = false; onlyBox.checked = false; }
  }
  function show(why) {
    if (pic) { stage.replaceChildren(view); paint(); }
    else stage.replaceChildren(h("div", { class: "fb-none" },
      h("p", null, (why ? why + " " : "") + "You can paste a picture here, choose one, or just write a comment."),
      h("div", { class: "fb-r" }, button("Take a screenshot", () => take(), { size: "s" }), button("Choose an image", () => pick.click(), { size: "s" }))));
    sync();
  }
  async function take() {   // the dialog steps aside so it isn't in the picture
    away = true; dlg.close();
    const r = await grab();
    if (r.pic) { pic = r.pic; boxes = []; }
    dlg.showModal(); away = false; show(r.why); text.focus();
  }
  async function load(f) {
    try { const b = await createImageBitmap(f); pic = toCanvas(b, b.width, b.height); boxes = []; b.close(); say.hidden = true; show(); }
    catch (e) { say.textContent = "That file isn't a picture this browser can read."; say.hidden = false; }
  }
  pick.addEventListener("change", () => { if (pick.files[0]) load(pick.files[0]); pick.value = ""; });
  dlg.addEventListener("paste", e => {
    const it = [...(e.clipboardData ? e.clipboardData.items : [])].find(i => i.type.startsWith("image/")); if (!it) return;
    e.preventDefault(); load(it.getAsFile());
  });

  // ---- boxes: canvas pixels, whatever size it's shown at ----
  const at = e => { const r = view.getBoundingClientRect(), k = pic.width / r.width;
    return { x: Math.max(0, Math.min(pic.width, (e.clientX - r.left) * k)), y: Math.max(0, Math.min(pic.height, (e.clientY - r.top) * k)) }; };
  let start = null;
  view.addEventListener("pointerdown", e => { start = at(e); view.setPointerCapture(e.pointerId); e.preventDefault(); });
  view.addEventListener("pointermove", e => { if (!start) return; const p = at(e);
    drag = { x: Math.min(start.x, p.x), y: Math.min(start.y, p.y), w: Math.abs(p.x - start.x), h: Math.abs(p.y - start.y) }; paint(); });
  const end = () => { if (drag && drag.w > 6 && drag.h > 6) boxes.push(drag); start = drag = null; paint(); sync(); };
  view.addEventListener("pointerup", end); view.addEventListener("pointercancel", end);

  function picture() {   // the file to send: the whole picture with its boxes, or only what they cover
    const u = only && boxes.length ? boxes.reduce((a, b) => ({ x0: Math.min(a.x0, b.x), y0: Math.min(a.y0, b.y), x1: Math.max(a.x1, b.x + b.w), y1: Math.max(a.y1, b.y + b.h) }), { x0: 1e9, y0: 1e9, x1: 0, y1: 0 }) : null;
    const pad = u ? Math.max(4, pic.width / 200) : 0;
    const x = u ? Math.max(0, Math.floor(u.x0 - pad)) : 0, y = u ? Math.max(0, Math.floor(u.y0 - pad)) : 0;
    const w = u ? Math.min(pic.width, Math.ceil(u.x1 + pad)) - x : pic.width, hh = u ? Math.min(pic.height, Math.ceil(u.y1 + pad)) - y : pic.height;
    const c = document.createElement("canvas"); c.width = w; c.height = hh;
    const ctx = c.getContext("2d"); ctx.drawImage(pic, -x, -y); drawBoxes(ctx, boxes, x, y);
    return new Promise((ok, no) => c.toBlob(b => (b ? ok(new File([b], "screenshot.png", { type: "image/png" })) : no(Error("the picture couldn't be made"))), "image/png"));
  }
  async function go() {
    const said = text.value.trim();
    if (!said && !pic) { say.textContent = "Write a comment or add a picture first."; say.hidden = false; return; }
    say.hidden = true; send.disabled = true; send.textContent = "Sending…";
    const where = location.hash.slice(1).split(/[/?]/)[0] || "home";
    try {
      const files = pic ? [await upload(await picture())] : [];
      await post("/api/post", { text: `Feedback on the app, from the ${where} page:\n\n${said || "(no comment, see the screenshot)"}`, thread: "feedback", files });
      FB.comment = "";
      dlg.replaceChildren(h("div", { class: "fb-f" }, h("p", null, "Sent. An agent in your Hive will read it."),
        h("div", { class: "fb-r" }, button("Open the conversation", null, { href: threadHref("feedback"), size: "s" }), button("Close", () => dlg.close(), { size: "s" }))));
      dlg.querySelector("a").addEventListener("click", () => dlg.close());
    } catch (e) {   // the comment and picture stay, so it can be sent again
      say.textContent = "It wasn't sent: " + e.message; say.hidden = false; send.disabled = false; send.textContent = "Send";
    }
  }
  dlg.addEventListener("click", e => { if (e.target === dlg) dlg.close(); });
  dlg.addEventListener("close", () => { if (!away) { dlg.remove(); FB.open = false; } });
  document.body.append(dlg); dlg.showModal(); show(g.why); text.focus();
}
