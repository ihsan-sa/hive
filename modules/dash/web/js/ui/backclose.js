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
  else if (!open && pushed) { pushed = false; if (history.state && history.state[MARK]) history.back(); }
}

export function backCloses() {
  new MutationObserver(() => { if (!queued) { queued = true; requestAnimationFrame(check); } })
    .observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["aria-expanded", "open", "class"] });
  addEventListener("popstate", e => {
    if (pushed && !(e.state && e.state[MARK])) { pushed = false; if (isOpen()) shut(); return; }
    // Landed on an extra entry whose menu went away with a page change: nothing to close here, so this Back goes on to the page before.
    if (e.state && e.state[MARK] && !isOpen()) history.back();
  });
  addEventListener("hashchange", () => { if (pushed && isOpen()) { pushed = false; shut(); } });
}
