// Progress: is the hive getting better, and is anything owed? The scoreboard (core/progress.py), the asks register
// (~/asks.jsonl), runs' outcomes (~/outcomes.jsonl) and the improvements ledger (~/improvements.jsonl), from /api/progress.
// The server caches the scoreboard for a few minutes and answers stale while it re-reads, so this page never waits on it.
import { h } from "../core/dom.js";
import { S, can, jget } from "../core/store.js";
import { ago, pct, plural, short } from "../core/format.js";
import { metrics } from "../templates.js";
import { empty, pageHeader, section, seg, stats } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { table } from "../ui/table.js";

const P = { v: null, failed: false, asks: "owed", outs: "all" };
const OWED = new Set(["open", "owned", "held", "person"]);
const ASK_TONE = { open: "warn", owned: "info", held: "quiet", person: "info", done: "good" };
const ASK_TEXT = { open: "open, nobody on it", owned: "owned", held: "held", person: "waits on the person", done: "done" };
const GRADE_TONE = { delivered: "good", partial: "warn", nothing: "bad", silent: "bad", quiet: "quiet", not_asked: "quiet" };
const GRADE_TEXT = { delivered: "delivered", partial: "partial", nothing: "nothing", silent: "silent", quiet: "quiet", not_asked: "not asked" };
const IMP_TONE = { landed: "info", held: "good", reverted: "bad", inconclusive: "quiet" };
// The scoreboard's lines, in plain words. lower: a fall is the good direction.
const LINES = [
  ["asks_open", "Asks open", true], ["asks_oldest_open_d", "Oldest open ask, days", true], ["asks_orphaned", "Asks nobody is on", true], ["asks_done", "Asks done"],
  ["outcome_delivered_pct", "Runs that delivered, %"], ["outcomes_silent", "Runs that went silent", true], ["outcomes_nothing", "Runs that did nothing", true], ["outcomes_graded", "Runs graded"],
  ["person_noticed_first", "Problems the person saw first", true], ["person_reply_p50_s", "Reply to the person, median s", true], ["person_reply_p90_s", "Reply to the person, p90 s", true], ["watchdog_trips", "Watchdog trips", true], ["stalled_threads", "Stalled threads", true],
  ["improvements_landed", "Improvements landed"], ["improvements_judged", "Improvements judged"], ["improvements_overdue", "Judgements overdue", true], ["held", "Held after judgement"], ["reverted", "Reverted", true],
  ["runs", "Runs"], ["fail_pct", "Runs that failed, %", true], ["keeper_rounds", "Keeper rounds"], ["improve_rounds", "Improver rounds"], ["core_commits", "Core commits"], ["spend_usd", "Spent, $", true],
];

const tone = (t, text, title) => h("span", { class: "pg-t", "data-tone": t || null, title: title || null }, text);
const when = iso => { const t = Date.parse(iso || ""); return Number.isFinite(t) ? h("span", { title: iso }, ago(t)) : "?"; };
const num = v => (v == null ? "–" : typeof v === "number" && !Number.isInteger(v) ? v.toFixed(1) : String(v));

function lead(V) {
  const B = V.scoreboard && V.scoreboard.now, owed = V.asks.filter(a => OWED.has(a.status)), orphan = owed.filter(a => a.orphaned).length;
  const due = V.improvements.filter(i => i.due).length, c = V.outcomes.counts, graded = Object.entries(c).filter(([g]) => g !== "quiet" && g !== "not_asked").reduce((a, [, n]) => a + n, 0);
  const bits = [`${plural(owed.length, "ask")} owed${orphan ? `, ${orphan} with nobody on them` : ""}`];
  if (graded) bits.push(`${pct(c.delivered || 0, graded)} of the ${plural(graded, "graded run")} in the last day delivered`);
  if (due) bits.push(`${plural(due, "improvement")} due for judgement`);
  const worse = B && V.scoreboard.worse ? V.scoreboard.worse.length : 0;
  const text = bits.join(". ") + "." + (B ? (worse ? ` ${plural(worse, "line")} of the scoreboard got worse since the last day.` : " No line of the scoreboard got worse since the last day.") : " The scoreboard hasn't been read yet.");
  return { text, tone: orphan || due || (c.silent || 0) + (c.nothing || 0) > 0 ? "warn" : null };
}

