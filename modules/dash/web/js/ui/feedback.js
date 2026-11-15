// Feedback from anywhere in the app: a shortcut or More > Send feedback. The screen freezes and greys at once, the person drags boxes straight on it, writes a comment, Send.
// The frozen picture is a copy of the page (snapshot.js), so there's no browser prompt and nothing is loaded from elsewhere. Where that fails, or the picture looks wrong,
// the browser's own capture is one button away (it asks first); where that can't either (a plain http address, most phones, or a No) the person can paste or choose an image, or send the comment alone.
// In a consumer Hive (one with settings.json) it goes to the makers, through /api/feedback and the report module; nothing lands on the board and no agent starts.
// In a Hive that is its makers' own, it goes on the board in the thread "feedback", like any message. The overlay says which, and what's in it.
import { h, tr } from "../core/dom.js";
import { S, can, post, setupDone } from "../core/store.js";
import { threadHref } from "../core/router.js";
import { button } from "./kit.js";
import { snapshot } from "./snapshot.js";
import { uploadFile } from "../core/upload.js";

const MAC = /Mac|iPhone|iPad/.test(navigator.platform || "");
export const FEEDBACK_KEY = MAC ? "⌥⇧S" : "Alt+Shift+S";
const BOX = "#e5484d";   // the same on any picture, whatever the theme
const GREY = "rgb(0 0 0 / .38)";   // over the frozen screen; a box shows through it at full brightness. Never in the file that's sent.
const FB = { open: false, comment: "" };
const toMakers = () => !!S.app.settings;
export const canFeedback = () => !!S.app.app && can("post") && (toMakers() || setupDone());   // to the makers it needs no agents, so it works when setup is what's wrong
addEventListener("keydown", e => {
  if (!e.altKey || !e.shiftKey || e.ctrlKey || e.metaKey || e.code !== "KeyS" || e.repeat || !canFeedback()) return;
  e.preventDefault(); openFeedback();
});

// Opening feedback must leave the page where it is (the person, 11041326d0c5: Alt+Shift+S scrolled it). The snapshot briefly un-sticks headers and closing
// the window hands focus back to whatever had it, either of which can move a scroller in some browsers: note every scroll position and put them back.
function holdScroll() {
  const at = [document.scrollingElement || document.documentElement, ...document.body.querySelectorAll("*")]
    .filter(el => el.scrollTop || el.scrollLeft || /auto|scroll/.test(getComputedStyle(el).overflowY)).map(el => [el, el.scrollLeft, el.scrollTop, el.scrollTop && el.scrollTop + el.clientHeight >= el.scrollHeight - 2]);
  return () => { for (const [el, x, y0, end] of at) { const y = end ? el.scrollHeight : y0; if (el.isConnected && (el.scrollLeft !== x || Math.abs(el.scrollTop - y) > 2 && !(end && el.scrollTop + el.clientHeight >= el.scrollHeight - 2))) { el.scrollLeft = x; el.scrollTop = y; } } };   // at the end stays at the end, as new messages come
}

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
/** Base64 JPEG small enough for the makers' receiver. The picture only loses detail if it has to: try the sharpest first. */
function jpeg(c) {
  for (const [wide, q] of [[1600, .8], [1600, .6], [1280, .6], [1024, .5], [800, .4], [640, .3]]) {
    const k = Math.min(1, wide / c.width), o = document.createElement("canvas");
    o.width = Math.round(c.width * k); o.height = Math.round(c.height * k);
    const x = o.getContext("2d"); x.fillStyle = "#fff"; x.fillRect(0, 0, o.width, o.height); x.drawImage(c, 0, 0, o.width, o.height);   // JPEG has no see-through: white, not black
    const b64 = o.toDataURL("image/jpeg", q).split(",")[1];
    if (b64.length <= 400000) return b64;
  }
  throw Error("the picture is too large. Tick Send only the boxed part, or leave the picture out.");
}
function sentNote() {   // Sending ends the overlay: the person is back on the page they were on, with a small note in the corner
  for (const old of document.querySelectorAll(".fb-sent")) old.remove();
  const note = toMakers()
    ? h("div", { class: "fb-sent", role: "status" }, h("span", null, "Sent to the makers of Hive. Thank you."), button("Close", () => note.remove(), { size: "s", kind: "quiet" }))
    : h("div", { class: "fb-sent", role: "status" }, h("span", null, "Sent. An agent in your Hive will read it."),
        button("Open the conversation", null, { href: threadHref("feedback"), size: "s" }), button("Close", () => note.remove(), { size: "s", kind: "quiet" }));
  note.querySelector("a")?.addEventListener("click", () => note.remove());
  document.body.append(note); setTimeout(() => note.remove(), 10000);
}

