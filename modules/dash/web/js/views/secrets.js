// Secrets: a write-only box. A value goes in and no route gives it back; the page holds it only until it's sent.
import { h } from "../core/dom.js";
import { S, can, jget, post, fullEnded, kept, keep, KEPT_NOTE } from "../core/store.js";
import { ago } from "../core/format.js";
import { button, empty, field, fieldErr, input, invalid, list, pageHeader, section } from "../ui/kit.js";
import { secretsReach } from "../ui/setup.js";
import { confirm } from "../ui/dialog.js";

const F = { items: null, name: "", value: "", say: null, tone: null, asked: false, bad: {}, lost: false };
const say = (text, tone) => { F.say = text; F.tone = tone || null; };

async function load(ctx) {
  try { F.items = await jget("/api/secrets"); F.readErr = null; } catch (e) { F.readErr = e.message; }
  ctx.update();
}

/** The box and the list, on this page and in Settings. */
export function secretParts(ctx, level) {
    if (!F.items) {
      if (!F.asked) { F.asked = true; load(ctx); }
      if (!F.readErr) return [empty("Reading…", { kind: "loading" })];
      return [empty(`Couldn't read what's stored: ${F.readErr}`, { kind: "error" }), button("Try again", () => { F.readErr = null; F.asked = false; ctx.update(); }, { size: "s" })];
    }
    // The name typed before full mode ended comes back after the sign-in (store.js keep); the value never is kept, so it's asked for again.
    const back = kept("secret");
    if (back && back.name && !F.back && !F.name && !F.lost && can("secrets")) { F.back = true; F.name = back.name; say(KEPT_NOTE + " Type the value again, then Save."); }
    const save = async () => {
      // An empty box says so beside itself and is marked (WCAG 3.3.1), not only in the line by Save.
      F.bad = { name: F.name.trim() ? "" : "Give it a name first, like github-token.", value: F.value ? "" : "Type the value too." };
      if (F.bad.name || F.bad.value) { say(null); ctx.update(); return; }
      try { await post("/api/secret", { name: F.name.trim(), value: F.value }); F.lost = false; keep("secret", null); say(`Saved ${F.name.trim()}. It can't be shown again.`, "good"); F.name = F.value = ""; await load(ctx); }
      catch (e) {
        // Full mode ended while the box was open: the box stays with its name and the way back beside Save (WCAG 3.3.1, 2.2.5). The value
        // goes: a token shouldn't sit in a page that is about to be signed out, and it's typed again after.
        if (fullEnded(e)) { F.lost = true; F.value = ""; F.bad.value = "Type the value again after signing in."; keep("secret", { name: F.name }); }
        say(e.message, "bad");
      }
      ctx.update();
    };
    const remove = name => confirm({ title: `Remove ${name}?`, body: ["This can't be undone."], ok: "Remove", kind: "danger", onOk: async () => {
      try { await post("/api/secret", { name, delete: true }); say(`Removed ${name}.`, "good"); await load(ctx); }
      catch (e) { say(e.message, "bad"); ctx.update(); }
    } });
    // Replace: the row's name goes in the box and the cursor to the value, so only the new value is typed. The old value is never read.
    const replace = name => {
      F.name = name; F.value = ""; say(`Type the new value for ${name}, then Save. It overwrites the old one.`);
      ctx.update(); setTimeout(() => { const i = document.querySelector(".secrets input[type=password]"); if (i) { i.focus(); i.scrollIntoView({ block: "center" }); } }, 0);
    };
    const reach = S.app.settings ? secretsReach() : "Every agent on this account can read the file, so use tokens you can rotate.";
    return [
      section({ title: "Add or replace", note: "send a token to the Hive", level },
        h("p", { class: "field-h sec-lead" }, "Then tell an agent the name in chat and what it's for. ", reach, S.app.route === "outside" ? " Cloudflare carries it on the way here." : ""),
        h("div", { class: "fgrid" },
          h("div", { class: "field-w" }, field("Name", input({ value: F.name, autocomplete: "off", placeholder: "github-token", ...invalid("err-sname", F.bad.name), oninput: (e, n) => { F.name = n.value; } })), fieldErr("err-sname", F.bad.name)),
          h("div", { class: "field-w" }, field("Value", input({ type: "password", value: F.value, autocomplete: "new-password", ...invalid("err-sval", F.bad.value), oninput: (e, n) => { F.value = n.value; } })), fieldErr("err-sval", F.bad.value))),
        h("div", { class: "frow" }, button("Save", save, { kind: "primary" }),
          F.say ? h("p", { class: "fmsg", "data-tone": F.tone, role: "status" }, F.say) : null)),
      section({ title: "Stored", note: "names only; values are never shown", level }, list(F.items.map(r => ({
        key: r.name, title: r.name, meta: "set " + ago(r.at * 1000),
        text: h("span", { class: "frow" }, button("Replace", () => replace(r.name), { size: "s", kind: "quiet", title: `Type a new value for ${r.name}` }), button("Remove", () => remove(r.name), { size: "s", kind: "quiet" })),
      })), { empty: "Nothing stored yet." }))];
}

export const forgetSecrets = () => { F.value = ""; F.say = null; F.bad = {}; F.lost = false; };
/** Full mode ended while the secret box was being filled in: Settings keeps the box drawn. */
export const secretsLost = () => F.lost;
export default {
  id: "secrets", label: "Secrets", tab: false,   // One place: the Secrets section in Settings. This page is the address #secrets, which goes there.
  enter() { location.hash = "#settings/secrets"; },
  leave: forgetSecrets,
  render(ctx) {
    if (!S.app.app || (!can("secrets") && !F.lost)) return h("div", { class: "page", "data-width": "narrow" }, empty(S.app.secrets_on === false ? "Secrets are off on this Hive. Whoever runs it can turn them on with secrets_on in web.json, on the machine." : "Secrets need full mode. Sign in again if your hour is up.", { kind: "page", title: "Secrets" }));
    return h("div", { class: "page", "data-width": "narrow", "data-key": "secrets" },
      pageHeader({ title: "Secrets", lead: "For giving the hive a token without putting it in chat. Nothing here shows a value again, and it never goes on the board." }),
      h("div", { class: "stack secrets" }, secretParts(ctx, 1)));
  },
};
