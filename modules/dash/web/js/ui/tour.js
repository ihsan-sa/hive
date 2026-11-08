// The app's guided tour: a floating window that says what to click, rings it and waits. Offered once after sign-in, restarted from More.
// It only watches the page. It sends nothing, presses nothing and never asks for a click that changes the hive, except sending a first job, which the user types and can skip.
// A view's tree is morphed, so the window and the ring live on <body>. The deck's own tour is js/deck/tour.js.
import { S, can, subscribe } from "../core/store.js";
import { simpleOn } from "../core/simple.js";
import { go, parse } from "../core/router.js";
import { OUT } from "./composer.js";

export const HELP = "https://hivelab.ca/guide/";   // export.sh swaps the site name for its placeholder, then fills that in
const SEEN = "hive.tour.seen", SEEN_SIMPLE = "hive.tour.seen.simple";

const here = () => parse().id;
const q = s => [...document.querySelectorAll(s)].find(e => e.getClientRects().length) || null;   // the first one on screen: on a phone the top tabs are hidden and the bottom bar's are the ones to ring
const lastMsg = () => { const a = [...document.querySelectorAll(".cm-msgs .msg")].filter(e => e.getClientRects().length); return a.reverse().find(e => e.dataset.who !== "me") || a[0] || q(".cm-msgs"); };   // the newest agent message, not the whole column, so the window has room beside it
const heading = text => [...document.querySelectorAll("#main .sec-t")].find(e => e.textContent.trim() === text) || null;
const HOME = '.cp[data-place="home"] .cp-box';   // on a phone it's behind New conversation
const onFocus = () => here() === "focus";   // a focus page (#focus/<id>), the second depth
const inThread = () => { const r = parse(); return r.id === "comms" && !!r.arg && r.arg[0] !== "~"; };
const tabSel = id => `.tabs a[data-key="${id}"], .top-icons a[data-key="${id}"], .mn-bar a[data-key="${id}"]`;   // Settings is an icon at the right of the bar; on a phone the tabs are in the bottom bar
const tab = id => () => q(tabSel(id));
const hasTab = id => !!document.querySelector(tabSel(id));   // there, if not on screen: inside a conversation a phone hides its bottom bar
const toBar = id => () => { if (inThread() && !q(tabSel(id))) go("comms"); };   // a step that says to click a tab: a phone hides its bottom bar inside a conversation, so step back out to the list
const tabWhere = () => (q(".mn-bar") ? "at the bottom" : "at the top");   // on a phone the tabs are in the bottom bar
const moreBtn = () => q(".more > button") || q(".mn-bar button.mn-t");   // on a phone More is in the bottom bar
const mine = () => (S.posts || []).filter(p => p.from === "person").length;
const threads = () => (S.threads ? S.threads.size : 0);
let sentFrom = 0, outFrom = 0;

