// While you were away: on coming back after AWAY_GAP or more, what was delivered, what stopped and why, what waits on you (core/away.py).
// On the app's home and on Agents. It stays until "Got it", through a reload: the time you left is kept (awaySince) until then.
import { h } from "../core/dom.js";
import { S, can, jget, getPref, setPref } from "../core/store.js";
import { clock, now, plural, HOUR, MIN } from "../core/format.js";
import { section, button } from "./kit.js";
import { threadRef } from "./refs.js";

const AWAY_GAP = 4 * HOUR, A = { v: null, ctx: null };
async function awayLoad() {
  const since = getPref("awaySince", 0); if (!since || A.v) return;
  try { A.v = await jget(`/api/away?since=${Math.min(336, Math.ceil((now() - since) / HOUR))}h`); } catch (e) { return; }
  if (A.ctx) A.ctx.update();
}
function awayTick() {
  if (document.visibilityState !== "visible") return;
  const was = getPref("awaySeen", 0), t = now();
  setPref("awaySeen", t);
  if (was && t - was >= AWAY_GAP && !getPref("awaySince", 0)) setPref("awaySince", was);
  awayLoad();
}
/** Once, from the app's first render, whichever page it is on: the time you were last here is kept from then. */
export function awayWire(ctx) {
  if (A.ctx || !S.app.app || !can("read")) return;
  A.ctx = ctx;
  addEventListener("visibilitychange", awayTick); setInterval(awayTick, MIN);
  awayTick();
}
function awayDone(ctx) { A.v = null; setPref("awaySince", 0); ctx.update(); }
export function awayCard(ctx) {
  const a = A.v; if (!a || !((a.delivered || []).length + (a.stopped || []).length + (a.waiting || []).length)) return null;
  const group = (title, rows) => rows.length ? h("div", { class: "away-g" }, h("h3", { class: "away-h" }, title), h("ul", { class: "away-l" }, rows.map(r => h("li", null, r)))) : null;
  const shown = 5, more = n => n > shown ? [h("span", { class: "meta" }, `and ${n - shown} more`)] : [];
  const stops = new Map();
  for (const x of a.stopped) { const k = `${x.why}; ${x.then}`; stops.set(k, (stops.get(k) || 0) + 1); }
  return section({ title: "While you were away", note: "since " + clock(a.since * 1e3) }, h("div", { class: "card away", "data-key": "away" },
    group("Waiting on you", a.waiting.map(w => w.thread ? [threadRef(w.thread), ": ", w.text] : w.text)),
    group("Stopped", [...stops].map(([k, n]) => `${plural(n, "agent")}: ${k}.`)),
    group("Delivered", [...a.delivered.slice(0, shown).map(d => [threadRef(d.thread), ": ", d.text]), ...more(a.delivered.length)]),
    button("Got it", () => awayDone(ctx), { size: "s", kind: "quiet" })));
}
