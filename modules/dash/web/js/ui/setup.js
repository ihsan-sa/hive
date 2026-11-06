// The consumer app's setup parts: accounts, data sharing, power and the sandbox. The first-run walkthrough and Settings show the
// same parts, so they can't say different things. Only on a Hive with settings.json (S.app.settings); facts come from /api/settings.
import { h } from "../core/dom.js";
import { confirm } from "./dialog.js";
import { S, can, jget, post, loadApp } from "../core/store.js";
import { ago } from "../core/format.js";
import { button, empty, field, input, seg, statusLabel } from "./kit.js";
import { copyButton } from "./refs.js";
import { act, begin, finish, isBusy } from "./activity.js";

// What's being typed lives here, so a poll never loses it. A key is held only until it's sent.
const P = { page: null, asked: false, err: "", keyKind: "anthropic", key: "", keySay: null, codexSay: "", ticking: false, codexSeen: null, seq: 0, saving: {}, saved: null, want: [] };
const BARE = "Agents would run without a sandbox. An unsandboxed agent acts on everything your logins and files reach: it can read your credentials, change any file you can, and reach every service on this machine and network.";
const ALWAYS = ["spending money", "credentials", "opening this machine to the network", "anything sent off this machine with your accounts or keys (git push, gh, publishing, mail, posting)", "publishing private details", "deleting repos, commits or history"];
export const POWER = [
  [1, "Ask me first", "Agents work inside the sandbox on their own. A step that has to leave the sandbox comes to you, and waits for your answer.", "A bigger step comes to you first, and the agent waits for your answer."],
  [2, "A warden decides", "A warden, an agent whose only job is judging these steps, approves the step and it runs as your computer account. A step that's hard to undo needs more than one opinion."],
  [3, "A warden decides, I hear afterwards", "A warden settles it without you, and you're told what happened afterwards."],
];

export const MODELS = [
  ["opus", "Opus", "For hard work. It uses up a plan's limits faster than Sonnet."],
  ["sonnet", "Sonnet", "Uses a plan's limits more slowly. For everyday work."],
  ["haiku", "Haiku", "The lightest. For small chores."],
  ["fable", "Fable", "Another Claude model. Your plan may not include it."],
];

export const local = () => S.app.route === "local";
const sayMsg = (text, tone) => text ? h("p", { class: "fmsg", "data-tone": tone || null, role: "status" }, text) : null;
const localOnly = () => local() ? null : h("p", { class: "field-h" }, "This can only be changed on this computer.");

export async function loadPage(update) {
  P.asked = true;
  try { P.page = await jget("/api/settings"); P.err = ""; } catch (e) { P.err = e.message; }
  update();
}
/** The page as it should look: what the Hive last said, plus any choice still on its way there. A re-render during a save would otherwise put the old choice back. */
function shown() {
  if (!P.want.length || !P.page || !P.page.settings) return P.page;
  const settings = P.want.reduce((v, d) => ({ ...v, ...d, share: { ...v.share, ...d.share } }), P.page.settings);
  return { ...P.page, settings };
}
export function pageData(update) {
  if (!P.asked && can("config")) loadPage(update);
  return P.page && P.page.settings ? shown() : null;
}
/** Just the accounts, on a Hive with no settings file (or for someone without config rights): from /api/accounts, which needs only logins and works from outside, where /api/settings is refused. */
export function accountsData(update) {
  if (!P.asked && can("logins")) { P.asked = true; loadAccounts(update); }
  return P.page && P.page.accounts ? P.page.accounts : null;
}
async function loadAccounts(update) {
  try { const accounts = await jget("/api/accounts"); P.page = { ...(P.page || { settings: null }), accounts }; P.err = ""; } catch (e) { P.err = e.message; }
  update();
}
/** After an account changes: the whole page where there is one, else just the accounts. */
const reloadAccounts = update => P.page && P.page.settings && can("config") ? loadPage(update) : loadAccounts(update);
/** A key or remove answer: the settings page, or {settings: null, accounts}. */
const takeAnswer = v => { if (v && v.settings) P.page = v; else if (v && v.accounts) P.page = { ...(P.page || { settings: null }), accounts: v.accounts }; };
export const reading = () => P.err ? sayMsg(P.err, "bad") : empty("Reading…", { kind: "loading" });
export const forget = () => { P.asked = false; P.key = ""; P.keyUrl = ""; P.keySay = null; P.codexSay = ""; P.saved = null; P.replacing = null; P.repKey = ""; };

