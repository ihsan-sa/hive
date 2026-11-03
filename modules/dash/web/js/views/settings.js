// Settings: the hive, logins, modules, access from outside. Shown only where can() allows each part.
import { h } from "../core/dom.js";
import { S, can, built, jget, post, loadApp } from "../core/store.js";
import { simpleOn, setSimple } from "../core/simple.js";
import { form } from "../templates.js";
import { pageHeader, section, block, blocks, button, field, input, empty, statusLabel } from "../ui/kit.js";
import { act, isBusy } from "../ui/activity.js";
import { startSignin, signinBox, clearSignin } from "../ui/signin.js";
import { ago, plural } from "../core/format.js";
import { href } from "../core/router.js";
import { copyButton } from "../ui/refs.js";
import { MODELS, POWER, codexPart, forget, keysPart, loadPage, modelPart, pageData, powerPart, reading, rootNote, sandboxPart, sharePart } from "../ui/setup.js";
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
async function loadUpdate(ctx) {
  if (!can("config")) return;
  try { F.up = await jget("/api/update"); } catch (e) { return false; }   // an update restarts the app, so a poll can miss
  ctx.update(); return true;
}
const EVERY = [[300, "Every 5 minutes"], [900, "Every 15 minutes"], [3600, "Every hour"], [21600, "Every 6 hours"], [86400, "Every day"], [604800, "Every week"]];
const pause = ms => new Promise(r => setTimeout(r, ms));
async function checkNow(ctx) {
  say("up", "");
  try { await act("up-check", "Looking for a release", async () => { F.up = await post("/api/update", { do: "check" }); }, ctx.update); }
  catch (e) { say("up", e.message, "bad"); }
  ctx.update();
}
/** Update now: it runs in its own service, and restarts this app on the way, so wait it out by asking until it says it's over. */
async function updateNow(ctx) {
  const from = F.up.tag;
  say("up", "Updating. This can take a few minutes, and this page may reconnect.");
  try {
    await act("up-install", "Updating", async () => {
      F.up = await post("/api/update", { do: "install" });
      let began = F.up.running;
      for (const t0 = Date.now(); Date.now() - t0 < 30 * 60e3; await pause(2000)) {
        const read = await loadUpdate(ctx);
        if (read && F.up.running) began = true;
        if (read && !F.up.running && (began || Date.now() - t0 > 20e3)) break;   // not begun 20 s on: whatever it left behind says why
      }
    }, ctx.update);
    if (F.up.error) say("up", "It didn't update: " + F.up.error, "bad");
    else say("up", F.up.tag !== from ? `Updated to ${F.up.tag}.` : "It's still running in the background, or it found nothing to do.", F.up.tag !== from ? "good" : null);
  } catch (e) { say("up", e.message, "bad"); }
  ctx.update();
}
/** The installed release, Check now, Update now, and how often to look. Only a Hive installed from a release has these. */
function versionBlocks(ctx, save) {
  const U = F.up;
  if (!U || !U.installed) return null;
  const busy = isBusy("up-check") || isBusy("up-install");
  const seen = U.checked ? `Looked for a release ${ago(U.checked * 1000)}.` : "It hasn't looked for a release yet.";
  const news = U.running ? "Updating now." : U.newest ? `${U.newest} is available.` : U.checked && !U.failed.length ? "This is the newest." : "";
  const every = U.update_every_s, opts = EVERY.some(o => o[0] === every) ? EVERY : [...EVERY, [every, `Every ${plural(Math.round(every / 60), "minute")}`]];
  return [
    block({ title: "Version", sub: `${seen} ${news}`, state: h("b", { "data-up": "tag" }, U.tag || "before the first release"),
      actions: [button("Check now", () => checkNow(ctx), { size: "s", busy: isBusy("up-check"), disabled: busy || U.running }),
        U.newest ? button("Update now", () => updateNow(ctx), { size: "s", kind: "primary", busy: isBusy("up-install"), disabled: busy || U.running }) : null] },
      U.failed.length ? h("p", { class: "meta" }, `Skipped because updating to it failed: ${U.failed.join(", ")}.`) : null,
      U.error && !F.say.up ? h("p", { class: "fmsg", "data-tone": "bad" }, "Last try: " + U.error) : null,
      msg("up")),
    block({ title: "Look for new releases", sub: "How often this Hive looks. It installs what it finds if Update by itself is on.",
      actions: h("select", { class: "input", value: every, "aria-label": "Look for new releases", title: "Look for new releases", style: "width:auto", onchange: e => { const was = U.update_every_s, v = +e.target.value; U.update_every_s = v; save({ update_every_s: v }, () => { U.update_every_s = was; }); } },
        opts.map(([v, label]) => h("option", { value: v }, label))) }),
  ];
}
async function loadMods(ctx) {
  if (!can("modules")) return;
  try { F.mods = await jget("/api/modules"); } catch (e) { F.mods = F.mods || []; }
  ctx.update();
}

