// Theme: Auto follows the system; Light and Dark are kept per browser.
const KEY = "theme";
export const THEMES = [["auto", "Auto"], ["light", "Light"], ["dark", "Dark"]];
export function getTheme() { const t = localStorage.getItem(KEY); return t === "light" || t === "dark" ? t : "auto"; }
export function setTheme(t) {
  const r = document.documentElement;
  if (t === "auto") { delete r.dataset.theme; localStorage.removeItem(KEY); } else { r.dataset.theme = t; localStorage.setItem(KEY, t); }
  dispatchEvent(new Event("themechange"));
}
export function cycleTheme() { const i = THEMES.findIndex(x => x[0] === getTheme()); setTheme(THEMES[(i + 1) % 3][0]); }
/** Resolve a CSS colour token to a value canvas can use (light-dark() is resolved by the browser). */
const probe = () => probe.el || (probe.el = Object.assign(document.createElement("i"), { hidden: true }), document.body.append(probe.el), probe.el);
export function color(token) { const p = probe(); p.style.color = `var(${token})`; return getComputedStyle(p).color; }
