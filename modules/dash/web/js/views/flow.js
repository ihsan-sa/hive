// Flow: the hive's work as one moving picture, by goal. One lane per focus in the person's order (the needs-you summary's
// states, as needs_you.py writes them) and one for everything else; time runs into a fixed now line. A run is a bar; a hand-off
// is a bar that begins where the last ended; a wait on the person is a hollow stretch until their next word; a decision taken
// for them is a mark; their own posts are their marks; right of now, the words for what comes next. Below, what moved since
// they last spoke. Reads only what the hive already writes (sessions, posts, status bars, the summary): no new field, no
// agent instruction. The only motion is time, at one speed. Design: projects/ui-redesign/flow-architecture.md.
import { h, s } from "../core/dom.js";
import { S, SYS, can } from "../core/store.js";
import { loadNeedsYou, waitingOn } from "../core/needs.js";
import { isRunning, sEnd, threadChildren, threadTitle, displayText } from "../core/model.js";
import { ago, clock, dayName, dur, mdPlain, now, plural, short, DAY, HOUR, MIN } from "../core/format.js";
import { threadHref, go, href } from "../core/router.js";
import { openSheet } from "../ui/panel.js";
import { card as projectCard } from "../ui/projectcard.js";
import { focusOf, sayPlain as sayPlain0, isCode } from "../core/foci.js";
/** Plain words for the chart and its lists: no ids, jargon or agent codes (an agent is just "an agent", the chart has no room for pills). */
const sayPlain = (t, n) => sayPlain0(String(t || "").replace(/`?@?\b([a-z][0-9a-f]{4})\b`?/g, (m, w) => (isCode(w) ? "an agent" : m)), n);
import { gist } from "../core/gist.js";
import { sayTo } from "../ui/composer.js";
import { canvas } from "../templates.js";
import { button, empty, icon, infoButton, pageNav, seg, toolbar, waiting } from "../ui/kit.js";

const WIN = { "1h": HOUR, "6h": 6 * HOUR, "24h": DAY, "3d": 3 * DAY };
const PRE = [["1h", "1h"], ["6h", "6h"], ["24h", "24h"], ["3d", "3d"]];
const F = { win: "6h", fold: true, v: null, failed: false, raf: 0, last: 0, w: 0, pw: 400, ver: -1, update: () => {} };
const CHAT = "M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v9a1.5 1.5 0 0 1-1.5 1.5H10l-4.5 4v-4h0A1.5 1.5 0 0 1 4 14.5z";
const AXIS = 32, LANE = 64, BOTTOM = 18, ROWS = 4;
const RESULT = /^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*/i;
const ok = () => S.app.app && can("read");
const rungText = r => String(r || "").split(/\s+check:/i)[0].trim();
// a post's lines, parsed once per post (the board is long; the page redraws often)
const LC = new Map();
const lines = p => { let v = LC.get(p.id); if (!v) { if (LC.size > 4000) LC.clear(); v = mdPlain(displayText(p)).split("\n").map(l => l.trim()).filter(Boolean); LC.set(p.id, v); } return v; };
const lineOf = (p, re) => lines(p).find(l => re.test(l));
const at = iso => (iso ? Date.parse(iso) || 0 : 0);

async function refresh(ctx) {
  await loadNeedsYou(true); F.v = S.needsYou;   // the one read of the summary (core/needs.js)
  F.ver = -1; ctx.update();
}

// ---- reading ----
/** The lanes: the summary's focuses in order, each with the threads the summary counts for it (the goal's thread, its project's
 *  and children), then every other thread with a run or a post in the window as one lane. */
