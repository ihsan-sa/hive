// The consumer app's setup parts: accounts, data sharing, power and the sandbox. The first-run walkthrough and Settings show the
// same parts, so they can't say different things. Only on a Hive with settings.json (S.app.settings); facts come from /api/settings.
import { h } from "../core/dom.js";
import { S, can, jget, post, loadApp } from "../core/store.js";
import { ago } from "../core/format.js";
import { button, empty, field, input, seg, statusLabel } from "./kit.js";
import { copyButton } from "./refs.js";

// What's being typed lives here, so a poll never loses it. A key is held only until it's sent.
const P = { page: null, asked: false, err: "", keyKind: "anthropic", key: "", keySay: null, codexSay: "", ticking: false };
const BARE = "Agents would run without a sandbox. An unsandboxed agent acts on everything your logins and files reach: it can read your credentials, change any file you can, and reach every service on this machine and network.";
const ALWAYS = ["spending money", "credentials", "opening this machine to the network", "anything sent off this machine with your accounts or keys (git push, gh, publishing, mail, posting)", "publishing private details", "deleting repos, commits or history"];
export const POWER = [
  [1, "Ask me first", "Agents work inside the sandbox on their own. A step that has to leave the sandbox comes to you, and waits for your answer.", "A bigger step comes to you first, and the agent waits for your answer."],
  [2, "A warden decides", "A warden, an agent whose only job is judging these steps, approves the step and it runs as your computer account. A step that's hard to undo needs more than one opinion."],
  [3, "A warden decides, I hear afterwards", "A warden settles it without you, and you're told what happened afterwards."],
];

export const local = () => S.app.route === "local";
const sayMsg = (text, tone) => text ? h("p", { class: "fmsg", "data-tone": tone || null, role: "status" }, text) : null;
const localOnly = () => local() ? null : h("p", { class: "field-h" }, "This can only be changed on this computer.");

export async function loadPage(update) {
  P.asked = true;
  try { P.page = await jget("/api/settings"); P.err = ""; } catch (e) { P.err = e.message; }
  update();
}
export function pageData(update) {
  if (!P.asked && can("config")) loadPage(update);
  return P.page && P.page.settings ? P.page : null;
}
export const reading = () => P.err ? sayMsg(P.err, "bad") : empty("Reading…", { kind: "loading" });
export const forget = () => { P.asked = false; P.key = ""; P.keySay = null; P.codexSay = ""; };

/** Save settings; the answer is the whole page. Returns false if it was refused. */
export async function saveSettings(d, update) {
  try { P.page = await post("/api/settings", d); P.err = ""; await loadApp(); update(); return true; }
  catch (e) { P.err = e.message; update(); return false; }
}
const saveErr = () => P.err ? sayMsg(P.err, "bad") : null;

/** Whether an account other than a Claude login can run agents, as this page knows it. */
export const anyAccount = () => { const a = P.page && P.page.accounts, s = S.app.setup || {}; return !!(s.signed_in || s.api_key || s.other || (a && (a.keys.length || a.codex.signed_in))); };

