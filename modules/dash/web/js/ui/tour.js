// The app's guided tour: a floating window that says what to click, rings it and waits. Offered once after sign-in, restarted from More.
// It only watches the page. It sends nothing, presses nothing and never asks for a click that changes the hive, except sending a first job, which the user types and can skip.
// A view's tree is morphed, so the window and the ring live on <body>. The deck's own tour is js/deck/tour.js.
import { S, can, subscribe } from "../core/store.js";
import { go, parse } from "../core/router.js";
import { OUT } from "./composer.js";

export const HELP = "https://hive.ihsan.cc/guide/";   // export.sh fills this in
const SEEN = "hive.tour.seen";

const here = () => parse().id;
const q = s => { const e = document.querySelector(s); return e && e.getClientRects().length ? e : null; };
const heading = text => [...document.querySelectorAll("#main .sec-t")].find(e => e.textContent.trim() === text) || null;
const HOME = '.cp[data-place="home"] .cp-box';   // on a phone it's behind New conversation
const inThread = () => { const r = parse(); return r.id === "comms" && !!r.arg && r.arg[0] !== "~"; };
const tab = id => () => q(`.tabs a[data-key="${id}"]`);
const hasTab = id => !!q(`.tabs a[data-key="${id}"]`);
const mine = () => (S.posts || []).filter(p => p.from === "person").length;
const threads = () => (S.threads ? S.threads.size : 0);
let sentFrom = 0, outFrom = 0;

/** A step: title, text, ring() what to mark, done() the page shows they did it (no done: a Next button), skip() not on this install or page. */
const STEPS = [
  { title: "A quick look around", text: "A few minutes, one step at a time. This window says what to click and waits for you. You can drag it, and leave whenever you like." },
  { title: "This is where you talk to the hive", text: "Click Chat at the top.", ring: tab("comms"), done: () => here() === "comms", skip: () => here() === "comms" },
  { title: "Start a job", text: () => `${q(HOME) ? "Type" : "Click New conversation, then type"} what you want done, the way you'd tell a person, and press Start. An agent starts on it and answers here. This is a real job and uses your Claude limits, so skip it if you're not ready.`, ring: () => q(HOME) || q(".cm-newbtn"), done: () => mine() > sentFrom || OUT.length > outFrom || inThread(), enter: () => { sentFrom = mine(); outFrom = OUT.length; if (parse().arg) go("comms"); }, skip: () => !can("post") },
  { title: "Open a conversation", text: "Each job is one conversation, and they're all listed here. Click one.", ring: () => q(".cm-tl a.lr-in"), done: inThread, enter: () => { if (here() === "comms" && parse().arg) go("comms"); }, skip: () => inThread() || !threads() },
  { title: "Answers land here", text: "Agents write back in this conversation as they go. You can close the app: the work carries on, and it's all here when you come back.", ring: () => q(".cm-msgs"), skip: () => !inThread() },
  { title: "Who's on it", text: "This line says who is working and what they're doing. If an agent is running, Stop… is here too. It asks you to type the agent's name before anything ends, so you don't need to try it now.", ring: () => q(".cm-strip"), skip: () => !inThread() },
  { title: "When an agent asks you something", text: () => (q('.cm-line[data-key="asks"] button') ? "An agent here is waiting for you. Click Answer to reply to its question." : "A line appears here with an Answer button, and the Chat tab shows a number. The Chat page lists everything waiting for you."), ring: () => q('.cm-line[data-key="asks"] button') || q(".cm-strip"), done: () => (q('.cm-line[data-key="asks"] button') ? !!q(".cp-reply") : false), hasNext: true, skip: () => !inThread() },
  { title: "Reply any time", text: "Write here to carry on the conversation, even while the agent works. The To button shows who gets it, and you can change that. Nothing is sent until you press Send.", ring: () => q('.cp[data-place="thread"] .cp-t'), skip: () => !inThread() },
  { title: "Attach files", text: "Attach adds files to your message. You can also paste them or drop them on the conversation.", ring: () => [...document.querySelectorAll('.cp[data-place="thread"] .cp-bar button')].find(b => b.textContent === "Attach") || null, skip: () => !inThread() },
  { title: "See every agent", text: "Click Agents at the top.", ring: tab("now"), done: () => here() === "now", skip: () => !hasTab("now") },
  { title: "Agents", text: "Everyone working right now and what they're doing. Click a name to see its whole session.", ring: () => heading("What each is doing"), skip: () => here() !== "now" },
  { title: "Check your limits", text: "Click Usage at the top.", ring: tab("usage"), done: () => here() === "usage", skip: () => !hasTab("usage") },
  { title: "Usage", text: "How much of your Claude limits is left and when it comes back. New agents stop at a usage line so they don't use it all. You can change that line, or pause an account, under Claude accounts.", ring: () => heading("Claude accounts"), skip: () => here() !== "usage" },
  { title: "Settings", text: "Click Settings at the top.", ring: tab("settings"), done: () => here() === "settings", skip: () => !hasTab("settings") },
  { title: "How the hive runs", text: "Add a second Claude account here, and turn on extras: optional tools your agents can use.", ring: () => heading("Claude accounts"), skip: () => here() !== "settings" },
  { title: "One more", text: "Click More at the top.", ring: () => q(".more > button"), done: () => !!q(".more > button[aria-expanded=true]"), skip: () => !q(".more > button") },
  { title: "Help is under More", text: "Help opens the user guide, and Take the tour brings this window back. The other views here are for looking deeper. That's it: say what you want done in Chat.", ring: () => q('.more-i[data-key="help"]'), last: true },
];

