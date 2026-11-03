// Design: Claude Design from the dashboard. A real Claude Code session on a login, which the person starts and types into; the server keeps it (class DesignRoom).
import { h, selecting } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { ago } from "../core/format.js";
import { pageHeader, section, button, input, empty } from "../ui/kit.js";

const D = { v: null, text: "", err: "", busy: false };
const SAVE = "Save the full source of the design you just made into a new folder here, named after it, and tell me the path.";

async function act(body, ctx) {
  D.err = ""; D.busy = true; ctx.update();
  try { await post("/api/design", body); D.v = await jget("/api/design"); } catch (e) { D.err = e.message; }
  D.busy = false; redraw(ctx);
}
// The screen stays at its end unless the person has scrolled back to read. Scroll events aren't delegated, so look just before each redraw.
let stuck = true;
function look() { const n = document.querySelector(".dscreen"); if (n) stuck = n.scrollHeight - n.scrollTop - n.clientHeight < 40; }
function follow() { const n = document.querySelector(".dscreen"); if (n && stuck) n.scrollTop = n.scrollHeight; }
function redraw(ctx) { look(); ctx.update(); queueMicrotask(follow); }   // update draws in a microtask: follow goes after it
// morph leaves a focused field alone, so set it here as well
function setText(v, focus) { D.text = v; const n = document.querySelector(".dtype input"); if (n) { n.value = v; if (focus) n.focus(); } }

const linksPart = v => v.links && v.links.length ? section({ title: "Your designs", note: "only you can open them until you share them", key: "links" },
  h("ul", { class: "list" }, v.links.map(l => h("li", { class: "lr", "data-key": l.url },
    h("div", { class: "lr-in frow fsplit" }, h("a", { href: l.url, target: "_blank", rel: "noreferrer noopener" }, l.url), h("span", { class: "muted", style: "margin-left:auto" }, (l.by ? l.by + ": " + (l.brief || "") + " · " : "") + ago(l.at * 1e3))))))) : null;

const GRANT = "Lets agents read and change your claude.ai/design projects on this account. Agents can already make new designs without this. You can take it back with /design revoke.";

export default {
  id: "design", label: "Design", about: "make designs with Claude Design", tab: () => !!S.app.app && can("logins"),
  poll: { every: 1500, async run(ctx) { try { const v = await jget("/api/design"); if (selecting()) return; D.v = v; redraw(ctx); } catch (e) { D.err = e.message; ctx.update(); } } },
  render(ctx) {
    if (!can("logins")) return h("div", { class: "page" }, empty("This page isn't open from here.", { kind: "page", title: "Design" }));
    const v = D.v;
    if (!v) return h("div", { class: "page" }, empty("Reading…", { kind: "loading" }));
    const say = D.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, D.err) : null;
    const header = pageHeader({ title: "Design", lead: "Tell Claude what to design and get a link to it. This is a real Claude Code session on your account: what you type goes straight to it, and it uses that account's limits." });
    if (!v.running) return h("div", { class: "page", "data-key": "design" }, header,
      section({ title: "Start a session", key: "start", note: "it ends by itself after 6 hours" },
        h("div", { class: "frow" }, v.logins.map(l => button(`Start on ${l.name}` + (l.weekly != null ? ` (${Math.round(l.weekly)}% of the week used)` : ""), () => act({ do: "start", login: l.name }, ctx), { kind: "primary", busy: D.busy, type: "button" })), v.logins.length ? null : h("p", { class: "field-h" }, "Every login is paused or past its weekly line right now.")), say),
      section({ title: "Give agents your Claude Design projects", key: "grant", note: GRANT },
        h("div", { class: "frow" }, v.logins.map(l => button(`Give access on ${l.name}`, () => act({ do: "grant", login: l.name }, ctx), { size: "s", type: "button", disabled: D.busy })))),
      say, linksPart(v));
    const send = () => { const text = D.text; if (!text.trim()) return; setText(""); stuck = true; act({ do: "send", text }, ctx); };
    const key = (label, k) => button(label, () => act({ do: "key", key: k }, ctx), { size: "s", kind: "quiet", type: "button" });
    return h("div", { class: "page", "data-key": "design" }, header,
      h("pre", { class: "dscreen", tabindex: "0", "aria-label": "The session's screen" }, v.screen),
      h("form", { class: "frow dtype", onsubmit: e => { e.preventDefault(); send(); } },
        h("div", { style: "flex:1;min-width:14rem" }, input({ "aria-label": "Type to Claude", placeholder: "/design a landing page for…", autocomplete: "off", spellcheck: "false", value: D.text, oninput: (e, n) => { D.text = n.value; } })),
        button("Send", null, { kind: "primary", type: "submit" })),
      h("div", { class: "frow" }, key("Up", "up"), key("Down", "down"), key("Enter", "enter"), key("Esc", "esc"), key("Tab", "tab"), key("Interrupt", "stop"), button("Give agents my Claude Design projects", () => { stuck = true; act({ do: "grant" }, ctx); }, { size: "s", type: "button", title: GRANT }),
        h("span", { style: "margin-left:auto" }, button("Save it for the hive", () => setText(SAVE, true), { size: "s", type: "button", title: "Fills in the message; Send sends it" }), " ", button("End session", () => act({ do: "stop" }, ctx), { size: "s", kind: "quiet", type: "button" }))),
      h("p", { class: "field-h" }, "Use the keys to answer questions on its screen. Saved designs go under ", h("code", null, v.dir), ", where agents can read them."),
      say, linksPart(v));
  },
};