// ---- accounts ----
function tickCodex(update) {
  if (P.ticking) return;
  P.ticking = true;
  const tick = async () => {
    let v; try { v = await jget("/api/signin-codex"); } catch (e) { return setTimeout(tick, 1500); }
    if (P.page && P.page.accounts) P.page.accounts.codex = v;
    update();
    if (v.state === "starting" || v.state === "waiting") return setTimeout(tick, 1500);
    P.ticking = false;
    await loadApp(); await loadPage(update);
  };
  tick();
}
export function codexPart(update) {
  const a = P.page.accounts, c = a.codex;
  const go = async () => { P.codexSay = ""; try { const v = await post("/api/signin-codex", {}); a.codex = v; tickCodex(update); } catch (e) { P.codexSay = e.message; } update(); };
  const stop = async () => { try { a.codex = await post("/api/signin-codex", { cancel: true }); } catch (e) { P.codexSay = e.message; } update(); };
  const busy = c.state === "starting" || c.state === "waiting";
  if (busy) tickCodex(update);
  return h("div", { class: "signin", "data-key": "codex" },
    h("div", { class: "frow fsplit" }, h("b", null, "Codex"), h("span", { class: "muted" }, "with a ChatGPT login"),
      c.installed ? statusLabel(c.signed_in ? "working" : "ended", c.signed_in ? "signed in" : "not signed in") : statusLabel("ended", "not installed"),
      c.installed && can("logins") && !busy ? h("span", { style: "margin-left:auto" }, button(c.signed_in ? "Sign in again" : "Sign in", go, { size: "s" })) : null),
    c.installed ? null : h("p", { class: "field-h" }, "To use it, install it in a terminal as this account: ", h("code", null, "npm install -g @openai/codex"), ". Then come back here."),
    c.state === "starting" ? sayMsg("Starting Codex's sign-in…") : null,
    c.state === "waiting" ? [
      h("ol", { class: "steps" },
        h("li", null, /^https:\/\//.test(c.url) ? h("a", { href: c.url, target: "_blank", rel: "noreferrer noopener" }, "Open the sign-in page") : "Codex didn't give a usable link.", " in any browser, and sign in."),
        h("li", null, "Enter this code: ", h("b", { class: "mono", "data-key": "code" }, c.code), " ", copyButton(c.code), h("span", { class: "field-h" }, "It lasts 15 minutes."))),
      h("div", { class: "frow" }, button("Cancel", stop, { kind: "quiet", size: "s" }), h("span", { class: "meta" }, "Waiting for you to finish…"))] : null,
    c.state === "failed" ? sayMsg(c.error || "That didn't work.", "bad") : null,
    c.state === "done" ? sayMsg("Signed in to Codex.", "good") : null,
    sayMsg(P.codexSay, "bad"));
}
export function keysPart(update) {
  const a = P.page.accounts;
  const save = async () => {
    const value = P.key; P.key = ""; P.keySay = null;  // gone from the page before the answer, whatever it is
    try { P.page = await post("/api/account-key", { kind: P.keyKind, value }); P.keySay = { text: "Saved. It can't be shown again, and it isn't checked until an agent uses it.", tone: "good" }; await loadApp(); }
    catch (e) { P.keySay = { text: e.message, tone: "bad" }; }
    update();
  };
  const remove = async k => {
    if (!confirm(`Remove the ${k.label}?`)) return;
    try { P.page = await post("/api/account-key", { kind: k.kind, delete: true }); P.keySay = { text: "Removed.", tone: "good" }; await loadApp(); }
    catch (e) { P.keySay = { text: e.message, tone: "bad" }; }
    update();
  };
  return h("div", { class: "signin", "data-key": "keys" },
    h("div", { class: "frow fsplit" }, h("b", null, "API keys"), h("span", { class: "muted" }, "charged per use")),
    a.keys.length ? h("ul", { class: "list" }, a.keys.map(k => h("li", { class: "lr", "data-key": k.kind },
      h("div", { class: "lr-in frow fsplit" }, h("b", null, k.label), h("span", { class: "meta" }, (k.ends ? "ends " + k.ends + ", " : "") + "saved " + ago(k.at * 1000)),
        can("logins") ? h("span", { style: "margin-left:auto" }, button("Remove", () => remove(k), { size: "s", kind: "quiet" })) : null)))) : null,
    can("logins") ? [
      h("p", { class: "field-h" }, "Used only when no Claude or Codex login has room. Every run costs money on that key, so set a spending limit with the provider. An OpenRouter key must have a limit on it, or Hive won't use it."),
      seg([["anthropic", "Anthropic"], ["openrouter", "OpenRouter"]], P.keyKind, v => { P.keyKind = v; update(); }, { label: "Which provider" }),
      h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (P.key.trim()) save(); } },
        h("div", { style: "flex:1;min-width:12rem" }, field("Key", input({ type: "password", autocomplete: "new-password", spellcheck: "false", "data-key": "account-key", value: P.key, oninput: (e, n) => { P.key = n.value; } }))),
        button("Save key", null, { type: "submit" }))] : null,
    P.keySay ? sayMsg(P.keySay.text, P.keySay.tone) : null);
}
export const rootNote = () => P.page.accounts.root ? h("p", { class: "fmsg", "data-tone": "warn" }, "This computer account can use sudo without a password, so Hive won't run Codex or API-key models on it: an outside model must not share an account with root. Claude logins still work. Give Hive an account without sudo to use the others.") : null;

// ---- data sharing ----
const SHARES = [
  ["failures", "Failure reports", "What failed, when and why."],
  ["usage", "Usage reports", "Counts of runs, restarts and limit hits."],
  ["everything", "Everything, when I say so", "Separate from the reports above: a scrubbed copy of recent logs and of the board, and that includes your chats. It goes only when you run hive-report bundle --yes yourself, and you can read it first."],
];
export function sharePart(update) {
  const pg = P.page, on = pg.settings.share;
  return [
    pg.share_text ? h("div", { class: "share-text", "data-key": "share-text" }, pg.share_text.split(/\n+/).map(t => h("p", null, t)))
      : h("p", { "data-key": "share-text" }, "The report module isn't installed here, so nothing can be sent, whatever is ticked."),
    h("div", { class: "checks", role: "group", "aria-label": "What to share" }, SHARES.map(([k, label, hint]) => h("label", { class: "check", "data-key": k },
      h("input", { type: "checkbox", checked: on[k], disabled: !local(), onchange: (e, n) => saveSettings({ share: { [k]: n.checked } }, update) }),
      h("span", null, label, h("span", { class: "field-h" }, hint))))),
    h("p", { class: "field-h" }, "All off until you turn one on. You can change this in Settings at any time."),
    localOnly(), saveErr()];
}