/** A step: title, text, ring() what to mark, done() the page shows they did it (no done: a Next button), skip() not on this install or page. */
const FULL_STEPS = [
  { title: "A quick look around", text: "A few minutes, one step at a time. This window says what to click and waits for you. You can drag it, and leave whenever you like." },
  { title: "This is where you talk to the Hive", text: () => `Click Chat ${tabWhere()}.`, ring: tab("comms"), done: () => here() === "comms", skip: () => here() === "comms" },
  // The four depths (the person, ui-redesign, 2026-11-07): the home says what you want, what needs you and where each focus stands; a focus page is the whole picture; a conversation's two lines say where it stands; then the conversation itself.
  { title: "Say what you want", text: () => `${q(HOME) ? "Type" : "Click New conversation, then type"} what you want done, the way you'd tell a person, and press Start.${q(".cm-focus-row") ? " Focus, beside the box, files it under one piece of your work; Misc is none." : ""} An agent starts on it and answers here. This is a real job and uses your Claude limits, so skip it if you're not ready.`, ring: () => q(HOME) || q(".cm-newbtn"), done: () => mine() > sentFrom || OUT.length > outFrom || inThread(), enter: () => { sentFrom = mine(); outFrom = OUT.length; if (parse().arg) go("comms"); }, skip: () => !can("post") },
  { title: "What needs you", text: "This one line is the first thing to read: how many things wait on you, and a link to each. When it says nothing needs you, you can close the app.", ring: () => q(".cm-hline"), skip: () => !q(".cm-hline") },
  { title: "Where things stand", text: "One card per focus, a piece of your work with a goal: what moves now, what waits on you, what changed lately, and two small gauges for its share of the Hive's work and cost. Click a focus name for the whole picture.", ring: () => q(".cm-foci .fc") || q(".cm-foci"), done: onFocus, hasNext: true, skip: () => !q(".cm-foci") && !onFocus() },
  { title: "The whole picture", text: "Everything about this focus on one page: what moves, what changed, what needs you, its to-do list, its agents and its conversations. Click a conversation for the detail.", ring: () => q(".fp .ph") || q(".fp"), done: inThread, hasNext: true, skip: () => !onFocus() },
  { title: "Open a conversation", text: "Each job is one conversation, and they're all listed here. Click one.", ring: () => q(".cm-tl a.lr-in"), done: inThread, enter: () => { if (here() === "comms" && parse().arg) go("comms"); }, skip: () => inThread() || !threads() },
  { title: "Where it stands", text: "Two lines at the top of a conversation say what it is about and where it stands; the agent on it keeps them current. Home, a focus, these two lines, then the conversation itself: each is one click deeper.", ring: () => q(".cm-status"), skip: () => !inThread() || !q(".cm-status") },
  { title: "Answers land here", text: "Agents write back in this conversation as they go. You can close the app: the work carries on, and it's all here when you come back.", ring: lastMsg, skip: () => !inThread() },
  { title: "Who's on it", text: "This line says who is working and what they're doing. If an agent is running, the round stop button is here too. It asks you to type the agent's name before anything ends, so you don't need to try it now.", ring: () => q(".cm-strip"), skip: () => !inThread() },
  { title: "When an agent asks you something", text: () => (q('.cm-line[data-key="asks"] button') ? "An agent here is waiting for you. Click Answer to reply to its question." : "A line appears here with an Answer button, and the Chat tab shows a number. The Chat page lists everything waiting for you."), ring: () => q('.cm-line[data-key="asks"] button') || q(".cm-strip"), done: () => (q('.cm-line[data-key="asks"] button') ? !!q(".cp-reply") : false), hasNext: true, skip: () => !inThread() },
  { title: "Reply any time", text: "Write here to carry on the conversation, even while the agent works. The To button shows who gets it, and you can change that. Nothing is sent until you press Send.", ring: () => q('.cp[data-place="thread"] .cp-t'), skip: () => !inThread() },
  { title: "Attach files", text: "Attach adds files to your message. You can also paste them or drop them on the conversation.", ring: () => q('.cp[data-place="thread"] .cp-attach'), skip: () => !inThread() },
  { title: "Look at what it keeps", text: () => `Click Knowledge ${tabWhere()}.`, ring: tab("knowledge"), done: () => here() === "knowledge", enter: toBar("knowledge"), skip: () => !hasTab("knowledge") },
  { title: "Knowledge", text: "Notes, projects and the files agents attach, to browse and search. It only reads. Keys and passwords are blanked out.", ring: () => q(".kb-side") || q('input[type="search"]'), skip: () => here() !== "knowledge" },
  { title: "See every agent", text: () => `Click Agents ${tabWhere()}.`, ring: tab("now"), done: () => here() === "now", enter: toBar("now"), skip: () => !hasTab("now") },
  { title: "Agents", text: "Everyone working right now and what they're doing. Click a name to see its whole session. The row under the top bar has the rest: every agent, the timeline, the map and the history.", ring: () => q("#main table.nowtbl"), skip: () => here() !== "now" },
  { title: "Check your limits", text: () => `Click Usage ${tabWhere()}.`, ring: tab("usage"), done: () => here() === "usage", enter: toBar("usage"), skip: () => !hasTab("usage") },
  { title: "Usage", text: "How much of your Claude limits is left and when it comes back. New agents stop at a usage line so they don't use it all. You can change that line, or pause an account, under Claude accounts.", ring: () => heading("Claude accounts"), skip: () => here() !== "usage" },
  { title: "Settings", text: "Click Settings at the top right (the sliders).", ring: tab("settings"), done: () => here() === "settings", skip: () => !hasTab("settings") },
  { title: "How the Hive runs", text: "Add a second Claude account here. Modules, in the list on the left, turns on optional tools your agents can use.", ring: () => heading("Accounts"), enter: () => { if (here() === "settings") go("settings", "accounts"); }, skip: () => here() !== "settings" },
  { title: "One more", text: () => (q(".more > button") ? "Click More at the top right (the three dots)." : "Tap More at the bottom."), ring: moreBtn, done: () => !!moreBtn() && moreBtn().getAttribute("aria-expanded") === "true", skip: () => !moreBtn() },
  { title: "Help is under More", text: "The user guide is here, Keyboard shortcuts lists them (Ctrl+K jumps to any conversation, agent or page), and Take the tour brings this window back. The guide's last tutorial, How it works, shows how to ask the Hive about itself: how it runs, what's wrong, how to improve it. That's it: say what you want done in Chat.", ring: () => q('.more-i[data-key="help"], .mn-i[data-key="help"]'), last: true },
];

