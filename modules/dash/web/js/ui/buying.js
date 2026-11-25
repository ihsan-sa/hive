// Settings › Buying (modules/buy, GET/POST /api/buy): sign this Hive in to Stripe Link, save the delivery details, see the buys.
// Warden 112409231096: only the person's own signed-in app (through Cloudflare in full mode, or a pinned Tailscale device) can start
// the sign-in or save the details; the server refuses the rest and never sends them the phrase or link. Nothing here approves a buy:
// each one is approved in the Link app on the person's phone, and only there.
import { h } from "../core/dom.js";
import { can, jget, post } from "../core/store.js";
import { section, block, blocks, button, field, input, statusLabel, empty } from "./kit.js";
import { act, isBusy } from "./activity.js";
import { confirm } from "./dialog.js";
import { copyButton } from "./refs.js";
import { ago } from "../core/format.js";
import { shopsBlock, forgetShops, sitesBlock } from "./shop_signin.js";

const B = { v: null, err: "", say: null, ticking: false, t: 0, c: {}, line2: false, loading: false, gen: 0 };
const FIELDS = [["name", "Full name", "name"], ["email", "Email", "email"], ["phone", "Phone", "tel"], ["line1", "Address", "address-line1"],
  ["line2", "Apartment, suite (optional)", "address-line2"], ["city", "City", "address-level2"], ["state", "State or province (2 letters)", "address-level1"],
  ["postal_code", "ZIP or postal code", "postal-code"]];
const say = (text, tone) => { B.say = text ? { text, tone } : null; };
const msg = () => B.say ? h("p", { class: "fmsg", "data-tone": B.say.tone || null, role: "status", "data-key": "buy-say" }, B.say.text) : null;
const linkOk = u => { try { const x = new URL(u); return x.protocol === "https:" && (x.hostname === "link.com" || x.hostname.endsWith(".link.com")); } catch { return false; } };
const pending = v => v && v.signin && (v.signin.state === "starting" || v.signin.state === "waiting");

/** Reads /api/buy. Settings' enter and back call it; while a sign-in waits, tick() keeps reading it. */
// Whether Buying was there last time, kept across leaving Settings and across reloads (a yes/no only, never the sign-in phrase or link):
// while /api/buy is on its way the tab stays put with a "loading" body, instead of appearing late or blinking out on every section switch.
const KNOWN = "h2.buy.installed";
export const buyKnown = () => { try { return localStorage.getItem(KNOWN) === "1"; } catch (e) { return false; } };
const remember = v => { try { if (v && v.installed) localStorage.setItem(KNOWN, "1"); else localStorage.removeItem(KNOWN); } catch (e) { /* none */ } };
export const buyingWaiting = () => section({ title: "Buying", note: "Stripe Link, for agents' purchases", level: 1, key: "buying" }, empty("Reading…", { kind: "loading" }));

export async function loadBuying(update) {
  if (!can("logins") || B.loading) return;
  B.loading = true;
  try { B.v = await jget("/api/buy"); B.err = ""; B.t = Date.now(); remember(B.v); } catch (e) { B.err = e.message; }
  B.loading = false;
  if (pending(B.v)) tick(update);
  update();
}
/** Leaving Settings: drop what was typed, and the phrase with it, and stop asking. Coming back reads it afresh. */
export function forgetBuying() { forgetShops(); B.c = {}; B.line2 = false; B.say = null; if (B.v && B.v.signin) { delete B.v.signin.phrase; delete B.v.signin.url; } B.v = null; B.t = 0; B.err = ""; B.gen++; B.ticking = false; }

function tick(update) {
  if (B.ticking) return;
  B.ticking = true;
  const gen = B.gen;
  const go = async () => {
    if (gen !== B.gen) return;   // left Settings: forgetBuying stopped this
    let v = null;
    try { v = await jget("/api/buy"); B.err = ""; } catch (e) { B.err = e.message; }
    if (gen !== B.gen) return;
    if (!v) { if (B.v && B.v.signin) B.v.signin = { state: "failed", error: "This page can't reach the sign-in any more. Open Settings › Buying again." }; B.ticking = false; say("This page can't reach the sign-in any more. Open Settings › Buying again.", "bad"); update(); return; }
    B.v = v; update();
    if (pending(B.v)) return setTimeout(go, 1500);
    B.ticking = false;
    const s = B.v && B.v.signin && B.v.signin.state;
    if (s === "done") say("Signed in to Link. Agents can now ask to buy; each buy still waits for your approval in the Link app.", "good");
    else if (s === "failed") say(B.v.signin.error || "The sign-in didn't finish.", "bad");
    update();
  };
  setTimeout(go, 1500);
}