// ---- power and the sandbox ----
/** "enforced": the sandbox is on and its own check says it works here. Anything else, the levels are not a limit and the page says so. */
const enforced = pg => pg.settings.sandbox === "on" && pg.sandbox_check.state === "works";
export function powerPart(update) {
  const pg = P.page, cur = pg.settings.power, a = pg.accounts, claude = a.claude.some(l => l.signed_in);
  return [
    h("p", null, enforced(pg) ? "Agents work inside a sandbox on this machine. This sets who decides when one needs a step outside it, like installing something or changing a file elsewhere on this computer."
      : "This sets who decides when an agent wants to do something bigger. Read the sandbox note below: without a working sandbox, this is an instruction to the agents, and nothing on this machine holds them to it."),
    h("div", { class: "checks", role: "radiogroup", "aria-label": "How much power the Hive has" }, POWER.map(([n, label, hint, loose]) => h("label", { class: "check", "data-key": "power-" + n },
      h("input", { type: "radio", name: "power", checked: cur === n, disabled: !local(), onchange: () => saveSettings({ power: n }, update) }),
      h("span", null, `${n}. ${label}`, h("span", { class: "field-h" }, (!enforced(pg) && loose) || hint))))),
    h("p", { "data-key": "always" }, h("b", null, "At every level, these stay your call: "), ALWAYS.join(", ") + "."),
    h("p", { class: "field-h", "data-key": "approve-word" }, "When a step comes to you, you answer in the thread with approve step and the step's code. The code isn't a password and doesn't say what the step does, so read the step first. It ties your answer to that exact script, only you can give it, and it works once."),
    claude ? null : h("p", { class: "fmsg", "data-tone": "warn", "data-key": "no-warden" }, "Wardens run on Claude, and this Hive has no Claude login signed in. So there's no warden: every step comes to you, whichever level is set."),
    localOnly(), saveErr()];
}
export function sandboxPart(update) {
  const pg = P.page, c = pg.sandbox_check, bare = pg.settings.sandbox === "bare";
  const leave = () => { if (confirm(BARE + "\n\nRun without the sandbox anyway?")) saveSettings({ sandbox: "bare" }, update); };
  const back = () => saveSettings({ sandbox: "on" }, update);
  const leaveBtn = () => local() ? h("div", { class: "frow" }, button("Run without the sandbox", leave, { kind: "quiet", size: "s" })) : null;
  let body;
  if (bare) body = [h("p", null, statusLabel("limited", "Off: you chose to run without the sandbox.")), h("p", null, BARE), h("p", null, "The levels above are only instructions here. Nothing on this machine enforces them."),
    local() ? h("div", { class: "frow" }, button("Turn the sandbox back on", back, { size: "s" })) : null];
  else if (c.state === "works") body = [h("p", null, statusLabel("working", "The sandbox works on this machine.")),
    h("p", null, "Each agent runs shut in. It can't reach this machine's services or your private network, and your other logins, ~/.ssh and the rest of your home aren't there. One thing it can read: the sign-in of the login it runs on."),
    leaveBtn()];
  else if (c.state === "broken") body = [h("p", null, statusLabel("failed", "The sandbox doesn't work on this machine.")), c.why ? h("p", null, c.why) : null, c.fix ? h("p", null, h("b", null, "What you can do: "), c.fix) : null,
    h("p", null, "Agents won't start until it works, or until you choose to run without it. Without it, nothing on this machine enforces the levels above."), leaveBtn()];
  else body = [h("p", null, statusLabel("ended", "This app couldn't find out whether the sandbox works here.")),
    h("p", null, "So don't count on the levels above as a limit: nothing here confirms that this machine enforces them."), leaveBtn()];
  return [h("div", { "data-key": "sandbox", "data-state": bare ? "bare" : c.state }, body), localOnly(), saveErr()];
}
/** What the Secrets box can truthfully say about who reads the file, given the sandbox. */
export function secretsReach() {
  const pg = P.page;
  if (!pg || !pg.settings || pg.settings.sandbox === "bare") return "Every agent on this account can read the file, so use tokens you can rotate.";
  return "An agent in the sandbox can't open the file. A step that runs outside the sandbox can, and that step goes through the power level you set. Use tokens you can rotate.";
}