function lanes(t0, t1) {
  const states = ((F.v && F.v.states) || []).filter(x => x && x.name && x.thread);
  const focus = states.filter(x => x.kind === "focus"), src = focus.length ? focus : states.slice(0, 8);
  const out = src.map(x => ({ key: x.key, name: x.name, thread: x.thread, threads: new Set([x.thread, ...(Array.isArray(x.threads) ? x.threads : []), ...threadChildren(x.thread)]), state: x }));
  const taken = new Set(out.flatMap(l => [...l.threads]));
  const rest = new Set();
  for (const x of S.sessions) if (x.thread && !taken.has(x.thread) && sEnd(x) >= t0 && x.start <= t1) rest.add(x.thread);
  for (const p of S.posts) if (p.th && !taken.has(p.th) && !SYS.has(p.from) && p.t >= t0 && p.t <= t1) rest.add(p.th);
  if (rest.size) out.push({ key: "rest", name: src.length ? "Everything else" : "All work, no focuses yet", thread: null, threads: rest, state: null, rest: true });
  return out;
}
/** What a lane holds in the window: runs (with hand-offs), waits on the person, decisions, the person's marks, result posts.
 *  A hand-off is a run whose starting post was written by an earlier runner in the lane: the work passed on, not two runs that
 *  happen to touch. Open waits are the one list (core/needs.js: the same entries the Needs page and the Chat badge count), plus the
 *  summary's stale ones drawn as old; a wait the person has since answered is read from the posts: an ask line, then their next word. */
function read(l, t0, t1) {
  const runs = S.sessions.filter(x => l.threads.has(x.thread) && sEnd(x) >= t0 && x.start <= t1).sort((a, b) => a.start - b.start);
  const hand = new Map(), ran = new Set();
  for (const r of runs) { const p = S.byId[r.post]; if (p && p.from !== r.name && ran.has(p.from)) hand.set(r, p.from); ran.add(r.name); }
  const pre = t1 - 3 * WIN[F.win];   // posts a little before the window too: a wait or a hand-off may have started there
  const posts = S.posts.filter(p => l.threads.has(p.th) && !SYS.has(p.from) && p.t <= t1 && p.t >= pre).sort((a, b) => a.t - b.t);
  const v = F.v && !F.v.absent ? F.v : {};
  const waits = [], marks = [], decided = [], results = [], open = {}, parsed = [];
  for (const p of posts) {
    if (p.from === "person") { for (const w of open[p.th] || []) w.until = p.t; open[p.th] = []; marks.push(p); continue; }
    const ls = lines(p), last = ls[ls.length - 1] || "", ask = ls.find(l => /^For you:/i.test(l)) || (/^Needs the person:/i.test(last) ? last : null);
    const dec = lineOf(p, /^Decided:/i), dflt = dec ? dec.replace(/^Decided:\s*/i, "") : "";
    if (ask) { const w = { from: p.t, until: null, text: ask.replace(/^(For you|Needs the person):\s*/i, ""), post: p, dflt }; (open[p.th] = open[p.th] || []).push(w); parsed.push(w); }
    else if (dec) decided.push({ t: p.t, text: dflt, post: p });
    const m = ls.length > 1 && last.match(RESULT);
    if (m) results.push({ t: p.t, word: m[1].toLowerCase(), text: gist(ls.slice(0, -1), last), post: p });
  }
  for (const w of parsed) if (w.until) waits.push(w);   // an ask the person answered: drawn to their word. Still open ones come from the one list below, so every page agrees
  const dfltOf = p => (p.id && S.byId[p.id] && (lineOf(p, /^Decided:/i) || "").replace(/^Decided:\s*/i, "")) || "";
  for (const w of waitingOn()) {
    if (!l.threads.has(w.th)) continue;
    const p = (w.post && S.byId[w.post]) || { th: w.th, id: w.post };
    waits.push({ from: w.t || t1, until: null, stale: false, text: w.text, post: p, dflt: dfltOf(p) });
  }
  for (const w of v.stale || []) {
    if (!w || !l.threads.has(w.thread)) continue;
    const p = S.byId[w.post] || { th: w.thread, id: w.post };
    waits.push({ from: at(w.at) || p.t || t1, until: null, stale: true, text: w.action || "", post: p, dflt: dfltOf(p) });
  }
  const live = runs.filter(x => !x.end && isRunning(x.name));
  return { runs, hand, waits: waits.filter(w => w.until == null || w.until >= t0), marks: marks.filter(p => p.t >= t0), decided: decided.filter(d => d.t >= t0), results: results.filter(r => r.t >= t0), live };
}
/** The words right of now: the open rung, else the last Next: line, else the status bar's second line. */
function nextWords(l, r) { return sayPlain(nextWords0(l, r), 90); }
function nextWords0(l, r) {
  if (l.state && l.state.rung) return rungText(l.state.rung);
  for (let i = r.results.length - 1; i >= 0; i--) { const n = lineOf(r.results[i].post, /^Next:/i); if (n) return n.replace(/^Next:\s*/i, ""); }
  const st = l.thread && S.threadStatus[l.thread]; if (st && st.text) { const l2 = st.text.split("\n").map(x => x.trim()).filter(Boolean)[1]; if (l2) return l2.replace(/^Next:\s*/i, ""); }
  return r.live.length ? "running" : "";
}
/** When the person last spoke anywhere on the board. */
function lastWord() { let t = 0; for (const p of S.posts) if (p.from === "person" && p.t > t) t = p.t; return t; }

