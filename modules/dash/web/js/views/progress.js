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
const ASK_TONE = { open: "warn", owned: "good", held: "quiet", person: "info", done: "quiet" };
const ASK_TEXT = { open: "open, nobody on it", owned: "owned", held: "held", person: "waits on the person", done: "done" };
const ASK_TEXT_APP = { open: "nobody on it yet", owned: "in hand", held: "held", person: "waits on you", done: "done", orphaned: "owner gone" };
const app = () => !!S.app.app;
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
  const nobody = owed.filter(a => a.status === "open").length, lone = orphan + nobody;
  const due = V.improvements.filter(i => i.due).length, c = V.outcomes.counts, graded = Object.entries(c).filter(([g]) => g !== "quiet" && g !== "not_asked").reduce((a, [, n]) => a + n, 0);
  const needs = lone || due || (c.silent || 0) + (c.nothing || 0) > 0;
  const title = owed.length ? `${plural(owed.length, "ask")} ${owed.length === 1 ? "is" : "are"} owed${lone ? `, and ${lone === 1 ? "one has nobody on it" : lone + " have nobody on them"}` : ""}.` : "Nothing is owed.";
  const bits = [];
  if (graded) bits.push(`${pct(c.delivered || 0, graded)} of the ${plural(graded, "graded run")} in the last day delivered.`);
  if (due) bits.push(`${plural(due, "improvement")} due for judgement.`);
  else if (B) { const worse = V.scoreboard.worse ? V.scoreboard.worse.length : 0; bits.push(worse ? `${plural(worse, "line")} of the scoreboard got worse.` : "Nothing on the scoreboard got worse."); }
  else bits.push("The scoreboard hasn't been read yet.");
  return { title, text: bits.slice(0, 2).join(" "), state: needs ? { tone: "warn", text: "Needs a look" } : { tone: "good", text: "All fine" }, tone: needs ? "warn" : null };
}

function tiles(V) {
  const B = V.scoreboard && V.scoreboard.now; if (!B) return null;
  const worse = new Set((V.scoreboard.worse || []).map(w => w[1]));
  // The register is live and the scoreboard hourly, so this counts what the lead and the table below count.
  const owed = V.asks.filter(a => OWED.has(a.status)), lone = owed.filter(a => a.orphaned || a.status === "open").length;
  const t = (k, label, value, note) => ({ label, value, note, worse: worse.has(k) ? "worse than the day before" : null });
  return stats([
    t("asks_open", "Asks open", owed.length, `oldest ${num(B.asks_oldest_open_d)} days; ${lone} with nobody on them`),
    t("outcome_delivered_pct", "Runs that delivered", B.outcome_delivered_pct != null ? Math.round(B.outcome_delivered_pct) + "%" : "–", `${plural(B.outcomes_graded || 0, "run")} graded; ${B.outcomes_silent || 0} silent, ${B.outcomes_nothing || 0} did nothing`),
    t("person_noticed_first", app() ? "You saw it first" : "Person saw it first", num(B.person_noticed_first), "problems the hive should have caught"),
    t("improvements_landed", "Improvements landed", num(B.improvements_landed), `${B.improvements_judged || 0} judged, ${B.improvements_overdue || 0} overdue`),
    t("fail_pct", "Runs that failed", B.fail_pct != null ? num(B.fail_pct) + "%" : "–", `of ${plural(B.runs || 0, "run")}; ${plural(B.stalled_threads || 0, app() ? "stalled conversation" : "stalled thread")}`),
  ], { card: true });
}

