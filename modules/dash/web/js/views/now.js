// Now: what each agent is doing. Template: overview.
import { h } from "../core/dom.js";
import { S, SYS } from "../core/store.js";
import { threadTitle, agentState, isRunning, hiveStats, tierHours, agentThread, spendSince, sEnd, hasTiers, activeUse } from "../core/model.js";
import { now, ago, dur, clock, money, pct, plural, firstSentence, short, HOUR, DAY, MIN } from "../core/format.js";
import { TIERS, status } from "../core/status.js";
import { threadHref, agentHref, href } from "../core/router.js";
import { overview, cols } from "../templates.js";
import { pageHeader, section, tally, stats, list, share, empty, waiting, button, statusDot } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { agentCard as cardRows, displayText } from "../core/model.js";
import { killButton } from "../actions.js";
import { chart } from "../ui/chart.js";
import { signInWords } from "../ui/shell.js";
import { awayCard } from "../ui/away.js";

const V = {};

const tag = (tone, text, title) => h("span", { class: "pg-t", "data-tone": tone || null, title: title || null }, text);
const TONE = { working: "good", queue: "warn", stuck: "bad", quiet: "quiet" };

// Running agents as rows, not cards: one line to scan, the same columns all the way down, however many there are.
// An agent whose conversation waits on the person says so, and sorts first.
function agentRows(run, states, t, waits) {
  return h("div", { class: "tbl-w" }, h("table", { class: "tbl nowtbl" },
    h("caption", { class: "vh" }, "Running agents"),
    h("thead", null, h("tr", null, ["Agent", "Conversation", "State", "Doing now"].map(x => h("th", { scope: "col" }, x)), h("th", { class: "num", scope: "col" }, "Up"), h("th", null, h("span", { class: "vh" }, "Actions")))),
    h("tbody", null, run.map(a => {
      const st = states.get(a.name), S_ = status(st.key), task = S.byId[a.post], model = (cardRows(a.name)[0] || {}).short, wait = waits.has(a.thread);
      const asked = task ? (a.from ? (a.from === "person" && S.app.app ? "you" : a.from) + ": " : "") + firstSentence(displayText(task)) : null;
      return h("tr", { "data-key": a.name, "data-tone": S_.tone, "data-attn": st.key === "stuck" ? "" : null, title: model ? "Runs on " + model : null },
        h("td", { "data-label": "Agent", class: "nt-a" }, agentRef(a.name)),
        h("td", { "data-label": "Conversation", class: "nt-th" }, threadRef(a.thread)),
        h("td", { "data-label": "State", class: "nt-st", title: S_.hint }, wait ? tag("info", "waits on you") : tag(TONE[st.key], st.text)),
        h("td", { "data-label": "Doing now", class: "nt-do" }, a.doing ? h("div", { class: "nt-now", title: a.doing }, a.doing) : null, asked ? h("div", { class: a.doing ? "nt-ask meta" : "nt-now", title: task.text }, "Asked by " + asked) : a.doing ? null : h("span", { class: "muted" }, "–")),
        h("td", { "data-label": "Up", class: "num meta" }, dur(t - a.since * 1e3)),
        h("td", { "data-label": "", class: "nt-x" }, a.alive ? killButton(a.name) : null));
    }))));
}

/** Developer Now: what needs the owner first. Stuck or dead agents, logins at their limit, queue waits over an hour, posts waiting. One row each. */
function needsYou(run, states, waiting, L) {
  const rows = [];
  for (const a of run) if (states.get(a.name).key === "stuck") rows.push({ sev: 0, tone: "bad", head: a.name, text: states.get(a.name).text, href: agentHref(a.name), act: "Open" });
  for (const l of (S.usage && S.usage.logins) || []) if (l.paused) rows.push({ sev: 1, tone: "warn", head: (l.name || "A login") + " is paused", text: "new agents won't start on it", href: href("usage"), act: "Usage" });
  if (L.limit_until) rows.push({ sev: 1, tone: "warn", head: "Starts held for the usage limit", text: "until " + clock(L.limit_until * 1e3), href: href("usage"), act: "Usage" });
  for (const a of run) { const j = a.jobs || {}; if (j.queued && !j.running && j.waited > 3600) rows.push({ sev: 2, tone: "warn", head: a.name, text: `waiting ${dur(j.waited * 1e3)} for a job slot`, href: agentHref(a.name), act: "Open" }); }
  if (waiting.length) rows.push({ sev: 3, tone: "quiet", head: plural(waiting.length, "post") + " waiting", text: "for an agent to start: " + waiting.slice(0, 4).map(p => p.name).join(", ") + (waiting.length > 4 ? "…" : ""), href: href("comms"), act: "Chat" });
  rows.sort((a, b) => a.sev - b.sev);
  if (!rows.length) return null;
  return section({ title: "Needs you", key: "needs" }, h("ul", { class: "nd-list" }, rows.map((r, i) => h("li", { "data-tone": r.tone, "data-key": r.head + i },
    statusDot(r.tone === "bad" ? "stuck" : "limited"), h("b", null, r.head), h("span", { class: "meta", title: r.text }, r.text), h("a", { href: r.href, class: "nd-act" }, r.act)))));
}

