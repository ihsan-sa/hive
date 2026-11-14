// App: mounts the shell, routes, and re-renders the current view when data or the route changes.
// Rendering is a morph of a freshly built tree onto the live one: only changed nodes are touched.
import "./core/lang.js";   // first: the interface language has to be set before anything is built
import { brandIcon, brandName } from "./core/brand.js";
import { dropFiles } from "./ui/dropfiles.js";
import { S, can, needsAccount, onOwnFail, setupDone, start, subscribe } from "./core/store.js";
import { loadFoci } from "./core/foci.js";
import { loadNeedsYou } from "./core/needs.js";
import { h, mount, delegate, selecting, copyText, tr } from "./core/dom.js";
import { button } from "./ui/kit.js";
import { go, parse } from "./core/router.js";
import { topBar } from "./ui/shell.js";
import { mobileNav, setView } from "./ui/mobilenav.js";
import views, { APP_NAV, SIMPLE_NAV } from "./views/index.js";
import { simpleOn } from "./core/simple.js";
import { resume, setViews, watch } from "./deck/control.js";
import { offer as offerDeckTour } from "./deck/tour.js";
import { offerTour } from "./ui/tour.js";
import { initSwitcher } from "./ui/switcher.js";
import { watchVersion } from "./ui/newversion.js";

const byId = new Map(views.map(v => [v.id, v]));
const top = document.getElementById("top"), main = document.getElementById("main");
const mnav = document.body.appendChild(Object.assign(document.createElement("div"), { id: "mnav" }));   // phone bottom bar (css/phone.css hides it on wider screens)
new ResizeObserver(() => { if (top.offsetHeight) document.documentElement.style.setProperty("--bar-h", top.offsetHeight + "px"); }).observe(top);   // phones wrap the bar and a tab's row adds to it; sticky headers and full-height pages follow
const scrolls = {};
// Where an empty address lands: the chat in the app, Now on the developer route.
const home = () => byId.get(S.app.app ? "comms" : "now");
let booted = false;   // true once S.app is in: home() depends on it, so a stale address waits for it before it is corrected
let cur = home(), arg = null, raf = 0, timer = 0, entered = false;   // entered: the first route() enters the page even when it is home, so a fresh load draws it before the data comes

const ctx = { update: () => schedule(true), get arg() { return arg; } };

function render(force) {
  brandIcon();
  mount(top, topBar({ views, current: cur.id, update: ctx.update, nav: simpleOn() ? SIMPLE_NAV : APP_NAV }));
  setView(cur.id, arg);   // body[data-view]: css/phone.css hides the bars in a conversation or a file
  mount(mnav, mobileNav({ views, current: cur.id, update: ctx.update, nav: simpleOn() ? SIMPLE_NAV : APP_NAV }));
  if (!force && selecting()) return;          // hold still while text is selected
  let page;
  try { page = cur.render(ctx); } catch (e) { console.error("view " + cur.id + " failed:", e); page = crashPage(cur, e); }
  mount(main, page);
  where();
  // On a phone the page head's row (.subnav) scrolls sideways: bring the page you are on into view once, when you arrive. Sideways only, so it never moves the page.
  const here = main.querySelector(".subnav a[aria-current=page]"), row = here && here.parentNode;
  if (row && (centred !== cur.id || row !== centredRow)) { centred = cur.id; centredRow = row; centreTab(here, row); }
  else if (!row) centred = centredRow = null;
}
// Every screen says where you are (WCAG 2.4.2, 2.4.6, 1.3.1): the tab's title names this screen, then its page, then the brand; a screen with no h1 of its own
// (a chart page, or a phone layout that hides the page head) gets a screen-reader-only one before main, so each screen has exactly one.
const line = e => (e && (e.innerText || e.textContent) || "").split("\n")[0].replace(/\s+/g, " ").trim();
const clip = s => (s.length > 60 ? s.slice(0, 59).trimEnd() + "…" : s);
const shown = e => e.getClientRects().length > 0;
const h1 = document.body.insertBefore(Object.assign(document.createElement("h1"), { id: "screen-h1", className: "vh" }), main);
function screenName() {
  const page = typeof cur.label === "function" ? cur.label() : cur.label;
  let inner = cur.title ? cur.title(arg) : null;
  if (inner == null) {
    const head = [...main.querySelectorAll("h1")].find(shown), sub = main.querySelector(".subnav a[aria-current=page]") || main.querySelector("nav a[aria-current=page] > span");   // a section menu (Settings) keeps its name in the link's first span, a status note after it
    inner = arg ? [line(head), line(sub)].find(x => x && x !== page) || decodeURIComponent(arg).split("/").pop() : line(sub);
  }
  return [clip(inner || ""), page].filter((x, i, a) => x && a.indexOf(x) === i).join(" · ");
}
function where() {
  const name = screenName(), brand = brandName() || "Hive";
  document.title = `${name} · ${brand}` + (S.live ? ` · ${S.live.agents.length} running` : "");
  const own = [...main.querySelectorAll("h1")].some(shown);
  h1.hidden = own; const shownName = tr(name); if (!own && h1.textContent !== shownName) h1.textContent = shownName;
}
addEventListener("resize", () => where());   // a phone layout can hide or show the page's own head
const centreTab = (here, row) => { const a = here.getBoundingClientRect(), r = row.getBoundingClientRect(); row.scrollLeft += a.left - r.left - (r.width - a.width) / 2; };
// Turning a phone or narrowing the window makes the row scroll where it fitted before, so the page you are on is brought into view again. Width only: a phone's keyboard changes the height.
let tabW = innerWidth, tabT = 0;
addEventListener("resize", () => { if (innerWidth === tabW) return; tabW = innerWidth; clearTimeout(tabT); tabT = setTimeout(() => { const here = main.querySelector(".subnav a[aria-current=page]"); if (here) centreTab(here, here.parentNode); }, 120); });
let centred = null, centredRow = null;   // the page whose row was last brought into view, and that row: a page that draws its head again (Progress, Flow once their data lands) gets a new row at scrollLeft 0, so it is centred again (R7b)
// A view that throws shows this instead of leaving the page before it on screen.
function crashPage(v, e) {
  const detail = `${v.id}: ${e && e.stack || e}`;
  return h("div", { class: "view-crash", role: "alert", "data-key": "crash-" + v.id },
    h("h2", null, "This page hit a problem"),
    h("p", null, "The rest of the app still works. Reloading usually clears it."),
    h("div", { class: "row" }, button("Reload", () => location.reload()), button("Copy details", (ev, b) => { copyText(detail).then(() => { b.textContent = tr("Copied"); }, () => { b.textContent = tr("Couldn't copy"); }); }, { kind: "quiet" })));
}
function schedule(force) { if (raf) { raf.force ||= force; return; } raf = { force }; queueMicrotask(() => { const f = raf.force; raf = 0; render(f); }); }

