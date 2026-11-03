// Placeholder for views not yet carried over to the new system (next pass).
import { h } from "../core/dom.js";
import { empty } from "../ui/kit.js";

export const pending = (id, label, opts = {}) => ({
  id, label, tab: true, ...opts,
  render: ({ arg }) => h("div", { class: "page" },
    empty("This view moves onto the new templates in the next pass. Now, Usage and the system page show the direction.", { kind: "page", title: label + (arg ? ` · ${arg}` : "") })),
});
