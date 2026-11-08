// One banner component for the whole app (R7 step 5): a line under the bar that says what is wrong in words, with the
// one button that helps. Tones: bad (lost contact, the hive stopped), info (a newer version), warn. Nothing else on the
// page moves when one shows: it takes its own row in the shell, above the page.
import { h } from "../core/dom.js";

/** banner({tone, text, actions, cls, key}) → .banner[data-tone]. `text` is a string or nodes; the first <b> reads as the lead. */
export function banner({ tone = "info", text, actions = [], cls, key = "band" } = {}) {
  return h("div", { class: "banner" + (cls ? " " + cls : ""), role: "status", "data-tone": tone, "data-key": key },
    h("span", { class: "banner-t" }, text), ...[].concat(actions).filter(Boolean));
}
