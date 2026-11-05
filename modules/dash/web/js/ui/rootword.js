// "Give your word" to rootwarden (the person, 11041507df94; rootwarden's ruling, 11041509d44b). The page can't carry a word itself:
// anything this Hive serves, the Hive itself could forge. So this only helps the person give it on a path root trusts. It shows the exact
// `yes: <action>` line to copy, opens their DM with the box's Slack app (or gives the `sudo h2warden-word` line), and takes back the
// Slack link to the message they sent. That link goes to rootwarden in a post, as before, and root checks the message itself in Slack.
import { h, copyText } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { button } from "./kit.js";
import { say } from "./composer.js";

/** The same shape warden-root's gate copies into its spool (src/gate.py PERMALINK). */
export const PERMALINK = /https:\/\/[a-z0-9][a-z0-9-]{0,62}\.slack\.com\/archives\/[CGD][A-Z0-9]{6,20}\/p\d{16}(?:\?[A-Za-z0-9_=.&%-]{0,200})?/;
const ASKED = /`yes:\s*([^`\n]{3,300})`/gi;

/** The action an agent's post asks the person to say yes to, from its last whole `yes: ...` in backticks, or null.
 *  A line cut short ("yes: rootwarden may ring …") only refers to one: prefilling it sent the person's word as "…" (11042335ee5a). */
export function wordAsked(p) {
  if (!p || p.from === "person" || !can("post")) return null;
  let a = null; for (const m of String(p.text || "").matchAll(ASKED)) if (!/(…|\.\.\.)$/.test(m[1].trim())) a = m[1].trim();
  return a;
}
const line = a => "yes: " + a.trim().replace(/^yes:\s*/i, "");
const sudoLine = a => `sudo h2warden-word ${JSON.stringify(line(a))}`;
/** The Slack link the person pasted, cut to exactly what the gate takes, or null. */
export const permalinkIn = t => { const m = PERMALINK.exec(String(t || "")); return m ? m[0] : null; };

function copyBtn(get, label) {
  return button(label, (e, b) => copyText(get()).then(() => { b.dataset.done = ""; setTimeout(() => delete b.dataset.done, 1400); }), { size: "s", kind: "quiet" });
}

/** The window. th: the thread the post goes to; action: prefilled from the agent's post. */
export function openWord(th, action = "") {
  const act = h("input", { class: "input mono rw-act", spellcheck: "false", value: action.replace(/^yes:\s*/i, ""), "aria-label": "What you say yes to" });
  const shown = h("code", { class: "rw-line" }), sudo = h("code", { class: "rw-sudo" });
  const link = h("input", { class: "input mono rw-link", spellcheck: "false", autocomplete: "off", placeholder: "https://….slack.com/archives/D…/p…", "aria-label": "Link to your Slack message" });
  const err = h("div", { class: "dlg-err", role: "status" });
  const send = button("Send to rootwarden", null, { type: "submit", kind: "primary" });
  const dm = S.app.rootwarden_dm;
  const ready = () => {
    const a = act.value.trim();
    shown.textContent = a ? line(a) : "yes: …"; sudo.textContent = a ? sudoLine(a) : "";
    send.disabled = !a || !permalinkIn(link.value);
    err.textContent = link.value.trim() && !permalinkIn(link.value) ? "That isn't a link to a Slack message. In Slack, open the message's ⋯ menu and choose Copy link." : "";
  };
  act.addEventListener("input", ready); link.addEventListener("input", ready);
  const form = h("form", { method: "dialog", class: "rw" },
    h("h2", null, "Give rootwarden your word"),
    h("p", null, "Rootwarden only acts on a word it can check itself, so this page can't send it for you. Send this line to the box's Slack app yourself, then paste the link to your message here."),
    h("label", { class: "field" }, h("span", { class: "field-l" }, "1. What you say yes to"), act),
    h("div", { class: "frow rw-row" }, shown, copyBtn(() => line(act.value), "Copy")),
    h("p", { class: "field-l" }, "2. Send it in your DM with the box's Slack app"),
    h("div", { class: "frow" },
      dm ? h("a", { class: "btn", "data-size": "s", href: dm, target: "_blank", rel: "noopener noreferrer", "data-key": "rw-dm" }, "Open the DM in Slack") : h("span", { class: "field-h" }, "Open your DM with the box's Slack app."),
      h("span", { class: "field-h" }, "It counts for 24 hours, for one action.")),
    h("details", { class: "rw-alt" }, h("summary", null, "On the machine instead"),
      h("p", { class: "field-h" }, "Run this in a terminal on the Hive's machine as yourself. Then there's no link to paste: tell rootwarden in the thread."),
      h("div", { class: "frow rw-row" }, sudo, copyBtn(() => sudoLine(act.value), "Copy"))),
    h("label", { class: "field" }, h("span", { class: "field-l" }, "3. Paste the link to your message"), link,
      h("span", { class: "field-h" }, "In Slack: the message's ⋯ menu, then Copy link.")),
    err, h("div", { class: "dlg-a" }, button("Cancel", () => dlg.close(), {}), send));
  const dlg = h("dialog", { class: "dlg", "aria-label": "Give rootwarden your word" }, form);
  form.addEventListener("submit", e => {
    e.preventDefault(); const l = permalinkIn(link.value), a = act.value.trim();
    if (!l || !a) return ready();
    say(th, `My word for \`${line(a)}\`, sent in Slack: ${l}`, "rootwarden");   // say() adds the @rootwarden, or the server does: one mention either way
    dlg.close();
  });
  dlg.addEventListener("close", () => dlg.remove());
  document.body.append(dlg); ready(); dlg.showModal(); (action ? link : act).focus();
  return dlg;
}

/** On an agent's post that asks for a `yes: ...` line: the button that opens the window, prefilled. */
export function wordButton(p) {
  const a = wordAsked(p);
  return a ? button("Give your word", () => openWord(p.th, a), { size: "s", kind: "quiet", cls: "msg-word", title: "Send rootwarden this yes in Slack, then hand it the link" }) : null;
}