const check = (label, on, onChange, hint) => h("label", { class: "check" }, h("input", { type: "checkbox", checked: on, onchange: (e, n) => onChange(n.checked) }), h("span", null, label, hint ? h("span", { class: "field-h" }, hint) : null));

// Kept in this browser, like the theme: another person signed in elsewhere keeps theirs.
function viewSection(ctx) {
  const on = simpleOn();
  return section({ title: "Simple view", level: 1 },
    h("p", null, on ? "On: you see a chat and what your Hive has made, and it talks to you in plain words." : "Off: you see every tab, and what the agents say to each other. Turn it on for just a chat and what's been made, with agents told to use plain words."),
    button(on ? "Show everything" : "Turn on simple view", () => { setSimple(!on); ctx.update(); }, { size: "s" }));
}
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
      versionBlocks(ctx, save),
      block({ title: "Update by itself", sub: F.up && F.up.installed ? "Installs each new release. Keeps your settings and goes back if it fails. When it's off, this Hive still looks and says so above, and you choose when to update." : "Installs each new release. Keeps your settings and goes back if it fails.",
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
    section({ title: "Model", level: 1, note: "what agents run on" }, pg ? modelPart(ctx.update) : reading()),
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
  const about = x => x.about.replace(/`/g, "") + (x.needs ? ` Needs ${x.needs}.` : "");
  const WHERE = { github: "On GitHub", git: "In a Git repository elsewhere", local: "Only on this machine" };
  const slackBtn = () => button(F.slack ? "Close" : "Set up", () => { F.slack = F.slack ? null : { step: 1, bot: "", app: "", channel: "#hive" }; ctx.update(); }, { size: "s", kind: F.slack ? "quiet" : null });
  const end = (x, inBrowse) => {
    if (x.name === "dash") return h("span", { class: "meta" }, "this app");
    if (x.name === "slack-door" && x.state !== "installed" && built("slack-door")) return slackBtn();
    if (x.state === "installed" && x.default) return h("span", { class: "meta" }, "comes with the Hive");
    if (x.state === "installed" && inBrowse) return h("span", { class: "meta" }, "installed");
    return button(x.state === "installed" ? "Remove" : "Add", () => act(x.state === "installed" ? "remove" : "add", x.name), { size: "s", busy: F.busy[x.name] });
  };
  const row = (x, inBrowse) => h("li", { class: "lr", "data-key": x.name },
    h("div", { class: "lr-in mod-row" },
      h("b", { class: "mod-name" }, x.repo ? h("a", { href: x.repo, target: "_blank", rel: "noreferrer noopener" }, x.name) : x.name),
      h("span", { class: "muted mod-about" }, about(x), x.source === "own" ? h("span", { class: "mod-where" }, WHERE[x.where] || WHERE.local) : null),
      end(x, inBrowse)));
  const list = (rows, inBrowse) => h("ul", { class: "list" }, rows.map(x => row(x, inBrowse)));
  const group = (key, title, note, ...body) => h("div", { class: "mod-group", "data-group": key }, h("h3", { class: "mod-gt" }, title, note ? h("span", { class: "mod-gn" }, note) : null), body);
  const shelves = () => {
    const pub = F.mods.filter(x => x.source !== "own"), own = F.mods.filter(x => x.source === "own");
    const have = pub.filter(x => x.state === "installed"), rec = pub.filter(x => x.recommended && x.state !== "installed");
    const q = (F.modQ || "").trim().toLowerCase(), shown = pub.filter(x => !q || (x.name + " " + x.about).toLowerCase().includes(q));
    return [
      group("public", "Public, installed", null, have.length ? list(have) : empty("None installed yet.")),
      group("recommended", "Recommended", null,
        rec.length ? list(rec) : h("p", { class: "muted mod-none" }, "You have everything we recommend."),
        h("div", { class: "lr-more" }, button(F.browse ? "Hide the full list" : `Browse all ${pub.length} public extras`, () => { F.browse = !F.browse; ctx.update(); }, { size: "s", kind: "quiet" })),
        F.browse ? h("div", { class: "mod-browse" },
          field("Filter", input({ placeholder: "Name or what it does", value: F.modQ || "", oninput: (e, n) => { F.modQ = n.value; ctx.update(); } })),
          shown.length ? list(shown, true) : empty("No extra matches that.")) : null),
      group("own", "This Hive's own", "made here, or added from a Git URL",
        own.length ? list(own) : h("p", { class: "muted mod-none" }, "Nothing yet. Ask an agent to make something into an extra and it shows up here."),
        h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.url.trim()) act("add", F.url.trim()); } },
          h("div", { style: "flex:1;min-width:12rem" }, field("Add from a Git URL", input({ placeholder: "Git URL of a module", value: F.url, oninput: (e, n) => { F.url = n.value; } }))),
          button("Add", null, { type: "submit", busy: F.busy[F.url.trim()] })),
        h("p", { class: "muted mod-none" }, "An extra's code runs with the same rights as your agents, so only add ones you trust.")),
    ];
  };
  return section({ title: "Extras", level: 1, note: "optional tools your agents can use (modules)" },
    F.mods == null ? empty("Reading…", { kind: "loading" }) : F.mods.length ? shelves() : empty("No modules listed."),
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
    row("Model", "Model", "off", (MODELS.find(x => x[0] === pg.settings.model) || [0, pg.settings.model])[1]);
    row("Power", "Power", "off", (POWER.find(x => x[0] === pg.settings.power) || [0, "level " + pg.settings.power])[1]);
    const c = pg.sandbox_check.state;
    row("Sandbox", "Sandbox", pg.settings.sandbox === "bare" ? "limited" : c === "works" ? "working" : c === "broken" ? "failed" : "ended", pg.settings.sandbox === "bare" ? "off" : c === "works" ? "on" : c === "broken" ? "not working" : "unknown");
  }
  if (o) { const n = Object.keys(o.sites || {}).length + o.hosts.length; row("Access from outside", "From outside", n ? "working" : "off", n ? "set up" : "off"); }
  return rows.length ? h("div", { class: "set-gs", "data-key": "glance" }, rows) : null;
}

export default {
  id: "settings", label: "Settings", about: "accounts, extras, access", tab: () => S.app.app && (can("config") || can("modules") || can("logins")),
  enter(ctx) { F.ed = null; F.saved = null; F.access = null; F.acctOpen = null; F.accessOpen = null; F.say = {}; clearSignin(); forget(); F.up = null; loadCfg(ctx); loadUpdate(ctx); loadMods(ctx); if (S.app.settings && can("config")) loadPage(ctx.update); },
  leave() { forget(); forgetSecrets(); },
  render(ctx) {
    if (!S.app.app) return h("div", { class: "page" }, empty("Settings show only in the app, opened from this machine.", { kind: "page", title: "Settings" }));
    const secs = [viewSection(ctx), hiveSection(ctx), loginsSection(ctx), ...setupSections(ctx), secretsSection(ctx), modulesSection(ctx), accessSection(ctx)].filter(Boolean);
    const header = pageHeader({ title: "Settings", lead: simpleOn() ? "Choices for your Hive." : "How the hive runs, which accounts it uses, and what extras it has." });
    if (!secs.length) return form({ header, sections: [empty("This connection can't change any settings.")] });
    const nav = secs.map(n => n.getAttribute("aria-label"));
    return h("div", { class: "page set", "data-key": "settings" }, header, glance(ctx),
      h("div", { class: "set-b" },
        h("nav", { class: "set-n", "aria-label": "Settings sections", "data-key": "nav" }, nav.map(t => h("button", { type: "button", "data-key": t, onclick: () => jump(t) }, t))),
        h("div", { class: "set-s" }, secs)));
  },
};