function route() {
  if (parse().id === "plans") { location.replace("#knowledge/projects"); return; }   // the old Plans page lives in Knowledge now
  const r = parse();
  if (booted && r.id && !byId.get(r.id)) { location.replace("#" + home().id); return; }   // a stale or mistyped address lands on the first page and says so in the address bar, so Back and reload agree with what is on screen
  const next = (r.id && byId.get(r.id)) || home();
  if (!entered || next !== cur || r.arg !== arg) {
    entered = true;
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
import { backCloses } from "./ui/backclose.js"; backCloses();
delegate(document); dropFiles(document);
setViews(views); initSwitcher(views); watch(() => schedule(false)); offerDeckTour(); resume();
// The classic scrollbar's width on this browser and zoom, for the top bar on pages with no window scrollbar (base.css).
const measureBar = () => { const d = document.createElement("div"); d.style.cssText = "position:absolute;top:-200px;width:100px;height:100px;overflow:scroll;visibility:hidden"; document.body.append(d); document.documentElement.style.setProperty("--sbw", d.offsetWidth - d.clientWidth + "px"); d.remove(); };
measureBar(); addEventListener("resize", measureBar);
addEventListener("hashchange", route);
addEventListener("keydown", e => { if (e.key === "Escape" && cur.escape) cur.escape(ctx); });
const markRoute = () => document.documentElement.toggleAttribute("data-dev", !S.app.app);   // the developer route keeps its dense log (components.css)
markRoute();
watchVersion();
let hiveAway = false;
// A page's own read failed while the Hive still answers (core/store.js onOwnFail): run the page again in 4 s, then every 8 s (or its own timer, if sooner) while they keep failing, so its "couldn't refresh" note and stale lines go within about 10 s of the read recovering. A failed local read every 8 s costs little. Quiet for a minute starts the wait over.
let soon = 0, soonWait = 0, lastOwnFail = 0;
onOwnFail(() => {
  const t = Date.now();
  if (t - lastOwnFail > 60e3) soonWait = 0;
  lastOwnFail = t;
  if (soon || hiveAway) return;
  soonWait = Math.min(soonWait ? soonWait * 2 : 4e3, 8e3, cur.poll?.every || 8e3);
  const page = cur;
  soon = setTimeout(() => { soon = 0; if (hiveAway || cur !== page || document.hidden) return; if (cur.poll) cur.poll.run(ctx); if (cur.back) cur.back(ctx); }, soonWait);
});
subscribe(w => {
  markRoute(); schedule(false);
  if (w?.failed) hiveAway = true;
  else if (w?.live && hiveAway) { hiveAway = false; if (S.foci !== undefined) loadFoci(true); if (S.needsYou !== undefined) loadNeedsYou(true); if (cur.poll) cur.poll.run(ctx); if (cur.back) cur.back(ctx); }   // the Hive came back: the shared reads and the open page read now, or Needs you would say "couldn't refresh" for up to five more minutes; a page with no poll that reads on enter names its read as back
});
import { reportLoad } from "./core/loadtime.js";
start().then(() => {
  booted = true; reportLoad();
  markRoute();
  offerTour();
  const lapsed = S.app.setup?.has_posts && S.app.settings?.setup_done !== false;   // a Hive already in use whose login has expired isn't a first run: it lands on the chat, with the band saying what to do
  if (S.app.app && !location.hash && !setupDone() && !lapsed && can("post")) return go(simpleOn() && S.app.settings?.setup_done !== false && !needsAccount() ? "comms" : "start");   // first run lands on setup; simple mode's first page is the chat, with its ideas
  route(); render(true);
});
route();
