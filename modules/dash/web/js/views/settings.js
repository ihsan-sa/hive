// Settings: the hive, logins, modules, access from outside. Shown only where can() allows each part.
import { h } from "../core/dom.js";
import { S, can, built, jget, post, loadApp } from "../core/store.js";
import { form } from "../templates.js";
import { pageHeader, section, block, blocks, button, field, input, empty, statusLabel } from "../ui/kit.js";
import { act, isBusy } from "../ui/activity.js";
import { startSignin, signinBox, clearSignin } from "../ui/signin.js";
import { plural } from "../core/format.js";
import { href } from "../core/router.js";
import { copyButton } from "../ui/refs.js";
import { POWER, codexPart, forget, keysPart, loadPage, pageData, powerPart, reading, rootNote, sandboxPart, sharePart } from "../ui/setup.js";
import { forgetSecrets, secretParts } from "./secrets.js";

// Everything the person is typing lives here, so a poll never loses it.
const F = { cfg: null, ed: null, mods: null, say: {}, newLogin: "", url: "", busy: {}, access: null };
const say = (k, text, tone) => { F.say[k] = text ? { text, tone } : null; };
const msg = k => F.say[k] ? h("p", { class: "fmsg", "data-tone": F.say[k].tone || null, role: "status" }, F.say[k].text) : null;
const CAPTEXT = { read: "Read everything", files: "Read files: notes, modules, scripts, projects", post: "Post as you, which starts agents", stop: "Kill agents", tier: "Change a thread's tier", limits: "Pause logins and change stop lines", logins: "Sign in and add logins", config: "Change settings", modules: "Add and remove modules", plans: "Edit project plans", secrets: "Store secrets" };

async function loadCfg(ctx) {
  if (!can("config")) return;
  try { F.cfg = await jget("/api/config"); } catch (e) { F.cfg = {}; }
  F.ed ||= { max: F.cfg.max_sessions ?? "", kp: !!F.cfg.keeper_every_s, au: F.cfg.auto_update !== false };
  F.saved ||= { max: F.ed.max };
  ctx.update();
}
async function loadMods(ctx) {
  if (!can("modules")) return;
  try { F.mods = await jget("/api/modules"); } catch (e) { F.mods = F.mods || []; }
  ctx.update();
}

const check = (label, on, onChange, hint) => h("label", { class: "check" }, h("input", { type: "checkbox", checked: on, onchange: (e, n) => onChange(n.checked) }), h("span", null, label, hint ? h("span", { class: "field-h" }, hint) : null));

function hiveSection(ctx) {
  if (!can("config")) return null;
  if (!F.ed) return section({ title: "The hive", level: 1 }, empty("Reading…", { kind: "loading" }));
  const E = F.ed, up = S.app.setup && S.app.setup.starter;
  // Each control saves on its own and sends only its own field; a refusal puts the old value back.
  const save = async (fields, undo) => {
    try { await post("/api/config", fields); say("hive", "Saved. The hive uses it within seconds.", "good"); }
    catch (e) { undo(); say("hive", e.message, "bad"); }
    ctx.update();
  };
  const toggle = (label, on, onChange) => h("input", { type: "checkbox", "aria-label": label, checked: on, onchange: (e, n) => onChange(n.checked) });
  const saveMax = () => {
    const was = F.saved.max, v = String(E.max).trim();
    if (v === String(was)) return;
    if (v !== "" && !(Number.isInteger(+v) && +v >= 1 && +v <= 200)) { E.max = was; say("hive", "Agents at once is a whole number from 1 to 200, or empty for no limit.", "bad"); ctx.update(); return; }
    F.saved.max = v;
    save({ max_sessions: v === "" ? null : +v }, () => { E.max = F.saved.max = was; });
  };
  const starter = async () => {
    say("starter", up ? "Stopping…" : "Starting…");
    try {
      await act("starter", up ? "Stopping the hive" : "Starting the hive", async () => {
        await post("/api/starter", { do: up ? "stop" : "start" });
        await new Promise(r => setTimeout(r, up ? 500 : 4000));
        await loadApp();
      }, ctx.update);
      const now = S.app.setup && S.app.setup.starter;
      say("starter", now === up ? (up ? "It hasn't stopped yet." : "It still isn't running. Try once more.") : "", now === up ? "bad" : null);
    } catch (e) { say("starter", e.message, "bad"); }
    ctx.update();
  };
  return section({ title: "The hive", level: 1 },
    blocks(
      block({ title: "Status", sub: up ? "Stopping lets agents finish their turn." : null, state: statusLabel(up ? "working" : "ended", up ? "running" : "not running"), actions: button(up ? "Stop the hive" : "Start the hive", starter, { size: "s", busy: isBusy("starter") }) }),
      block({ title: "Keep work moving", sub: "Every 3 hours, pick up work that stopped and start work nobody asked for. It uses your limits.",
        actions: toggle("Keep work moving", E.kp, v => { E.kp = v; save({ keeper_every_s: v ? (F.cfg.keeper_every_s || 10800) : 0 }, () => { E.kp = !v; }); }) }),
      block({ title: "Update by itself", sub: "Installs each new release. Keeps your settings and goes back if it fails.",
        actions: toggle("Update by itself", E.au, v => { E.au = v; save({ auto_update: v }, () => { E.au = !v; }); }) }),
      block({ title: "Agents at once", sub: "Empty means no limit.",
        actions: input({ type: "number", min: 1, max: 200, placeholder: "no limit", "aria-label": "Agents at once", style: "width:7rem", value: E.max, oninput: (e, n) => { E.max = n.value; }, onchange: saveMax }) })),
    msg("starter"), msg("hive"));
}

