// Confirm dialog: the one window for every action that changes something. Built once per open, never morphed.
import { h } from "../core/dom.js";
import { button } from "./kit.js";

/**
 * confirm({title, body:[nodes|strings], ok, kind:"primary"|"danger", typed, onOk})
 * typed: a code that must be typed exactly before OK enables (Kill).
 * onOk: async; throw to show "Nothing changed: <reason>." and keep the window open. Return a string to show it and keep open.
 */
export function confirm({ title, body = [], ok = "OK", kind = "primary", typed, failText = "Nothing changed", onOk }) {
  const err = h("div", { class: "dlg-err", role: "status" });
  const code = typed ? h("input", { class: "input", autocomplete: "off", spellcheck: "false", placeholder: typed, "aria-label": `Type ${typed} to confirm` }) : null;
  const yes = button(ok, null, { kind, type: "submit" });
  const no = button("Cancel", () => dlg.close(), {});
  const form = h("form", { method: "dialog" },
    h("h2", null, title),
    body.filter(Boolean).map(b => (typeof b === "string" ? h("p", null, b) : b)),
    code ? h("label", { class: "field" }, h("span", { class: "field-l" }, "Type ", h("b", null, typed), " to confirm"), code) : null,
    err, h("div", { class: "dlg-a" }, no, yes));
  const dlg = h("dialog", { class: "dlg", "aria-label": title }, form);
  const ready = () => { yes.disabled = !!code && code.value !== typed; };
  if (code) code.addEventListener("input", ready);
  form.addEventListener("submit", async e => {
    e.preventDefault();
    if (yes.disabled || yes.dataset.busy) return;
    yes.dataset.busy = "1"; yes.disabled = true; err.textContent = ""; err.removeAttribute("data-ok");
    try {
      const msg = await onOk(code ? code.value : undefined);
      if (typeof msg === "string" && msg) { err.textContent = msg; err.setAttribute("data-ok", ""); yes.remove(); no.textContent = "Close"; if (code) code.disabled = true; }
      else dlg.close();
    } catch (x) { err.textContent = `${failText}: ${x.message}.`; }
    finally { delete yes.dataset.busy; ready(); }
  });
  dlg.addEventListener("close", () => dlg.remove());
  document.body.append(dlg); ready(); dlg.showModal(); (code || yes).focus();
  return dlg;
}
