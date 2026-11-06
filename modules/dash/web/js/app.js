// App: mounts the shell, routes, and re-renders the current view when data or the route changes.
// Rendering is a morph of a freshly built tree onto the live one: only changed nodes are touched.
import { brandIcon, brandName } from "./core/brand.js";
import { dropFiles } from "./ui/dropfiles.js";
import { S, can, needsAccount, setupDone, start, subscribe } from "./core/store.js";
import { h, mount, delegate, selecting, copyText } from "./core/dom.js";
import { button } from "./ui/kit.js";
import { go, parse } from "./core/router.js";
import { topBar } from "./ui/shell.js";
import { mobileNav } from "./ui/mobilenav.js";
import views, { APP_NAV, SIMPLE_NAV } from "./views/index.js";
import { simpleOn } from "./core/simple.js";
import { awayWire } from "./ui/away.js";
import { resume, setViews, watch } from "./deck/control.js";
import { offer as offerDeckTour } from "./deck/tour.js";
import { offerTour } from "./ui/tour.js";
import { initSwitcher } from "./ui/switcher.js";
import { watchVersion } from "./ui/newversion.js";

const byId = new Map(views.map(v => [v.id, v]));
const top = document.getElementById("top"), main = document.getElementById("main");
const mnav = document.body.appendChild(Object.assign(document.createElement("div"), { id: "mnav" }));   // phone tab bar (css/mobile.css hides it on wider screens)
new ResizeObserver(() => { if (top.offsetHeight) document.documentElement.style.setProperty("--bar-h", top.offsetHeight + "px"); }).observe(top);   // phones wrap the bar and a tab's row adds to it; sticky headers and full-height pages follow
const scrolls = {};
// Where an empty address lands: the chat in the app, Now on the developer route.
const home = () => byId.get(S.app.app ? "comms" : "now");
let cur = home(), arg = null, raf = 0, timer = 0;

const ctx = { update: () => schedule(true), get arg() { return arg; } };

function render(force) {
  awayWire(ctx);
  if (S.live) document.title = brandName() ? `${brandName()} · ${S.live.agents.length} running` : `Hive · ${S.live.agents.length} running`;
  brandIcon();
  mount(top, topBar({ views, current: cur.id, update: ctx.update, nav: simpleOn() ? SIMPLE_NAV : APP_NAV }));
  // On a phone the sub-page row scrolls sideways: keep the page you are on in view.
  const here = top.querySelector(".subtab[aria-current=page]");
  if (here && here.scrollIntoView) here.scrollIntoView({ block: "nearest", inline: "center" });
  mount(mnav, mobileNav({ views, current: cur.id, update: ctx.update, nav: simpleOn() ? SIMPLE_NAV : APP_NAV }));
  if (!force && selecting()) return;          // hold still while text is selected
  let page;
  try { page = cur.render(ctx); } catch (e) { console.error("view " + cur.id + " failed:", e); page = crashPage(cur, e); }
  mount(main, page);
}
// A view that throws shows this instead of leaving the page before it on screen.
function crashPage(v, e) {
  const detail = `${v.id}: ${e && e.stack || e}`;
  return h("div", { class: "view-crash", role: "alert", "data-key": "crash-" + v.id },
    h("h2", null, "This page hit a problem"),
    h("p", null, "The rest of the app still works. Reloading usually clears it."),
    h("div", { class: "row" }, button("Reload", () => location.reload()), button("Copy details", (ev, b) => { copyText(detail).then(() => { b.textContent = "Copied"; }, () => { b.textContent = "Couldn't copy"; }); }, { kind: "quiet" })));
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

import { initAlias } from "./core/alias.js"; initAlias();
import { initEaster } from "./core/easter.js"; initEaster();
import { phoneInit } from "./ui/phone.js"; phoneInit();
import { agentHoverInit } from "./ui/agentHover.js"; agentHoverInit();
import { popupKeys } from "./ui/popup.js"; popupKeys();
delegate(document); dropFiles(document);
setViews(views); initSwitcher(views); watch(() => schedule(false)); offerDeckTour(); resume();
addEventListener("hashchange", route);
addEventListener("keydown", e => { if (e.key === "Escape" && cur.escape) cur.escape(ctx); });
const markRoute = () => document.documentElement.toggleAttribute("data-dev", !S.app.app);   // the developer route keeps its dense log (components.css)
markRoute();
watchVersion();
subscribe(() => { markRoute(); schedule(false); });
start().then(() => {
  markRoute();
  offerTour();
  const lapsed = S.app.setup?.has_posts && S.app.settings?.setup_done !== false;   // a Hive already in use whose login has expired isn't a first run: it lands on the chat, with the band saying what to do
  if (S.app.app && !location.hash && !setupDone() && !lapsed && can("post")) return go(simpleOn() && S.app.settings?.setup_done !== false && !needsAccount() ? "comms" : "start");   // first run lands on setup; simple mode's first page is the chat, with its ideas
  route(); render(true);
});
route();