// Codex and API keys, with settings.json only: same section as Claude, because to the person they're all accounts.
function otherAccounts(ctx) {
  if (!S.app.settings) return null;
  const pg = pageData(ctx.update);
  return pg ? [rootNote(), codexPart(ctx.update), keysPart(ctx.update)] : reading();
}

function loginsSection(ctx) {
  if (!can("logins")) return null;
  const logins = (S.app.setup && S.app.setup.logins) || [], yes = logins.filter(l => l.signed_in).length, out = logins.length - yes;
  const state = !logins.length ? statusLabel("off", "none yet") : out ? statusLabel("out", `${out} not signed in`) : statusLabel("working", "signed in");
  return section({ title: "Accounts", level: 1 },
    blocks(block({ title: "Claude", sub: logins.length ? (out ? `${yes} of ${plural(logins.length, "account")}` : plural(logins.length, "account")) : "a subscription runs the agents", state, key: "claude", fold: true, open: F.acctOpen ?? (out > 0 || !logins.length), onToggle: o => { F.acctOpen = o; } },
      h("p", { class: "field-h" }, "What each has left, pausing it and where it stops are under ", h("a", { href: href("usage") }, "Usage"), "."),
      logins.length ? h("ul", { class: "list" }, logins.map(l => h("li", { class: "lr", "data-key": l.name },
        h("div", { class: "lr-in frow fsplit" }, h("b", null, l.name), l.signed_in ? null : statusLabel("out"),
          h("span", { style: "margin-left:auto" }, button(l.signed_in ? "Sign in again" : "Sign in", () => startSignin("login-" + l.name, l.name, false, ctx.update), { size: "s", kind: l.signed_in ? "quiet" : null }))),
        signinBox("login-" + l.name, ctx.update)))) : null,
      h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.newLogin.trim()) startSignin("login-new", F.newLogin.trim(), false, ctx.update); } },
        h("div", { style: "flex:1;min-width:12rem;max-width:20rem" }, field("Add another account", input({ placeholder: "a name for it, like work", value: F.newLogin, oninput: (e, n) => { F.newLogin = n.value; } }))),
        button("Sign in to it", null, { type: "submit" })),
      h("p", { class: "field-h" }, "A second subscription gives the hive more room: new agents go to whichever has the most left. Adding one signs in, checks it isn't one you already have, and makes one short test run."),
      signinBox("login-new", ctx.update))),
    otherAccounts(ctx));
}

// ---- with settings.json only: the choices the first run made, to change here ----
function setupSections(ctx) {
  if (!S.app.settings) return [];
  const pg = pageData(ctx.update);
  if (!can("config")) return [];
  return [
    section({ title: "Data sharing", level: 1, note: "what's sent, and whether it is" }, pg ? sharePart(ctx.update) : reading()),
    section({ title: "Power", level: 1, note: "who decides what agents may do" }, pg ? powerPart(ctx.update) : reading()),
    section({ title: "Sandbox", level: 1 }, pg ? sandboxPart(ctx.update) : reading())];
}
const secretsSection = ctx => S.app.settings && can("secrets") ? section({ title: "Secrets", level: 1, note: "send a token to the hive; it's never shown back" }, secretParts(ctx)) : null;