/** Developer Now, the two tables: running (newest first) and waiting for the job queue (longest first). */
function devTables(run, states, t) {
  const doing = a => { const task = S.byId[a.post]; return a.doing || (task ? "Asked by " + firstSentence(displayText(task)) : "–"); };
  const title = a => { const task = S.byId[a.post]; return [a.doing, task ? "Asked by " + (a.from || "") + ": " + firstSentence(displayText(task)) : null].filter(Boolean).join("\n") || null; };
  const working = run.filter(a => states.get(a.name).key !== "queue").sort((a, b) => (b.since || 0) - (a.since || 0) || String(a.name).localeCompare(String(b.name)));
  const queued = run.filter(a => states.get(a.name).key === "queue").sort((a, b) => ((b.jobs || {}).waited || 0) - ((a.jobs || {}).waited || 0) || String(a.name).localeCompare(String(b.name)));
  const w = h("div", { class: "tbl-w" }, h("table", { class: "tbl nowtbl" }, h("caption", { class: "vh" }, "Working"),
    h("thead", null, h("tr", null, ["Agent", "Thread", "Doing now"].map(x => h("th", { scope: "col" }, x)), h("th", { class: "num", scope: "col" }, "Up"), h("th", null, h("span", { class: "vh" }, "Actions")))),
    h("tbody", null, working.map(a => h("tr", { "data-key": a.name },
      h("td", { class: "nt-a", "data-label": "Agent" }, agentRef(a.name)), h("td", { class: "nt-th", "data-label": "Thread" }, threadRef(a.thread)),
      h("td", { class: "nt-do", "data-label": "Doing now", title: title(a) }, h("div", { class: "nt-now" }, doing(a))),
      h("td", { class: "num meta", "data-label": "Up" }, dur(t - a.since * 1e3)),
      h("td", { class: "nt-x", "data-label": "" }, a.alive ? killButton(a.name) : null))))));
  const q = h("div", { class: "tbl-w" }, h("table", { class: "tbl nowtbl" }, h("caption", { class: "vh" }, "In the job queue"),
    h("thead", null, h("tr", null, ["Agent", "Thread", "Jobs"].map(x => h("th", { scope: "col" }, x)), h("th", { class: "num", scope: "col" }, "Waited"))),
    h("tbody", null, queued.map(a => { const j = a.jobs || {}; return h("tr", { "data-key": a.name },
      h("td", { class: "nt-a", "data-label": "Agent" }, agentRef(a.name)), h("td", { class: "nt-th", "data-label": "Thread" }, threadRef(a.thread)),
      h("td", { class: "meta", "data-label": "Jobs" }, String(j.queued || 0)),
      h("td", { class: ["num", j.waited > 3600 ? "nt-long" : "meta"], "data-label": "Waited" }, dur((j.waited || 0) * 1e3))); }))));
  return cols(
    section({ title: "Working", note: String(working.length) }, working.length ? w : empty("None.")),
    section({ title: "In the job queue", note: String(queued.length) }, queued.length ? q : empty("None.")));
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
    { label: S.app.app ? "Conversations with 2+ agents" : "Threads with 2+ agents", value: `${hs.multi} of ${hs.threads}`, note: (S.app.app ? "conversations" : "threads") + " where more than one agent posted" },
    { label: S.app.app ? "Talks with most agents" : "Best connected", value: hs.hub || "–", note: hs.hub ? `talked with ${hs.hubN} other agents` + (agentThread(hs.hub) ? `, on ${agentThread(hs.hub)}` : "") : "no agent-to-agent mentions" },
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

const APP_TIER = { tier1: "Top priority", tier2: "Next", held: "Kept going", other: "Everything else" };   // the app look says it in plain words
const WORDS = ["No", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten"];
const nWord = n => WORDS[n] || String(n);

/** Today, for the consumer look: sessions finished, hours of agent work, and what's left of the week (links to Usage). */
function todayCard(t) {
  let ms = 0, done = 0;
  for (const s of S.sessions) { const a = Math.max(s.start, t - DAY), b = Math.min(sEnd(s), t); if (b > a) ms += b - a; if (s.end && s.end >= t - DAY && s.end <= t && s.exit === 0) done++; }
  const act = S.usage ? activeUse(S.usage, t) : null;
  const left = act ? Math.max(0, 100 - Math.round(act.pct)) + "%" : "–";
  return section({ title: "Today", note: "last 24 hours" }, h("div", { class: "card now-today" }, stats([
    { label: "Finished", value: String(done), note: plural(done, "run") + " ended cleanly" },
    { label: "Hours of work", value: (ms / HOUR).toFixed(1) + "h", note: "agent time, all agents" },
    { label: "Week left", value: h("a", { href: href("usage") }, left), note: act ? "of your weekly use" : "not read yet" },
  ])));
}

export default {
  id: "now", label: () => (S.app.app ? "Agents" : "Now"), about: "what each agent is doing", tab: true,
  render(ctx) {
    const L = S.live, t = now();
    if (!L) return h("div", { class: "page" }, waiting(S.failed, "Reading the board…", "the board"));

    // Working agents keep a fixed order (longest up first), so a new turn doesn't move a card. The rest follow, least quiet first; gone processes last.
    const isWorking = a => a.alive && t - (a.last || 0) * 1e3 < 3 * MIN, group = a => (isWorking(a) ? 0 : a.alive ? 1 : 2);
    const waits = S.app.app ? new Set(forYouRows(t).map(r => r.key)) : new Set();
    const run = [...L.agents].sort((a, b) => (waits.has(b.thread) - waits.has(a.thread)) || group(a) - group(b) || (group(a) ? (b.last || 0) - (a.last || 0) : (a.since || 0) - (b.since || 0)) || String(a.name).localeCompare(String(b.name)));
    const states = new Map(run.map(a => [a.name, agentState(a)]));
    const count = k => run.filter(a => states.get(a.name).key === k).length;
    const active = run.filter(isWorking).length, threads = new Set(run.map(a => a.thread)).size;
    const queuedPosts = L.pending.filter(p => !isRunning(p.name));
    const lastHour = S.posts.filter(p => p.t > t - HOUR && !SYS.has(p.from)).length;

    if (S.app.app) {
      const fy = forYouRows(t), stuckN = count("stuck"), sign = signInWords(), stopped = !!L.limit_until && !(S.app.setup && S.app.setup.other);
      const state = stopped || sign ? { tone: "bad", text: "Stopped" } : fy.length || stuckN ? { tone: "info", text: "Needs you" } : { tone: "good", text: "All fine" };
      const one = threads === 1 ? threadTitle(run[0].thread) : `${nWord(threads).toLowerCase()} conversations`;
      const title = run.length ? `${nWord(run.length)} ${run.length === 1 ? "agent is" : "agents are"} working on ${one}.` : "No agents are working right now.";
      const waitN = run.filter(a => waits.has(a.thread)).length;
      const lead = sign ? sign : stopped ? `New agents wait for the usage limit until ${clock(L.limit_until * 1e3)}.`
        : waitN ? `${nWord(waitN)} ${waitN === 1 ? "agent is" : "agents are"} waiting for your answer.`
        : fy.length === 1 ? `${fy[0].title} is waiting for your answer.`
        : fy.length ? `${nWord(fy.length)} conversations are waiting for your answer.`
        : stuckN ? `${nWord(stuckN)} ${stuckN === 1 ? "agent has" : "agents have"} stopped moving.` : null;
      const header = pageHeader({ state, title, lead });
      const main = section({}, run.length ? agentRows(run, states, t, waits) : empty("None."));
      const hrs = tierHours(t - DAY, t), tot = Object.values(hrs).reduce((a, b) => a + b, 0) || 1;
      const more = h("details", { class: "now-more", open: V.more || null, ontoggle: (e, n) => { if (e.target === n) V.more = n.open; } }, h("summary", null, "Details"),
        h("p", { class: "meta" }, [`${plural(lastHour, "post")} in the last hour`, queuedPosts.length ? `${queuedPosts.length} waiting to start` : null, S.usage ? `${money(spendSince(t - DAY))} of work in 24h, at list prices` : null].filter(Boolean).join(" · ")),
        section({ title: "How it's working as a hive", note: "last 24 hours" }, stats(hiveFigures(t))),
        cols(...activityCharts(t)),   // "Agents at work" would draw the same line as "running at once": one chart, not two
        hasTiers() ? section({ title: "Where the agent hours went", note: "against your priority list, last 24 hours" },
          share(TIERS.map(x => ({ key: x.key, label: S.app.app ? APP_TIER[x.key] || x.label : x.label, tone: x.tone, value: hrs[x.key], text: `${(hrs[x.key] / HOUR).toFixed(1)}h (${Math.round((100 * hrs[x.key]) / tot)}%)` })))) : null);
      return overview({ header, main: [awayCard(ctx), main].filter(Boolean), after: [todayCard(t), more] });
    }

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
        queuedPosts.length ? `${queuedPosts.length} waiting to start` : null,
        L.limit_until ? `starts held for the usage limit until ${clock(L.limit_until * 1e3)}` : null,
        S.usage ? `${money(spendSince(t - DAY))} of work in 24h, at list prices` : null,
      ],
    });

    const hive = section({ title: "How it's working as a hive", note: "last 24 hours" }, stats(hiveFigures(t)));
    const charts = cols(...activityCharts(t));

    const hrs = tierHours(t - DAY, t), tot = Object.values(hrs).reduce((a, b) => a + b, 0) || 1;
    const tiers = section({ title: "Where the agent hours went", note: "against your priority list, last 24 hours" },
      share(TIERS.map(x => ({ key: x.key, label: S.app.app ? APP_TIER[x.key] || x.label : x.label, tone: x.tone, value: hrs[x.key], text: `${(hrs[x.key] / HOUR).toFixed(1)}h (${Math.round((100 * hrs[x.key]) / tot)}%)` }))));

    return overview({ header, main: [needsYou(run, states, queuedPosts, L), run.length ? devTables(run, states, t) : empty("None.")], after: [hive, charts, hasTiers() ? tiers : null] });
  },
};