// Which part of the page a save belongs to, and how the log words it.
function saying(d) {
  if (d.setup_done) return ["finish", "Finishing setup", "Setup finished"];
  if (d.share) { const [k, on] = Object.entries(d.share)[0], n = (SHARES.find(s => s[0] === k) || [k, k])[1]; return ["share", `Turning ${on ? "on" : "off"}: ${n}`, `${n}: ${on ? "on" : "off"}`]; }
  if (d.power) { const n = (POWER.find(x => x[0] === d.power) || [0, d.power])[1]; return ["power", `Setting the power level to ${n}`, `Power level: ${n}`]; }
  if (d.model) { const n = (MODELS.find(x => x[0] === d.model) || [0, d.model])[1]; return ["model", `Setting the model to ${n}`, `Model: ${n}`]; }
  if (d.sandbox) return ["sandbox", d.sandbox === "bare" ? "Turning the sandbox off" : "Turning the sandbox on", d.sandbox === "bare" ? "Sandbox off" : "Sandbox on"];
  return ["other", "Saving", "Done"];
}
/** Save settings; the answer is the whole page. Returns false if it was refused. */
export async function saveSettings(d, update) {
  const [part, doing, done] = saying(d);
  P.saved = null; P.saving[part] = (P.saving[part] || 0) + 1; P.want.push(d);
  try {
    if (await act(part === "finish" ? part : "save" + ++P.seq, doing, async () => { P.page = await post("/api/settings", d); P.err = ""; P.want.splice(P.want.indexOf(d), 1); await loadApp(); }, update, done) === false) return false;
    P.saved = part;
    const n = P.seq; setTimeout(() => { if (P.saved === part && P.seq === n) { P.saved = null; update(); } }, 4000);  // "Saved." is news, not a state
    return true;
  }
  catch (e) { P.err = e.message; return false; }
  finally { if (P.want.includes(d)) P.want.splice(P.want.indexOf(d), 1); P.saving[part]--; update(); }
}
const saveErr = () => P.err ? sayMsg(P.err, "bad") : null;
/** Under a choice that saves as you click: says it's going, then that it went. */
const saveLine = part => P.saving[part] ? sayMsg("Saving…") : P.saved === part ? sayMsg("Saved.", "good") : null;
export const finishing = () => isBusy("finish");

/** Whether an account other than a Claude login can run agents, as this page knows it. */
export const anyAccount = () => { const a = P.page && P.page.accounts, s = S.app.setup || {}; return !!(s.signed_in || s.api_key || s.other || (a && ((a.keys || []).length || a.codex.signed_in || (a.codex.logins || []).some(l => l.signed_in)))); };

