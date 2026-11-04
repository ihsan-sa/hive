// Now: what each agent is doing. Template: overview.
import { h } from "../core/dom.js";
import { S, SYS } from "../core/store.js";
import { threadTitle, agentState, isRunning, hiveStats, tierHours, agentThread, spendSince, sEnd, hasTiers } from "../core/model.js";
import { now, ago, dur, clock, money, pct, plural, firstSentence, short, HOUR, DAY, MIN } from "../core/format.js";
import { TIERS, status } from "../core/status.js";
import { threadHref, agentHref } from "../core/router.js";
import { overview, cols } from "../templates.js";
import { pageHeader, section, tally, stats, list, share, empty, button, statusDot } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { agentCard as cardRows, displayText } from "../core/model.js";
import { killButton } from "../actions.js";
import { chart } from "../ui/chart.js";

const V = {};

// Running agents as rows, not cards: one line to scan, the same columns all the way down, however many there are.
function agentRows(run, states, t) {
  return h("div", { class: "tbl-w" }, h("table", { class: "tbl nowtbl" },
    h("caption", { class: "vh" }, "Running agents"),
    h("thead", null, h("tr", null, ["Agent", "Conversation", "State", "Doing now", "Model", "Up"].map(x => h("th", { scope: "col", class: x === "Up" ? "num" : null }, x)), h("th", null, h("span", { class: "vh" }, "Actions")))),
    h("tbody", null, run.map(a => {
      const st = states.get(a.name), S_ = status(st.key), task = S.byId[a.post], model = (cardRows(a.name)[0] || {}).short;
      const asked = task ? (a.from ? (a.from === "person" && S.app.app ? "you" : a.from) + ": " : "") + firstSentence(displayText(task)) : null;
      return h("tr", { "data-key": a.name, "data-tone": S_.tone, "data-attn": st.key === "stuck" ? "" : null },
        h("td", { "data-label": "Agent", class: "nt-a" }, agentRef(a.name)),
        h("td", { "data-label": "Conversation", class: "nt-th" }, threadRef(a.thread)),
        h("td", { "data-label": "State", class: "nt-st", title: S_.hint }, st.text),
        h("td", { "data-label": "Doing now", class: "nt-do" }, a.doing ? h("div", { class: "nt-now", title: a.doing }, a.doing) : null, asked ? h("div", { class: a.doing ? "nt-ask meta" : "nt-now", title: task.text }, "Asked by " + asked) : a.doing ? null : h("span", { class: "muted" }, "–")),
        h("td", { "data-label": "Model", class: "nt-m meta" }, model || "–"),
        h("td", { "data-label": "Up", class: "num meta" }, dur(t - a.since * 1e3)),
        h("td", { "data-label": "", class: "nt-x" }, a.alive ? killButton(a.name) : null));
    }))));
}

/** Addressed to the person, no reply yet, last 3 days: one row per thread. For the TV tile; the page itself leaves this to Chat. */
export function forYouRows(t = now()) {
  const lastYou = {}; for (const p of S.posts) if (p.from === "person" || p.from === "box") lastYou[p.th] = p.t;
  const fy = {}; for (const p of S.posts) if (p.t > t - 3 * DAY && p.men.includes("person") && p.t > (lastYou[p.th] || 0)) fy[p.th] = p;
  return Object.values(fy).sort((a, b) => b.t - a.t).map(p => ({ key: p.th, href: threadHref(p.th), title: threadTitle(p.th), sub: p.from, meta: ago(p.t), text: firstSentence(p.text) }));
}

/** How it's working as a hive, last 24 hours: the figures for stats(). */
export function hiveFigures(t = now()) {
  const hs = hiveStats(t - DAY, t);
  return [
    { label: "Agents talking to agents", value: pct(hs.a2a, hs.total), note: `${hs.a2a} of ${hs.total} posts mention another agent` },
    { label: "Time to answer a mention", value: hs.lat == null ? "–" : dur(hs.lat), note: `median; ${pct(hs.answered, hs.asked)} of ${hs.asked} mentions answered within 6h` },
    { label: "New agents started by agents", value: pct(hs.byAgents, hs.fresh), note: `${hs.byAgents} by agents, ${hs.byYou} by ${S.app.app ? "you" : "you or the box"}, ${hs.bySys} by the keeper or starter` },
    { label: "Threads with 2+ agents", value: `${hs.multi} of ${hs.threads}`, note: "threads where more than one agent posted" },
    { label: "Best connected", value: hs.hub || "–", note: hs.hub ? `talked with ${hs.hubN} other agents` + (agentThread(hs.hub) ? `, on ${agentThread(hs.hub)}` : "") : "no agent-to-agent mentions" },
    { label: "Session ends", value: hs.failed ? `${hs.failed} errored` : `${hs.clean} finished`, tone: hs.failed ? "bad" : null, note: `${hs.clean} finished, ${hs.stopped} stopped by a signal, ${hs.failed} errored; ${hs.limited} usage-limit stops` },
  ];
}

