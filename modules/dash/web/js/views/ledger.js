// Ledger: every event, one row each, newest first. S.ledger is built by the store as data.
// The rows are rebuilt only when the data or the filter changes, and morphed by key, so new rows slide in and nothing else moves.
// Steps (every tool call and what agents said) are too many for the browser: /api/ledger filters and pages them on the server,
// and they're merged with the events here. The newest N of both together are within the newest N of each, so asking for N is enough.
import { h } from "../core/dom.js";
import { S, jget } from "../core/store.js";
import { clock, dayName, mdPlain, plural, short } from "../core/format.js";
import { metrics } from "../templates.js";
import { button, empty, waiting, input, pageHeader, seg, staleNote, toolbar } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";

const KINDS = [["all", "Everything"], ["step", "Steps"], ["post", "Messages"], ["agent", "Agents"], ["thread", "Threads"], ["trouble", "Trouble"], ["hive", "Hive"]];
const L = { kind: "all", q: "", limit: 300, seen: 0, sig: "", fsig: "", count: 0 };
const ST = { key: "", rows: [], total: 0, sig: "", busy: false, reading: false };   // the server's page of steps for the current filter
const wantSteps = () => L.kind === "all" || L.kind === "step";

const stepKey = () => [L.kind, L.q, L.limit].join("|");
async function loadSteps(onChange) {
  const key = stepKey();
  if (!wantSteps()) { if (ST.key !== key) { Object.assign(ST, { key, rows: [], total: 0, sig: "" }); onChange(); } return; }
  if (ST.busy) return;
  ST.busy = true;
  try {
    const r = await jget(`/api/ledger?limit=${L.limit}&q=${encodeURIComponent(L.q)}`), rows = Array.isArray(r.rows) ? r.rows : [], sig = JSON.stringify([r.total, rows[0], r.ready]);
    if (key === stepKey() && (sig !== ST.sig || key !== ST.key)) {   // else the filter changed while it was asked: asked again below
      const n = {};
      ST.rows = rows.map(([t, a, th, k, tool, x, d]) => { const id = `${t}${a}${k}`; n[id] = (n[id] || 0) + 1; return { t: t * 1000, a, th, k, tool, x, d, key: "s" + id + n[id] }; });
      Object.assign(ST, { key, total: r.total || 0, sig, reading: r.ready === false }); onChange();
    }
    if (ST.failed) { ST.failed = false; onChange(); }
  } catch (e) { const was = ST.failed; if (ST.key !== key) Object.assign(ST, { key, rows: [], total: 0, sig: "" }); ST.failed = true; if (!was) onChange(); }   // keeps the steps it read last, and says so
  ST.busy = false;
  if (stepKey() !== ST.key) loadSteps(onChange);
  else if (ST.reading) setTimeout(() => loadSteps(onChange), 1000);   // the server's first read of the logs takes a few seconds
}

/** Before new rows go in above, note the first row in view and where it is; after the page is redrawn, scroll so it's there again.
 *  Steps arrive every few seconds, so without this a reader further down the list would be pushed along. */
function holdPlace() {
  if (scrollY < 50) return;
  const el = [...document.querySelectorAll(".lg > li")].find(e => e.getBoundingClientRect().bottom > 0);
  if (!el) return;
  const top = el.getBoundingClientRect().top;
  requestAnimationFrame(() => { if (el.isConnected) scrollBy(0, el.getBoundingClientRect().top - top); });
}

function stepRow(r, fresh) {
  const said = r.k === "said", x = said ? r.x : String(r.x).replace(/^Running: /, "");
  return h("li", { class: "lg-r lg-step", "data-key": r.key, "data-kind": "step", "data-fresh": fresh ? "" : null },
    h("time", { class: "lg-t", title: new Date(r.t).toISOString() }, clock(r.t)),
    h("span", { class: "lg-b", title: [r.a, r.th ? "in " + r.th : "", said ? "said" : r.tool, x].filter(Boolean).join(" ") }, agentRef(r.a), r.th ? " in " : "", r.th ? threadRef(r.th) : "",
      h("span", { class: "feed-k lg-k", "data-said": said ? "" : null }, said ? "said" : r.tool),
      h("span", { class: "lg-q", title: r.d && r.d !== x ? r.d : x && x.length > 220 ? x : null }, x ? short(x, 220) : "")));
}

const part = x => (typeof x === "string" ? x : x.a !== undefined ? agentRef(x.a) : threadRef(x.th));