/** An agent drafted feedback (hive feedback-draft): open the window with its words in, once. The person edits and presses Send, or closes it; either clears the draft. */
export function offerDraft(d) {
  if (FB.open || FB.offered === d.id || !canFeedback()) return;
  FB.offered = d.id; FB.comment = String(d.text || "").slice(0, 8000); FB.draftId = d.id;
  openFeedback();
}

/** Open the overlay: the grey comes up at once, then the frozen picture under it. The overlay itself is left out of the picture. */
export async function openFeedback() {
  if (FB.open) return; FB.open = true;
  let pic = null, boxes = [], drag = null, only = false, away = false, frozen = false;
  const view = h("canvas", { class: "fb-c", "aria-label": "The screen, frozen. Drag to draw a box." });
  const stage = h("div", { class: "fb-s" });
  const say = h("p", { class: "meta", role: "alert", "data-tone": "bad", style: "color:var(--ci)", hidden: true });
  const text = h("textarea", { class: "input fb-t", "aria-label": "Your comment", placeholder: "What's wrong, or what would you change? Ctrl+Enter sends." });
  text.value = FB.comment; text.addEventListener("input", () => { FB.comment = text.value; });
  text.addEventListener("keydown", e => { if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.isComposing && !send.disabled) { e.preventDefault(); go(); } });   // Ctrl+Enter (Cmd+Enter on a Mac) sends
  const pick = h("input", { type: "file", accept: "image/*", hidden: true });
  const undo = button("Undo box", () => { boxes.pop(); paint(); sync(); }, { size: "s", kind: "quiet" });
  const onlyBox = h("input", { type: "checkbox" }); onlyBox.addEventListener("change", () => { only = onlyBox.checked; });
  const onlyL = h("label", { class: "meta" }, onlyBox, " Send only the boxed part");
  const noPicBox = h("input", { type: "checkbox" });
  const noPicL = h("label", { class: "meta" }, noPicBox, " Leave the picture out");
  noPicL.hidden = !toMakers();
  const send = button("Send", () => go(), { kind: "primary", cls: "fb-send" });
  const gh = button("Open as a GitHub issue", () => ghIssue(), { size: "s", kind: "quiet", title: "Opens a prefilled issue on the Hive's GitHub page, which you check and post from your own GitHub account. The picture isn't included." });
  gh.hidden = !toMakers();
  const again = button("Browser capture", () => take(), { size: "s", kind: "quiet", title: "If the picture looks wrong: take it with the browser's own screen capture, which asks first" });
  const more = h("details", { class: "fb-more" }, h("summary", { class: "btn", "data-kind": "quiet", "data-size": "s" }, "More ways"), h("div", { class: "fb-more-b" }, again, gh));   // Send is the one primary; the rest wait here
  const hint = h("div", { class: "fb-hint", hidden: true }, "Drag on the screen to draw a box");
  const card = h("div", { class: "fb-card" },
    h("div", { class: "fb-r" }, h("b", { class: "fb-title" }, "Send feedback"), undo, button("Close", () => dlg.close(), { size: "s" })),
    text, say, h("div", { class: "fb-r" }, onlyL, noPicL),
    h("div", { class: "fb-r" }, more, h("span", { class: "fb-gap" }), send),
    h("p", { class: "meta" }, toMakers()
      ? "This goes to the makers of Hive: your comment, the picture, the page you're on, this Hive's version and a random install id. The picture holds whatever is on the screen, so box only the part that matters or leave it out. A copy stays on this machine, in reports/sent."
      : "This goes to your own Hive, in the conversation called feedback, where an agent reads it. It isn't sent to the makers of Hive."));
  const dlg = h("dialog", { class: "fb", "aria-label": "Send feedback" }, stage, hint, card, pick);

  function drawBoxes(ctx, list, dx = 0, dy = 0) {
    ctx.strokeStyle = BOX; ctx.lineWidth = Math.max(2, pic.width / 500);
    for (const b of list) ctx.strokeRect(b.x - dx, b.y - dy, b.w, b.h);
  }
  function paint() {
    if (!pic) return;
    view.width = pic.width; view.height = pic.height;
    const c = view.getContext("2d"), list = drag ? [...boxes, drag] : boxes; c.drawImage(pic, 0, 0);
    c.fillStyle = GREY; c.fillRect(0, 0, pic.width, pic.height);
    for (const b of list) if (b.w && b.h) c.drawImage(pic, b.x, b.y, b.w, b.h, b.x, b.y, b.w, b.h);
    drawBoxes(c, list);
    dodge(list[list.length - 1]);
  }
  function dodge(b) {   // the card moves to the other corner rather than sit on what's being boxed
    if (!b) return;
    const k = view.getBoundingClientRect().width / pic.width, v = view.getBoundingClientRect(), r = card.getBoundingClientRect();
    const x0 = v.left + b.x * k, x1 = v.left + (b.x + b.w) * k, y = v.top + b.y * k < r.bottom && v.top + (b.y + b.h) * k > r.top;
    const on = (l, rt) => y && x0 < rt && x1 > l, side = card.parentElement.getBoundingClientRect(), ml = side.left + side.right - r.right;
    // Move only if the other corner is clear: a box across both corners would make the card jump back and forth on every drag step.
    if (on(r.left, r.right) && !on(ml, ml + r.width)) card.toggleAttribute("data-left");
  }
  function sync() {
    undo.hidden = !boxes.length; onlyL.hidden = !boxes.length; again.hidden = !pic; more.hidden = again.hidden && gh.hidden; hint.hidden = !pic || !!boxes.length;
    if (!boxes.length) { only = false; onlyBox.checked = false; }
  }
  function show(why) {
    if (pic) { stage.replaceChildren(view); paint(); view.classList.toggle("fb-full", frozen); }
    else stage.replaceChildren(h("div", { class: "fb-none" },
      h("p", null, (why ? why + " " : "") + "You can paste or drop a picture here, choose one, or just write a comment."),
      h("div", { class: "fb-r" }, button("Take a screenshot", () => take(), { size: "s" }), button("Choose an image", () => pick.click(), { size: "s" }))));
    sync();
  }
  async function take() {   // the browser's own capture; the overlay steps aside so it isn't in the picture
    away = true;
    const closed = new Promise(ok => dlg.addEventListener("close", ok, { once: true }));   // close fires a moment later: where grab() answers at once it mustn't arrive after we reopen, or it ends the overlay
    dlg.close();
    const r = await grab(); await closed;
    if (r.pic) { pic = r.pic; boxes = []; frozen = false; }
    dlg.showModal(); away = false; show(r.why); text.focus({ preventScroll: true });
  }
  async function load(f) {
    try { const b = await createImageBitmap(f); pic = toCanvas(b, b.width, b.height); boxes = []; frozen = false; b.close(); say.hidden = true; show(); }
    catch (e) { say.textContent = tr("That file isn't a picture this browser can read."); say.hidden = false; }
  }
  pick.addEventListener("change", () => { if (pick.files[0]) load(pick.files[0]); pick.value = ""; });
  dlg.addEventListener("paste", e => {
    const it = [...(e.clipboardData ? e.clipboardData.items : [])].find(i => i.type.startsWith("image/")); if (!it) return;
    e.preventDefault(); load(it.getAsFile());
  });

  dlg.addEventListener("dragover", e => { if ([...(e.dataTransfer.items || [])].some(i => i.type.startsWith("image/"))) e.preventDefault(); });
  dlg.addEventListener("drop", e => { const f = [...e.dataTransfer.files].find(f => f.type.startsWith("image/")); if (f) { e.preventDefault(); load(f); } });

  // ---- boxes: canvas pixels, whatever size it's shown at ----
  const at = e => { const r = view.getBoundingClientRect(), k = pic.width / r.width;
    return { x: Math.max(0, Math.min(pic.width, (e.clientX - r.left) * k)), y: Math.max(0, Math.min(pic.height, (e.clientY - r.top) * k)) }; };
  let start = null;
  view.addEventListener("pointerdown", e => { start = at(e); view.setPointerCapture(e.pointerId); e.preventDefault(); });
  view.addEventListener("pointermove", e => { if (!start) return; const p = at(e);
    drag = { x: Math.min(start.x, p.x), y: Math.min(start.y, p.y), w: Math.abs(p.x - start.x), h: Math.abs(p.y - start.y) }; paint(); });
  const end = () => { if (drag && drag.w > 6 && drag.h > 6) boxes.push(drag); start = drag = null; paint(); sync(); };
  view.addEventListener("pointerup", end); view.addEventListener("pointercancel", end);

  function framed() {   // what's sent: the whole picture with its boxes, or only what they cover
    const u = only && boxes.length ? boxes.reduce((a, b) => ({ x0: Math.min(a.x0, b.x), y0: Math.min(a.y0, b.y), x1: Math.max(a.x1, b.x + b.w), y1: Math.max(a.y1, b.y + b.h) }), { x0: 1e9, y0: 1e9, x1: 0, y1: 0 }) : null;
    const pad = u ? Math.max(4, pic.width / 200) : 0;
    const x = u ? Math.max(0, Math.floor(u.x0 - pad)) : 0, y = u ? Math.max(0, Math.floor(u.y0 - pad)) : 0;
    const w = u ? Math.min(pic.width, Math.ceil(u.x1 + pad)) - x : pic.width, hh = u ? Math.min(pic.height, Math.ceil(u.y1 + pad)) - y : pic.height;
    const c = document.createElement("canvas"); c.width = w; c.height = hh;
    const ctx = c.getContext("2d"); ctx.drawImage(pic, -x, -y); drawBoxes(ctx, boxes, x, y);
    return c;
  }
  function picture() {
    const c = framed();
    return new Promise((ok, no) => c.toBlob(b => (b ? ok(new File([b], "screenshot.png", { type: "image/png" })) : no(Error("the picture couldn't be made"))), "image/png"));
  }
  async function go() {
    const said = text.value.trim();
    const withPic = pic && !(toMakers() && noPicBox.checked);
    if (!said && !withPic) { say.textContent = tr("Write a comment or add a picture first."); say.hidden = false; return; }
    say.hidden = true; send.disabled = true; send.textContent = tr("Sending…");
    const where = location.hash.slice(1).split(/[/?]/)[0] || "home";
    try {
      if (toMakers()) await post("/api/feedback", { text: said, page: where, ...(withPic ? { image: jpeg(framed()) } : {}) });
      else {
        const files = pic ? [await uploadFile(await picture())] : [];
        await post("/api/post", { text: `Feedback on the app, from the ${where} page:\n\n${said || "(no comment, see the screenshot)"}`, thread: "feedback", files });
      }
      FB.comment = "";
      dlg.close(); sentNote();
    } catch (e) {   // the comment and picture stay, so it can be sent again
      say.textContent = tr("It wasn't sent: ") + e.message; say.hidden = false; send.disabled = false; send.textContent = tr("Send");
    }
  }
  async function ghIssue() {   // a prefilled link, scrubbed on this machine; the person reviews it and posts it as themselves, so no token is needed
    const said = text.value.trim();
    if (!said) { say.textContent = tr("Write what went wrong first."); say.hidden = false; return; }
    const tab = window.open("about:blank", "_blank");   // opened in the click, so the browser doesn't block it
    try {
      const r = await post("/api/issue-link", { text: said, page: location.hash.slice(1).split(/[/?]/)[0] || "home" });
      if (tab) { tab.opener = null; tab.location = r.url; } else location.assign(r.url);
      say.hidden = true;
    } catch (e) { if (tab) tab.close(); say.textContent = tr("No link: ") + e.message; say.hidden = false; }
  }
  dlg.addEventListener("keydown", e => e.stopPropagation());   // what's open under the picture stays as it is: Escape closes this overlay only, and no shortcut acts on the page under it
  dlg.addEventListener("close", () => { if (FB.draftId && !away) { post("/api/feedback-draft-done", { id: FB.draftId }).catch(() => {}); FB.draftId = null; }
    if (!away) { dlg.remove(); FB.open = false; back(); requestAnimationFrame(back); } });
  const back = holdScroll();
  document.body.append(dlg); dlg.showModal(); text.focus({ preventScroll: true }); back();
  await new Promise(r => { requestAnimationFrame(() => setTimeout(r)); setTimeout(r, 100); });   // let the grey show first: copying a big page takes a moment (the timeout is for a tab with no frames)
  let why = "";
  try { pic = await snapshot(); frozen = true; back(); } catch (e) { why = "The screen couldn't be frozen here."; console.warn("feedback: snapshot failed:", e); }
  if (dlg.open) show(why);
}