async function run(update, id, text, body, done) {
  say(null);
  try { B.v = await act(id, text, () => post("/api/buy", body), update, done); if (pending(B.v)) tick(update); return true; }
  catch (e) { say(e.message, "bad"); return false; }
  finally { update(); }
}

function signinBlock(v, update) {
  const st = v.signin || { state: "idle" }, on = !!v.signed_in, busy = pending(v), here = !!v.here;
  const actions = !here || !v.link_cli ? null : busy ? button("Cancel", () => run(update, "buy-cancel", "Cancelling the Link sign-in", { do: "cancel" }, "Cancelled"), { size: "s", kind: "quiet", busy: isBusy("buy-cancel") })
    : on ? button("Sign out", () => confirm({ title: "Sign this Hive out of Link?", body: ["Agents can't ask to buy until you sign in again. Buys already approved aren't changed."], ok: "Sign out", kind: "danger",
        onOk: () => run(update, "buy-signout", "Signing out of Link", { do: "signout" }, "Signed out of Link") }), { size: "s", kind: "quiet", busy: isBusy("buy-signout") })
    : button("Sign in to Link", () => run(update, "buy-signin", "Starting the Link sign-in", { do: "signin" }), { size: "s", kind: "primary", busy: isBusy("buy-signin") });
  const ready = st.state === "waiting" && st.phrase && linkOk(st.url);
  return block({ title: "Stripe Link", sub: on ? "Agents can ask to buy. You approve each one in the Link app." : "Sign in once so agents can ask to buy with your Link wallet.",
    state: !v.link_cli ? statusLabel("ended", "link-cli not installed") : statusLabel(on ? "working" : busy ? "asks" : "off", on ? "signed in" : busy ? "waiting for you" : "not signed in"), key: "buy-link", actions },
    !here ? h("p", { class: "field-h", "data-key": "buy-where" }, "Signing in and the delivery details work only from your own app opened through Cloudflare (full mode) or a Tailscale device, not from this connection. Agents on this machine can reach it, so it can't start a sign-in.") : null,
    st.state === "starting" ? h("p", { class: "fmsg", role: "status" }, "Asking Link for a sign-in link…") : null,
    ready ? h("ol", { class: "steps", "data-key": "buy-steps" },
      h("li", null, h("a", { href: st.url, target: "_blank", rel: "noreferrer noopener" }, "Open Link's sign-in page"), " and sign in with your Link account."),
      h("li", null, "Check the page shows this phrase: ", h("span", { class: "frow fsplit" }, h("b", { class: "mono", "data-key": "phrase" }, st.phrase), copyButton(st.phrase))),
      h("li", null, "Approve the connection named Hive in the Link app on your phone. ", h("span", { class: "field-h" }, "This lasts 15 minutes; the page notices by itself."))) : null,
    st.state === "waiting" && !ready && here ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, "Link's sign-in link wasn't on link.com, so it isn't shown. Cancel and try again.") : null);
}

function contactBlock(v, update) {
  const c = v.contact || {}, here = !!v.here;
  const set = (k, val) => { B.c[k] = val; };
  const save = async e => {
    e.preventDefault();
    const body = {}; for (const [k] of FIELDS) if ((B.c[k] || "").trim()) body[k] = B.c[k].trim();
    if (B.c.country) body.country = B.c.country;
    if (B.line2) body.clear_line2 = true;
    if (!Object.keys(body).length) { say("Type at least one field to change.", "bad"); update(); return; }
    if (await run(update, "buy-contact", "Saving the delivery details", { do: "contact", contact: body }, "Saved the delivery details")) { B.c = {}; B.line2 = false; say("Saved. Only the summary above is shown back.", "good"); update(); }
  };
  const clear = () => confirm({ title: "Clear the delivery details?", body: ["Agents can't ask to buy anything shipped until you save them again."], ok: "Clear", kind: "danger",
    onOk: () => run(update, "buy-contact-clear", "Clearing the delivery details", { do: "contact_clear" }, "Cleared the delivery details") });
  return block({ title: "Delivery details", sub: c.saved ? c.summary : "Where things are shipped and the contact a shop asks for. US or Canada.",
    state: statusLabel(c.saved && !(c.missing || []).length ? "working" : "off", c.saved ? ((c.missing || []).length ? "incomplete" : "saved") : "not saved"), key: "buy-contact", fold: true, open: B.open ?? !c.saved, onToggle: o => { B.open = o; },
    actions: here && c.saved ? button("Clear", clear, { size: "s", kind: "quiet", busy: isBusy("buy-contact-clear") }) : null },
    !here ? h("p", { class: "field-h" }, "Open your app through Cloudflare or Tailscale to change these.") : h("form", { class: "stack", autocomplete: "on", onsubmit: save, "data-key": "buy-contact-form" },
      h("p", { class: "field-h" }, c.saved ? "Leave a field blank to keep what's saved. What you saved is never shown back, only the summary." : "Saved only on this machine, in a file only this account can read. Agents never fill these in."),
      (c.missing || []).length && c.saved ? h("p", { class: "fmsg", "data-tone": "warn", role: "status" }, "Still needed: " + c.missing.map(m => (FIELDS.find(f => f[0] === m) || [, m])[1].replace(/ \(.*\)$/, "")).join(", ")) : null,
      h("div", { class: "fgrid" }, FIELDS.map(([k, label, ac]) => field(label, input({ name: k, autocomplete: ac, value: B.c[k] || "", type: k === "email" ? "email" : k === "phone" ? "tel" : "text",
        maxlength: k === "state" ? 2 : 200, oninput: (e, n) => set(k, n.value) }))),
        field("Country", h("select", { class: "input", name: "country", autocomplete: "country", value: B.c.country || "", onchange: e => set("country", e.target.value) },
          h("option", { value: "" }, c.saved ? "keep saved" : "choose"), h("option", { value: "US" }, "United States"), h("option", { value: "CA" }, "Canada")))),
      c.saved ? h("label", { class: "check" }, h("input", { type: "checkbox", checked: B.line2, onchange: (e, n) => { B.line2 = n.checked; } }), h("span", null, "Remove the apartment or suite line")) : null,
      h("div", { class: "frow" }, button("Save delivery details", null, { type: "submit", kind: "primary", busy: isBusy("buy-contact") }))));
}