/** The short walk for simple view: chat and Settings only, in the words that view uses. */
const SIMPLE_STEPS = [
  { title: "A quick look around", text: "Two minutes. This window says what to click and waits for you. You can drag it, and leave whenever you like." },
  { title: "Say what you want", text: () => `${q(HOME) ? "Type" : "Click New conversation, then type"} what you'd like done, the way you'd tell a person, and press Start. Your Hive works on it and answers here. This is a real request, so skip it if you're not ready.`, ring: () => q(HOME) || q(".cm-newbtn"), done: () => mine() > sentFrom || OUT.length > outFrom || inThread(), enter: () => { sentFrom = mine(); outFrom = OUT.length; if (parse().arg) go("comms"); }, skip: () => !can("post") },
  { title: "Your conversations", text: "Each request is one conversation, listed here. Click one to read it.", ring: () => q(".cm-tl a.lr-in"), done: inThread, enter: () => { if (here() === "comms" && parse().arg) go("comms"); }, skip: () => inThread() || !threads() },
  { title: "Answers appear here", text: "Your Hive writes back as it goes. You can close this page: the work carries on, and it's all here when you come back.", ring: lastMsg, skip: () => !inThread() },
  { title: "Reply any time", text: "Write here to carry on, even while it works. Nothing is sent until you press Send. You can also attach files.", ring: () => q('.cp[data-place="thread"] .cp-t'), skip: () => !inThread() },
  { title: "Settings", text: "Click Settings at the top right.", ring: tab("settings"), done: () => here() === "settings", skip: () => !hasTab("settings") },
  { title: "Settings", text: "How much of your week is used is here, and how to sign in again. That's all: say what you want done in Chat.", ring: () => heading("Usage limit") || q("#main .sec-t"), enter: () => { if (here() === "settings") go("settings", "usage-limit"); }, last: true, skip: () => here() !== "settings" },
];
let STEPS = FULL_STEPS;

let win = null, ringEl = null, at = 0, timer = 0, raf = 0, dragged = false, moving = 0, placed = "", armed = true;
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
  const stop = win.querySelector(".tw-end"); stop.textContent = first ? "Not now" : "End tour"; stop.hidden = !!s.last;   // Finish already ends it
}
function show(i, dir = 1) {
  clearTimeout(moving); moving = 0;
  while (STEPS[i] && STEPS[i].skip && STEPS[i].skip()) i += dir;
  if (!STEPS[i]) { if (dir > 0) return end(); i = 0; }
  at = i; placed = "";   // a new step: place the window afresh
  const s = step(); if (s.enter) s.enter();
  armed = dir > 0 || !s.done || !s.done();   // back on a step already done: wait for it, or it would bounce straight forward again
  paint();
  setTimeout(() => { const e = s.ring && s.ring(); if (e && step() === s) e.scrollIntoView({ block: "nearest" }); }, 60);
}
const next = () => (step().last ? end() : show(at + 1));

// ---- the ring follows its target through morphs and scrolling; the window stays out of its way ----
const M = 16, GAP = 12, PHONE = 640;   // the window's margin from the screen's edges, its gap from the ring; at or under PHONE it docks to an edge
const topOf = () => (document.getElementById("top") || { offsetHeight: 0 }).offsetHeight + 12;
/** Put the window at left/top (px), or on a phone at the bottom or top edge. */
function moveTo(p) {
  const st = win.style;
  st.left = p.left + "px"; st.right = "auto";
  if (p.dock === "b") { st.top = "auto"; st.bottom = M + "px"; } else { st.top = p.top + "px"; st.bottom = "auto"; }
}
function home() { moveTo(innerWidth <= PHONE ? { left: M, dock: "b" } : { left: M, top: innerHeight - M - win.offsetHeight }); }
const covered = (a, b) => Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left)) * Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top));
function follow() {
  if (!win) return;
  raf = requestAnimationFrame(follow);
  const s = step(), modal = document.querySelector("dialog[open]"), e = !modal && s.ring ? s.ring() : null, r = e && e.getBoundingClientRect();
  ringEl.hidden = !r;
  if (!r) { if (!dragged) { const b = win.getBoundingClientRect(); if (b.width && (b.left < 0 || b.top < 0 || b.right > innerWidth || b.bottom > innerHeight)) { home(); placed = ""; } } return; }   // nothing ringed: keep it on the screen through a resize
  const st = ringEl.style, p = 4; st.left = r.left - p + "px"; st.top = r.top - p + "px"; st.width = r.width + 2 * p + "px"; st.height = r.height + 2 * p + "px";
  if (!dragged) place(r);
}
/** Place the window by the ringed thing: below it if there is room, else above, else beside; on a phone, docked to the bottom or top edge
 *  away from it. A target too big to clear, like a whole conversation, gets the place that covers least of it. The place is kept until the
 *  target, the window or the screen changes size or moves, so the window doesn't hop between places from frame to frame. */