// ---- accounts ----
function tellCodex(v, update) {
  if (v.state === P.codexSeen) return;
  const was = P.codexSeen; P.codexSeen = v.state;
  if (v.state === "starting") begin("codex", "Starting Codex's sign-in", update);
  else if (v.state === "waiting") { finish("codex", "ok", "Got a sign-in link and code from Codex", update); begin("codex", "Waiting for you to sign in to Codex", update); }
  else if (!was) return;  // an old result the page loaded with, not something happening now
  else if (v.state === "done") finish("codex", "ok", "Signed in to Codex", update);
  else if (v.state === "failed") finish("codex", "bad", "Codex's sign-in failed: " + (v.error || "no reason given"), update);
  else finish("codex", "info", "Codex's sign-in stopped", update);
}
function tickCodex(update) {
  if (P.ticking) return;
  P.ticking = true;
  const tick = async () => {
    let v; try { v = await jget("/api/signin-codex"); } catch (e) { return setTimeout(tick, 1500); }
    if (P.page && P.page.accounts) P.page.accounts.codex = v;
    tellCodex(v, update);
    update();
    if (v.state === "starting" || v.state === "waiting") return setTimeout(tick, 1500);
    P.ticking = false;
    await loadApp(); await reloadAccounts(update);
  };
  tick();
}
const codexName = n => n === "codex" ? "Codex" : "Codex " + n.slice(6);
export function codexPart(update) {
  const a = P.page.accounts, c = a.codex;
  const busy = c.state === "starting" || c.state === "waiting", here = c.login || "codex";
  const go = async body => {
    P.codexSay = ""; P.codexSeen = "starting"; begin("codex", body.add ? "Adding a Codex login" : "Starting Codex's sign-in", update);
    try { const v = await post("/api/signin-codex", body); a.codex = v; tellCodex(v, update); tickCodex(update); }
    catch (e) { P.codexSay = e.message; P.codexSeen = null; finish("codex", "bad", "Codex's sign-in didn't start: " + e.message, update); }
    update();
  };
  const stop = async () => { try { a.codex = await act("codex-stop", "Cancelling Codex's sign-in", () => post("/api/signin-codex", { cancel: true }), update, "Asked Codex's sign-in to stop"); tellCodex(a.codex, update); } catch (e) { P.codexSay = e.message; } update(); };
  const remove = n => confirm({ title: `Remove the login ${n}?`, body: ["It signs out of that ChatGPT account here; runs move to the other Codex logins."], ok: "Remove", kind: "danger", onOk: async () => {
    try { const v = await act("codex-rm-" + n, "Removing " + n, () => post("/api/signin-codex", { remove: n }), update, "Removed " + n); a.codex = v; await reloadAccounts(update); }
    catch (e) { P.codexSay = e.message; }
    update();
  } });
  if (busy) tickCodex(update);
  const logins = c.logins && c.logins.length ? c.logins : [{ name: "codex", signed_in: c.signed_in }];
  if (busy && !logins.some(l => l.name === here)) logins.push({ name: here, signed_in: false, extra: true });
  const row = l => {
    const mine = l.name === here, on = l.signed_in;
    return h("div", { class: "signin", "data-key": l.name },
      h("div", { class: "frow fsplit" }, h("b", null, codexName(l.name)), l.name === "codex" ? h("span", { class: "muted" }, "with a ChatGPT login") : h("span", { class: "muted mono" }, l.name),
        c.installed ? statusLabel(on ? "working" : "ended", on ? "signed in" : "not signed in") : statusLabel("ended", "not installed"),
        c.installed && can("logins") && !busy ? h("span", { class: "frow-end" },
          button(on ? "Sign in again" : "Sign in", () => go({ login: l.name }), { size: "s", busy: isBusy("codex") && mine }),
          l.name !== "codex" ? button("Remove", () => remove(l.name), { size: "s", kind: "quiet", busy: isBusy("codex-rm-" + l.name) }) : null) : null),
      !mine ? null : [
        c.state === "starting" ? sayMsg("Starting Codex's sign-in…") : null,
        c.state === "waiting" ? [
          h("ol", { class: "steps" },
            h("li", null, /^https:\/\//.test(c.url) ? h("a", { href: c.url, target: "_blank", rel: "noreferrer noopener" }, "Open the sign-in page") : "Codex didn't give a usable link.", " in any browser, and sign in", l.name === "codex" ? "." : " with the ChatGPT account for this login."),
            h("li", null, "Enter this code: ", h("b", { class: "mono", "data-key": "code" }, c.code), " ", copyButton(c.code), h("span", { class: "field-h" }, "It lasts 15 minutes."))),
          h("div", { class: "frow" }, button("Cancel", stop, { kind: "quiet", size: "s", busy: isBusy("codex-stop") }), h("span", { class: "meta" }, "Waiting for you to finish…"))] : null,
        c.state === "failed" ? sayMsg(c.error || "That didn't work.", "bad") : null,
        c.state === "done" ? sayMsg("Signed in to Codex.", "good") : null]);
  };
  const first = logins.find(l => l.name === "codex");
  return h("div", { class: "codex-logins" },
    logins.map(row),
    c.installed ? null : h("p", { class: "field-h" }, "To use it, install it in a terminal as this account: ", h("code", null, "npm install -g @openai/codex"), ". Then come back here."),
    c.installed && can("logins") && !busy && first && first.signed_in ? h("div", { class: "frow" },
      button("Add another Codex login", () => go({ add: true }), { size: "s", kind: "quiet" }),
      h("span", { class: "field-h" }, "Another ChatGPT account. When one runs out, runs move to the next with room.")) : null,
    sayMsg(P.codexSay, "bad"));
}
export function keysPart(update) {
  const a = P.page.accounts, kinds = a.kinds && a.kinds.length ? a.kinds : [{ kind: "anthropic", label: "Anthropic API key" }, { kind: "openrouter", label: "OpenRouter API key" }];
  if (!kinds.some(k => k.kind === P.keyKind)) P.keyKind = kinds[0].kind;
  const kind = kinds.find(k => k.kind === P.keyKind);
  const send = async (body, doing, done, say) => {
    P.keySay = null;
    try { await act("key", doing, async () => { takeAnswer(await post("/api/account-key", body)); await loadApp(); }, update, done); P.keySay = { text: say, tone: "good" }; return true; }
    catch (e) { P.keySay = { text: e.message, tone: "bad" }; return false; }
    finally { update(); }
  };
  const save = async () => {
    const value = P.key; P.key = "";  // gone from the page before the answer, whatever it is
    const body = { kind: P.keyKind, value }; if (kind.own_url) body.base_url = (P.keyUrl || "").trim();
    if (await send(body, "Saving the API key", "API key saved", "Saved. It can't be shown again, and it isn't checked until an agent uses it.")) P.keyUrl = "";
  };
  const replace = async k => { const value = P.repKey; P.repKey = ""; if (await send({ name: k.name, value }, "Replacing " + k.name, "Replaced " + k.name, "Replaced. The old key is gone.")) P.replacing = null; };
  const remove = k => confirm({ title: `Remove ${k.name} (${k.label})?`, ok: "Remove", kind: "danger", onOk: () => send({ name: k.name, delete: true }, "Removing " + k.name, "Removed " + k.name, "Removed.") });
  const keys = a.keys || [];
  const hint = a.settings_file
    ? "Used only when no Claude or Codex login has room. Every run costs money on that key, so set a spending limit with the provider. An OpenRouter key must have a limit on it, or Hive won't use it."
    : "A key starts nothing on its own: an agent uses it when a post names it, as --model NAME:MODEL. Every run costs money on that key, so set a spending limit with the provider.";
  return h("div", { class: "signin", "data-key": "keys" },
    h("div", { class: "frow fsplit" }, h("b", null, "API keys"), h("span", { class: "muted" }, "charged per use")),
    keys.length ? h("ul", { class: "list" }, keys.map(k => h("li", { class: "lr", "data-key": k.name },
      h("div", { class: "lr-in frow fsplit" }, h("b", { class: "mono" }, k.name), h("span", { class: "muted" }, k.label),
        h("span", { class: "meta" }, (k.ends ? "ends " + k.ends + ", " : "") + "saved " + ago(k.at * 1000)),
        can("logins") ? h("span", { class: "frow-end" },
          button("Replace", () => { P.replacing = P.replacing === k.name ? null : k.name; P.repKey = ""; update(); }, { size: "s", kind: "quiet" }),
          button("Remove", () => remove(k), { size: "s", kind: "quiet" })) : null),
      P.replacing === k.name ? h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (P.repKey.trim()) replace(k); } },
        h("div", { class: "frow-grow" }, field("New key for " + k.name, input({ type: "password", autocomplete: "new-password", spellcheck: "false", "data-key": "replace-key", value: P.repKey, oninput: (e, n) => { P.repKey = n.value; } }))),
        button("Save", null, { type: "submit", size: "s", busy: isBusy("key") })) : null))) : null,
    a.by_hand && a.by_hand.length ? h("p", { class: "field-h" }, "Also set up by hand in config.json: ", a.by_hand.map(b => b.name + (b.paused ? " (paused)" : "")).join(", "), ".") : null,
    can("logins") && S.app.secrets_on === false ? h("p", { class: "field-h" }, "Saving API keys is off on this name, as secrets are. Whoever runs the Hive can turn it on with secrets_on in web.json, on the machine.") :
    can("logins") ? [
      h("p", { class: "field-h" }, hint),
      seg(kinds.map(k => [k.kind, k.own_url ? "Other API" : k.label.replace(/ API key$/, "")]), P.keyKind, v => { P.keyKind = v; update(); }, { label: "Which provider" }),
      kind.own_url ? field("API address", input({ type: "url", placeholder: "https://api.example.com", spellcheck: "false", "data-key": "account-url", value: P.keyUrl || "", oninput: (e, n) => { P.keyUrl = n.value; } }), "An Anthropic-compatible address, such as https://api.deepseek.com/anthropic. The key is named after its site.") : null,
      h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (P.key.trim()) save(); } },
        h("div", { class: "frow-grow" }, field("Key", input({ type: "password", autocomplete: "new-password", spellcheck: "false", "data-key": "account-key", value: P.key, oninput: (e, n) => { P.key = n.value; } }))),
        button(keys.length ? "Add key" : "Save key", null, { type: "submit", busy: isBusy("key") }))] : null,
    P.keySay ? sayMsg(P.keySay.text, P.keySay.tone) : null);
}
export const rootNote = () => P.page.accounts.root ? h("p", { class: "fmsg", "data-tone": "warn" }, "This computer account can use sudo without a password, so the Hive won't run Codex or API-key models on it: an outside model must not share an account with root. Claude logins still work. Give the Hive an account without sudo to use the others.") : null;