function tiles(V) {
  const B = V.scoreboard && V.scoreboard.now; if (!B) return null;
  const worse = new Set((V.scoreboard.worse || []).map(w => w[1]));
  const t = (k, label, value, note) => ({ label, value, note, tone: worse.has(k) ? "bad" : null, title: worse.has(k) ? `${label}: worse than the day before` : null });
  return stats([
    t("asks_open", "Asks open", num(B.asks_open), `oldest ${num(B.asks_oldest_open_d)} days; ${plural(B.asks_orphaned || 0, "orphan")}`),
    t("outcome_delivered_pct", "Runs that delivered", B.outcome_delivered_pct != null ? num(B.outcome_delivered_pct) + "%" : "–", `${plural(B.outcomes_graded || 0, "run")} graded; ${B.outcomes_silent || 0} silent, ${B.outcomes_nothing || 0} did nothing`),
    t("person_noticed_first", "Person saw it first", num(B.person_noticed_first), "problems the hive should have caught"),
    t("improvements_landed", "Improvements landed", num(B.improvements_landed), `${B.improvements_judged || 0} judged, ${B.improvements_overdue || 0} overdue`),
    t("fail_pct", "Runs that failed", B.fail_pct != null ? num(B.fail_pct) + "%" : "–", `of ${plural(B.runs || 0, "run")}; ${B.stalled_threads || 0} stalled threads`),
  ]);
}

function asksSection(V, update) {
  const rows = V.asks.filter(a => P.asks === "all" || (P.asks === "owed" ? OWED.has(a.status) : a.status === "done"))
    .sort((a, b) => (a.orphaned !== b.orphaned ? (a.orphaned ? -1 : 1) : (a.when || "") < (b.when || "") ? -1 : 1));
  return section({ title: "Asks", key: "asks", note: "Every ask from the person, read out of the board every hour. Nothing is dropped: an ask is open until an agent marks it done with evidence, or the person says so.",
    actions: seg([["owed", "Owed"], ["done", "Done lately"], ["all", "All"]], P.asks, k => { P.asks = k; update(); }, { label: "Which asks" }) },
    table({ key: r => r.id, empty: P.asks === "owed" ? "Nothing is owed." : "No asks here.", caption: "Asks",
      columns: [
        { label: "Asked", render: r => when(r.when) },
        { label: "Ask", clip: true, render: r => h("span", { title: r.ask || "" }, r.ask || "") },
        { label: "Status", render: r => tone(r.orphaned ? "bad" : ASK_TONE[r.status], r.orphaned ? "orphaned" : ASK_TEXT[r.status] || r.status, r.orphaned ? "owned, but its owner is gone and nobody posted" : r.why || null) },
        { label: "Owner", render: r => (r.owner ? agentRef(r.owner) : "–") },
        { label: "Thread", render: r => threadRef(r.thread) },
        { label: "Evidence", clip: true, render: r => h("span", { title: r.evidence || "" }, short(r.evidence || "", 80)) },
      ], rows }));
}

function outcomesSection(V, update) {
  const c = V.outcomes.counts, order = ["delivered", "partial", "nothing", "silent", "quiet", "not_asked"];
  const rows = V.outcomes.recent.filter(r => P.outs === "all" || r.grade === P.outs);
  return section({ title: "Runs' outcomes", key: "outcomes", note: "Each run the person woke is graded an hour after it ends: did it deliver what the post asked? Silent means it posted nothing at all.",
    actions: seg([["all", "All"], ...order.filter(g => c[g]).map(g => [g, `${GRADE_TEXT[g]} ${c[g]}`])], P.outs, k => { P.outs = k; update(); }, { label: "Which grade" }) },
    table({ key: r => r.key, empty: "No run graded in the last day.", caption: "Runs' outcomes in the last day",
      columns: [
        { label: "Ended", render: r => when(r.ended) },
        { label: "Agent", render: r => agentRef(r.name) },
        { label: "Thread", render: r => threadRef(r.thread) },
        { label: "Grade", render: r => tone(GRADE_TONE[r.grade], GRADE_TEXT[r.grade] || r.grade) },
        { label: "Why", clip: true, render: r => h("span", { title: r.why || "" }, r.why || "") },
      ], rows }));
}