let win = null, ringEl = null, at = 0, timer = 0, raf = 0, dragged = false, spot = 0, moving = 0, dodged = "", armed = true;
const SPOTS = ["bl", "br", "tr", "tl"];
const step = () => STEPS[at];
const val = x => (typeof x === "function" ? x() : x);

function paint() {
  const s = step();
  win.querySelector(".tw-n").textContent = `${at + 1} of ${STEPS.length}`;
  win.querySelector(".tw-t").textContent = s.title;
  win.querySelector(".tw-p").textContent = val(s.text);
  const next = win.querySelector(".tw-next"), first = at === 0, waits = s.done && !s.hasNext;
  next.textContent = first ? "Show me around" : s.last ? "Finish" : waits ? "Skip" : "Next";
  next.dataset.kind = waits ? "quiet" : "primary";
  win.querySelector(".tw-back").hidden = first;
  win.querySelector(".tw-end").textContent = first ? "Not now" : "End tour";
}
function show(i, dir = 1) {
  clearTimeout(moving); moving = 0;
  while (STEPS[i] && STEPS[i].skip && STEPS[i].skip()) i += dir;
  if (!STEPS[i]) { if (dir > 0) return end(); i = 0; }
  at = i;
  const s = step(); if (s.enter) s.enter();
  armed = dir > 0 || !s.done || !s.done();   // back on a step already done: wait for it, or it would bounce straight forward again
  paint();
  setTimeout(() => { const e = s.ring && s.ring(); if (e && step() === s) e.scrollIntoView({ block: "nearest" }); }, 60);
}
const next = () => (step().last ? end() : show(at + 1));

