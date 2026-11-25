// A page (an html file) open full screen inside the app: #page/<its view address>. On a phone the file panel's Open lands here
// instead of a new tab, which a phone or an installed app shows with no way back (E56a). A slim bar on top has Back, the file's
// name and "New tab"; the page runs below it in the same sandbox as the panel's frame (preview/render.js), so it can't reach
// the dashboard. Only the dashboard's own page addresses are framed: anything else shows a note and Back.
// The panel's "Open in new tab" lands here too, with the conversation after the address (#page/<address>#thread=<id>): the page
// gets the panel's comment mode and notes bar (preview/markup.js), the notes kept and sent to that conversation as from the panel
// (the person, 112514142c42: "make comments, boxes, etc work also when the html page side panel is opened in other tab").
import { h } from "../core/dom.js";
import { button } from "../ui/kit.js";
import { closePreview, keyOf } from "../ui/preview.js";

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
// The address and the conversation: "/files/x/a.html#thread=1124..." → { src, thread }. A file's own # is %23 in its address.
const split = arg => { const i = arg.indexOf("#"); return i < 0 ? { src: arg, thread: null } : { src: arg.slice(0, i), thread: new URLSearchParams(arg.slice(i + 1)).get("thread") }; };
// The file as the panel names it, so the notes are the same ones (keyOf): a library file by its path, a post's file by id and name.
const itemOf = src => {
  const u = new URL(src, location.origin);
  if (u.pathname.startsWith("/api/kb/raw")) { const kb = u.searchParams.get("path") || ""; return { kb, name: kb.split("/").pop() || "Page" }; }
  const ps = u.pathname.split("/").slice(2).map(decodeURIComponent);
  return { id: ps.slice(0, -1).join("/"), name: ps[ps.length - 1] || "Page" };
};
let mk = null;   // the markup on the page shown, with the holder it is in
const unmark = () => { if (mk) { mk.stop(); mk = null; } };
function marks(src, thread) {
  const host = document.querySelector(".pgv-b");
  if (mk && mk.host === host) { if (mk.refresh) mk.refresh(); return; }   // the board came in: "Send to" names the conversation's agent
  if (!host || !thread) return;
  unmark();
  const it = itemOf(src), mine = mk = { host, stop() {} };
  import("../preview/markup.js").then(m => {
    if (mk !== mine || !host.isConnected) return;
    const where = it.kb ? it.kb : it.id ? "attached to post " + it.id : it.name;
    mk = Object.assign(m.markup(it, thread, { root: host, body: host, key: keyOf(it), where }), { host });
    host.append(mk.bar);
  });
}

export default {
  id: "page",
  label: "Page",
  tab: false,
  title: arg => (arg ? nameOf(arg) : "Page"),
  enter() { fromApp = !fresh; closePreview(); },   // the file panel it was opened from would sit over it; Back shows the conversation
  leave() { unmark(); },
  render(ctx) {
    const { src, thread } = split(ctx.arg || ""), ok = OK.test(src), name = ok ? nameOf(src) : "Page", sig = src + "#" + (thread || "");
    if (ok) setTimeout(() => marks(src, thread));
    return h("div", { class: "pgv", "data-key": "pgv" },
      h("div", { class: "pgv-bar", "data-key": "bar" },
        button("Back", back, { kind: "quiet", size: "s", icon: "chevron-left", cls: "pgv-back" }),
        h("h1", { class: "pgv-n", title: name }, name),
        ok ? h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: src, target: "_blank", rel: "noopener noreferrer", title: "Open in a new tab" }, "New tab") : null),
      ok ? h("div", { class: "pgv-b", "data-key": "b:" + sig, "data-own": "", "data-sig": sig },   // its own children: the notes bar and card markup.js adds stay
        h("iframe", { class: "pgv-frame", src, title: name, sandbox: "allow-scripts allow-modals allow-pointer-lock", referrerpolicy: "no-referrer" }))
        : h("p", { class: "pgv-none meta", "data-key": "none" }, "This page can't be opened here."));
  },
};