// ---- data sharing ----
const SHARES = [
  ["failures", "Failure reports and a daily check-in", "What failed, when and why, and one small message a day saying this Hive is running and whether an agent has been started yet (yes or no)."],
  ["usage", "Usage reports", "Counts of runs, restarts and limit hits."],
  ["everything", "Everything, when I say so", "Separate from the reports above: a scrubbed copy of recent logs and of the board, and that includes your chats. Ticking this sends nothing: it goes only when you press Send a copy now, and each one is kept in reports/bundles in your Hive folder."],
  ["github", "Open GitHub issues about failures from my GitHub login. I'll review each fix before it's sent.", "Off by default. Failures go to the Hive project as public issues from your own GitHub account, at most 3 a day, one per kind of failure. When an agent drafts a fix it waits here: nothing of it goes until you read it and press Send."],
];
const GH_INSTALL = [["Linux (Debian, Ubuntu)", "sudo apt install gh"], ["Mac", "brew install gh"], ["Windows", "winget install --id GitHub.cli"]];
/** How to install gh on the machine the Hive runs on, then sign in. The Hive can't install it itself: it needs the machine's admin. */
const ghInstall = () => h("div", { class: "field-h", "data-key": "gh-install" },
  h("p", null, "To install it, run one of these in a terminal on the computer the Hive runs on, then run gh auth login there and reload this page. Other systems: ",
    h("a", { href: "https://cli.github.com", target: "_blank", rel: "noopener" }, "cli.github.com"), "."),
  h("ul", { class: "list" }, GH_INSTALL.map(([os, cmd]) => h("li", null, os + ": ", h("code", null, cmd), " ", copyButton(cmd)))));
