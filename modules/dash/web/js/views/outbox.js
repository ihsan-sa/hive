// Outbox: every email an agent asked to send outside the Hive (modules/outward, `h2-out outbox`), with its thread, files, root's checks and
// what became of it (projects/outward/DESIGN.md "The Outbox page"). Owner-only (`files` right). Approve and Auto on open root's own /confirm
// card (openApproval), so the person signs there what root shows; nothing on this page approves or sends. Reject and Auto off act at once
// (POST /api/outbox -> h2-out reject / h2-out off). Everything an agent wrote goes in as text; pictures come only as PNG or JPEG by their bytes.
import { h, tr } from "../core/dom.js";
import { S, can, jget, post } from "../core/store.js";
import { ago } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { empty, pageHeader, needFull, note, block, blocks, section, statusWord, button } from "../ui/kit.js";
import { confirm } from "../ui/dialog.js";
import { openApproval, cardWhy } from "../ui/rootword.js";
import { parseOutward } from "../rich/outward_parse.js";
import { chatPlace } from "./comms.js";

const O = { data: null, open: new Set(), note: {} };
const KIND = { quote: "Quotes", followup: "Follow-ups" };
const TONE = { waiting: "warn", good: "good", bad: "bad", off: "quiet" };
const STATES = new Set(["waiting", "sent", "refused", "failed", "cancelled", "expired"]);
const IMG = new Set(["png", "jpeg"]);
const kb = n => n < 1024 ? `${n} B` : n < 1 << 20 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`;
const row = (k, ...v) => h("div", { class: "rc-buy-row" }, h("div", { class: "rc-buy-k" }, k), h("div", { class: "rc-buy-v" }, ...v));
const when = s => { const t = Date.parse(s || ""); return Number.isFinite(t) ? ago(t) : ""; };

const load = async ctx => { try { O.data = await jget("/api/outbox"); } catch (e) { O.data = { items: [], auto: [], ...O.data, error: true }; } ctx.update(); };   // a failed refresh keeps what was read

/** One item, read the way the thread's card reads it. Root's own state wins over the item's: root refused or sent it, whatever the copy here says. */
export function model(it, now = Date.now()) {
  const rs = it && it.root && STATES.has(it.root.state) ? it.root.state : null;
  try { return { ...parseOutward(JSON.stringify(rs ? { ...it, state: rs } : it), now), thread: typeof it.thread === "string" ? it.thread : "", root: it.root || null, at: it.at, raw: it }; }
  catch (e) { return null; }
}

/** Ask why, then act at once. what: {do, id} or {do, kind}. */
function askWhy(ctx, { title, ok, lead, body, key, done }) {
  const why = h("textarea", { class: "input", rows: "2", maxlength: "300", "aria-label": "Why", "data-key": "ob-why" });
  confirm({
    title, ok, kind: "danger", failText: "Nothing changed",
    body: [lead, h("label", { class: "field" }, h("span", { class: "field-l" }, "Why (the agent reads this)"), why)],
    onOk: async () => {
      const w = why.value.trim();
      if (!w) { why.setAttribute("aria-invalid", "true"); why.focus(); throw Error(tr("say why")); }
      let d;
      try { d = await post("/api/outbox", { ...body, why: w }); }
      catch (e) { throw Error(e.status === 403 ? tr("this connection can't act on the Outbox") : e.message); }
      O.note[key] = tr(done);
      await load(ctx);
    },
  });
  setTimeout(() => why.focus(), 0);
}

function approve(d, key, ctx) {
  const bad = cardWhy(d.words);
  if (bad) { O.note[key] = bad; ctx.update(); return; }
  if (!openApproval(d.thread || "hive-mail-outbound", d.words)) { O.note[key] = tr("Root's approval card isn't reachable from here; approve it from the thread in the app."); ctx.update(); }
}

function thumbs(d) {
  return d.files.map((f, i) => {
    const n = (d.raw.files[i] && d.raw.files[i].n) || i + 1, line = `${f.name} (${f.type}, ${kb(f.size)})`;
    if (!IMG.has(f.type)) return h("div", { class: "ob-file" }, line);
    const src = "/api/outbox/file?" + new URLSearchParams({ id: d.id, n: String(n) });
    return h("figure", { class: "ob-file" }, h("img", { class: "ob-thumb", src, alt: `${f.name}, attached`, loading: "lazy", decoding: "async" }), h("figcaption", null, line));
  });
}

function itemBlock(d, ctx) {
  const key = "ob:" + d.id, open = O.open.has(d.id), checks = d.root && d.root.checks && typeof d.root.checks === "object" ? Object.entries(d.root.checks) : [];
  const acts = [];
  if (d.approvable && can("post")) acts.push(button("Approve…", () => approve(d, key, ctx), { kind: "primary", size: "s", cls: "ob-approve" }));
  if (d.state === "waiting" && d.tone !== "off") acts.push(button("Reject…", () => askWhy(ctx, { key, title: `Reject the email to ${d.to}?`, ok: "Reject",
    lead: "It is cancelled now and never sent. The agent that asked is told why in its thread.", body: { do: "reject", id: d.id }, done: "Rejected. The agent is told why in its thread." }), { size: "s", cls: "ob-reject" }));
  const told = O.note[key];
  // Its own <details>, as block({fold}) draws it, with the open kept on the click itself: a re-render before the toggle event would undo it (kit.js section).
  const flip = (e, n) => { const el = n.parentNode; el.open ? O.open.delete(d.id) : O.open.add(d.id); };
  return h("details", { class: "blk", "data-key": key, open: open ? "" : null, ontoggle: (e, n) => { if (e.target === n) n.open ? O.open.add(d.id) : O.open.delete(d.id); } },
    h("summary", { class: "blk-h", onclick: flip }, h("b", { class: "blk-t" }, d.subject), h("span", { class: "blk-s" }, `To ${d.to}`, " · ", d.kind, d.at ? " · " : null, d.at ? when(d.at) : null),
      statusWord(TONE[d.tone] || "quiet", d.label)),
    h("div", { class: "blk-b" },
    row("From", d.from), row("To", d.to), row("Subject", d.subject),
    row("Text", h("div", { class: "rc-out-body" }, d.body)),
    d.files.length ? row("Files", ...thumbs(d), h("div", { class: "rc-buy-fine" }, `sha256 of all ${d.files.length}: ${d.files_sha}`)) : null,
    d.purpose ? row("For", d.purpose) : null,
    ...d.holds.map(x => row("Hold", x)),
    d.note ? row("Note", d.note) : null,
    checks.length ? row("Checks", ...checks.map(([k, v]) => h("div", null, `${k}: ${v}`))) : null,
    d.root && (d.root.why || d.root.state) ? row("Root", `${d.root.state || ""}${d.root.why ? ": " + d.root.why : ""}${d.root.at ? " (" + when(d.root.at) + ")" : ""}`) : null,
    d.thread ? row("Thread", h("a", { href: threadHref(d.thread) }, d.thread)) : null,
    d.why ? h("p", { class: "rc-buy-fine" }, d.why) : null,
    acts.length || told ? h("div", { class: "rc-buy-act" }, h("span", { class: "rc-buy-note", role: "status" }, told || ""), ...acts) : null));
}

function autoBlock(a, ctx, paused) {
  const key = "ob-auto:" + a.kind, name = KIND[a.kind] || a.kind, told = O.note[key];
  const sub = a.on ? `On${a.until ? ", until " + new Date(Date.parse(a.until)).toLocaleDateString() : ""}: root sends these without a card, up to 10 a day, to businesses only.` : "Off: each one waits for your approval.";
  const acts = a.on
    ? button("Auto off…", () => askWhy(ctx, { key, title: `Turn off automatic ${name.toLowerCase()}?`, ok: "Turn off",
        lead: "From now on each one waits for your approval again. Root stops sending them on its next look, within a minute.", body: { do: "off", kind: a.kind }, done: "Turned off. Each one waits for your approval again." }), { size: "s", cls: "ob-off" })
    : can("post") && !paused ? button("Auto on…", () => { const bad = cardWhy(a.word); if (bad) { O.note[key] = bad; ctx.update(); return; }
        if (!openApproval("hive-mail-outbound", a.word)) { O.note[key] = tr("Root's approval card isn't reachable from here."); ctx.update(); } }, { size: "s", cls: "ob-on" }) : null;
  return block({ key, title: name, sub: sub + (a.off_at ? ` Turned off here ${when(a.off_at)}.` : ""), actions: acts }, told ? h("p", { class: "rc-buy-note", role: "status" }, told) : null);
}

export default chatPlace("outbox", {
  id: "outbox", label: "Outbox", tab: () => can("files"),
  poll: { every: 30000, run: load },
  render(ctx) {
    if (!can("files")) return needFull("Outbox", "Outbox");
    const d = O.data;
    if (!d && !O.asked) { O.asked = true; load(ctx); }
    const head = pageHeader({ title: "Outbox", lead: "Every email an agent asked to send outside the Hive, and what became of it. Approve and Auto on open root's approval card, where you sign; Reject and Auto off act here at once." });
    if (!d) return h("div", { class: "page", "data-width": "narrow", "data-key": "outbox" }, head, empty("Reading…", { kind: "loading" }));
    const now = Date.now(), all = (d.items || []).map(it => [it, model(it, now)]), rows = all.map(x => x[1]).filter(Boolean);
    const odd = all.filter(x => !x[1]).map(x => x[0] && typeof x[0].id === "string" ? x[0].id.slice(0, 20) : "?");
    const waiting = rows.filter(r => r.state === "waiting" && r.tone !== "off");
    return h("div", { class: "page", "data-width": "narrow", "data-key": "outbox" }, head,
      d.error ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, rows.length ? "Couldn't read the outbox just now. This is what was there before; it tries again in 30 seconds." : "Couldn't read the outbox. It tries again in 30 seconds.") : null,
      d.paused ? note({ tone: "warn", title: "Outward mail is paused", key: "ob-paused" }, `Nothing goes out while it is paused${d.paused.at ? " (since " + when(d.paused.at) + ")" : ""}.${d.paused.why ? " " + d.paused.why : ""}`) : null,
      !d.unset && d.root === false ? note({ tone: "info", title: "Root's mail job isn't installed yet", key: "ob-noroot" }, "Agents can draft here, but nothing can be sent until root installs its part.") : null,
      d.unset ? empty("The outward module isn't installed on this machine.") : [
        (d.auto || []).length ? section({ title: "Automatic", key: "ob-auto", note: "Kinds root may send without a card, under your standing word." }, blocks(...d.auto.filter(a => KIND[a.kind]).map(a => autoBlock(a, ctx, !!d.paused)))) : null,
        section({ title: waiting.length ? `Emails (${waiting.length} waiting for you)` : "Emails", key: "ob-items" },
          odd.length ? h("p", { class: "meta", "data-key": "ob-odd" }, `${odd.length} more this page can't show as written (${odd.join(", ")}); its thread has the card.`) : null,
          rows.length ? blocks(...rows.map(r => itemBlock(r, ctx))) : odd.length ? null : empty("No emails yet. When an agent drafts one with h2-out, it shows here.")),
      ]);
  },
});
