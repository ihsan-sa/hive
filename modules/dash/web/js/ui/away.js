// While you were away: on coming back after AWAY_GAP or more, a 15-line brief of what was made, decided, needs you and broke, with all of it in a fold (core/away.py).
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
  const a = A.v; if (!a || !a.counts || !Object.values(a.counts).some(Boolean)) return null;
  const line = r => { const label = (r.evidence || []).join(", "); return [r.thread ? [threadRef(r.thread), ": "] : null, label ? label + ": " : "", r.text]; };
  const group = (title, rows, cls) => rows.length ? h("div", { class: "away-g" }, h("h3", { class: "away-h" }, title), h("ul", { class: "away-l" + (cls ? " " + cls : "") }, rows.map(r => h("li", null, r)))) : null;
  // The brief: the server says how many rows of each section fit in 15 lines, so the card and the terminal brief show the same rows.
  const brief = (title, key) => { const rows = a[key], n = a.show[key]; return group(`${title} (${n === rows.length ? n : `${n} of ${rows.length}`})`, rows.slice(0, n).map(line), "away-brief"); };
  // Needs you heads with the one count (open_on_person in core/away.py); the rows are lines of it, and several can share a thread.
  const needsHead = n => `${a.needs_head || "Needs you"} (${plural(n, "line")}${n < a.needs.length ? " shown" : ""})`;
  const count = (title, rows, f = line) => group(`${title} (${rows.length})`, rows.map(f));
  const wait = a.agents_wait, back = a.stopped.filter(x => x.carried_on).length;
  return section({ title: "While you were away", note: "since " + clock(a.since * 1e3) }, h("div", { class: "card away", "data-key": "away" },
    brief("Made", "made"), brief("Decided for you", "decided"), group(needsHead(a.show.needs), a.needs.slice(0, a.show.needs).map(line), "away-brief"),
    wait.length ? h("p", { class: "away-wait meta" }, `Agents wait on: ${wait[0].text}${wait.length > 1 ? `; and ${wait.length - 1} more` : ""}.`) : null,
    brief("Broke", "broke"),
    h("div", { class: "away-foot" },
      section({ fold: true, key: "away-all", title: "All of it", summary: `${plural(a.updates.length, "other thread")} with updates, ${plural(a.stopped.length, "agent")} stopped (${back} carried on)` },
        count("Made", a.made), count("Decided for you", a.decided), group(needsHead(a.needs.length), a.needs.map(line)), count("Agents wait on", wait), count("Broke", a.broke),
        count("Other updates", a.updates), count("Stopped", a.stopped, x => `${x.name}: ${x.why}; ${x.then}.`)),
      button("Got it", () => awayDone(ctx), { size: "s", kind: "quiet" }))));
}