function asksSection(V, update) {
  const rows = V.asks.filter(a => P.asks === "all" || (P.asks === "owed" ? OWED.has(a.status) : a.status === "done"))
    .sort((a, b) => (a.orphaned !== b.orphaned ? (a.orphaned ? -1 : 1) : (a.when || "") < (b.when || "") ? -1 : 1));
  return section({ title: "Asks", key: "asks", info: "Every ask from the person, read out of the board every hour. Nothing is dropped: an ask is open until an agent marks it done with evidence, or the person says so.",
    actions: seg([["owed", "Owed", null, V.asks.filter(a => OWED.has(a.status)).length], ["done", "Done lately", null, V.asks.filter(a => a.status === "done").length], ["all", "All", null, V.asks.length]], P.asks, k => { P.asks = k; update(); }, { label: "Which asks" }) },
    table({ key: r => r.id, empty: P.asks === "owed" ? "Nothing is owed." : "No asks here.", caption: "Asks",
      columns: [
        { label: "Asked", render: r => when(r.when) },
        { label: "Ask", clip: true, render: r => (app() || !r.evidence ? h("span", { title: r.ask || "" }, r.ask || "")
          : h("span", { title: r.ask || "" }, r.ask || "", h("div", { class: "meta", title: r.evidence }, "Evidence: " + short(r.evidence, 80)))) },
        { label: "Status", render: r => tone(r.orphaned ? "bad" : ASK_TONE[r.status], r.orphaned ? (app() ? ASK_TEXT_APP.orphaned : "orphaned") : (app() ? ASK_TEXT_APP : ASK_TEXT)[r.status] || r.status, r.orphaned ? "owned, but its owner is gone and nobody posted" : r.why || null) },
        { label: "Agent", render: r => (r.owner ? agentRef(r.owner) : "–") },
        { label: app() ? "Conversation" : "Thread", render: r => threadRef(r.thread) },
      ].filter(Boolean), rows }));
}

function outcomesSection(V, update) {
  const c = V.outcomes.counts, order = ["delivered", "partial", "nothing", "silent", "quiet", "not_asked"];
  const rows = V.outcomes.recent.filter(r => P.outs === "all" || r.grade === P.outs);
  return section({ title: "Runs' outcomes", key: "outcomes", info: "Each run the person woke is graded an hour after it ends: did it deliver what the post asked? Silent means it posted nothing at all.",
    actions: seg([["all", "All"], ...order.filter(g => c[g]).map(g => [g, `${GRADE_TEXT[g]} ${c[g]}`])], P.outs, k => { P.outs = k; update(); }, { label: "Which grade" }) },
    table({ key: r => r.key, empty: "No run graded in the last day.", caption: "Runs' outcomes in the last day",
      columns: [
        { label: "Ended", render: r => when(r.ended) },
        { label: "Agent", render: r => agentRef(r.name) },
        { label: app() ? "Conversation" : "Thread", render: r => threadRef(r.thread) },
        { label: "Grade", render: r => tone(GRADE_TONE[r.grade], GRADE_TEXT[r.grade] || r.grade) },
        { label: "Why", clip: true, render: r => h("span", { title: r.why || "" }, r.why || "") },
      ], rows }));
}

function improvementsSection(V) {
  const rows = [...V.improvements].sort((a, b) => (a.due !== b.due ? (a.due ? -1 : 1) : (a.at || "") > (b.at || "") ? -1 : 1));
  return section({ title: "Improvements", key: "improvements", info: "Every change to the hive itself, with the measure it should move and the day it is judged on. A landed change isn't an improvement until the measure says so.", fold: app(), summary: plural(rows.length, "change") + (rows.some(i => i.due) ? `, ${rows.filter(i => i.due).length} due` : "") },
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
  return section({ title: "Scoreboard", key: "scoreboard", info: `Every measure from \`hive progress\`, the last ${now.since || "day"} against the ${now.since || "day"} before${B.stale ? ", from the last snapshot (the live read failed)" : ""}. Lower is better where it says so.`, fold: app(), summary: plural(rows.length, "measure") + (B.stale ? ", from the last snapshot" : "") },
    table({ key: r => r.k, caption: "Scoreboard", columns: [
      { label: "Measure", render: r => (r.lower ? [r.label, h("span", { class: "muted" }, " · lower is better")] : r.label) },
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
    const V = P.v && Array.isArray(P.v.asks) ? P.v : null;
    if (!V) return h("div", { class: "page" }, P.failed ? empty("Couldn't read the progress data. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    const head = lead(V), update = () => ctx.update();
    return metrics({
      header: pageHeader({ state: head.state, title: head.title, lead: head.text, tone: head.tone, meta: [S.app.app ? null : "hive progress, hive asks, hive outcomes, hive improve"].filter(Boolean) }),
      sections: [tiles(V), asksSection(V, update), outcomesSection(V, update), improvementsSection(V), scoreboardSection(V)].filter(Boolean),
    });
  },
};
