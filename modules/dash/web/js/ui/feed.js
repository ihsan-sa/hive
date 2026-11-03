// Activity feed: tool calls and what an agent said, newest first. Keeps its own scroll, which morph leaves alone.
import { h } from "../core/dom.js";
import { clock, short } from "../core/format.js";
import { empty } from "./kit.js";

export const toolTone = t => (/^Bash$/.test(t) ? "warn" : /^(Read|Grep|Glob|WebFetch|WebSearch)$/.test(t) ? "tier1" : /^(Edit|Write|NotebookEdit)$/.test(t) ? "good" : /^(Agent|Task|Skill)$/.test(t) ? "tier2" : "other");

/** items: [{t, k: "tool"|"said", tool, x}], oldest first as the server sends them. null: still loading. */
export function feed(items, { max = 700 } = {}) {
  if (items == null) return empty("Reading…", { kind: "loading" });
  if (!items.length) return empty("No session log left to read.");
  const seen = new Map();
  const rows = items.map(e => {
    const said = e.k !== "tool", base = `${e.t}|${e.tool || "said"}|${String(e.x).slice(0, 48)}`, c = seen.get(base) || 0; seen.set(base, c + 1);
    return h("li", { class: "feed-r", "data-key": base + "|" + c, "data-said": said ? "" : null },
      h("time", null, e.t ? clock(e.t * 1e3) : ""),
      h("span", { class: "feed-k", "data-tone": said ? "tier1" : toolTone(e.tool) }, said ? "said" : e.tool),
      h("div", { class: "feed-x" }, short(said ? e.x : String(e.x).replace(/^Running: /, ""), max)));
  });
  return h("ol", { class: "feed", tabindex: 0, "aria-label": "Latest activity" }, rows.reverse());
}