const WORD = { created: "waiting for your approval", pending_approval: "waiting for your approval", requires_action: "needs a step in Link", approved: "approved",
  succeeded: "bought", done: "bought", denied: "declined", canceled: "cancelled", expired: "expired", failed: "failed" };
const TONE = { created: "asks", pending_approval: "asks", requires_action: "asks", approved: "working", succeeded: "ended", done: "ended", denied: "ended", canceled: "ended", expired: "ended", failed: "failed" };
const OPEN = new Set(["created", "pending_approval", "requires_action"]);
function buysBlock(v) {
  const bs = v.buys || [];
  return block({ title: "Buys", sub: `Agents may ask for up to ${v.cap || "the cap"} a buy. Each waits for you in the Link app; nothing here approves one.`, key: "buy-list", fold: true, open: B.listOpen ?? bs.some(b => OPEN.has(b.status)), onToggle: o => { B.listOpen = o; } },
    bs.length ? h("ul", { class: "list" }, bs.map(b => h("li", { class: "lr", "data-key": "buy-" + b.id },
      h("div", { class: "lr-in frow fsplit" }, h("b", null, b.item || "a buy"), h("span", { class: "muted" }, [b.merchant, b.total, b.test ? "test" : null].filter(Boolean).join(" · ")),
        statusLabel(TONE[b.status] || "off", WORD[b.status] || b.status || "unknown"),
        h("span", { class: "frow-end muted" }, [b.agent, b.updated ? ago(Date.parse(b.updated)) : null].filter(Boolean).join(" · "))),
      OPEN.has(b.status) && b.approval_url && linkOk(b.approval_url) ? h("p", { class: "field-h" }, "Approve or decline it in the Link app, or ", h("a", { href: b.approval_url, target: "_blank", rel: "noreferrer noopener" }, "open it on link.com"), ".") : null)))
      : empty("No buys yet. An agent asks with hive buy; you get a request in the Link app."));
}

/** The section. Shown with the logins right, once the buy module is installed (hive module add buy). */
export function buyingSection(ctx) {
  if (!can("logins")) return null;
  const v = B.v;
  if (!v) { if (!B.err && !B.loading && !B.t) loadBuying(ctx.update); return B.err ? section({ title: "Buying", note: "Stripe Link, for agents' purchases", level: 1 }, h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, B.err)) : buyKnown() ? buyingWaiting() : null; }
  if (!v.installed) return null;
  return section({ title: "Buying", note: "Stripe Link, for agents' purchases; you approve each buy on your phone", level: 1, key: "buying" },
    v.error ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, "The buy module didn't answer: " + v.error) : null,
    blocks(signinBlock(v, ctx.update), shopsBlock(!!v.here, ctx.update), contactBlock(v, ctx.update), buysBlock(v)), msg(), B.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, B.err) : null);
}

/** Settings › Connectors › Sign in to a site: shown once /api/buy has said whether this app is the person's own. */
export const sitesPart = update => B.v ? sitesBlock(!!B.v.here, update) : null;
