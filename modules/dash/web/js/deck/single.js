// The deck's EXIT button: single screen in full screen, and back. One document throughout: a page load would drop full screen and the MIDI link.
// A MIDI press isn't a user gesture, so the browser may refuse full screen until a click or key. Then the page asks for one. A window that already fills the screen (F11) needs nothing.
import { h } from "../core/dom.js";

let frame = null, ask = null, askTimer = 0, wasFull = false, zoom = "", overflow = "";
export const singleOpen = () => !!frame;
const src = () => {
  const t = document.documentElement.dataset.theme, dark = t ? t === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;   // no theme set: Auto
  return /preview/.test(location.pathname) ? `preview-display.html?page=single-screen${dark ? "/dark" : ""}` : `/single-screen${dark ? "/dark" : ""}`;
};
const dropAsk = () => { clearTimeout(askTimer); removeEventListener("keydown", onKey, true); if (ask) ask.remove(); ask = null; };
const full = () => document.documentElement.requestFullscreen();
function onKey(e) { if (e.key === "Escape") return dropAsk(); e.preventDefault(); dropAsk(); full().catch(() => {}); }
function askClick() {
  ask = h("button", { type: "button", class: "deck-single-ask", onclick: () => { dropAsk(); full().catch(() => {}); } }, "Click, or press a key, for full screen");
  document.body.append(ask);
  addEventListener("keydown", onKey, true);
  askTimer = setTimeout(dropAsk, 10e3);
}
const fillsScreen = () => !!document.fullscreenElement || matchMedia("(display-mode: fullscreen)").matches || (innerWidth >= screen.width && innerHeight >= screen.height);

export function toggleSingle(say) { return frame ? leaveSingle() : enter(say); }
function enter(say) {
  frame = h("iframe", { class: "deck-single", src: src(), title: "Single screen" });
  document.body.append(frame);
  const r = document.documentElement;
  zoom = r.style.zoom; r.style.zoom = "";   // TEMPO right's zoom on the dashboard would scale the frame too
  overflow = r.style.overflow; r.style.overflow = "hidden"; r.style.scrollbarGutter = "auto";   // (the stable gutter would still leave a strip)  and a fixed frame can't cover the dashboard's scrollbar
  wasFull = !!document.fullscreenElement;
  if (fillsScreen()) return;
  full().catch(() => { if (frame) { askClick(); if (say) say("Single screen. Press EXIT to leave"); } });
}
export function leaveSingle() {
  dropAsk();
  if (frame) { frame.remove(); const r = document.documentElement; r.style.zoom = zoom; r.style.overflow = overflow; r.style.scrollbarGutter = ""; }
  frame = null; wasFull = false;
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
}
/** Pass a deck message to the page in the frame. It may still be loading. */
export function sendSingle(e, say) { const w = frame && frame.contentWindow; if (w && w.deckEvent) w.deckEvent(e, say); }

// Esc leaves full screen in the browser, before the page hears it: that leaves single screen too.
document.addEventListener("fullscreenchange", () => { if (document.fullscreenElement) wasFull = true; else if (wasFull && frame) leaveSingle(); });
addEventListener("keydown", e => { if (e.key === "Escape" && frame && !ask) leaveSingle(); });
// A link in the frame, or Esc there.
addEventListener("message", e => {
  if (e.origin !== location.origin || !e.data || e.data.leaveSingle === undefined || !frame || e.source !== frame.contentWindow) return;
  leaveSingle();
  if (e.data.leaveSingle) location.hash = e.data.leaveSingle;
});