// ---- the ring follows its target through morphs and scrolling; the window stays out of its way ----
function setSpot(n) { moveTo(n); spot = n; }
function moveTo(n) {
  const st = win.style;
  const topH = (document.getElementById("top") || { offsetHeight: 0 }).offsetHeight + 12, k = SPOTS[n];
  st.left = k[1] === "l" ? "16px" : "auto"; st.right = k[1] === "r" ? "16px" : "auto";
  st.top = k[0] === "t" ? topH + "px" : "auto"; st.bottom = k[0] === "b" ? "16px" : "auto";
}
const covered = (a, b) => Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left)) * Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top));
function follow() {
  if (!win) return;
  raf = requestAnimationFrame(follow);
  const s = step(), modal = document.querySelector("dialog[open]"), e = !modal && s.ring ? s.ring() : null, r = e && e.getBoundingClientRect();
  ringEl.hidden = !r;
  if (!r) return;
  const st = ringEl.style, p = 4; st.left = r.left - p + "px"; st.top = r.top - p + "px"; st.width = r.width + 2 * p + "px"; st.height = r.height + 2 * p + "px";
  if (!dragged) dodge(r);
}
/** Put the window in the corner that covers least of the target, and leave it there. A target too big to clear, like a whole conversation, is under every corner, so the first clear corner would never come and the window would jump between them on every frame. */
function dodge(r) {
  const w = win.getBoundingClientRect(), key = [r.left, r.top, r.width, r.height, w.width, w.height, innerWidth, innerHeight].join();
  if (key === dodged) return;
  dodged = key;
  let best = spot, least = covered(r, w);
  for (let k = 1; least && k < SPOTS.length; k++) { const n = (spot + k) % SPOTS.length; moveTo(n); const c = covered(r, win.getBoundingClientRect()); if (c < least) { best = n; least = c; } }
  setSpot(best);
}
/** What runs on its own every moment: a modal window sits above the page, so the tour rides inside it; and a waiting step moves on when it's done. */
function tick() {
  if (!win) return;
  const home = document.querySelector("dialog[open]") || document.body;
  if (win.parentNode !== home) home.append(win);
  const s = step();
  const isDone = !!s.done && s.done();
  if (!isDone) armed = true;
  if (isDone && armed && !moving) moving = setTimeout(() => { moving = 0; if (win && step() === s) next(); }, 500);
  else if (typeof s.text === "function") { const t = val(s.text), p = win.querySelector(".tw-p"); if (p.textContent !== t) p.textContent = t; }
}
function drag(handle) {
  handle.addEventListener("pointerdown", ev => {
    if (ev.target.closest("button")) return;
    const r = win.getBoundingClientRect(), dx = ev.clientX - r.left, dy = ev.clientY - r.top;
    handle.setPointerCapture(ev.pointerId);
    const mv = m => { dragged = true; const st = win.style; st.left = m.clientX - dx + "px"; st.top = m.clientY - dy + "px"; st.right = st.bottom = "auto"; };
    const up = () => { handle.removeEventListener("pointermove", mv); handle.removeEventListener("pointerup", up); };
    handle.addEventListener("pointermove", mv); handle.addEventListener("pointerup", up);
  });
}
const btn = (text, cls, fn) => { const b = document.createElement("button"); b.type = "button"; b.className = "btn " + cls; b.dataset.size = "s"; b.dataset.kind = "quiet"; b.textContent = text; b.addEventListener("click", fn); return b; };

export function startTour() {
  if (win) return;
  try { localStorage.setItem(SEEN, "1"); } catch (e) { /* no storage: it may offer itself again */ }
  win = document.createElement("div"); win.className = "tour-w"; win.setAttribute("role", "dialog"); win.setAttribute("aria-label", "Tour of the app");
  win.innerHTML = `<div class="tw-top"><span class="tw-n meta"></span></div><h3 class="tw-t"></h3><p class="tw-p" aria-live="polite"></p><div class="tw-btns"></div>`;
  win.querySelector(".tw-btns").append(btn("", "tw-end", end), btn("Back", "tw-back", () => show(Math.max(0, at - 1), -1)), btn("", "tw-next", next));
  ringEl = document.createElement("div"); ringEl.className = "tour-ring"; ringEl.setAttribute("aria-hidden", "true"); ringEl.hidden = true;
  document.body.append(ringEl, win);
  drag(win.querySelector(".tw-top"));
  dragged = false; dodged = ""; setSpot(0); at = 0; show(0);
  timer = setInterval(tick, 200); raf = requestAnimationFrame(follow);
}
function end() {
  if (!win) return;
  clearInterval(timer); cancelAnimationFrame(raf); clearTimeout(moving); moving = 0;
  win.remove(); ringEl.remove(); win = ringEl = null;
}
/** Offer it once in this browser, the first time someone is signed in to the app. */
export function offerTour() {
  const look = () => {
    if (win || !S.app || !S.app.app || !can("post") || document.querySelector("dialog[open]")) return;
    const s = S.app.setup || {}; if (!(s.signed_in || s.api_key)) return;
    let seen = "1"; try { seen = localStorage.getItem(SEEN); } catch (e) { /* no storage: don't nag */ }
    if (!seen) startTour();
  };
  subscribe(look); look();
}
