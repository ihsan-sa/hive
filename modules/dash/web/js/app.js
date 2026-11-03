// App: mounts the shell, routes, and re-renders the current view when data or the route changes.
// Rendering is a morph of a freshly built tree onto the live one: only changed nodes are touched.
import { S, can, setupDone, start, subscribe } from "./core/store.js";
import { mount, delegate, selecting } from "./core/dom.js";
import { go, parse } from "./core/router.js";
import { topBar } from "./ui/shell.js";
import views from "./views/index.js";
import { resume, setViews, watch } from "./deck/control.js";
import { offer as offerTour } from "./deck/tour.js";

const byId = new Map(views.map(v => [v.id, v]));
const top = document.getElementById("top"), main = document.getElementById("main");
const scrolls = {};
let cur = byId.get("now"), arg = null, raf = 0, timer = 0;

const ctx = { update: () => schedule(true), get arg() { return arg; } };

function render(force) {
  if (S.live) document.title = `Hive · ${S.live.agents.length} running`;
  mount(top, topBar({ views, current: cur.id, update: ctx.update }));
  if (!force && selecting()) return;          // hold still while text is selected
  mount(main, cur.render(ctx));
}
function schedule(force) { if (raf) { raf.force ||= force; return; } raf = { force }; queueMicrotask(() => { const f = raf.force; raf = 0; render(f); }); }

function route() {
  const r = parse(), next = byId.get(r.id) || byId.get("now");
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
setViews(views); watch(() => schedule(false)); offerTour(); resume();
addEventListener("hashchange", route);
addEventListener("keydown", e => { if (e.key === "Escape" && cur.escape) cur.escape(ctx); });
subscribe(() => schedule(false));
start().then(() => {
  if (S.app.app && !location.hash && !setupDone() && can("post")) return go("start");   // first run lands on setup
  route(); render(true);
});
route();