/** Where GitHub issues stand here: no gh, not signed in, or ready. Null when the report module can't say. */
function ghLine(g) {
  if (!g) return null;
  if (!g.gh) return [sayMsg("GitHub's command line tool, gh, isn't installed, so failures go to the makers' receiver instead.", "warn"), ghInstall()];
  if (!g.ready) return sayMsg("gh isn't signed in to GitHub, so failures go to the makers' receiver instead. Run gh auth login in a terminal to sign in.", "warn");
  return sayMsg("gh is signed in. Issues go to " + (g.repo || "the Hive project") + ".", "good");
}
function draftDo(id, act, update) {
  if (act !== "send") return confirm({ title: "Discard this fix?", body: ["It's deleted and nothing is sent."], ok: "Discard", kind: "danger", onOk: () => draftRun(id, act, update) });
  confirm({ title: "Send this fix to GitHub?", body: ["It goes from your account, exactly as shown, and it will be public."], ok: "Send", onOk: () => draftRun(id, act, update) });
}
async function draftRun(id, act, update) {
  P.draftBusy = id; P.draftSay = null; update();
  try { P.page = await post("/api/draft", { id, do: act }); P.draftSay = { text: act === "send" ? "Approved. It goes on the next report run, within a few hours." : "Discarded. Nothing was sent.", tone: "good" }; }
  catch (e) { P.draftSay = { text: e.message, tone: "warn" }; }
  finally { P.draftBusy = null; update(); }
}
/** Every fix an agent drafted, exactly as it would go: title, text and diff. Only Send here approves one. */
function draftsPart(g, update) {
  const ds = (g && g.drafts) || [];
  if (!ds.length) return null;
  return h("div", { class: "drafts", "data-key": "drafts" },
    h("p", null, ds.length === 1 ? "An agent drafted a fix. Read it: this is exactly what would go." : `Agents drafted ${ds.length} fixes. Read each: this is exactly what would go.`),
    ds.map(d => h("div", { class: "card draft", "data-key": "draft-" + d.id },
      h("p", null, h("b", null, d.title)),
      h("p", { class: "field-h" }, (d.how === "pull request" ? "Goes as a pull request from your fork" : "Goes as an issue" + (d.patch ? " with the patch" : "")) + (d.repo ? " on " + d.repo : "") + (d.at ? " · drafted " + d.at : "") + (d.approved ? " · approved, waiting to go" : "")),
      h("pre", { class: "draft-text" }, d.text),
      d.patch ? h("pre", { class: "draft-diff" }, d.patch) : null,
      local() ? h("div", { class: "frow" },
        d.approved ? null : button("Send", () => draftDo(d.id, "send", update), { size: "s", busy: P.draftBusy === d.id, disabled: !shown().settings.share.github }),
        button("Discard", () => draftDo(d.id, "discard", update), { kind: "quiet", size: "s" })) : null)));
}
function sendCopy(update) {
  confirm({ title: "Send the makers a copy?", body: ["A scrubbed copy of your recent logs and board, chats included. They keep it for 30 days."], ok: "Send", onOk: () => sendCopyRun(update) });
}
async function sendCopyRun(update) {
  P.copyBusy = true; P.copySay = null; update();
  try { await post("/api/share-copy", {}); P.copySay = { text: "Sent. A copy of what went is in reports/bundles in your Hive folder.", tone: "good" }; }
  catch (e) { P.copySay = { text: e.message, tone: "warn" }; }
  finally { P.copyBusy = false; update(); }
}
export function sharePart(update, inSettings = false) {
  const pg = shown(), on = pg.settings.share;
  return [
    pg.share_text ? h("div", { class: "share-text", "data-key": "share-text" }, pg.share_text.split(/\n+/).map(t => h("p", null, t)))
      : h("p", { "data-key": "share-text" }, "The report module isn't installed here, so nothing can be sent, whatever is ticked."),
    h("div", { class: "checks cards", role: "group", "aria-label": "What to share" }, SHARES.map(([k, label, hint]) => h("label", { class: "check", "data-key": k },
      h("input", { type: "checkbox", checked: on[k], disabled: !local(), onchange: (e, n) => saveSettings({ share: { [k]: n.checked } }, update) }),
      h("span", null, label, h("span", { class: "field-h" }, hint))))),
    on.everything && local() ? h("div", { class: "frow", "data-key": "send-copy" }, button("Send a copy now", () => sendCopy(update), { kind: "quiet", size: "s", busy: P.copyBusy })) : null,
    P.copySay ? sayMsg(P.copySay.text, P.copySay.tone) : null,
    on.github ? ghLine(pg.github) : null,
    draftsPart(pg.github, update),
    P.draftSay ? sayMsg(P.draftSay.text, P.draftSay.tone) : null,
    inSettings ? null : h("p", { class: "field-h" }, "Nothing is sent until you finish setup. You can change any of this in Settings at any time."),
    saveLine("share"), localOnly(), saveErr()];
}