// ---- drawing ----
const box = h("div", { class: "fl-box", tabindex: "-1" });
const svg = s("svg", { class: "fl-svg", "aria-label": "The work, by goal, moving" });
box.append(svg);
const stage = h("div", { class: "cvp-stage", "data-own": "", "data-sig": "flow" }, box);
// the lists before and after the chart are data-own boxes in render's tree (a fresh one each render; morph keeps the attached one); draw fills them in place
const pageBox = sel => box.closest(".fl-page")?.querySelector(":scope > " + sel);
const movedBox = () => pageBox(".fl-moved");

function say(l, r) {
  const th = l.thread || (r.live[0] && r.live[0].thread) || (r.runs.length ? r.runs[r.runs.length - 1].thread : null); if (!th) return;
  const who = r.live[0] ? r.live[0].name : r.runs.length ? r.runs[r.runs.length - 1].name : null;
  if (who && !SYS.has(who)) sayTo(th, who);
  go("comms", th);
}
function draw() {
  // a throw here would come back every frame from tick(): say it once in the console and wait for new data
  try { drawNow(); F.err = false; } catch (e) { if (!F.err) console.error("flow: draw failed", e); F.err = true; F.last = now(); F.ver = S.ver; }
}
function drawNow() {
  // a phone (the page under 640) reads the list first and folds the chart: the chart then has no width and is laid out at a stand-in one
  const t = now(), cw = box.clientWidth, W = cw || 800, main = box.closest("#main"), phone = (main ? main.clientWidth : W) < 640;
  const win = WIN[F.win], t1 = t, t0 = t1 - win, narrow = W < 640;
  const G = narrow ? 92 : 160, NX = phone ? 0 : narrow ? 118 : 230, X0 = G, X1 = W - NX - 10, px = (X1 - X0) / win, x = tt => X0 + (tt - t0) * px;
  const L = lanes(t0, t1), H = AXIS + L.length * LANE + BOTTOM;
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`); svg.setAttribute("width", W); svg.setAttribute("height", H); stage.style.height = H + "px";
  const kids = [];
  // axis: a tick each hour (local time, so 3h and 12h steps land on round local hours), a label where it fits
  const step = win <= HOUR ? 10 * MIN : win <= 6 * HOUR ? HOUR : win <= DAY ? 3 * HOUR : 12 * HOUR, off = new Date(t).getTimezoneOffset() * MIN;
  let lastLabel = -1e9, lastDay = "";
  for (let tt = Math.ceil((t0 - off) / step) * step + off; tt < t1; tt += step) {
    const xx = x(tt); kids.push(s("line", { class: "fl-grid", x1: xx, x2: xx, y1: AXIS, y2: H - BOTTOM }));
    const day = dayName(tt).replace(/^\w+,? /, ""), label = win >= DAY && day !== lastDay ? day + " " + clock(tt) : clock(tt), w = label.length * 5.6;
    if (xx - w / 2 > Math.max(X0 + 4, lastLabel) && xx + w / 2 < X1 - 32) { kids.push(s("text", { x: xx, y: 14, "text-anchor": "middle" }, label)); lastLabel = xx + w / 2 + 8; lastDay = day; }
  }
  kids.push(s("line", { class: "fl-nowl", x1: X1, x2: X1, y1: AXIS - 4, y2: H - BOTTOM }));
  kids.push(s("text", { class: "fl-nowt", x: X1 - 4, y: 14, "text-anchor": "end" }, "now"));
  if (!phone) kids.push(s("text", { class: "fl-nextt", x: X1 + 12, y: 14 }, "next"));
  const you = lastWord();
  if (you > t0 && you < t1) { kids.push(s("line", { class: "fl-you", x1: x(you), x2: x(you), y1: AXIS - 4, y2: H - BOTTOM })); const nearNow = x(you) > X1 - 90; kids.push(s("text", { class: "fl-yout", x: x(you) + (nearNow ? -3 : 3), y: H - 5, "text-anchor": nearNow ? "end" : "start" }, "you, " + ago(you))); }
  const since = [], list = [];
  L.forEach((l, i) => {
    const y = AXIS + i * LANE, r = read(l, t0, t1), cold = !r.live.length;
    // a goal's lane opens its project card in the panel (the thread, the agents and the ladder's rung are one click down in it), its full page the focus page (else the thread);
    // the bars, waits and say inside keep their own targets
    const open = l.state ? e => { if (e.target.closest("a,.fl-say")) return; openSheet("work:" + l.key, l.name, () => h("div", { class: "wk-sheet" }, projectCard(l.state)), focusOf(l.thread) ? href("focus", focusOf(l.thread)) : threadHref(l.thread)); } : null;
    const g = s("g", { class: "fl-lane", "data-key": l.key, "data-live": r.live.length ? "" : null, "data-cold": cold && !r.runs.length ? "" : null, "data-card": open ? "" : null, onclick: open });
    g.append(s("rect", { class: "fl-bg", x: 0, y, width: W, height: LANE }));
    g.append(s("line", { class: "fl-rule", x1: X0, x2: X1, y1: y + LANE - 1, y2: y + LANE - 1 }));
    // the lane's name: for a goal a button for its card, for the rest lane plain words
    const name = s("text", { class: "fl-name", "data-live": r.live.length ? "" : null, x: 8, y: y + 26, role: open ? "button" : null, tabindex: open ? "0" : null, onkeydown: open ? e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(e); } } : null }, short(l.name, narrow ? 14 : 26));
    if (open) name.append(s("title", null, `${l.name}: open its card`));
    g.append(name);
    const openW0 = r.waits.filter(w => !w.until);
    const sub = openW0.length && !l.rest ? "waits on you \u00b7 " + dur(now() - Math.min(...openW0.map(w => w.from))) : l.rest && r.runs.length ? `${plural(r.runs.length, "run")} in ${plural(l.threads.size, "conversation")}` : r.live.length ? r.live.length + " running" : r.runs.length ? (now() - sEnd(r.runs[r.runs.length - 1]) < DAY ? "quiet since " + clock(sEnd(r.runs[r.runs.length - 1])) : "last run " + ago(sEnd(r.runs[r.runs.length - 1]))) : "cold";
    g.append(s("text", { class: "fl-sub", "data-cold": cold ? "" : null, "data-wait": openW0.length && !l.rest ? "" : null, x: 8, y: y + 44 }, short(sub, narrow ? 14 : 28)));
    // runs
    const rows = []; // for the rest lane: greedy packing by end time, ROWS deep
    for (const run of r.runs) {
      const live = !run.end && isRunning(run.name), b = Math.min(x(live ? t : sEnd(run)), X1), a = Math.min(Math.max(x(run.start), X0), b - 2), w = b - a;
      let by = y + 18, bh = 12;
      if (l.rest) { let i = rows.findIndex(e => e <= a); if (i < 0) { if (rows.length < ROWS) { rows.push(0); i = rows.length - 1; } else i = r.runs.indexOf(run) % ROWS; } rows[i] = b + 1; by = y + 8 + i * 11; bh = 8; }
      const hit = s("a", { href: threadHref(run.thread), class: "fl-hit" });
      const from = r.hand.get(run);
      hit.append(s("title", null, `${run.name}${live ? " is on " : " ran in "}${threadTitle(run.thread)}, ${live ? "since " + ago(run.start) : "for " + dur(sEnd(run) - run.start)}${from ? "\nHanded on by " + from : ""}`));
      hit.append(s("rect", { class: "fl-bar", "data-live": live ? "" : null, "data-hand": from || null, x: a, y: by, width: w, height: bh, rx: 2 }));
      if (from && x(run.start) >= X0 && !l.rest) hit.append(s("path", { class: "fl-hand", d: `M${a} ${y + 24} l-4 -4 M${a} ${y + 24} l-4 4` }));
      g.append(hit);
    }
    // waits on the person: hollow, dashed, until their next word. The open ones in a lane are one band (a lane with twelve old asks
    // is one line, not a solid block): from the oldest, to now, with a tick where each later one began and all of them in the tip.
    const tip = wt => `${wt.text}${wt.stale ? " (old: the conversation has moved on since)" : ""}`;
    const openW = r.waits.filter(w => !w.until).sort((p, q) => p.from - q.from);
    for (const wt of r.waits.filter(w => w.until)) {
      const a = Math.max(x(wt.from), X0), b = Math.min(x(wt.until), X1);
      const hit = s("a", { href: threadHref(wt.post.th), class: "fl-hit" });
      hit.append(s("title", null, `For you: ${wt.text}\nYou answered ${ago(wt.until)}`));
      hit.append(s("rect", { class: "fl-wait", x: a, y: y + 38, width: Math.max(3, b - a), height: 10, rx: 5 }));
      g.append(hit);
    }
    if (openW.length) {
      const first = openW[0], a = Math.max(x(first.from), X0), b = X1, allOld = openW.every(w => w.stale);
      const hit = s("a", { href: threadHref(openW[openW.length - 1].post.th), class: "fl-hit" });
      const text = openW.length === 1 ? `For you: ${first.text}${first.dflt ? "\nDefault: " + first.dflt : "\nMeanwhile the Hive carries on."}${first.stale ? "\nOld: the conversation has moved on since" : ""}`
        : `${openW.length} things wait on you here, the oldest since ${ago(first.from)}:\n` + openW.slice().reverse().slice(0, 6).map(w => "• " + tip(w)).join("\n") + (openW.length > 6 ? `\n… and ${openW.length - 6} more; the Needs page has them all` : "");
      hit.append(s("title", null, text));
      hit.append(s("rect", { class: "fl-wait", "data-open": "", "data-stale": allOld ? "" : null, "data-n": openW.length, x: a, y: y + 38, width: Math.max(3, b - a), height: 10, rx: 5 }));
      for (const w of openW.slice(1)) if (x(w.from) > a + 2) hit.append(s("line", { class: "fl-waitt", x1: x(w.from), x2: x(w.from), y1: y + 38, y2: y + 48 }));
      g.append(hit);
    }
    for (const d of r.decided) { const hit = s("a", { href: threadHref(d.post.th), class: "fl-hit" }); hit.append(s("title", null, "Decided for you: " + d.text)); hit.append(s("path", { class: "fl-dec", d: `M${x(d.t)} ${y + 44} l4 4 l-4 4 l-4 -4 z` })); g.append(hit); }
    for (const p of r.marks) { const hit = s("a", { href: threadHref(p.th), class: "fl-hit" }); hit.append(s("title", null, "You, " + ago(p.t) + ": " + short(mdPlain(displayText(p)), 120))); hit.append(s("path", { class: "fl-mark", d: l.rest ? `M${x(p.t)} ${y + LANE - 8} l3 6 l-6 0 z` : `M${x(p.t)} ${y + 50} l4 7 l-8 0 z` })); g.append(hit); }
    // right of now: the words for what comes next (a goal's; the rest lane has no one direction); on a phone they are in the list instead
    const nx = l.rest ? "" : openW.length ? sayPlain(openW[openW.length - 1].text, 60) : nextWords(l, r);
    list.push({ l, r, open, sub, nx, openW });
    if (nx && !phone) {
      const tx = s("text", { class: "fl-next", "data-wait": openW.length ? "" : null, x: X1 + 12, y: y + 26 }, short(nx, narrow ? 18 : 38)); tx.append(s("title", null, nx));
      if (openW.length) { const lk = s("a", { href: threadHref(openW[openW.length - 1].post.th), class: "fl-hit" }); lk.append(tx); g.append(lk); } else g.append(tx);
    }
    if (l.thread && !phone) {
      const sy = s("g", { class: "fl-say", role: "button", tabindex: "0", onclick: () => say(l, r), onkeydown: e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); say(l, r); } } });
      sy.append(s("title", null, "Say something to whoever is on " + l.name));
      sy.append(s("rect", { x: X1 + 12, y: y + 34, width: 28, height: 28, rx: 8 }), s("path", { class: "fl-say-ic", d: CHAT, transform: `translate(${X1 + 18} ${y + 40}) scale(.67)` }));
      g.append(sy);
    }
    kids.push(g);
    for (const rs of r.results) if (rs.t > you && rs.text) since.push({ lane: l.name, ...rs });
  });
  svg.replaceChildren(...kids);
  // on a phone the list comes first: each goal's name, state and the whole of its next words, with say; the chart is folded under it (css shows it there only)
  const lb = pageBox(".fl-listbox");
  if (lb) lb.replaceChildren(
    h("h2", { class: "sec-t" }, "Next, by goal"),
    h("ul", { class: "fl-nextl" }, list.map(({ l, r, open, sub, nx, openW }) => h("li", { class: "fl-row", "data-wait": openW.length ? "" : null, "data-key": l.key, "data-live": r.live.length ? "" : null },
      open ? h("button", { type: "button", class: "fl-rn", title: `${l.name}: open its card`, onclick: open }, l.name) : h("span", { class: "fl-rn" }, l.name),
      l.thread ? h("button", { type: "button", class: "btn fl-rsay", "data-size": "s", "data-kind": "quiet", title: "Say something to whoever is on " + l.name, "aria-label": "Say something to whoever is on " + l.name, onclick: () => say(l, r) }, icon("chat")) : null,
      h("span", { class: "meta fl-rs" }, openW.length ? null : sub, openW.length ? [h("a", { class: "fl-rw", href: threadHref(openW[openW.length - 1].post.th) }, openW.length === 1 ? "waits on you" : openW.length + " wait on you")] : null),
      nx ? h("p", { class: "fl-rx" }, nx) : null))),
    button(F.fold ? "Show the timeline" : "Hide the timeline", () => { F.fold = !F.fold; F.update(); }, { size: "s", kind: "quiet", cls: "fl-fold" }));
  // below the chart: what finished since the person last spoke, within the window; when their last word is older than the window, the window
  since.sort((a, b) => b.t - a.t);
  const inWin = you >= t0, head = inWin ? `Since you last spoke, ${ago(you)}` : `Finished in the last ${F.win}` + (you ? ` (you last spoke ${ago(you)})` : "");
  const mv = movedBox();
  if (mv) mv.replaceChildren(
    h("h2", { class: "sec-t" }, head),
    since.length ? h("ul", { class: "fl-list" }, since.slice(0, 12).map(rs => h("li", null, h("span", { class: "fl-lanename" }, rs.lane), h("a", { href: threadHref(rs.post.th) }, sayPlain(rs.text, 140)), h("span", { class: "meta" }, ago(rs.t) + (rs.word !== "done" ? " · " + rs.word : "")))))
      : h("p", { class: "meta" }, L.length ? "Nothing finished in this window yet; the bars above are the work under way." : "Nothing ran in this window."));
  F.w = cw; F.pw = Math.max(40, X1 - X0); F.ver = S.ver;
  F.last = mv ? t : 0;  // no list box yet (drawn before the page attached): the next frame draws again
}
function tick() {
  F.raf = requestAnimationFrame(tick);
  if (!box.isConnected) return;  // not on the page yet (the app attaches a view after its render): keep the loop, leave() ends it
  // time is the only motion: one redraw per pixel of time (at least every 5 s), none while the tab is hidden
  const t = now(), perPx = WIN[F.win] / F.pw;
  if (!document.hidden && (t - F.last >= Math.max(5e3, perPx) || F.ver !== S.ver || box.clientWidth !== F.w)) draw();
}

export default {
  id: "flow", label: "Flow", about: "the work by goal, moving: runs, hand-offs, what waits on you, what comes next",
  tab: ok,
  enter(ctx) { F.update = ctx.update; F.ver = -1; if (ok()) refresh(ctx); if (!F.raf) F.raf = requestAnimationFrame(tick); },
  leave() { if (F.raf) cancelAnimationFrame(F.raf); F.raf = 0; },
  poll: { every: 5 * MIN, run(ctx) { return refresh(ctx); } },
  render(ctx) {
    F.update = ctx.update;
    const title = "Flow";
    if (!ok()) return h("div", { class: "page" }, pageNav(), empty("This page isn't available on this connection.", { kind: "page", title }));
    if (!S.live) return h("div", { class: "page" }, pageNav(), waiting(S.failed, "Reading…", "the flow"));
    if (box.isConnected && (F.ver !== S.ver || box.clientWidth !== F.w)) draw(); else if (!box.isConnected) queueMicrotask(() => { if (box.isConnected) { draw(); if (!F.raf) F.raf = requestAnimationFrame(tick); } });
    const bar = toolbar(
      h("h1", { class: "ph-title" }, title),   // the Nov 8 handoff §5.2: the page names itself, the legend sits right
      seg(PRE, F.win, v => { F.win = v; F.ver = -1; draw(); }, { label: "Window" }),
      h("span", { class: "meta fl-legend" }, h("i", { class: "fl-k fl-k-run" }), " ran ", h("i", { class: "fl-k fl-k-live" }), " running ", h("i", { class: "fl-k fl-k-wait" }), " waits on you ", h("i", { class: "fl-k fl-k-you" }), " you"),   // "decided for you" left the key; the diamond keeps its <title> (§5.2)
      infoButton("About this chart", "One lane per goal in your order; time runs into now. Open a lane for its card, a bar for its conversation; the speech bubble speaks to whoever is on it."));
    const nav = pageNav();   // the page row (R7 step 6): outside the canvas, which a phone folds away
    return h("div", { class: "fl-page", "data-fold": F.fold ? "" : null }, nav && h("div", { class: "cvp-nav" }, nav), h("div", { class: "fl-listbox", "data-own": "", "data-sig": "flow" }), canvas({ key: "flow", bar, note: null, stage }), h("div", { class: "fl-moved", "data-own": "", "data-sig": "flow" }));
  },
};
