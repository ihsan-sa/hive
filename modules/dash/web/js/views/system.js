// #system: the design system, drawn with the real components. Not a tab; linked from ARCHITECTURE.md.
import { h } from "../core/dom.js";
import { STATUS, TIERS } from "../core/status.js";
import { HOUR, now } from "../core/format.js";
import { metrics, cols } from "../templates.js";
import { pageHeader, section, tally, stats, meter, button, seg, field, input, empty, list, rank, share, statusLabel, note, block, blocks, cmd } from "../ui/kit.js";
import { table } from "../ui/table.js";
import { chart } from "../ui/chart.js";
import { gaugeRows } from "../ui/gauge.js";
import { confirm } from "../ui/dialog.js";

const V = { seg: "6h", fold: false };
const sw = (name, token) => h("div", { class: "swatch", "data-key": token }, h("i", { style: `--c:var(${token})` }), h("span", null, h("b", null, name), " ", h("span", { class: "mono" }, token)));
const spec = (token, size, weight, text) => [h("span", null, `${token} ${size}`), h("div", { style: `font-size:var(${token});font-weight:${weight};line-height:1.3` }, text)];

export default {
  id: "system", label: "System", tab: false,
  render(ctx) {
    const t = now(), demo = Array.from({ length: 48 }, (_, i) => Math.round(8 + 6 * Math.sin(i / 5) + i / 4));
    return metrics({
      header: pageHeader({ title: "System", lead: "The tokens, components and templates every view is built from. Everything below is the real component, in the current theme.",
        meta: ["Switch theme in the top bar to check both", "Code: css/tokens.css, css/components.css, js/ui/, js/templates.js"] }),
      sections: [
        section({ title: "Neutrals", note: "cool grey, near-zero chroma; text never sits below --ink-3" },
          h("div", { class: "swatches" }, [["Background", "--bg"], ["Surface", "--surface"], ["Sunken", "--sunken"], ["Hover", "--hover"], ["Line", "--line"], ["Line strong", "--line-2"], ["Ink", "--ink"], ["Ink 2", "--ink-2"], ["Ink 3", "--ink-3"], ["Focus", "--focus"], ["Link", "--link"]].map(([n, k]) => sw(n, k)))),
        section({ title: "Statuses", note: "one table in js/core/status.js; tone + shape + word, so colour is never alone" },
          table({ columns: [{ label: "Status", render: r => statusLabel(r[0]) }, { label: "Key", render: r => h("span", { class: "mono" }, r[0]) }, { label: "Tone", render: r => r[1].tone }, { label: "Shape", render: r => r[1].shape }, { label: "Means", wrap: true, render: r => r[1].hint }],
            rows: Object.entries(STATUS).filter(([k]) => k !== "outside"), key: r => r[0] })),
        section({ title: "Categories", note: "priority tiers; small squares, so they never read as a status dot" },
          h("div", { class: "swatches" }, TIERS.map(x => sw(x.label, "--" + x.tone)))),
        section({ title: "Type", note: "system UI font, no web fonts; tabular numbers for figures" },
          h("div", { class: "specimen" },
            spec("--fs-display", "30", 650, "The Hive (website and guide only)"),
            spec("--fs-head", "21", 650, "24 agents on 23 threads"), spec("--fs-lead", "17", 400, "Only acct-3 can take new runs."), spec("--fs-figure", "24", 600, "$369 · 16%"),
            spec("--fs-title", "15.5", 650, "a6a89 (names)"), spec("--fs-body", "14", 400, "can start once the queue clears and the review passes"),
            spec("--fs-small", "13", 400, "Secondary text, tables, lists, section titles at 650"), spec("--fs-meta", "12", 400, "Metadata, notes, timestamps · 4m ago"), spec("--fs-micro", "11", 400, "Axis ticks, badges"))),
        section({ title: "Space, radius, elevation, density" },
          h("ul", { class: "rules" },
            h("li", null, "Space on a 4 px grid: 4, 8, 12, 16, 24, 32, 48. Sections 32 apart, items inside 8–12."),
            h("li", null, "Radius: 4 for small things, 6 for controls, 10 for things that float. Nothing on the page is a rounded box unless it floats."),
            h("li", null, "Elevation only for things above the page: tooltip (e1), dialog and side panel (e2). Groups are separated by hairlines and space, not cards."),
            h("li", null, "Rows: 34 px, 28 px dense. Controls 30 px, 24 px small. Phone hit targets 44 px: rows and folded cards are whole-row targets."),
            h("li", null, "One primary line per item: an agent's now line, a limit's percent. Everything else is 12–13 px, ink-2 or ink-3."),
            h("li", null, "Numbers are tabular and right-aligned in tables. Units smaller than the figure."),
            h("li", null, "Colour means status or category only. Accent blue is links, focus and neutral data series."))),
        section({ title: "Buttons, choices, fields" },
          h("div", { class: "demo" }, button("Primary", () => {}, { kind: "primary" }), button("Default", () => {}), button("Quiet", () => {}, { kind: "quiet" }), button("Small", () => {}, { size: "s" }),
            button("Kill", () => {}, { kind: "quiet", size: "s", tone: "bad" }), button("Danger", () => {}, { kind: "danger" }), button("Disabled", () => {}, { disabled: true }),
            seg([["1h", "1h"], ["6h", "6h"], ["24h", "24h"], ["all", "All"]], V.seg, k => { V.seg = k; ctx.update(); }, { label: "Window" }),
            button("Open a confirm window", () => confirm({ title: "Kill a6a89?", kind: "danger", ok: "Kill a6a89", typed: "a6a89", body: ["This ends the run right away. Type the code to enable the button."], onOk: async () => { throw Error("this is the system page"); } }))),
          h("div", { class: "cols", style: "margin-top:var(--s4)" }, field("Session limit", input({ type: "number", value: "40" }), "How many agents run at once. Empty means no limit."), field("Find", input({ type: "search", placeholder: "Filter by agent, thread or words" })))),
        section({ title: "Section heads", note: "two levels, one component: section() in kit.js" },
          h("div", { class: "stack" },
            section({ title: "Level 1: a part of a long page", note: "Settings groups, website parts, guide h2", level: 1 }, h("p", { class: "muted" }, "The larger title, no rule. Use it where a page has parts someone scans for.")),
            section({ title: "Level 2: a group inside a part", note: "the default; every view's sections" }, h("p", { class: "muted" }, "The smaller bold title with a quiet note beside it. The list under it draws its own rules.")))),
        section({ title: "Notes and warnings", note: "note() in kit.js; a title line and short points, never a paragraph wall" },
          h("div", { class: "notes" },
            note({ tone: "bad", title: "The sandbox isn't working on this machine" }, h("ul", null, h("li", null, "Agents can reach everything this account can."), h("li", null, "No new agent starts until you fix it or choose to run without it."))),
            note({ tone: "warn", title: "Agents run without asking first" }, h("ul", null, h("li", null, "Inside the sandbox, each step just runs."), h("li", null, "Money, credentials and the network still need your yes."))),
            note({ tone: "info", title: "It spends your usage", actions: button("Usage", () => {}, { size: "s" }) }, "New agents stop at 95% of your week."))),
        section({ title: "Blocks", note: "block() in a blocks() group: one setting or account per row, hairlines not boxes" },
          blocks(
            block({ title: "Claude", sub: "your subscription", state: statusLabel("working", "signed in"), actions: button("Sign in again", () => {}, { size: "s" }) }),
            block({ title: "Codex", sub: "with a ChatGPT login", state: statusLabel("off"), actions: button("Sign in", () => {}, { size: "s" }) }),
            block({ title: "API keys", sub: "charged per use, for when no login has room", fold: true, open: V.fold, onToggle: o => { V.fold = o; } },
              h("p", null, "Every run costs money on that key, so set a spending limit with the provider."),
              h("div", { class: "frow" }, h("div", { style: "flex:1;min-width:12rem" }, field("Key", input({ placeholder: "sk-…" }))), button("Save key", () => {}))),
            block({ title: "Reach the hive from Slack", state: h("span", { class: "nb" }, "not built yet") }, h("p", null, "One channel is one conversation.")))),
        section({ title: "Command, not built yet" },
          cmd("curl -fsSL https://<THIS-SITE>/install.sh | sh -s -- --this-account"),
          h("p", { class: "muted", style: "margin-top:var(--s3)" }, "Stop from the conversation ", h("span", { class: "nb" }, "not built yet"), " Neutral and dashed: amber is the job queue, and this isn't a state.")),
        section({ title: "Tally, stats, meter" },
          tally([{ n: 9, label: "active now", status: "working" }, { n: 12, label: "in the job queue", status: "queue" }, { n: 0, label: "quiet", status: "quiet" }, { n: 1, label: "stuck", status: "stuck" }]),
          h("div", { style: "margin-top:var(--s4)" }, stats([{ label: "CPU", value: "97%", meter: { value: 97 }, note: "89% programs, 8% system" }, { label: "Load", value: "33", tone: "bad", meter: { value: 100, tone: "bad" }, note: "on 20 threads" }, { label: "Last 24 hours", value: "$369", note: "$15 an hour lately" }, { label: "Weekly", value: "16%", meter: { value: 16, stop: 98 }, note: "stop line at 98%" }]))),
        section({ title: "Gauge row", note: "one account; logins and providers share it" },
          gaugeRows({ rows: [{ key: "x", name: "acct-3", tag: "plan: corner", state: { key: "inuse" }, note: "Stops at 98% weekly (global).", gauges: [{ label: "Weekly", value: "16%", pct: 16, stop: 98, lines: ["resets Mon 12:00, in 7d"] }, { label: "5-hour", value: "4%", pct: 4, stop: 90, lines: ["resets 17:19, in 1.2h"] }],
            history: chart({ series: [demo.map(v => v * 3)], kind: "step", start: t - 48 * HOUR, step: HOUR, max: 100, mark: 98, compact: true, height: 44, area: false, fmt: v => v + "%" }), actions: [button("Stop lines", () => {}, { size: "s" }), button("Pause", () => {}, { size: "s", kind: "quiet" })] }] })),
        cols(
          section({ title: "List" }, list([{ key: "a", href: "#system", title: "project-62", sub: "a6aea", meta: "8m ago", text: "after the limit moved so the next build can start once the queue clears" }, { key: "b", href: "#system", title: "hive", sub: "ad670", meta: "28s ago", text: "block still needs a second look before release" }])),
          section({ title: "Rank" }, rank([{ key: "a", label: "project-55", value: 42, text: "$42", tone: "tier1" }, { key: "b", label: "hive", value: 30, text: "$30", tone: "tier2" }, { key: "c", label: "project-18", value: 12, text: "$12", tone: "other" }]))),
        section({ title: "Share" }, share(TIERS.map((x, i) => ({ key: x.key, label: x.label, tone: x.tone, value: [5, 3, 1, 8][i], text: [5, 3, 1, 8][i] + "h" })))),
        section({ title: "Table" }, table({ columns: [{ label: "Agent", key: "a" }, { label: "Lane", key: "l" }, { label: "Threads", key: "c", num: true }, { label: "Memory", key: "m", num: true }, { label: "Command", key: "x", mono: true, wrap: true }],
          rows: [{ a: "a06c8", l: "first", c: "0.9", m: "43 MB", x: "~/projects/project/file.txt" }, { a: "ae73f", l: "slot19", c: "1.2", m: "111 MB", x: "two small gaps the route check passed on rerun" }], key: r => r.a })),
        cols(chart({ title: "Chart frame", note: "step", series: [demo], kind: "step", start: t - 48 * HOUR, step: HOUR, fmt: v => v }), chart({ title: "Chart frame", note: "bars", series: [demo], kind: "bar", start: t - 48 * HOUR, step: HOUR, fmt: v => v })),
        section({ title: "Empty, loading, error" }, empty("Nothing stalled."), empty("Reading the board…", { kind: "loading" }), empty("Couldn't read the job data.", { kind: "error" })),
        section({ title: "Templates", note: "js/templates.js" },
          h("ul", { class: "rules" },
            h("li", null, h("b", null, "overview"), ": header, main sections, then full-width sections. Now."),
            h("li", null, h("b", null, "metrics"), ": header with the answer, then sections. Usage, Jobs, Agent page."),
            h("li", null, h("b", null, "listDetail"), ": list beside a reading pane, one pane at a time on a phone. Comms, Inbox, Ledger."),
            h("li", null, h("b", null, "canvas"), ": toolbar, full-bleed stage, side panel. Mesh, Timeline."),
            h("li", null, h("b", null, "form"), ": narrow column of field sections. Settings, Start."))),
      ],
    });
  },
};
