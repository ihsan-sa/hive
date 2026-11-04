// The hive's name and logo, set in Settings. The server's /api/app carries them; without one the app keeps its own name.
import { h, s } from "./dom.js";
import { S } from "./store.js";

export const DEFAULT_NAME = "Hive";
export const brandName = () => (S.app && S.app.brand && S.app.brand.name) || null;
export const brandLogo = () => (S.app && S.app.brand && S.app.brand.logo) || null;
/** What goes in a top bar: the logo, if there is one, and the name. */
/** The built-in mark: a honeycomb cell with an H, in the accent colour, so it follows light and dark. */
const hexMark = () => s("svg", { class: "brand-logo brand-hex", viewBox: "0 0 32 32", "aria-hidden": "true", "data-key": "logo" },
  s("path", { d: "M16 2.5 27.7 9.25v13.5L16 29.5 4.3 22.75V9.25z", fill: "var(--accent)", stroke: "var(--accent)", "stroke-width": "2.4", "stroke-linejoin": "round" }),
  s("path", { d: "M11.5 10.5v11M20.5 10.5v11M11.5 16h9", fill: "none", stroke: "var(--bg, #fff)", "stroke-width": "2.6", "stroke-linecap": "round" }));
export const brandMark = () => [brandLogo() ? h("img", { class: "brand-logo", src: brandLogo(), alt: "", "data-key": "logo" }) : hexMark(), h("span", { class: "brand-name", "data-key": "name" }, brandName() || DEFAULT_NAME)];
/** The browser tab's icon follows the logo. */
export function brandIcon() {
  const href = brandLogo();
  let l = document.head.querySelector('link[rel="icon"]');
  if (!href) {
    if (!l) { l = document.createElement("link"); l.rel = "icon"; document.head.append(l); }
    const u = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path d="M16 2.5 27.7 9.25v13.5L16 29.5 4.3 22.75V9.25z" fill="#4f5fd6" stroke="#4f5fd6" stroke-width="2.4" stroke-linejoin="round"/><path d="M11.5 10.5v11M20.5 10.5v11M11.5 16h9" fill="none" stroke="#fff" stroke-width="2.6" stroke-linecap="round"/></svg>');
    if (l.getAttribute("href") !== u) l.setAttribute("href", u);
    return;
  }
  if (!l) { l = document.createElement("link"); l.rel = "icon"; document.head.append(l); }
  if (l.getAttribute("href") !== href) l.setAttribute("href", href);
}
