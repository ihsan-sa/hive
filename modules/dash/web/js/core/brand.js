// The hive's name and logo, set in Settings. The server's /api/app carries them; without one the app keeps its own name.
import { h } from "./dom.js";
import { S } from "./store.js";

export const DEFAULT_NAME = "Hive";
export const brandName = () => (S.app && S.app.brand && S.app.brand.name) || null;
export const brandLogo = () => (S.app && S.app.brand && S.app.brand.logo) || null;
/** What goes in a top bar: the logo, if there is one, and the name. */
export const brandMark = () => [brandLogo() ? h("img", { class: "brand-logo", src: brandLogo(), alt: "", "data-key": "logo" }) : null, h("span", { class: "brand-name", "data-key": "name" }, brandName() || DEFAULT_NAME)];
/** The browser tab's icon follows the logo. */
export function brandIcon() {
  const href = brandLogo();
  let l = document.head.querySelector('link[rel="icon"]');
  if (!href) { if (l) l.remove(); return; }
  if (!l) { l = document.createElement("link"); l.rel = "icon"; document.head.append(l); }
  if (l.getAttribute("href") !== href) l.setAttribute("href", href);
}
