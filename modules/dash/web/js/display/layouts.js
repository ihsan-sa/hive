// Which tiles each display page shows and where. Area names are tile ids (tiles.js).
// A new screen layout is one entry here and its address in dash.py's DISPLAY_PAGES. The page is always 1536 units wide
// (app.js), so fractions here mean the same thing on every screen. Keep a tile at least ~300 x 220 units at the default size.
export const LAYOUTS = {
  // sized so that at 100% every running agent, every running thread and every login shows; Ctrl + is where they start to be counted instead.
  // what's running (agents, full height) and over time (timeline, tall), whether it can keep going (usage); then what happened and who talks
  "single-screen": { cols: "minmax(0,1fr) minmax(0,2.95fr) minmax(0,1.31fr)", rows: "minmax(0,47fr) minmax(0,11fr) minmax(0,57fr)", areas: ["agents timeline usage", "agents timeline ledger", "agents mesh ledger"] },
  // screen 1, now: who is running and how they connect, and what's waiting for you
  "dual-screen/1": { cols: "minmax(0,1fr) minmax(0,2.4fr) minmax(0,1fr)", rows: "minmax(0,1.3fr) minmax(0,1fr)", areas: ["agents mesh foryou", "agents mesh hive"] },
  // screen 2, over time: the timeline wide, every login's limits full height, then the ledger and the activity charts
  "dual-screen/2": { cols: "minmax(0,1.3fr) minmax(0,1.3fr) minmax(0,1fr)", rows: "minmax(0,1.7fr) minmax(0,1fr)", areas: ["timeline timeline usage", "ledger activity usage"] },
};

/** ?tiles=mesh,ledger,usage: those tiles in an even grid instead of the page's layout. */
export function evenLayout(ids) {
  const n = Math.ceil(Math.sqrt(ids.length)), rows = [];
  for (let i = 0; i < ids.length; i += n) { const r = ids.slice(i, i + n); while (r.length < n) r.push(r[r.length - 1]); rows.push(r.join(" ")); }
  return { cols: `repeat(${n},minmax(0,1fr))`, rows: `repeat(${rows.length},minmax(0,1fr))`, areas: rows };
}