function improvementsSection(V) {
  const rows = [...V.improvements].sort((a, b) => (a.due !== b.due ? (a.due ? -1 : 1) : (a.at || "") > (b.at || "") ? -1 : 1));
  return section({ title: "Improvements", key: "improvements", note: "Every change to the hive itself, with the measure it should move and the day it is judged on. A landed change isn't an improvement until the measure says so." },
    table({ key: r => r.id, empty: "No improvement logged yet.", caption: "Improvements",
      columns: [
        { label: "Logged", render: r => when(r.at) },
        { label: "By", render: r => (r.by ? agentRef(r.by) : "–") },
        { label: "What", clip: true, render: r => h("span", { title: r.what || "" }, r.what || "") },
        { label: "Measure", wrap: true, render: r => short(r.measure || "", 90) },
        { label: "Status", render: r => tone(r.due ? "warn" : IMP_TONE[r.status], r.due ? "due for judgement" : r.status, r.why || (r.due ? `revisit day ${r.revisit} has passed` : null)) },
        { label: "Judge on", render: r => r.revisit || "–" },
      ], rows }));
}

function scoreboardSection(V) {
  const B = V.scoreboard; if (!B || !B.now) return null;
  const now = B.now, before = B.before || {}, worse = new Set((B.worse || []).map(w => w[1]));
  const rows = LINES.filter(([k]) => now[k] != null).map(([k, label, lower]) => ({ k, label, lower, now: now[k], before: before[k] }));
  const change = r => {
    if (r.before == null || r.now === r.before) return r.before == null ? "–" : "same";
    const up = r.now > r.before, good = r.lower ? !up : up;
    return tone(worse.has(r.k) ? "bad" : good ? "good" : "quiet", `${up ? "up" : "down"} from ${num(r.before)}`);
  };
  return section({ title: "Scoreboard", key: "scoreboard", note: `Every measure from \`hive progress\`, the last ${now.since || "day"} against the ${now.since || "day"} before${B.stale ? ", from the last snapshot (the live read failed)" : ""}. Lower is better where it says so.` },
    table({ key: r => r.k, caption: "Scoreboard", columns: [
      { label: "Measure", render: r => r.label + (r.lower ? " (lower is better)" : "") },
      { label: "Now", num: true, render: r => num(r.now) },
      { label: "Before", num: true, render: r => num(r.before) },
      { label: "Change", render: change },
    ], rows }));
}

async function refresh(ctx) {
  try { P.v = await jget("/api/progress"); P.failed = false; } catch (e) { P.failed = true; }
  ctx.update();
}

export default {
  id: "progress", label: "Progress", about: "what is owed, what landed, and whether the hive is getting better", tab: () => can("read"),
  enter(ctx) { if (can("read")) refresh(ctx); },
  poll: { every: 60000, run(ctx) { return refresh(ctx); } },
  render(ctx) {
    const V = P.v;
    if (!V) return h("div", { class: "page" }, P.failed ? empty("Couldn't read the progress data.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    const head = lead(V), update = () => ctx.update();
    return metrics({
      header: pageHeader({ title: "Progress", lead: head.text, tone: head.tone, meta: [S.app.app ? null : "hive progress, hive asks, hive outcomes, hive improve"].filter(Boolean) }),
      sections: [tiles(V), asksSection(V, update), outcomesSection(V, update), improvementsSection(V), scoreboardSection(V)].filter(Boolean),
    });
  },
};