// ---- the model ----
export function modelPart(update) {
  const cur = shown().settings.model;
  return [
    h("p", null, "The Claude model an agent runs on, unless you ask for another in a message by writing --model and its name, like --model sonnet."),
    h("div", { class: "checks cards", role: "radiogroup", "aria-label": "Which model agents use" }, MODELS.map(([m, label, hint]) => h("label", { class: "check", "data-key": "model-" + m },
      h("input", { type: "radio", name: "model", checked: cur === m, disabled: !local(), onchange: () => saveSettings({ model: m }, update) }),
      h("span", null, label, h("span", { class: "field-h" }, hint))))),
    h("p", { class: "field-h" }, "An agent changes the next time it starts or wakes. One already working finishes on the model it has. If agents fail to start after a change, your plan may not include that model, so choose another."),
    saveLine("model"), localOnly(), saveErr()];
}

// ---- power and the sandbox ----
/** "enforced": the sandbox is on and its own check says it works here. Anything else, the levels are not a limit and the page says so. */
const enforced = pg => pg.settings.sandbox === "on" && pg.sandbox_check.state === "works";
export function powerPart(update) {
  const pg = shown(), cur = pg.settings.power, a = pg.accounts, claude = a.claude.some(l => l.signed_in);
  return [
    h("p", null, enforced(pg) ? "Agents run in a sandbox on this machine. This sets who decides when one needs a step outside it, like installing something or changing a file elsewhere on this computer."
      : "This sets who decides when an agent wants to do something bigger. Read the sandbox note below: without a working sandbox, this is an instruction to the agents, and nothing on this machine holds them to it."),
    claude ? null : h("p", { class: "fmsg", "data-tone": "warn", "data-key": "no-warden" }, "Wardens run on Claude, and this Hive has no Claude login signed in. So there's no warden: every step comes to you, whichever level is set."),
    h("div", { class: "checks cards", role: "radiogroup", "aria-label": "How much power the Hive has" }, POWER.map(([n, label, hint, loose]) => h("label", { class: "check", "data-key": "power-" + n },
      h("input", { type: "radio", name: "power", checked: cur === n, disabled: !local(), onchange: () => saveSettings({ power: n }, update) }),
      h("span", null, `${n}. ${label}`, h("span", { class: "field-h" }, (!enforced(pg) && loose) || hint))))),
    saveLine("power"),
    h("p", { "data-key": "always" }, h("b", null, "At every level, these stay your call: "), ALWAYS.join(", ") + "."),
    h("p", { "data-key": "approve-word" }, "When a step comes to you, you answer in the conversation with approve step and the step's code. The code isn't a password and doesn't say what the step does, so read the step first. It ties your answer to that exact script, only you can give it, and it works once."),
    localOnly(), saveErr()];
}
export function sandboxPart(update) {
  const pg = shown(), c = pg.sandbox_check, bare = pg.settings.sandbox === "bare";
  const leave = () => confirm({ title: "Run without the sandbox?", body: [BARE], ok: "Run without it", kind: "danger", onOk: () => saveSettings({ sandbox: "bare" }, update) });
  const back = () => saveSettings({ sandbox: "on" }, update);
  const leaveBtn = () => local() ? h("div", { class: "frow" }, button("Run without the sandbox", leave, { kind: "quiet", size: "s", busy: P.saving.sandbox > 0 })) : null;
  let body;
  if (bare) body = [h("p", null, statusLabel("limited", "Off: you chose to run without the sandbox.")), h("p", null, BARE), h("p", null, "The levels above are only instructions here. Nothing on this machine enforces them."),
    local() ? h("div", { class: "frow" }, button("Turn the sandbox back on", back, { size: "s", busy: P.saving.sandbox > 0 })) : null];
  else if (c.state === "works") body = [h("p", null, statusLabel("working", "The sandbox works on this machine.")),
    h("p", null, "Each agent runs shut in. It can't reach this machine's services or your private network, and your other logins, ~/.ssh and the rest of your home aren't there. One thing it can read: the sign-in of the login it runs on."),
    leaveBtn()];
  else if (c.state === "broken") body = [h("p", null, statusLabel("failed", "The sandbox doesn't work on this machine.")), c.why ? h("p", null, c.why) : null, c.fix ? h("p", null, h("b", null, "What you can do: "), c.fix) : null,
    h("p", null, "Agents won't start until it works, or until you choose to run without it. Without it, nothing on this machine enforces the levels above."), leaveBtn()];
  else body = [h("p", null, statusLabel("ended", "This app couldn't find out whether the sandbox works here.")),
    h("p", null, "So don't count on the levels above as a limit: nothing here confirms that this machine enforces them."), leaveBtn()];
  return [h("div", { "data-key": "sandbox", "data-state": bare ? "bare" : c.state }, body), saveLine("sandbox"), localOnly(), saveErr()];
}
/** What the Secrets box can truthfully say about who reads the file, given the sandbox. */
export function secretsReach() {
  const pg = shown();
  if (!pg || !pg.settings || pg.settings.sandbox === "bare") return "Every agent on this account can read the file, so use tokens you can rotate.";
  return "An agent in the sandbox can't open the file. A step that runs outside the sandbox can, and that step goes through the power level you set. Use tokens you can rotate.";
}
