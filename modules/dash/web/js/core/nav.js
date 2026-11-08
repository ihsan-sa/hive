// Where each view sits in the app's top bar. Pure: no page, no store, so test/test_nav.mjs can check it in node.
//
// Three places and one rule, so the bar doesn't grow as views and modules arrive:
// - main: the tabs. Keep it to what people open daily (MAX_MAIN). A new tab needs a reason.
// - groups: a tab's own pages, a row in each page's head (the page-head subnav, R7 step 6). Each page keeps its own address. Put a view here when it's another way to look at something a tab already covers.
// - more: everything else. Tools go here, in order. A view named nowhere lands here too, so a new view is still one line in views/index.js.
export const MAX_MAIN = 6, MAX_TOOLS = 6;   // MAX_MAIN counts word tabs: an icon place (Settings) takes no room in the bar

export const APP_NAV = {
  main: ["start", "comms", "knowledge", "now", "usage", "settings"],   // Chat · Knowledge · Agents · Usage, Settings as an icon. Where things stand is the home itself since 2026-11-07: one card per focus (person, 11071400de5b); the Work tab went with it.
  groups: {
    now: ["now", "agents", "timeline", "mesh", "ledger", "progress", "flow"],   // Agents' row: who is working, every agent, the timeline, the map, the history, the work in time
    usage: ["usage", "jobs"],
  },
  names: { now: "Now", agents: "All agents", made: "Files" },   // In the row, where the tab's own name would say the same thing twice.
  heads: { now: "Agents", usage: "This computer" },        // A group's heading in the More menu (moreGroups: the desktop "…" and the phone's sheet), where the tab's own name would mislead: Usage's other pages are about this machine.
  noRow: ["comms", "settings", "agent", "focus"],         // Full-height or already has its own side list: these pages show no row in their head. The tab stays lit.
  belongs: { agent: "now", secrets: "settings", focus: "comms", needs: "comms", yours: "comms", inbox: "comms", mail: "comms", made: "comms" }, // Lights a tab without being a page in its row. Needs you is the home's line, a page behind it; a focus page is the home a level down. Chat's pages (Yours, Inbox, Mail, Files) are the foot of its side list, so no row repeats them (R6 review, finding 12).
  more: ["design"],                             // The Work page went (R5): the focus pages carry what it showed, Flow's lanes open its card.
  icons: ["settings"],                           // Main places drawn as an icon at the right of the bar, not as a word tab (redesign 2026-11-04).
};
// Simple mode: only these. The rest stays reachable by "Show everything" under More.
export const SIMPLE_NAV = { main: ["comms", "settings"], more: [], icons: ["settings"], only: true };

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
  const owned = [...Object.values(groups).flat(), ...Object.keys(nav.belongs || {})];   // pages a tab stands for: in its row, or reached from its pages
  const placed = new Set([...nav.main, ...(nav.more || []), ...owned]);
  const orphans = owned.filter(id => !reachable(id));
  const unnamed = nav.only ? [] : views.map(v => v.id).filter(id => !placed.has(id));
  const more = [...new Set([...(nav.more || []), ...orphans, ...unnamed])].map(id => by.get(id)).filter(v => v && on(v));
  const tab = tabOf(current, nav);
  const active = tab && shown.has(tab) ? tab : null;
  const members = active && groups[active] ? groups[active].map(id => by.get(id)).filter(v => v && on(v)) : [];
  const row = members.length > 1 && !(nav.noRow || []).includes(current) && members.some(v => v.id === current) ? members : null;
  const icons = new Set(nav.icons || []);
  return { main: main.filter(v => !icons.has(v.id)), icons: main.filter(v => icons.has(v.id)), more, active, row };
}

/** The page-head row (kit.js pageNav): layout().row as plain items, in the group's order, the open one marked. label(v) is the view's own name; nav.names wins where it is set. */
export function rowItems(row, nav, current, label) {
  return (row || []).map(v => ({ id: v.id, label: (nav.names && nav.names[v.id]) || label(v), current: v.id === current }));
}

/** The grouped More menu (HANDOFF "Top bar"), the same on a desktop "…" and the phone's sheet: [heading, views] pairs.
 *  Conversations (Chat's own pages), then each shown tab's row less what the bar already has, then the rest: tools (nav.more)
 *  join the last tab group (This computer), anything else on the bar's side ends under "More", the icons (Settings) last under a rule.
 *  bar: the ids already on the bar, left out. label(v) names a tab for its heading. */
export const CHAT_PAGES = ["yours", "inbox", "made", "mail"];
export function moreGroups(views, nav, on, L, bar, label) {
  const by = new Map(views.map(v => [v.id, v])), shown = new Set([...L.main, ...L.icons].map(v => v.id)), used = new Set(bar);
  const pick = ids => ids.map(id => by.get(id)).filter(v => v && on(v) && !used.has(v.id) && used.add(v.id));
  const out = [];
  if (shown.has("comms")) out.push(["Conversations", pick(CHAT_PAGES.filter(id => (nav.belongs || {})[id] === "comms"))]);
  for (const [tab, ids] of Object.entries(nav.groups || {})) if (shown.has(tab)) out.push([groupHead(tab, nav, label(by.get(tab))), pick(ids)]);
  const tools = pick(L.more.map(v => v.id)), last = out.length > (shown.has("comms") ? 1 : 0) ? out[out.length - 1] : null;
  if (last) last[1].push(...tools); else out.push(["More", tools]);
  out.push(["More", pick(L.main.map(v => v.id))]);
  out.push(["", pick(L.icons.map(v => v.id))]);   // Settings, last, under a rule
  return out.filter(([, vs]) => vs.length);
}

/** A group's heading in a menu: nav.heads, else the tab's own label. */
export const groupHead = (tab, nav, label) => (nav.heads && nav.heads[tab]) || label || tab;
