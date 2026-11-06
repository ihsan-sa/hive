// Mail: the owner's copy of h2@ mail (hive-mail), read-only. Owner-only (`files` right). Bodies are untrusted: plain text nodes, nothing is linked or loaded.
import { h } from "../core/dom.js";
import { S, can, jget } from "../core/store.js";
import { ago } from "../core/format.js";
import { empty, list, pageHeader } from "../ui/kit.js";

const M = { data: null, open: null };
const kb = n => n == null ? "" : n < 1024 ? n + " B" : Math.round(n / 1024) + " KB";

const load = async ctx => { try { M.data = await jget("/api/mail"); } catch (e) { M.data = { items: [], error: e.message }; } ctx.update(); };

export default {
  id: "mail", label: "Mail", tab: () => can("files"),
  poll: { every: 30000, run: load },
  render(ctx) {
    if (!can("files")) return h("div", { class: "page" }, empty("Mail is for a full sign-in.", { kind: "page", title: "Mail" }));
    const d = M.data;
    if (!d && !M.asked) { M.asked = true; load(ctx); }
    if (!d) return h("div", { class: "page", "data-key": "mail" }, pageHeader({ title: "Mail" }), empty("Reading…", { kind: "loading" }));
    const rows = d.items.map(m => {
      if (m.held) return { key: m.id, title: "Held mail", sub: "kept in the store (it may hold marks)" };
      const on = M.open === m.id;
      return {
        key: m.id, title: m.subject || "(no subject)", meta: m.received_at ? ago(Date.parse(m.received_at)) : m.date, expanded: on,
        onClick: () => { M.open = on ? null : m.id; ctx.update(); },
        whole: on,
        text: on ? h("div", null,
          h("pre", { class: "mail-body", style: "white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;margin:0" }, m.text + (m.truncated ? "\n[cut]" : "")),
          m.attachments.length ? h("p", { class: "meta" }, "Attachments (names only): ", m.attachments.map(a => a.name + (a.size != null ? ` (${kb(a.size)})` : "")).join(", ")) : null) : (m.text || "").slice(0, 160),
      };
    });
    return h("div", { class: "page", "data-width": "narrow", "data-key": "mail" },
      pageHeader({ title: "Mail", lead: "Your mail to h2@, copied read-only. Plain text only; links aren't opened and attachments aren't brought across." + (d.checked ? " Checked " + ago(Date.parse(d.checked)) + "." : "") }),
      d.error ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, d.error) : null,
      list(rows, { empty: "No mail yet." }));
  },
};
