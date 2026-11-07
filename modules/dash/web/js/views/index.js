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
import focus from "./focus.js";
import work from "./work.js";
import mail from "./mail.js";
import inbox from "./inbox.js";
import flow from "./flow.js";
import mesh from "./mesh.js";
import timeline from "./timeline.js";
import start from "./start.js";
import settings from "./settings.js";
import secrets from "./secrets.js";
import deck from "./deck.js";
import knowledge from "./knowledge.js";
import kbtime from "./kbtime.js";
import design from "./design.js";
import made from "./made.js";
import progress from "./progress.js";

// Registry order is the developer look's tab order (and the Deck's left pads): keep the groups together.
export default [
  now,
  comms,
  focus,
  work,
  agents,
  timeline,
  mesh,
  ledger,
  progress,
  usage,
  jobs,
  start,
  flow,
  inbox,
  mail,
  made,
  secrets,
  settings,
  knowledge,
  kbtime,
  design,
  agent,
  system,
  deck,
];

// Where each view sits in the app's bar: core/nav.js.
export { APP_NAV, SIMPLE_NAV } from "../core/nav.js";
