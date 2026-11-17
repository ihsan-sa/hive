// A page (an html file) open full screen inside the app: #page/<its view address>. On a phone the file panel's Open lands here
// instead of a new tab, which a phone or an installed app shows with no way back (E56a). A slim bar on top has Back, the file's
// name and "New tab"; the page runs below it in the same sandbox as the panel's frame (preview/render.js), so it can't reach
// the dashboard. Only the dashboard's own page addresses are framed: anything else shows a note and Back.
import { h } from "../core/dom.js";
import { button } from "../ui/kit.js";
import { closePreview } from "../ui/preview.js";

const OK = /^\/(api\/kb\/raw\?|files\/)/;
let fresh = true; setTimeout(() => { fresh = false; });   // the first route() runs as the app loads: a page opened from a link outside the app has nothing behind it to go back to
let fromApp = false;
const nameOf = src => {
  try {
    const u = new URL(src, location.origin), p = u.pathname.startsWith("/api/kb/raw") ? u.searchParams.get("path") || "" : decodeURIComponent(u.pathname);
    return p.split("/").filter(Boolean).pop() || "Page";
  } catch (e) { return "Page"; }
};
const back = () => { if (fromApp) history.back(); else location.hash = ""; };   // nothing behind it: the app's first page

export default {
  id: "page",
  label: "Page",
  tab: false,
  title: arg => (arg ? nameOf(arg) : "Page"),
  enter() { fromApp = !fresh; closePreview(); },   // the file panel it was opened from would sit over it; Back shows the conversation
  render(ctx) {
    const src = ctx.arg || "", ok = OK.test(src), name = ok ? nameOf(src) : "Page";
    return h("div", { class: "pgv", "data-key": "pgv" },
      h("div", { class: "pgv-bar", "data-key": "bar" },
        button("Back", back, { kind: "quiet", size: "s", icon: "chevron-left", cls: "pgv-back" }),
        h("h1", { class: "pgv-n", title: name }, name),
        ok ? h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: src, target: "_blank", rel: "noopener noreferrer", title: "Open in a new tab" }, "New tab") : null),
      ok ? h("iframe", { class: "pgv-frame", "data-key": "fr:" + src, src, title: name, sandbox: "allow-scripts allow-modals allow-pointer-lock", referrerpolicy: "no-referrer" })
        : h("p", { class: "pgv-none meta", "data-key": "none" }, "This page can't be opened here."));
  },
};
