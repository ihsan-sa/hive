// Design: Claude Design from the dashboard. A real Claude Code session on a login, which the person starts and types into; the server keeps it (class DesignRoom).
import { h, selecting } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { ago } from "../core/format.js";
import { confirm } from "../ui/dialog.js";
import { table } from "../ui/table.js";
import { pageHeader, section, button, input, empty, waiting, meter, note } from "../ui/kit.js";

const D = { v: null, text: "", code: "", err: "", busy: false, full: false };
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
function setText(v, focus, ctx) { D.text = v; if (ctx) ctx.update(); const n = document.querySelector(".dtype input"); if (n) { n.value = v; if (focus) n.focus(); } }

// One screen of the session, not its whole history: old banners and old answers read as if they were happening now.
const SCREEN_LINES = 40;
const tailOf = t => { const lines = (t || "").split("\n"); return lines.length > SCREEN_LINES ? lines.slice(-SCREEN_LINES).join("\n") : t || ""; };
const working = t => /esc to interrupt/i.test((t || "").split("\n").slice(-8).join("\n"));
const state = (tone, text, title) => h("span", { class: "sw", "data-tone": tone, title: title || null }, h("span", { class: "st", "data-tone": tone, "aria-hidden": "true" }), h("span", null, text));
const GUIDE = "https://support.claude.com/en/articles/14604406-claude-design-admin-guide-for-team-and-enterprise-plans";

const designCell = a => a.used_here ? state("good", "Yes, working here")
  : a.design === "yes" ? state("info", "Yes", "This plan includes it. Not tried on this account yet.")
  : a.design === "admin" ? state("warn", "Yes, if your admin allows it", "An admin of this organization can turn it off. Not tried on this account yet.")
  : state("warn", "Can't tell", "The plan isn't known. This account may be signed out.");

const designDate = d => new Date(d + "T12:00:00").toLocaleDateString(undefined, { month: "long", day: "numeric", year: "numeric" });

function accountsPart(v, ctx) {
  const session = v.running ? v.session_login : null, cc = v.can_connect !== false;
  const here = a => session === a.name, other = a => !!session && !here(a);
  const why = a => other(a) ? `The session is on ${session}. End it first.` : a.open ? null : `This account is ${a.closed_why}.`;
  const columns = [
    { label: "Account", lead: true, render: a => [h("strong", null, a.name), here(a) ? h("span", { class: "meta dhere" }, "in session") : null] },
    { label: "Plan", render: a => a.plan || h("span", { class: "muted" }, "Not known") },
    { label: "Claude Design", render: designCell },
    cc ? { label: "Agents' access", render: a => a.connected ? state("good", "Approved") : h("span", { class: "muted" }, "Not approved") } : null,
    { label: "This week", cls: "dweek", render: a => a.weekly == null ? h("span", { class: "muted" }, "No reading") : h("span", { class: "dmeter" }, meter({ value: a.weekly, stop: a.stop, label: `${a.name}, this week` }), h("span", { class: "meta" }, Math.round(a.weekly) + "%")) },
    { label: h("span", { class: "vh" }, "Actions"), cls: "dacts", render: a => h("span", { class: "frow" },
        !a.open ? h("span", { class: "meta" }, a.closed_why[0].toUpperCase() + a.closed_why.slice(1)) : session ? null : button("Start session", () => act({ do: "start", login: a.name }, ctx), { kind: "primary", size: "s", disabled: D.busy }),
        !cc || (!a.open && !here(a)) ? null : a.connected
          ? button("Take back", () => act({ do: "revoke", login: a.name }, ctx), { size: "s", kind: "quiet", disabled: D.busy || other(a) || !!v.step, title: why(a) || "Stops agents opening your Claude Design projects on this account" })
          : button("Sign in & approve", () => act({ do: "grant", login: a.name }, ctx), { size: "s", disabled: D.busy || other(a) || !!v.step, title: why(a) || "Signs in to Claude Design if it needs to, then approves agents' access" })) },
  ].filter(Boolean);
  return section({ title: "Your Claude accounts", key: "accounts", note: session ? `one session at a time: this one is on ${session}` : "start a session on any of them; it ends by itself after 6 hours" },
    table({ caption: "Your Claude accounts", cls: "dacct", columns, rows: sorted(v.accounts, session), key: a => a.name, rowAttrs: a => ({ "data-closed": a.open || here(a) ? null : "" }) }),
    h("p", { class: "field-h dfoot" }, "You can make new designs without approving anything. ",
      cc ? `Approving lets agents open and change the projects you already have at claude.ai/design. It asks you to sign in only if it needs to, and goes away when standalone Claude Design closes on ${designDate(v.connect_until)}. ` : null,
      h("a", { href: GUIDE, target: "_blank", rel: "noreferrer noopener" }, "Which plans include it")));
}

