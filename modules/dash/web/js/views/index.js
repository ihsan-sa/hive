// The view registry. Adding a view: write views/<name>.js exporting {id, label, tab, render}, then add one line here.
// Order here is the tab order on the developer route. tab: true, false, or a function of what the server allows.
// label and about may be functions of the mode (S.app.app: the app anyone installs).
import { S, can } from "../core/store.js";
import now from "./now.js";
import agent from "./agent.js";
import usage from "./usage.js";
import system from "./system.js";
import jobs from "./jobs.js";
import ledger from "./ledger.js";
import agents from "./agents.js";
import comms from "./comms.js";
import inbox from "./inbox.js";
import mesh from "./mesh.js";
import timeline from "./timeline.js";
import start from "./start.js";
import settings from "./settings.js";
import secrets from "./secrets.js";
import deck from "./deck.js";
import knowledge from "./knowledge.js";
import design from "./design.js";

export default [
  start,
  now,
  jobs,
  inbox,
  agents,
  ledger,
  mesh,
  comms,
  timeline,
  usage,
  secrets,
  settings,
  knowledge,
  design,
  agent,
  system,
  deck,
];

// In the app the bar is short: these up front, in this order; the rest under More, in that order.
// A view named in neither goes at the end of More, so a new view is still one line above.
export const APP_NAV = {
  main: ["start", "comms", "knowledge", "now", "usage", "settings"],
  more: ["design", "inbox", "agents", "ledger", "timeline", "mesh", "jobs"],
};