function modulesSection(ctx) {
  if (!can("modules")) return null;
  const act = async (what, name) => {
    F.busy[name] = true; say("mods", (what === "add" ? "Adding " : "Removing ") + name + "…"); ctx.update();
    try { const r = await post("/api/module", { do: what, name }); say("mods", r.said); } catch (e) { say("mods", e.message, "bad"); }
    delete F.busy[name]; await loadMods(ctx);
  };
  return section({ title: "Extras", level: 1, note: "optional tools your agents can use (modules)" },
    F.mods == null ? empty("Reading…", { kind: "loading" }) : F.mods.length ? h("ul", { class: "list" }, F.mods.map(x => h("li", { class: "lr", "data-key": x.name },
      h("div", { class: "lr-in mod-row" }, h("b", { class: "mod-name" }, x.name), h("span", { class: "muted mod-about" }, x.about.replace(/`/g, ""), x.needs ? ` Needs ${x.needs}.` : ""),
        x.name === "dash" ? h("span", { class: "meta" }, "this app") : x.name === "slack-door" && x.state !== "installed" && built("slack-door") ? button(F.slack ? "Close" : "Set up", () => { F.slack = F.slack ? null : { step: 1, bot: "", app: "", channel: "#hive" }; ctx.update(); }, { size: "s", kind: F.slack ? "quiet" : null }) : button(x.state === "installed" ? "Remove" : "Add", () => act(x.state === "installed" ? "remove" : "add", x.name), { size: "s", busy: F.busy[x.name] }))))) : empty("No modules listed."),
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.url.trim()) act("add", F.url.trim()); } },
      h("div", { style: "flex:1;min-width:12rem" }, field("Add from a Git URL", input({ placeholder: "Git URL of a module", value: F.url, oninput: (e, n) => { F.url = n.value; } }))),
      button("Add", null, { type: "submit", busy: F.busy[F.url.trim()] })),
    F.slack ? slackSetup(ctx) : null,
    msg("mods"));
}

// ---- The Slack door, set up from here instead of a file. Not built yet: POST /api/slack-door {bot_token, app_token, channel}. ----
const MANIFEST = `display_information:
  name: Hive
features:
  bot_user:
    display_name: Hive
oauth_config:
  scopes:
    bot: [channels:history, channels:read, chat:write, files:read, files:write, groups:history]
settings:
  event_subscriptions:
    bot_events: [message.channels, message.groups]
  socket_mode_enabled: true`;
function slackSetup(ctx) {
  const Z = F.slack, step = (n, title, ...body) => h("li", { class: "sl-step", "data-on": Z.step === n ? "" : null, "data-done": Z.step > n ? "" : null },
    h("b", null, title), Z.step === n ? body : Z.step > n ? h("span", { class: "meta" }, "done") : null);
  const connect = async () => {
    try { await post("/api/slack-door", { bot_token: Z.bot.trim(), app_token: Z.app.trim(), channel: Z.channel.trim() }); say("slack", `Connected. Post in ${Z.channel} to test it: an agent answers there.`, "good"); Z.step = 4; }
    catch (e) { say("slack", e.message, "bad"); }
    ctx.update();
  };
  return h("div", { class: "signin slk", "data-key": "slack" },
    h("p", null, h("b", null, "Reach the hive from Slack"), " ", h("span", { class: "nb" }, "not built yet")),
    h("p", { class: "field-h" }, "One channel is one conversation. You make a small Slack app of your own, so messages go only between your Slack and this computer."),
    h("ol", { class: "sl-steps" },
      step(1, "Make the Slack app", h("p", { class: "field-h" }, "On Slack's page, choose From a manifest, pick your workspace, and paste this:"),
        h("div", { class: "sl-man" }, h("pre", null, MANIFEST), copyButton(MANIFEST)),
        h("div", { class: "frow" }, h("a", { class: "btn", href: "https://api.slack.com/apps?new_app=1", target: "_blank", rel: "noopener noreferrer" }, "Open Slack's app page"), button("Done", () => { Z.step = 2; ctx.update(); }, { kind: "primary" }))),
      step(2, "Paste its two keys", h("p", { class: "field-h" }, "Install the app to your workspace. Then copy the Bot token (OAuth & Permissions, starts xoxb-) and make an App token with connections:write (Basic Information, starts xapp-)."),
        h("div", { class: "fgrid" },
          field("Bot token", input({ placeholder: "xoxb-…", autocomplete: "off", spellcheck: "false", value: Z.bot, oninput: (e, n) => { Z.bot = n.value; } })),
          field("App token", input({ placeholder: "xapp-…", autocomplete: "off", spellcheck: "false", value: Z.app, oninput: (e, n) => { Z.app = n.value; } }))),
        h("div", { class: "frow" }, button("Next", () => { if (/^xoxb-/.test(Z.bot.trim()) && /^xapp-/.test(Z.app.trim())) { say("slack", ""); Z.step = 3; } else say("slack", "The bot token starts xoxb- and the app token xapp-.", "bad"); ctx.update(); }, { kind: "primary" }))),
      step(3, "Pick the channel", field("Channel", input({ value: Z.channel, oninput: (e, n) => { Z.channel = n.value; } }), "Invite the app to it in Slack first: /invite @Hive"),
        h("div", { class: "frow" }, button("Connect", connect, { kind: "primary" }), button("Back", () => { Z.step = 2; ctx.update(); }, { kind: "quiet" })))),
    msg("slack"));
}

function accessSection(ctx) {
  const o = S.app.route === "local" && S.app.outside;
  if (!o) return null;
  const who = mode => Object.keys(o.people || {}).filter(k => o.people[k] === mode).join(" ");
  const A = F.access ||= { can: new Set(o.can), hosts: o.hosts.join(" "), team: o.team || "", aud: o.aud || "", emails: o.emails.join(" "), full: who("full"), limited: who("limited") };
  const words = s => s.split(/\s+/).filter(Boolean);
  const save = async () => {
    const people = {};
    for (const a of words(A.limited)) people[a] = "limited";
    for (const a of words(A.full)) people[a] = people[a] ? "both" : "full";
    const both = Object.keys(people).find(a => people[a] === "both");
    // Lists stay absent until someone is named, so a Hive without them keeps the ticks. Emptied later, they let nobody in: never back to anyone Access passes.
    const named = Object.keys(people).length || Object.keys(o.people || {}).length;
    try {
      if (both) throw new Error(`${both} is on both lists. Keep it on one.`);
      say("access", "");
      await act("access", "Saving who can reach this from outside", () => post("/api/access", { can: o.caps.filter(c => A.can.has(c)), hosts: words(A.hosts), access: { team: A.team, aud: A.aud, emails: words(A.emails) }, ...(named ? { people } : {}) }), ctx.update);
      say("access", "Saved.", "good");
    }
    catch (e) { say("access", e.message, "bad"); }
    ctx.update();
  };
  const txt = (label, k, ph) => field(label, input({ placeholder: ph, value: A[k], oninput: (e, n) => { A[k] = n.value; } }));
  const sites = Object.keys(o.sites || {}), names = [...sites, ...o.hosts.filter(x => !sites.includes(x))];
  return section({ title: "Access from outside", level: 1, note: "only changeable from this machine" },
    h("p", null, statusLabel(names.length ? "working" : "off", names.length ? "Set up" : "Off"), " ", names.length ? "These names can reach this Hive from the internet, through Cloudflare, once their tunnel is up:" : "Only this computer can open this Hive. You need this only to reach it from a phone or another machine."),
    names.length ? h("ul", { class: "plain", "data-key": "access-names" }, names.map(n => h("li", { "data-key": n }, h("b", { class: "mono" }, n), sites.includes(n) ? h("span", { class: "muted" }, " has its own sign-in rules, kept in ~/.hive/web.json. The form below doesn't change them.") : null))) : null,
    blocks(block({ title: "How access works", key: "access-how", fold: true, open: F.accessHow, onToggle: o => { F.accessHow = o; } },
      h("p", { class: "field-h" }, "Opening a name takes a Cloudflare tunnel and an Access policy, set up in Cloudflare. If you name people below, each gets limited (read, chat, kill agents) or full (everything here, for an hour after signing in), and the ticks don't apply. Anyone else is refused, and emptying both lists afterwards refuses everyone. Type addresses here yourself: an agent's tools can refuse your account's address. From this machine, the secret gets everything. Through a tunnel, each request must carry a token signed by Cloudflare Access, and it can do only what's ticked here.")),
      block({ title: o.hosts.length || o.team ? "Change the settings" : "Set it up", key: "access-edit", fold: true, open: F.accessOpen ?? !!(o.hosts.length || o.team || Object.keys(o.people || {}).length), onToggle: v => { F.accessOpen = v; } },
        h("div", { class: "checks", role: "group", "aria-label": "What outside requests may do" }, o.caps.map(c => check(CAPTEXT[c] || c, A.can.has(c), v => { v ? A.can.add(c) : A.can.delete(c); }))),
        h("div", { class: "fgrid" }, txt("Hostnames", "hosts", "hive.example.com"), txt("Access team domain", "team", "yourteam.cloudflareaccess.com"),
          txt("Application audience", "aud", "the Access application's AUD tag"), txt("Allowed emails", "emails", "optional: only these emails"),
          txt("People with full access", "full", "you@example.com"), txt("People with limited access", "limited", "friend@example.com")),
        h("div", { class: "frow" }, button("Save", save, { kind: "primary", busy: isBusy("access") }), msg("access")))));
}

// The state of the whole page, up top, so you needn't scroll to learn it. Each one jumps to its section.
const jump = title => document.querySelector(`.set .sec[aria-label="${title}"]`)?.scrollIntoView({ behavior: "smooth", block: "start" });
function glance(ctx) {
  const s = S.app.setup || {}, logins = s.logins || [], pg = S.app.settings && can("config") ? pageData(ctx.update) : null, o = S.app.route === "local" && S.app.outside;
  const rows = [];
  const row = (title, label, tone, value) => rows.push(h("button", { type: "button", class: "set-g", "data-key": label, onclick: () => jump(title) }, h("span", { class: "set-gl" }, label), statusLabel(tone, value)));
  if (can("config")) row("The hive", "Hive", s.starter ? "working" : "ended", s.starter ? "running" : "not running");
  if (can("logins") && logins.length) row("Accounts", "Claude", logins.some(l => !l.signed_in) ? "out" : "working", `${logins.filter(l => l.signed_in).length} of ${logins.length} signed in`);
  if (pg) {
    row("Power", "Power", "off", (POWER.find(x => x[0] === pg.settings.power) || [0, "level " + pg.settings.power])[1]);
    const c = pg.sandbox_check.state;
    row("Sandbox", "Sandbox", pg.settings.sandbox === "bare" ? "limited" : c === "works" ? "working" : c === "broken" ? "failed" : "ended", pg.settings.sandbox === "bare" ? "off" : c === "works" ? "on" : c === "broken" ? "not working" : "unknown");
  }
  if (o) { const n = Object.keys(o.sites || {}).length + o.hosts.length; row("Access from outside", "From outside", n ? "working" : "off", n ? "set up" : "off"); }
  return rows.length ? h("div", { class: "set-gs", "data-key": "glance" }, rows) : null;
}

export default {
  id: "settings", label: "Settings", about: "accounts, extras, access", tab: () => S.app.app && (can("config") || can("modules") || can("logins")),
  enter(ctx) { F.ed = null; F.saved = null; F.access = null; F.acctOpen = null; F.accessOpen = null; F.say = {}; clearSignin(); forget(); loadCfg(ctx); loadMods(ctx); if (S.app.settings && can("config")) loadPage(ctx.update); },
  leave() { forget(); forgetSecrets(); },
  render(ctx) {
    if (!S.app.app) return h("div", { class: "page" }, empty("Settings show only in the app, opened from this machine.", { kind: "page", title: "Settings" }));
    const secs = [hiveSection(ctx), loginsSection(ctx), ...setupSections(ctx), secretsSection(ctx), modulesSection(ctx), accessSection(ctx)].filter(Boolean);
    const header = pageHeader({ title: "Settings", lead: "How the hive runs, which accounts it uses, and what extras it has." });
    if (!secs.length) return form({ header, sections: [empty("This connection can't change any settings.")] });
    const nav = secs.map(n => n.getAttribute("aria-label"));
    return h("div", { class: "page set", "data-key": "settings" }, header, glance(ctx),
      h("div", { class: "set-b" },
        h("nav", { class: "set-n", "aria-label": "Settings sections", "data-key": "nav" }, nav.map(t => h("button", { type: "button", "data-key": t, onclick: () => jump(t) }, t))),
        h("div", { class: "set-s" }, secs)));
  },
};
