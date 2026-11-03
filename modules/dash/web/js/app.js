// App: mounts the shell, routes, and re-renders the current view when data or the route changes.
// Rendering is a morph of a freshly built tree onto the live one: only changed nodes are touched.
import { S, can, setupDone, start, subscribe } from "./core/store.js";
import { mount, delegate, selecting } from "./core/dom.js";
import { go, parse } from "./core/router.js";
import { topBar } from "./ui/shell.js";
import views, { APP_NAV } from "./views/index.js";
import { resume, setViews, watch } from "./deck/control.js";
import { offer as offerDeckTour } from "./deck/tour.js";
import { offerTour } from "./ui/tour.js";
import { watchVersion } from "./ui/newversion.js";

const byId = new Map(views.map(v => [v.id, v]));
const top = document.getElementById("top"), main = document.getElementById("main");
const scrolls = {};
// Where an empty address lands: the chat in the app, Now on the developer route.
const home = () => byId.get(S.app.app ? "comms" : "now");
let cur = home(), arg = null, raf = 0, timer = 0;

const ctx = { update: () => schedule(true), get arg() { return arg; } };

function render(force) {
  if (S.live) document.title = `Hive · ${S.live.agents.length} running`;
  mount(top, topBar({ views, current: cur.id, update: ctx.update, nav: APP_NAV }));
  if (!force && selecting()) return;          // hold still while text is selected
  mount(main, cur.render(ctx));
}
function schedule(force) { if (raf) { raf.force ||= force; return; } raf = { force }; queueMicrotask(() => { const f = raf.force; raf = 0; render(f); }); }

function route() {
  if (parse().id === "plans") { location.replace("#knowledge/projects"); return; }   // the old Plans page lives in Knowledge now
  const r = parse(), next = (r.id && byId.get(r.id)) || home();
  if (next !== cur || r.arg !== arg) {
    scrolls[cur.id + (arg || "")] = scrollY;
    if (cur.leave) cur.leave();
    clearInterval(timer);
    cur = next; arg = r.arg;
    if (cur.enter) cur.enter(ctx);
    if (cur.poll) { cur.poll.run(ctx); timer = setInterval(() => { if (!document.hidden) cur.poll.run(ctx); }, cur.poll.every); }
    render(true);
    scrollTo(0, scrolls[cur.id + (arg || "")] || 0);
  }
}

delegate(document);
setViews(views); watch(() => schedule(false)); offerDeckTour(); resume();
addEventListener("hashchange", route);
addEventListener("keydown", e => { if (e.key === "Escape" && cur.escape) cur.escape(ctx); });
const markRoute = () => document.documentElement.toggleAttribute("data-dev", !S.app.app);   // the developer route keeps its dense log (components.css)
markRoute();
watchVersion();
subscribe(() => { markRoute(); schedule(false); });
start().then(() => {
  markRoute();
  offerTour();
  if (S.app.app && !location.hash && !setupDone() && can("post")) return go("start");   // first run lands on setup
  route(); render(true);
});
route();
