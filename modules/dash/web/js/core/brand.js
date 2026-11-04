// The hive's name and logo, set in Settings. The server's /api/app carries them; without one the app keeps its own name.
import { h, s } from "./dom.js";
import { S } from "./store.js";

export const DEFAULT_NAME = "Hive";
export const brandName = () => (S.app && S.app.brand && S.app.brand.name) || null;
export const brandLogo = () => (S.app && S.app.brand && S.app.brand.logo) || null;
/** What goes in a top bar: the logo, if there is one, and the name. */
/** The built-in mark: a honeycomb cell with an H, in the accent colour, so it follows light and dark. */
const HIVE_OUTER = "M781 389.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM243 389.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM646 155.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM378 155.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM646 623.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM378 623.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8z";
const HIVE_CENTER = "M512 389.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8z";
/** Rounded corners: each cell is drawn smaller and stroked back out with round joins. */
const hexMark = () => s("svg", { class: "brand-logo brand-hex", viewBox: "110 130 800 765", "aria-hidden": "true", "data-key": "logo" },
  s("path", { d: HIVE_OUTER, fill: "var(--accent)", stroke: "var(--accent)", "stroke-width": "30", "stroke-linejoin": "round" }),
  s("path", { d: HIVE_CENTER, fill: "var(--accent-soft)", stroke: "var(--accent-soft)", "stroke-width": "30", "stroke-linejoin": "round" }));
export const brandMark = () => [brandLogo() ? h("img", { class: "brand-logo", src: brandLogo(), alt: "", "data-key": "logo" }) : hexMark(), h("span", { class: "brand-name", "data-key": "name" }, brandName() || DEFAULT_NAME)];
/** The browser tab's icon follows the logo. */
export function brandIcon() {
  const href = brandLogo();
  let l = document.head.querySelector('link[rel="icon"]');
  if (!href) {
    if (!l) { l = document.createElement("link"); l.rel = "icon"; document.head.append(l); }
    const u = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><rect x="0" y="0" width="1024" height="1024" rx="200" fill="#1a1b20"/><path d="M781 389.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM243 389.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM646 155.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM378 155.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM646 623.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8zM378 623.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8z" fill="#f5b025" stroke="#f5b025" stroke-width="30" stroke-linejoin="round"/><path d="M512 389.2l106.6 61.4v122.8l-106.6 61.4-106.6-61.4v-122.8z" fill="#ffe296" stroke="#ffe296" stroke-width="30" stroke-linejoin="round"/></svg>');
    if (l.getAttribute("href") !== u) l.setAttribute("href", u);
    return;
  }
  if (!l) { l = document.createElement("link"); l.rel = "icon"; document.head.append(l); }
  if (l.getAttribute("href") !== href) l.setAttribute("href", href);
}
