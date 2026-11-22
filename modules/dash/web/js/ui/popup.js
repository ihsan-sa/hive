// Popup menus by keyboard, for every one at once. A button with aria-haspopup opened by Enter or Space puts focus on its menu's first item
// (the menu is often drawn later in the page, so Tab alone would skip it), and Escape from the menu or its button sends focus back to the button
// once the menu's own code has closed it. Pointer clicks are left alone. The menu is the [role=menu] or [role=listbox] nearest the button.
// Inside a menu, Down and Up move to the next and last item (round the ends), Home and End to the first and last, as in any menu; a radio group
// in a menu (Text size, Theme) keeps its own arrows, and a box in a menu keeps them for its text.
const MENU = "[role=menu],[role=listbox]";
const ITEM = "[role=menuitem]:not([disabled],[aria-disabled=true]),[role=option]:not([aria-disabled=true]),a[href],button:not([disabled]),input:not([disabled]),select";   // a disabled first item (Move earlier on the first card) would leave focus outside
let opener = null, wasIn = false;   // wasIn: focus was last on the button or in a menu (kept by focusin, so it holds after the menu is removed)
const menuOf = b => { for (let n = b.parentElement, i = 0; n && i < 4; n = n.parentElement, i++) { const m = n.querySelector(MENU); if (m && !m.hidden && m.getClientRects().length) return m; } return null; };
const later = (fn, tries = 6) => requestAnimationFrame(() => { if (!fn() && tries > 1) later(fn, tries - 1); });

export function popupKeys() {
  document.addEventListener("click", e => {
    const b = e.target.closest?.("[aria-haspopup]"); if (!b) return;
    opener = b; wasIn = true;
    if (e.detail !== 0) return;   // a pointer click: focus stays where the person put it
    later(() => { if (b.getAttribute("aria-expanded") !== "true") return true; const i = menuOf(b)?.querySelector(ITEM); if (i) i.focus(); return !!i; });
  });
  document.addEventListener("focusin", e => { wasIn = !!opener && (e.target === opener || !!e.target.closest?.(MENU)); });
  document.addEventListener("keydown", e => {
    const d = { ArrowDown: 1, ArrowUp: -1, Home: "first", End: "last" }[e.key];
    if (!d || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    const t = e.target, m = t.closest?.(MENU); if (!m || t.matches("input,textarea,select,[contenteditable]")) return;
    const all = [...m.querySelectorAll(ITEM)].filter(i => i.getAttribute("tabindex") !== "-1" && i.getClientRects().length); if (!all.length) return;
    e.preventDefault();
    const i = all.indexOf(t.closest(ITEM)), n = all.length;
    all[d === "first" ? 0 : d === "last" ? n - 1 : i < 0 ? (d > 0 ? 0 : n - 1) : (i + d + n) % n].focus();
  });
  addEventListener("keydown", e => {
    if (e.key !== "Escape" || !wasIn || !opener?.isConnected) return;
    const b = opener;
    later(() => { const f = document.activeElement; if (!f || f === document.body || !f.isConnected || f.closest(MENU)) b.focus(); return true; });
  }, true);
}
