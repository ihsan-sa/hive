// Three one-click first jobs for an empty conversation. Each needs nothing from the user's machine, stays small (Sonnet, two agents), and has
// one agent do the work while a second checks it, talking on the board. Clicking only fills the composer: the user reads and edits, then sends.
import { h } from "../core/dom.js";
import { button } from "./kit.js";
import { draft, focusComposer } from "./composer.js";

const TEAM = " Keep it light: use two agents on Sonnet, one to do it and one to check it, and have them talk it over on the board.";
export const FIRST_JOBS = [
  ["Explain this computer to me", "Look at this computer (what it is, how much memory and disk space it has, what's installed) and write me a short summary in plain English." + TEAM + " The checker should compare every claim against the machine."],
  ["Plan five easy dinners", "Plan five easy dinners for two people, with one shopping list for all of them." + TEAM + " The checker should make sure the list covers every meal and nothing is hard to cook."],
  ["Write a beginner's guide", "Write a one-page beginner's guide to baking sourdough bread." + TEAM + " The checker should fix anything wrong or unclear."],
];
/** The row of buttons. Cheap to show: it is only buttons. */
export const firstJobs = () => h("div", { class: "frow", "data-key": "starters", role: "group", "aria-label": "First jobs to try" },
  FIRST_JOBS.map(([label, text]) => button(label, () => { draft(null).text = text; focusComposer(); }, { size: "s" })));
/** One quiet line once a first message has gone: the page can be closed. */
export const closeLine = () => h("p", { class: "meta", "data-key": "closeok" }, "You can close this page. Your Hive carries on, and the answer is here when you come back.");