/** Agents running at once and posts per hour. 15-minute buckets, aligned so they hold still between polls. */
export function activityCharts(t = now()) {
  const span = Math.max(6, Math.min(48, Math.ceil((t - (S.posts[0] ? S.posts[0].t : t)) / HOUR))), q = HOUR / 4;
  const start = Math.floor((t - span * HOUR) / q) * q, N = span * 4;
  const conc = new Array(N).fill(0), bars = new Array(span).fill(0), hStart = Math.floor((t - span * HOUR) / HOUR) * HOUR;
  for (const s of S.sessions) { const b = sEnd(s); if (b < start) continue; const i0 = Math.max(0, Math.floor((s.start - start) / q)), i1 = Math.min(N - 1, Math.floor((b - start) / q)); for (let i = i0; i <= i1; i++) conc[i]++; }
  for (const p of S.posts) if (p.t >= hStart && !SYS.has(p.from)) bars[Math.min(span - 1, Math.floor((p.t - hStart) / HOUR))]++;
  return [
    chart({ title: "Agents running at once", note: `last ${span} hours`, series: [conc], kind: "step", start, step: q, fmt: v => `${v} running` }),
    chart({ title: "Board posts per hour", note: `last ${span} hours`, series: [bars], kind: "bar", start: hStart, step: HOUR, fmt: v => plural(v, "post") }),
  ];
}

export default {
  id: "now", label: () => (S.app.app ? "Agents" : "Now"), about: "what each agent is doing", tab: true,
  render(ctx) {
    const L = S.live, t = now();
    if (!L) return h("div", { class: "page" }, empty("Reading the board…", { kind: "loading" }));

    // Working agents keep a fixed order (longest up first), so a new turn doesn't move a card. The rest follow, least quiet first; gone processes last.
    const isWorking = a => a.alive && t - (a.last || 0) * 1e3 < 3 * MIN, group = a => (isWorking(a) ? 0 : a.alive ? 1 : 2);
    const run = [...L.agents].sort((a, b) => group(a) - group(b) || (group(a) ? (b.last || 0) - (a.last || 0) : (a.since || 0) - (b.since || 0)) || String(a.name).localeCompare(String(b.name)));
    const states = new Map(run.map(a => [a.name, agentState(a)]));
    const count = k => run.filter(a => states.get(a.name).key === k).length;
    const active = run.filter(isWorking).length, threads = new Set(run.map(a => a.thread)).size;
    const waiting = L.pending.filter(p => !isRunning(p.name));
    const lastHour = S.posts.filter(p => p.t > t - HOUR && !SYS.has(p.from)).length;

    const header = pageHeader({
      title: run.length ? `${plural(run.length, "agent")} on ${plural(threads, S.app.app ? "conversation" : "thread")}` : S.app.app ? "No agents are working right now" : "Nobody is running right now",
      after: run.length ? tally([
        { n: active, label: "active now", status: "working" },
        ...(!S.app.app || count("working") > active ? [{ n: count("working") - active, label: "on a running job", status: "working" }] : []),  // the app's install has no queue: don't show its counts at 0
        ...(!S.app.app || count("queue") ? [{ n: count("queue"), label: "in the job queue", status: "queue" }] : []),
        { n: count("quiet"), label: "quiet", status: "quiet" },
        { n: count("stuck"), label: "stuck", status: "stuck" },
      ]) : null,
      meta: [
        L.jobs && (!S.app.app || L.jobs.running || L.jobs.queued) ? `Machine: ${L.jobs.running} jobs running, ${L.jobs.queued} queued` : null,
        `${plural(lastHour, "post")} in the last hour`,
        waiting.length ? `${waiting.length} waiting to start` : null,
        L.limit_until ? `starts held for the usage limit until ${clock(L.limit_until * 1e3)}` : null,
        S.usage ? `${money(spendSince(t - DAY))} of work in 24h, at list prices` : null,
      ],
    });

    const main = section({ title: S.app.app ? "What each is doing" : "Agents at work", note: "one row each: working first, then quiet" },
      run.length ? agentRows(run, states, t) : empty("None."));

    const stuck = run.filter(a => states.get(a.name).key === "stuck");

    const stuckList = stuck.length ? section({ title: "Stuck", note: "running but not moving" },
      list(stuck.map(a => ({ key: a.name, href: agentHref(a.name), lead: statusDot("stuck"), title: a.name, sub: threadTitle(a.thread), text: states.get(a.name).text })))) : null;

    const hive = section({ title: "How it's working as a hive", note: "last 24 hours" }, stats(hiveFigures(t)));
    const charts = cols(...activityCharts(t));

    const hrs = tierHours(t - DAY, t), tot = Object.values(hrs).reduce((a, b) => a + b, 0) || 1;
    const tiers = section({ title: "Where the agent hours went", note: "against your priority list, last 24 hours" },
      share(TIERS.map(x => ({ key: x.key, label: x.label, tone: x.tone, value: hrs[x.key], text: `${(hrs[x.key] / HOUR).toFixed(1)}h (${Math.round((100 * hrs[x.key]) / tot)}%)` }))));

    return overview({ header, main: [stuckList, main], after: [hive, charts, hasTiers() ? tiers : null] });
  },
};