function stepPart(v, ctx) {
  const st = v.step; if (!st) return null;
  const on = st.login || "this account";
  if (st.phase === "starting") return note({ key: "step", title: `Starting a session on ${on}…` });
  if (st.phase === "asking") return note({ key: "step", title: `Asking Claude to approve on ${on}…` });
  if (st.phase === "revoking") return note({ key: "step", title: `Taking back agents' access on ${on}…` });
  if (st.phase === "done") return note({ key: "step", tone: "good", title: `Approved on ${on}. Agents can now open your Claude Design projects there.` });
  if (st.phase === "revoked") return note({ key: "step", tone: "good", title: `Agents' access is taken back on ${on}.` });
  if (st.phase === "failed") return note({ key: "step", tone: "bad", title: `That didn't finish on ${on}.` }, st.why);
  const sendCode = () => { const text = D.code.trim(); if (!text) return; D.code = ""; const n = document.querySelector(".dcode input"); if (n) n.value = ""; act({ do: "send", text }, ctx); };
  return note({ key: "step", tone: "warn", title: `Sign in to Claude Design on ${on}` },
    h("p", null, /^https:\/\//.test(st.link || "") ? "Open the sign-in page and approve it. If it gives you a code, paste it here. This page carries on by itself once it has worked." : "The sign-in link appears on the session's screen below in a moment. Approve it there. If it gives you a code, paste it here."),
    h("form", { class: "frow dcode", onsubmit: e => { e.preventDefault(); sendCode(); } },
      /^https:\/\//.test(st.link || "") ? h("a", { class: "btn", "data-kind": "primary", href: st.link, target: "_blank", rel: "noreferrer noopener" }, "Open the sign-in page") : null,
      h("div", { style: "flex:1;min-width:12rem" }, input({ "aria-label": "Sign-in code", placeholder: "Paste the code", autocomplete: "off", spellcheck: "false", value: D.code, oninput: (e, n) => { D.code = n.value; } })),
      button("Send code", null, { type: "submit" })));
}

function sessionPart(v, ctx) {
  const send = () => { const text = D.text; if (!text.trim()) return; setText(""); stuck = true; act({ do: "send", text }, ctx); };
  const key = (label, k) => button(label, () => act({ do: "key", key: k }, ctx), { size: "s", kind: "quiet" });
  return section({ title: v.session_login ? `Session on ${v.session_login}` : "Session", key: "session", actions: [working(v.screen) ? state("info", "Claude is working") : state("good", "Waiting for you"), button("End session", () => confirm({ title: "End this session?", body: ["Claude stops working here. You can start a new session afterwards."], ok: "End session", kind: "danger", onOk: () => act({ do: "stop" }, ctx) }), { size: "s" })] },
    h("pre", { class: "dscreen", tabindex: "0", "aria-label": "The session's screen" }, D.full ? v.screen : tailOf(v.screen)),
    h("form", { class: "frow dtype", onsubmit: e => { e.preventDefault(); send(); } },
      h("div", { style: "flex:1;min-width:14rem" }, input({ "aria-label": "Type to Claude", placeholder: "/design a landing page for…", autocomplete: "off", spellcheck: "false", value: D.text, oninput: (e, n) => { const was = !D.text.trim(); D.text = n.value; if (was !== !D.text.trim()) ctx.update(); } })),
      button("Send", null, { kind: "primary", type: "submit", disabled: !D.text.trim(), title: D.text.trim() ? null : "Type something first" })),
    h("div", { class: "frow dkeys" }, h("span", { class: "meta" }, "If it asks something:"), key("Up", "up"), key("Down", "down"), key("Enter", "enter"), key("Esc", "esc"), key("Tab", "tab"), key("Interrupt", "stop"),
      h("span", { class: "frow dright" }, button(D.full ? "Show less" : "Show whole session", () => { D.full = !D.full; stuck = true; redraw(ctx); }, { size: "s", kind: "quiet" }), button("Save it for the hive", () => setText(SAVE, true, ctx), { size: "s", title: `Fills in the message; Send sends it. Saved designs go under ${v.dir}, where agents can read them.` }))));
}

// Designs as cards, newest first: a name worth reading (its brief, else the thread it was made for), where it came from, and Open.
const designName = l => l.brief || (l.by ? `Design for ${l.by}` : "Untitled design");
const linksPart = (v, ctx) => v.links && v.links.length ? section({ fold: innerWidth < 640, title: "Your designs", note: "only you can open them until you share them; export to PNG or PDF from a design's page", key: "links" },
  h("ul", { class: "list dcards" }, [...v.links].sort((a, b) => (b.at || 0) - (a.at || 0)).map(l => { const url = /^https:\/\//.test(l.url) ? l.url : "#";
    return h("li", { class: "dcard", "data-key": l.url },
      h("a", { class: "dcard-a", href: url, target: "_blank", rel: "noreferrer noopener" },
        h("span", { class: "dcard-t" }, designName(l)),
        h("span", { class: "meta" }, [l.by && l.brief ? l.by : null, l.login, (() => { try { return new URL(l.url).host; } catch { return null; } })(), l.at ? ago(l.at * 1e3) : null].filter(Boolean).join(" · "))),
      h("div", { class: "dcard-f" },
        h("a", { class: "btn", "data-size": "s", href: url, target: "_blank", rel: "noreferrer noopener" }, "Open ↗"),
        button("Remove", () => act({ do: "forget", url: l.url }, ctx), { size: "s", kind: "quiet", title: "Takes it off this list. The design stays on claude.ai." }))); }))) : null;
// The account in session first, then the ones you can start on (least used first), then the rest.
const sorted = (accts, session) => [...(accts || [])].sort((a, b) => (b.name === session) - (a.name === session) || (b.open - a.open) || ((a.weekly ?? 101) - (b.weekly ?? 101)));

export default {
  id: "design", label: "Design", about: "make designs with Claude Design", tab: () => !!S.app.app && can("logins"),
  poll: { every: 1500, async run(ctx) { try { const v = await jget("/api/design"); D.readErr = ""; if (selecting()) return; D.v = v; redraw(ctx); } catch (e) { D.readErr = e.message; ctx.update(); } } },
  render(ctx) {
    if (!can("logins")) return h("div", { class: "page", "data-width": "narrow" }, empty("This page isn't open from here.", { kind: "page", title: "Design" }));
    const v = D.v;
    const head = pageHeader({ title: "Design", lead: "Tell Claude what to design and get a link to it." });
    if (!v) return h("div", { class: "page dpage", "data-width": "narrow", "data-key": "design" }, head, waiting(D.readErr, "Reading…", "the design page", "design"));
    return h("div", { class: "page dpage", "data-width": "narrow", "data-key": "design" },
      head,
      D.err || D.readErr ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, D.err || `Lost contact: ${D.readErr}`) : null,
      stepPart(v, ctx),
      v.running ? sessionPart(v, ctx) : null,
      accountsPart(v, ctx),
      linksPart(v, ctx));
  },
};
