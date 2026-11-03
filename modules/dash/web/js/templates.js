// Page templates. Every view returns one of these; layout CSS lives with them in components.css.
import { h } from "./core/dom.js";

/** Overview: header, a main column and a sticky rail (things that need you), then full-width sections.
 *  On narrow screens the rail goes above main; "later" rail sections go below it. */
export const overview = ({ header, main, aside = [], later = [], after }) =>
  h("div", { class: "page" }, header,
    h("div", { class: "ov" }, h("div", { class: "ov-main" }, main),
      h("aside", { class: "ov-aside", "aria-label": "Needs you" }, aside, later.filter(Boolean).map(x => h("div", { class: "ov-late" }, x)))),
    after && after.length ? h("div", { class: "after" }, after) : null);

/** Metrics: header, then sections stacked. */
export const metrics = ({ header, sections }) =>
  h("div", { class: "page" }, header, h("div", { class: "after", style: "margin-top:var(--s6)" }, sections));

/** Form: a narrow column of sections (Settings, Start). */
export const form = ({ header, sections }) =>
  h("div", { class: "page", "data-width": "narrow" }, header, h("div", { class: "after", style: "margin-top:var(--s5)" }, sections));

/** Columns: equal columns that stack on narrow screens. */
export const cols = (...kids) => h("div", { class: "cols" }, kids);

/** Canvas: toolbar, a full-bleed stage (mount it data-own so polls leave it alone) and an optional side pane. key keeps one view from morphing onto another. */
export const canvas = ({ key, bar, note, stage, grip, side, sideWidth }) =>
  h("div", { class: "cvp", "data-key": key },
    h("div", { class: "cvp-bar" }, bar),
    note ? h("div", { class: "cvp-note" }, note) : null,
    h("div", { class: "cvp-body", style: sideWidth ? `--side:${sideWidth}px` : null }, stage, grip || null, side ? h("aside", { class: "cvp-side", "aria-label": "Details" }, side) : null));
