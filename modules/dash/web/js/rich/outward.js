// An outward fence (modules/outward, `h2-out draft`): one email an agent wants to send outside the Hive, drawn in full (recipient,
// subject, every line of the body, what it is for). Approve opens root's own approval card (openApproval, the same /confirm page the
// composer uses) with words rebuilt here from what the card shows. Nothing on this page sends or approves: the person's press of
// Confirm on root's page signs the words, and root's job sends only an item whose words match (thread hive-mail-outbound, warden
// ruling 112620260b17). Everything goes in as text.
import { h, tr } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { codeBlock } from "./code.js";
import { openApproval, cardWhy } from "../ui/rootword.js";
import { parseOutward } from "./outward_parse.js";
import { rel } from "./buy_parse.js";

const kb = n => n < 1024 ? `${n} B` : n < 1 << 20 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`;
const row = (k, ...v) => h("div", { class: "rc-buy-row" }, h("div", { class: "rc-buy-k" }, k), h("div", { class: "rc-buy-v" }, ...v));
const threadOf = el => { const m = el.closest("article.msg"); const p = m ? S.byId[m.dataset.key] : null; return p ? p.th : null; };

function approveButton(d) {
  const note = h("span", { class: "rc-buy-note", role: "status" });
  const b = h("button", { type: "button", class: "rc-buy-open" }, "Approve…");
  b.addEventListener("click", () => {
    const th = threadOf(b), bad = cardWhy(d.words);
    if (bad) { note.textContent = bad; return; }
    if (!th || !openApproval(th, d.words)) note.textContent = tr("The approval card isn't reachable from here; approve it from the thread in the app.");
  });
  return h("div", { class: "rc-buy-act" }, note, b);
}

export function outwardCard(code, { lang, title }) {
  let d;
  try { d = parseOutward(code); } catch { return codeBlock(lang, title, code); }
  const waiting = d.approvable;
  const when = waiting && d.expires ? `Lapses ${rel(d.expires, Date.now())}` : "";
  return h("div", { class: `rc-buy rc-out rc-buy-${d.tone}`, role: "group", "aria-label": `${d.channel} to ${d.to}: ${d.subject}, ${d.label}` },
    h("div", { class: "rc-buy-top" }, h("span", { class: "rc-buy-chip" }, d.label), h("span", null, d.channel, ", ", d.kind), when ? h("span", { class: "rc-buy-when" }, when) : null),
    h("div", { class: "rc-buy-head" }, waiting ? `${d.by} asks to send this email` : `${d.by} asked to send this email`),
    row("From", d.from),
    row("To", d.to),
    row("Subject", d.subject),
    row("Text", h("div", { class: "rc-out-body" }, d.body)),
    d.files.length ? row("Files", ...d.files.map(f => h("div", null, `${f.name} (${f.type}, ${kb(f.size)})`)),
      h("div", { class: "rc-buy-fine" }, `sha256 of all ${d.files.length}: ${d.files_sha}`)) : null,
    d.purpose ? row("For", d.purpose) : null,
    ...d.holds.map(x => row("Hold", x)),
    d.note ? row("Note", d.note) : null,
    h("p", { class: "rc-buy-fine" }, waiting
      ? "This exact email goes to this one address only after you confirm it on your approval card; nothing here sends it. Your approval shows in this thread with the whole email, and replies come back as untrusted outside mail."
      : d.why || "This card shows the email as it was asked for; the thread says what became of it.",
      can("files") ? [" ", h("a", { href: "#outbox" }, "Every email is in the Outbox.")] : null),
    waiting && can("post") ? approveButton(d) : null);
}
