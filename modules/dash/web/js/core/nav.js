// Where each view sits in the app's top bar. Pure: no page, no store, so test/test_nav.mjs can check it in node.
//
// Three places and one rule, so the bar doesn't grow as views and modules arrive:
// - main: the tabs. Keep it to what people open daily (MAX_MAIN). A new tab needs a reason.
// - groups: a tab's own pages, a row under the bar. Each page keeps its own address. Put a view here when it's another way to look at something a tab already covers.
// - more: everything else. Tools go here, in order. A view named nowhere lands here too, so a new view is still one line in views/index.js.
export const MAX_MAIN = 6, MAX_TOOLS = 6;

export const APP_NAV = {
  main: ["start", "comms", "knowledge", "now", "usage", "settings"],
  groups: {
    comms: ["comms", "inbox"],
    now: ["now", "agents", "timeline", "mesh", "ledger", "progress"],
    usage: ["usage", "jobs"],
    settings: ["settings", "secrets"],
  },
  names: { now: "Now", agents: "All agents" },   // In the row, where the tab's own name would say the same thing twice.
  noRow: ["comms", "settings", "agent"],         // Full-height or already has its own side list: these pages show no row. The tab stays lit.
  belongs: { agent: "now" },                     // Lights a tab without being a page in its row.
  more: ["design", "made"],
  icons: ["settings"],                           // Main places drawn as an icon at the right of the bar, not as a word tab (redesign 2026-11-04).
};
// Simple mode: only these. The rest stays reachable by "Show everything" under More.
export const SIMPLE_NAV = { main: ["comms", "settings"], more: [], only: true };

/** The tab a view counts as, or null. */
export function tabOf(id, nav) {
  if (nav.main.includes(id)) return id;
  for (const [tab, ids] of Object.entries(nav.groups || {})) if (ids.includes(id)) return tab;
  return (nav.belongs && nav.belongs[id]) || null;
}

/** What to draw. on(view) says whether this sign-in may see it; a page whose tab is off falls back to More, so it can't become unreachable. */
export function layout(views, nav, on, current) {
  const by = new Map(views.map(v => [v.id, v]));
  const main = nav.main.map(id => by.get(id)).filter(v => v && on(v));
  const shown = new Set(main.map(v => v.id));
  const groups = nav.groups || {};
  const reachable = id => { const t = tabOf(id, nav); return t && shown.has(t); };
  const placed = new Set([...nav.main, ...(nav.more || []), ...Object.values(groups).flat()]);
  const orphans = Object.values(groups).flat().filter(id => !reachable(id));
  const unnamed = nav.only ? [] : views.map(v => v.id).filter(id => !placed.has(id));
  const more = [...new Set([...(nav.more || []), ...orphans, ...unnamed])].map(id => by.get(id)).filter(v => v && on(v));
  const tab = tabOf(current, nav);
  const active = tab && shown.has(tab) ? tab : null;
  const members = active && groups[active] ? groups[active].map(id => by.get(id)).filter(v => v && on(v)) : [];
  const row = members.length > 1 && !(nav.noRow || []).includes(current) && members.some(v => v.id === current) ? members : null;
  const icons = new Set(nav.icons || []);
  return { main: main.filter(v => !icons.has(v.id)), icons: main.filter(v => icons.has(v.id)), more, active, row };
}
