// The view registry. Adding a view: write views/<name>.js exporting {id, label, tab, render}, then add one line here.
// Order here is the tab order. tab: true, false, or a function of what the server allows.
import { S, can } from "../core/store.js";
import now from "./now.js";
import agent from "./agent.js";
import usage from "./usage.js";
import system from "./system.js";
import jobs from "./jobs.js";
import ledger from "./ledger.js";
import comms from "./comms.js";
import inbox from "./inbox.js";
import mesh from "./mesh.js";
import timeline from "./timeline.js";
import { pending } from "./pending.js";
import start from "./start.js";
import settings from "./settings.js";
import plans from "./plans.js";
import secrets from "./secrets.js";
import deck from "./deck.js";


export default [
  start,
  now,
  jobs,
  inbox,
  ledger,
  mesh,
  comms,
  timeline,
  usage,
  plans,
  secrets,
  settings,
  agent,
  system,
  deck,
];
