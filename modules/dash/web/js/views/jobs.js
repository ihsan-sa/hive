// Jobs: the machine and the heavy queue. /api/jobs is polled only while this view is open.
import { h, selecting } from "../core/dom.js";
import { S, jget } from "../core/store.js";
import { ago, clock, gb, now, plural, secs } from "../core/format.js";
import { metrics } from "../templates.js";
import { empty, pageHeader, section, share, stats, statusLabel } from "../ui/kit.js";
import { agentRef } from "../ui/refs.js";
import { table } from "../ui/table.js";

const J = { v: null, failed: false };
// In the app: the same four, in plain words. The queue line goes when there's no queue.
const APP_OWNERS = { jobs: "Big jobs in the queue", outside: "What the agents are running", agents: "The agents themselves", other: "Everything else on the computer" };
const OWNERS = [["jobs", "Queue jobs", "tier1"], ["outside", "Agents' work outside the queue", "held"], ["agents", "The agents themselves", "tier2"], ["other", "Everything else", "other"]];

const who = n => (n && n !== "?" ? agentRef(n) : "?");
const num = (label, render) => ({ label, num: true, render });
const col = (label, render) => ({ label, render });
const cmd = { label: "Command", mono: true, clip: true, render: r => (r.cmd == null ? "not shown" : h("span", { title: r.cmd }, r.cmd)) };

function headline(V) {
  const held = V.gates.filter(g => !g.ok);
  const why = !V.queued.length ? "" : !V.running.length ? " The next one can start."
    : held.length ? ` Held back by ${held.map(g => `${g.what} (${g.now}${g.unit}, needs ${g.need})`).join("; ")}.`
    : " Nothing is holding the next start back; jobs start at most every 30 seconds.";
  return { text: `${plural(V.running.length, "job")} running, ${V.queued.length} queued.${why}`, tone: V.queued.length && held.length ? "warn" : null };
}

function tiles(T) {
  const M = T.mem, P = T.psi, swap = M.SwapTotal - M.SwapFree, cpu = 100 - T.busy.idle - T.busy.iowait;
  const tile = (label, value, note, pct, hot, title) => ({ label, value, note, title, tone: hot ? "bad" : null, meter: { value: pct, tone: hot ? "bad" : "quiet", label } });
  return [
    tile("CPU", cpu + "%", `${T.busy.user}% programs, ${T.busy.system}% system`, cpu),
    tile("Waiting on disk", T.busy.iowait + "%", `${T.blocked} tasks blocked now, ${Math.round(P.io_full || 0)}% fully stalled`, T.busy.iowait, T.busy.iowait >= 30),
    tile("Load", T.load.toFixed(0), `on ${T.cpus} threads`, (T.load * 100) / T.cpus, T.load >= T.cpus),
    tile("Memory available", gb(M.MemAvailable), `of ${gb(M.MemTotal)}, ${Math.round(P.memory_full || 0)}% fully stalled`, ((M.MemTotal - M.MemAvailable) * 100) / M.MemTotal, M.MemAvailable < 5000),
    tile("Swap used", gb(swap), `of ${gb(M.SwapTotal)}`, M.SwapTotal ? (swap * 100) / M.SwapTotal : 0, M.SwapTotal && swap / M.SwapTotal >= 0.9),
    T.slice_high ? tile("Queue jobs' memory", gb(T.slice_used), `soft limit ${gb(T.slice_high)}`, (T.slice_used * 100) / T.slice_high, T.slice_used >= T.slice_high - 3000) : null,
    ...T.gpus.map(g => tile("GPU", g.util + "%", `${gb(g.mem)} of ${gb(g.total)}, ${g.name}`, g.util)),
    // One Disk tile: further disks fold into its note, so the grid doesn't end on a lone tile labelled with a raw path.
    ...T.disks.slice(0, 1).map(d => tile("Disk", d.used + " GB", [`of ${d.total} GB`, ...T.disks.slice(1).map(x => `${x.name}: ${x.used} of ${x.total} GB`)].join(", "), d.total ? (d.used * 100) / d.total : 0, T.disks.some(x => x.total && x.used / x.total >= 0.9))),
  ];
}

