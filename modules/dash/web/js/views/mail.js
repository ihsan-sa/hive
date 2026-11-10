// Mail: the owner's copy of the Hive's mail (hive-mail), read-only. Owner-only (`files` right). Bodies are untrusted: plain text nodes, nothing is linked or loaded.
import { h } from "../core/dom.js";
import { S, can, jget } from "../core/store.js";
import { ago } from "../core/format.js";
import { empty, list, pageHeader, needFull } from "../ui/kit.js";
import { icon } from "../ui/icons.js";
import { markMailSeen } from "../core/mailnew.js";
import { chatPlace } from "./comms.js";

const M = { data: null, open: null };
const kb = n => n == null ? "" : n < 1024 ? n + " B" : Math.round(n / 1024) + " KB";

const load = async ctx => { try { M.data = await jget("/api/mail"); markMailSeen(M.data.items); } catch (e) { M.data = { items: [], ...M.data, error: true }; } ctx.update(); };   // a failed refresh keeps what was already read

export default chatPlace("mail", {
  id: "mail", label: "Mail", tab: () => can("files"),
  poll: { every: 30000, run: load },
  render(ctx) {
    if (!can("files")) return needFull("Mail", "Mail");
    const d = M.data;
    if (!d && !M.asked) { M.asked = true; load(ctx); }
    if (!d) return h("div", { class: "page", "data-width": "narrow", "data-key": "mail" }, pageHeader({ title: "Mail" }), empty("Reading…", { kind: "loading" }));
    const rows = (d.items || []).map(m => {
      if (m.held) return { key: m.id, title: "Held mail", sub: "Kept in the store, not shown here" };
      const on = M.open === m.id;
      return {
        key: m.id, lead: icon(on ? "chevron-down" : "chevron-right"), title: m.subject || "(no subject)", meta: m.received_at ? ago(Date.parse(m.received_at)) : m.date, expanded: on,
        onClick: () => { M.open = on ? null : m.id; ctx.update(); },
        whole: on,
        text: on ? h("div", null,
          h("pre", { class: "mail-body" }, m.text + (m.truncated ? "\n[cut]" : "")),
          (m.attachments || []).length ? h("p", { class: "meta" }, "Attachments (names only): ", m.attachments.map(a => a.name + (a.size != null ? ` (${kb(a.size)})` : "")).join(", ")) : null) : (m.text || "").slice(0, 160),
      };
    });
    return h("div", { class: "page", "data-width": "narrow", "data-key": "mail" },
      pageHeader({ title: "Mail", lead: "Your mail, copied read-only. Plain text only; links aren't opened and attachments aren't brought across." + (d.checked ? " Checked " + ago(Date.parse(d.checked)) + "." : "") }),
      d.error ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, rows.length ? "Couldn't check for new mail. This is what was there before; it tries again in 30 seconds." : "Couldn't read your mail. It tries again in 30 seconds.") : null,
      d.error && !rows.length ? null : list(rows, { empty: d.unset ? "Mail isn't connected on this machine." : "No mail yet." }));
  },
});