export function row(r, i, fresh) {
  const p = r.post;
  return h("li", { class: "lg-r", "data-key": "e" + i, "data-kind": r.kind, "data-fresh": fresh ? "" : null },
    h("time", { class: "lg-t", title: new Date(r.t).toISOString() }, clock(r.t)),
    h("span", { class: "lg-b", title: r.parts.map(x => (typeof x === "string" ? x : x.a !== undefined ? x.a : x.th)).join("") + (p ? ": " + mdPlain(p.text).replace(/^(@\S+\s+)+/, "").slice(0, 400) : "") }, r.parts.map(part),
      p ? h("span", { class: "lg-q", title: mdPlain(p.text).slice(0, 600) }, ": " + short(mdPlain(p.text).replace(/^(@\S+\s+)+/, ""), 170), (p.files || []).length ? ` (${plural(p.files.length, "file")})` : "") : null));
}

function rows() {
  const words = L.q.toLowerCase().split(/\s+/).filter(Boolean), found = [];
  S.ledger.forEach((r, i) => { if ((L.kind === "all" || r.kind === L.kind) && words.every(w => r.lc.includes(w))) found.push([r, i]); });
  if (wantSteps()) for (const r of ST.rows) found.push([r, -1]);
  found.sort((a, b) => b[0].t - a[0].t);
  L.count = found.length - (wantSteps() ? ST.rows.length - ST.total : 0);
  const seen = L.seen; if (found.length) L.seen = Math.max(L.seen, found[0][0].t);
  const out = []; let day = null;
  for (const [r, i] of found.slice(0, L.limit)) {
    const d = dayName(r.t);
    if (d !== day) { out.push(h("li", { class: "lg-day", "data-key": "d" + d }, d)); day = d; }
    out.push(i < 0 ? stepRow(r, seen && r.t > seen) : row(r, i, seen && r.t > seen));
  }
  return out;
}

export default {
  id: "ledger", label: () => (S.app.app ? "History" : "Ledger"), about: "everything that happened, in order: messages, starts and ends, and every tool call", tab: true,
  poll: { every: 5000, run(ctx) { loadSteps(ctx.update); } },
  render(ctx) {
    if ([L.kind, L.q, L.limit].join("|") !== ST.key) loadSteps(ctx.update);
    const sig = [S.ver, L.kind, L.q, L.limit, ST.sig, ST.key].join("|"), changed = sig !== L.sig; L.sig = sig;
    if (changed && sig.split("|").slice(1, 4).join("|") === L.fsig) holdPlace();   // new rows above, same filter: the reader stays put
    L.fsig = [L.kind, L.q, L.limit].join("|");
    const reading = wantSteps() && !ST.failed && (ST.reading || ST.key !== stepKey());
    const built = changed ? rows() : null;     // on an unchanged poll the list below is an empty node with the same sig: morph leaves the page's list alone
    const set = (k, v) => { L[k] = v; ctx.update(); };
    return metrics({
      header: pageHeader({ title: S.app.app ? "History" : "Ledger", lead: S.app.app ? "Everything that happened, newest first: messages, every step agents took, agents starting and ending, and trouble." : null, after: [toolbar(
        seg(S.app.app ? KINDS.map(([k, l]) => [k, k === "thread" ? "Conversations" : l]) : KINDS, L.kind, k => set("kind", k), { label: "Kind of event" }),
        input({ type: "search", placeholder: `Filter by agent, ${S.app.app ? "conversation" : "thread"} or words`, "aria-label": "Filter", value: L.q, oninput: e => set("q", e.target.value) }),
        S.live ? h("span", { class: "meta" }, plural(L.count, "event")) : null), reading ? h("p", { class: "meta lg-reading", role: "status" }, "Reading agents’ steps… Messages and events are available under Everything.") : null, S.live && wantSteps() && ST.failed ? staleNote(ST.rows.length ? "Couldn't refresh the agents' steps, so they're the ones read last. It keeps trying." : "Couldn't read the agents' steps, so they're left out. It keeps trying.") : null] }),
      sections: [
        L.count || reading ? null : S.live && L.kind === "step" && ST.failed ? null : S.live ? empty(L.kind === "all" && !L.q.trim() ? "Nothing has happened yet." : "No events match.") : waiting(S.failed, "Reading…", "the history"),
        h("ol", { class: "lg", "data-own": "morph", "data-sig": sig }, built),
        L.count > L.limit ? h("div", { class: "lr-more" }, button(`Show older (${L.count - L.limit} more)`, () => set("limit", L.limit + 500), { kind: "quiet", size: "s" })) : null,
      ],
    });
  },
};