function split(V) {
  const bar = (label, field, total, unit, fmt) => {
    const used = OWNERS.reduce((a, [k]) => a + V.split[k][field], 0);
    return h("div", { class: "jobs-split" }, h("div", { class: "jobs-split-h" }, h("b", null, label), h("span", { class: "meta" }, `${fmt(used)} of ${fmt(total)}${unit}`)),
      share(OWNERS.map(([k, l, tone]) => ({ key: k, label: l, tone, value: V.split[k][field], text: fmt(V.split[k][field]) })), { total, label: `${label}: ${fmt(used)} of ${fmt(total)}${unit}`, legend: false }));
  };
  const memTotal = Math.max(V.machine.mem.MemTotal || 0, V.held, OWNERS.reduce((a, [k]) => a + V.split[k].mem, 0)) || 1;
  const own = OWNERS.filter(([k]) => !(S.app.app && k === "jobs" && !hasQueue(V))).map(([k, l, tone]) => [k, S.app.app ? APP_OWNERS[k] : l, tone]);
  return [bar("CPU", "cores", V.machine.cpus, " threads", v => v.toFixed(1)), bar("Memory in use", "mem", memTotal, "", gb),
    h("div", { class: "legend" }, own.map(([k, l, tone]) => h("span", { "data-tone": tone }, h("i", { class: "sw" }), `${l}: ${V.split[k].cores.toFixed(1)} threads, ${gb(V.split[k].mem)}`)))];
}

/** The queue tool is installed: it has slots, or something has used it. The app's install doesn't include it. */
const hasQueue = V => V.slots > 0 || V.running.length || V.queued.length || V.done.length;
/** The app's answer: is the computer coping? */
function machineAnswer(T) {
  const M = T.mem, cpu = 100 - T.busy.idle - T.busy.iowait, free = gb(M.MemAvailable);
  const tight = M.MemAvailable < 2000 || T.busy.iowait >= 30 || T.load >= T.cpus * 1.5;
  return { text: tight ? `The computer is busy: ${cpu}% CPU, ${free} of memory free. Agents may be slow.` : `The computer has room: ${cpu}% CPU in use, ${free} of memory free.`, tone: tight ? "warn" : null };
}

export default {
  id: "jobs", label: () => (S.app.app ? "This machine" : "Jobs"), about: "how busy the computer is", tab: true,
  poll: {
    every: 3000,
    async run(ctx) {
      try { J.v = await jget("/api/jobs"); J.failed = false; } catch (e) { J.failed = true; }
      if (!selecting()) ctx.update();
    },
  },
  render() {
    const V = J.v;
    if (!V) return h("div", { class: "page" }, J.failed ? empty("Couldn't read the job data.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    const t = now() / 1e3, app = S.app.app, q = hasQueue(V), head = app ? machineAnswer(V.machine) : headline(V);
    let pos = 0;
    return metrics({
      header: pageHeader({ title: app ? "This machine" : "Jobs", lead: head.text, tone: head.tone,
        after: app && !q ? h("p", { class: "ph-meta" }, "The queue for big jobs isn't installed here, so agents run their work directly. Its sections show once it is.") : null,
        meta: [J.failed ? "Couldn't refresh: this may be out of date" : `Updated ${clock(V.at * 1e3)}`, "CPU in threads in use, averaged since the last refresh"] }),
      sections: [
        section({ title: "The machine" }, stats(tiles(V.machine))),
        section({ title: "Who's using it", note: "Memory is what's in use, RAM plus swap. The unfilled part of its bar is the kernel, shared memory and tmpfs." }, split(V)),
        ...(app && !q ? [] : [section({ title: "Running", note: `${V.running.length} of ${V.slots} slots, longest first` }, table({
          rows: V.running, key: r => r.lane + r.since, empty: "Nothing running in the queue.",
          columns: [col("Agent", r => who(r.agent)), col("Lane", r => r.lane), col("Running", r => secs(t - r.since)), num("Threads", r => r.cores.toFixed(1)), num("Memory", r => gb(r.mem)), cmd],
        })),
        section({ title: "Queued", note: "in the order they'll start" }, table({
          rows: V.queued, key: q => q.agent + q.since + q.cmd, empty: "The queue is empty.",
          columns: [col("#", q => (q.first ? "first lane" : String(++pos))), col("Agent", q => who(q.agent)), col("Waiting", q => secs(t - q.since)), cmd],
        }))]),
        section({ title: app ? "Big work the agents are running" : "Outside the queue", note: app ? null : "the queue's limits don't apply to these" }, table({
          rows: V.outside, key: o => o.who, empty: "Nothing big outside the queue.",
          columns: [col("Who", o => (o.agent ? who(o.who) : o.who)), num("Threads", o => o.cores.toFixed(1)), num("Memory", o => gb(o.mem)), num("Processes", o => o.procs), { ...cmd, label: "Biggest command" }],
        })),
        ...(app && !q ? [] : [section({ title: "Just finished", note: "last 10 queue jobs" }, table({
          rows: V.done, key: d => d.end + d.cmd, empty: "None yet.",
          columns: [col("Finished", d => ago(d.end * 1e3)), col("Agent", d => who(d.agent)), col("Waited", d => secs(d.waited)), col("Ran", d => secs(d.ran)),
            col("Result", d => (d.rc ? statusLabel("error", `failed (${d.rc})`) : statusLabel("ok"))), cmd],
        }))]),
      ].filter(Boolean),
    });
  },
};
