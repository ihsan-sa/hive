// The browser's Back closes what is open on top of the page (a menu, a window, the reply pane, the agent panel) and stays on the page,
// as Back does on a phone. Every one at once, without each menu knowing: when something opens, a history entry for the same address is
// added; Back takes it away (no hashchange, so the page doesn't route) and the open thing is closed the way a person would close it.
// Closed some other way (a pick, Escape, a press outside), the extra entry is taken back off, so the next Back still goes to the page before.
// A page change under an open menu closes it too: a menu or pane from the page you left is never left over the next one.
// Checked by test/drive_app_back.mjs (part=3).
const OPEN = "[aria-haspopup]:not([aria-haspopup=false])[aria-expanded=true], dialog[open], .scrim, aside.qa, aside.panel";
const isOpen = () => [...document.querySelectorAll(OPEN)].some(e => e.getClientRects().length);
const MARK = "h2back";
let pushed = false, queued = false, closing = false;
// taking: the Back that takes the extra entry off is on its way (history.back() lands later). A page change before it lands (a link, or a test's
// next address, under load) would be undone by it: over keeps that address, and the Back's landing goes there again (drive_subagents, 2026-11-22).
let taking = null;
// Called on each popstate and hashchange while the Back is on its way. Another address is the page moving on (it comes as a popstate or a
// hashchange, in either order); the address the Back left from is the Back landing, which then goes on to where the page moved, if it did.
function landed() {
  if (Date.now() - taking.t > 10e3) { taking = null; return false; }   // a Back the browser dropped must not catch a later one of the person's
  if (location.href !== taking.from) { taking.over = location.href; return false; }
  const over = taking.over; taking = null;
  if (over) { location.replace(over); return true; }
  return false;
}

// The way a person closes it, in order: a window's own close, Escape, a press outside, the menu's button again. Views redraw on a later
// frame, so each way gets a moment to work before the next is tried (clicking the button of a menu already closing would open it again).
const STEPS = [
  () => { for (const d of document.querySelectorAll("dialog[open]")) d.close(); },
  () => (document.activeElement || document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })),
  () => [...document.querySelectorAll(".scrim")].filter(e => e.getClientRects().length).pop()?.click(),
  () => [...document.querySelectorAll("[aria-haspopup]:not([aria-haspopup=false])[aria-expanded=true]")].filter(e => e.getClientRects().length).pop()?.click(),
];
function shut(i = 0) {
  closing = i < STEPS.length && isOpen();   // no new entry for a menu that is on its way out
  if (!closing) return;
  STEPS[i]();
  setTimeout(() => shut(i + 1), 60);
}

function check() {
  queued = false;
  const open = isOpen();
  if (open && !pushed && !closing) { history.pushState({ [MARK]: 1 }, "", location.href); pushed = true; }
  else if (!open && pushed) { pushed = false; if (history.state && history.state[MARK]) { taking = { from: location.href, over: null, t: Date.now() }; history.back(); } }
}

export function backCloses() {
  new MutationObserver(() => { if (!queued) { queued = true; requestAnimationFrame(check); } })
    .observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["aria-expanded", "open", "class"] });
  addEventListener("popstate", e => {
    if (taking && landed()) return;
    if (pushed && !(e.state && e.state[MARK])) { pushed = false; if (isOpen()) shut(); return; }
    // Landed on an extra entry whose menu went away with a page change: nothing to close here, so this Back goes on to the page before.
    if (e.state && e.state[MARK] && !isOpen()) history.back();
  });
  addEventListener("hashchange", () => { if (taking && landed()) return; if (pushed && isOpen()) { pushed = false; shut(); } });
}
