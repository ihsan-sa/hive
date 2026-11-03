// Secrets: a write-only box. A value goes in and no route gives it back; the page holds it only until it's sent.
import { h } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { ago } from "../core/format.js";
import { button, empty, field, input, list, pageHeader, section } from "../ui/kit.js";

const F = { items: null, name: "", value: "", say: null, tone: null, asked: false };
const say = (text, tone) => { F.say = text; F.tone = tone || null; };

async function load(ctx) {
  try { F.items = await jget("/api/secrets"); } catch (e) { F.items = F.items || []; say(e.message, "bad"); }
  ctx.update();
}

export default {
  id: "secrets", label: "Secrets", tab: () => S.app.app && can("secrets"),
  leave() { F.value = ""; F.say = null; },
  render(ctx) {
    if (!S.app.app || !can("secrets")) return h("div", { class: "page" }, empty(S.app.secrets_on === false ? "Secrets are off on this Hive. Whoever runs it can turn them on with secrets_on in web.json, on the machine." : "Secrets need full mode. Sign in again if your hour is up.", { kind: "page", title: "Secrets" }));
    if (!F.items) { if (!F.asked) { F.asked = true; load(ctx); } return h("div", { class: "page" }, empty("Reading…", { kind: "loading" })); }
    const save = async () => {
      try { await post("/api/secret", { name: F.name.trim(), value: F.value }); say(`Saved ${F.name.trim()}. It can't be shown again.`, "good"); F.name = F.value = ""; await load(ctx); }
      catch (e) { say(e.message, "bad"); }
      ctx.update();
    };
    const remove = async name => {
      if (!confirm(`Remove ${name}? This can't be undone.`)) return;
      try { await post("/api/secret", { name, delete: true }); say(`Removed ${name}.`, "good"); await load(ctx); }
      catch (e) { say(e.message, "bad"); ctx.update(); }
    };
    return h("div", { class: "page" },
      pageHeader({ title: "Secrets", lead: "For giving the hive a token without putting it in chat. Nothing here shows a value again, and it never goes on the board." }),
      section({ title: "Add or replace" },
        h("p", { class: "field-h" }, "Then tell an agent the name in chat and what it's for. Every agent on this account can read the file, and Cloudflare carries it on the way here, so use tokens you can rotate."),
        h("div", { class: "fgrid" },
          field("Name", input({ value: F.name, autocomplete: "off", placeholder: "github-token", oninput: (e, n) => { F.name = n.value; } })),
          field("Value", input({ type: "password", value: F.value, autocomplete: "new-password", oninput: (e, n) => { F.value = n.value; } }))),
        h("div", { class: "frow" }, button("Save", save, { kind: "primary" }),
          F.say ? h("p", { class: "fmsg", "data-tone": F.tone, role: "status" }, F.say) : null)),
      section({ title: "Stored" }, list(F.items.map(r => ({
        key: r.name, title: r.name, meta: ago(r.at * 1000),
        text: button("Remove", () => remove(r.name), { size: "s", kind: "quiet" }),
      })), { empty: "Nothing stored yet." })));
  },
};
