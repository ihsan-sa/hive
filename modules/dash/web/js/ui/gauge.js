// Gauges: one limit (value, meter with stop line, reset), and gauge rows: one account's limits side by side,
// with its history and actions. Logins and other providers use the same row.
import { h } from "../core/dom.js";
import { meter, statusLabel } from "./kit.js";

/** gauge({label, value, unit, pct, stop, tone, lines:[...]}) */
export function gauge({ label, value, unit, pct, stop, tone, lines = [] }) {
  return h("div", { class: "gauge" },
    h("div", { class: "gauge-l" }, label),
    h("div", { class: "gauge-v" }, value, unit ? h("small", null, unit) : null),
    pct != null ? meter({ value: pct, stop, tone, label }) : null,
    h("div", { class: "gauge-n" }, lines.filter(Boolean).map(l => h("div", null, l))));
}

/** gaugeRows({head:[...4 labels], histKey, rows:[{key, name, tag, state:{key, text}, note, gauges:[...], history, actions, message, dim}]}) */
export function gaugeRows({ head, histKey, rows }) {
  return h("div", { class: "grs" },
    head ? h("div", { class: "grs-h", "aria-hidden": "true" }, head.map(x => h("span", null, x))) : null,
    rows.map(r => h("div", { class: "gr", "data-key": r.key, "data-dim": r.dim ? "" : null },
      h("div", { class: "gr-id" },
        h("div", { class: "gr-name" }, r.name),
        r.tag ? h("div", { class: "gr-tag" }, r.tag) : null,
        r.state ? statusLabel(r.state.key, r.state.text) : null,
        r.note ? h("div", { class: "gr-note" }, r.note) : null),
      r.message ? h("div", { class: "gr-msg" }, r.message) : h("div", { class: "gr-g" }, r.gauges.map(gauge)),
      r.message ? null : h("div", { class: "gr-hist" }, histKey ? h("div", { class: "gr-hist-key" }, histKey.cloneNode(true)) : null, r.history || null),
      h("div", { class: "gr-a" }, r.actions || null))));
}