function place(t) {
  const w = win.offsetWidth, ht = win.offsetHeight, W = innerWidth, H = innerHeight, key = [t.left, t.top, t.width, t.height, w, ht, W, H].join();
  if (key === placed) return;
  placed = key;
  const pad = 6, r = { left: t.left - pad, top: t.top - pad, right: t.right + pad, bottom: t.bottom + pad };   // the ring's own box
  const lo = topOf(), clampX = x => Math.max(M, Math.min(x, W - M - w)), clampY = y => Math.max(lo, Math.min(y, H - M - ht));
  const box = p => { const top = p.dock === "b" ? H - M - ht : p.top; return { left: p.left, top, right: p.left + w, bottom: top + ht }; };
  const spots = W <= PHONE
    ? [r.top + r.bottom > H ? { left: M, top: lo } : { left: M, dock: "b" }, r.top + r.bottom > H ? { left: M, dock: "b" } : { left: M, top: lo }]   // the edge further from it first
    : [{ left: clampX(r.left), top: r.bottom + GAP }, { left: clampX(r.left), top: r.top - GAP - ht },
      { left: r.right + GAP, top: clampY(r.top) }, { left: r.left - GAP - w, top: clampY(r.top) },
      { left: M, top: H - M - ht }, { left: W - M - w, top: H - M - ht }, { left: W - M - w, top: lo }, { left: M, top: lo }];   // the corners, for a target nothing clears
  const fits = p => { const b = box(p); return b.left >= M - 0.5 && b.right <= W - M + 0.5 && b.top >= lo - 0.5 && b.bottom <= H - M + 0.5; };
  let best = null, least = Infinity;
  for (const p of spots) { if (!fits(p)) continue; const c = covered(r, box(p)); if (c < least) { best = p; least = c; } if (!c) break; }
  moveTo(best || { left: M, top: lo });
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
  STEPS = simpleOn() ? SIMPLE_STEPS : FULL_STEPS;
  try { localStorage.setItem(simpleOn() ? SEEN_SIMPLE : SEEN, "1"); } catch (e) { /* no storage: it may offer itself again */ }
  win = document.createElement("div"); win.className = "tour-w"; win.setAttribute("role", "dialog"); win.setAttribute("aria-label", "Tour of the app");
  win.innerHTML = `<div class="tw-top"><span class="tw-n meta"></span></div><h3 class="tw-t"></h3><p class="tw-p" aria-live="polite"></p><div class="tw-btns"></div>`;
  win.querySelector(".tw-btns").append(btn("", "tw-end", end), btn("Back", "tw-back", () => show(Math.max(0, at - 1), -1)), btn("", "tw-next", next));
  ringEl = document.createElement("div"); ringEl.className = "tour-ring"; ringEl.setAttribute("aria-hidden", "true"); ringEl.hidden = true;
  document.body.append(ringEl, win);
  drag(win.querySelector(".tw-top"));
  dragged = false; placed = ""; home(); at = 0; show(0);
  timer = setInterval(tick, 200); raf = requestAnimationFrame(follow);
  addEventListener("keydown", onEsc, true);
}
/** Esc leaves the tour, unless it belongs to something else: a dialog, an open menu or a field being typed in. Capture, to see the menu before its own Esc closes it. */
function onEsc(e) {
  if (e.key !== "Escape" || e.target.closest?.("input, textarea, select") || document.querySelector("dialog[open], .more-m, .mn-sheet, .cp-menu")) return;
  end();
}
function end() {
  if (!win) return;
  clearInterval(timer); cancelAnimationFrame(raf); clearTimeout(moving); moving = 0;
  removeEventListener("keydown", onEsc, true);
  win.remove(); ringEl.remove(); win = ringEl = null;
}
/** Offer it once in this browser, once someone is signed in and the install's setup is finished: not over the walkthrough. */
export function offerTour() {
  const look = () => {
    if (win || !S.app || !S.app.app || !can("post") || document.querySelector("dialog[open]")) return;
    const s = S.app.setup || {}; if (!(s.signed_in || s.api_key)) return;
    if (S.app.settings && S.app.settings.setup_done === false) return;
    let seen = "1"; try { seen = localStorage.getItem(simpleOn() ? SEEN_SIMPLE : SEEN); } catch (e) { /* no storage: don't nag */ }
    if (!seen) startTour();
  };
  subscribe(look); look();
}
