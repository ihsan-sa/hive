// Settings: Everyday, Accounts, Power and sandbox, Data sharing, Secrets, This device, Set once, Modules, Access from outside (R7). Shown only where can() allows each part.
import { status as st } from "../core/status.js";
import { h } from "../core/dom.js";
import { S, can, built, jget, needsAccount, post, loadApp, said } from "../core/store.js";
import { simpleOn, setSimple } from "../core/simple.js";
import { deviceFingerprint } from "../core/sign.js";
import { notifyBlocked, notifyOn, notifySupported, notifyTest, pushOn, pushSupported, setNotify, setPush } from "../core/notify.js";
import { DEFAULT_NAME, brandLogo, brandName } from "../core/brand.js";
import { form } from "../templates.js";
import { pageHeader, section, block, blocks, button, field, infoButton, input, empty, meter, statusLabel, statusDot, waiting } from "../ui/kit.js";
import { act, isBusy } from "../ui/activity.js";
import { confirm } from "../ui/dialog.js";
import { startSignin, signinBox, clearSignin } from "../ui/signin.js";
import { ago, dur, now, plural } from "../core/format.js";
import { accountUse } from "../core/model.js";
import { href } from "../core/router.js";
import { copyButton } from "../ui/refs.js";
import { MODELS, POWER, accountsData, codexPart, forget, keysPart, loadPage, modelPart, pageData, powerPart, reading, rootNote, sandboxPart, sharePart } from "../ui/setup.js";
import { forgetSecrets, secretParts } from "./secrets.js";

// Everything the person is typing lives here, so a poll never loses it.
const F = { cfg: null, ed: null, mods: null, say: {}, newLogin: "", url: "", busy: {}, access: null, saved: null, flash: {} };
// A switch that saved says so beside it for 1.5 s.
const flash = (k, update) => { F.flash[k] = Date.now(); setTimeout(() => { if (Date.now() - (F.flash[k] || 0) >= 1400) { delete F.flash[k]; update(); } }, 1500); };
const savedMark = k => F.flash[k] ? h("span", { class: "blk-saved field-ok", role: "status" }, "✓ Saved") : null;
const say = (k, text, tone) => { F.say[k] = text ? { text, tone } : null; };
const msg = k => F.say[k] ? h("p", { class: "fmsg", "data-tone": F.say[k].tone || null, role: "status" }, F.say[k].text) : null;
const CAPTEXT = { read: "Read everything", files: "Read files: notes, modules, scripts, projects", post: "Post as you, which starts agents", stop: "Kill agents", tier: "Change a thread's tier", limits: "Pause logins and change stop lines", logins: "Sign in and add logins", config: "Change settings", modules: "Add and remove modules", plans: "Edit project plans", secrets: "Store secrets" };

async function loadCfg(ctx) {
  if (!can("config")) return;
  try { F.cfg = await jget("/api/config"); F.cfgErr = null; } catch (e) { F.cfg = F.cfg || {}; F.cfgErr = e.message; ctx.update(); return; }   // no defaults on a failed read: a toggle would save them over the real values
  F.ed ||= { max: F.cfg.max_sessions ?? "", kp: !!F.cfg.keeper_every_s, kpe: F.cfg.keeper_every_s || 10800, au: F.cfg.auto_update !== false, bud: F.cfg.max_budget_usd ?? "", day: F.cfg.max_daily_usd ?? "" };
  F.saved ||= { max: F.ed.max, bud: F.ed.bud, day: F.ed.day };
  ctx.update();
}
async function loadUpdate(ctx) {
  if (!can("config")) return;
  try { F.up = await jget("/api/update"); } catch (e) { return false; }   // an update restarts the app, so a poll can miss
  ctx.update(); return true;
}
/** How often the keeper round runs (keeper_every_s): the person asked to set it, 2026-11-04. */
const PACE = [[3600, "Every hour"], [7200, "Every 2 hours"], [10800, "Every 3 hours"], [21600, "Every 6 hours"], [43200, "Every 12 hours"], [86400, "Once a day"]];
const paceOpts = v => PACE.some(o => o[0] === v) ? PACE : [...PACE, [v, `Every ${dur(v * 1000)}`]].sort((a, b) => a[0] - b[0]);
const paceSay = v => (paceOpts(v).find(o => o[0] === v) || [, "Every 3 hours"])[1];
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
  const news = U.running ? "Updating now." : U.newest ? `${U.newest} is available.${U.update_pace === "daily" && U.auto_update ? " It goes in by itself once it has been out for 12 hours, at most one a day." : ""}` : U.checked && !U.failed.length ? "This is the newest." : "";
  const every = U.update_every_s, opts = EVERY.some(o => o[0] === every) ? EVERY : [...EVERY, [every, `Every ${plural(Math.round(every / 60), "minute")}`]];
  return [
    block({ title: "Version", sub: `${seen} ${news}`, state: h("b", { "data-up": "tag" }, U.tag || "before the first release"),
      actions: [button("Check now", () => checkNow(ctx), { size: "s", busy: isBusy("up-check"), disabled: busy || U.running }),
        U.newest ? button("Update now", () => updateNow(ctx), { size: "s", kind: "primary", busy: isBusy("up-install"), disabled: busy || U.running }) : null] },
      U.failed.length ? h("p", { class: "meta" }, `Skipped because updating to it failed: ${U.failed.join(", ")}.`) : null,
      U.error && !F.say.up ? h("p", { class: "fmsg", "data-tone": "bad" }, "Last try: " + U.error) : null,
      msg("up")),
    block({ title: "Look for new releases", sub: "How often this Hive looks. It installs what it finds if Update by itself is on.",
      actions: h("select", { class: "input", value: every, "aria-label": "Look for new releases", title: "Look for new releases", onchange: e => { const was = U.update_every_s, v = +e.target.value; U.update_every_s = v; save({ update_every_s: v }, () => { U.update_every_s = was; }); } },
        opts.map(([v, label]) => h("option", { value: v }, label))) }),
    block({ title: "How often to update", sub: "About once a day installs a release that has been out for at least 12 hours. Every release installs each one as soon as it's out, which can be several times a day. Update now always takes the newest.",
      actions: h("select", { class: "input", value: U.update_pace, "aria-label": "How often to update", title: "How often to update", onchange: e => { const was = U.update_pace, v = e.target.value; U.update_pace = v; save({ update_pace: v }, () => { U.update_pace = was; }); } },
        [["daily", "About once a day"], ["every", "Every release"]].map(([v, label]) => h("option", { value: v }, label))) }),
  ];
}
async function loadMods(ctx) {
  if (!can("modules")) return;
  try { F.mods = await jget("/api/modules"); F.modsErr = null; } catch (e) { F.modsErr = e.message; }
  ctx.update();
}

/** Once, at a section's foot: whether its changes need a Save. */
const foot = text => h("p", { class: "field-h set-foot", "data-key": "saves" }, text);
const AUTO = "Changes here save as you make them.";
const check = (label, on, onChange, hint) => h("label", { class: "check" }, h("input", { type: "checkbox", checked: on, onchange: (e, n) => onChange(n.checked) }), h("span", null, label, hint ? h("span", { class: "field-h" }, hint) : null));

// Kept in this browser, like the theme: another person signed in elsewhere keeps theirs.
function viewSection(ctx) {
  const on = simpleOn();
  return section({ title: "This device", note: "this browser only", level: 1 },
    blocks(block({ title: "Simple view", key: "simple", sub: on ? "On: you see a chat, and your Hive talks to you in plain words." : "Off: you see every tab, and what the agents say to each other. Turn it on for just a chat and what's been made, with agents told to use plain words.",
      actions: button(on ? "Show everything" : "Turn on simple view", () => { setSimple(!on); ctx.update(); }, { size: "s" }) }),
    notifyBlock(ctx), pushBlock(ctx), phoneBlock(ctx), signBlock(ctx)));
}
// This browser's signature on posts (core/sign.js). The fingerprint is what root pins, so it has to be readable here; nothing can be done with it but compare it.
function signBlock(ctx) {
  if (F.signFp === undefined) { F.signFp = null; deviceFingerprint().then(fp => { F.signFp = fp || ""; ctx.update(); }); }
  if (F.signFp === null) return null;
  return block({ title: "Signing your messages", key: "signing",
    sub: F.signFp ? `This browser signs what you send with a key only it holds, so an agent can't simply write a message in your name. Its fingerprint is ${F.signFp}. It counts once root has pinned it.` : "This browser can't sign messages, so what you send from it goes unsigned.",
    actions: F.signFp ? copyButton(F.signFp) : null });
}
// Phone alerts: the server sends one line through ntfy.sh when a post to you goes unread, so it works with this page closed. Off until switched on here.
async function loadPhone(ctx) {
  if (!can("config")) return;
  try { F.phone = await jget("/api/ntfy"); } catch (e) { F.phone = { failed: e.message }; }
  ctx.update();
}
async function phoneDo(ctx, what, ok) {
  try { const r = await post("/api/ntfy", { do: what }); await loadPhone(ctx); say("phone", ok, "good"); if (r && r.tested) say("phone", "Sent. Your phone should show it in a moment.", "good"); }
  catch (e) { say("phone", e.message, "bad"); }
  ctx.update();
}
function phoneBlock(ctx) {
  const P = F.phone;
  if (!can("config") || !P) return null;
  if (P.failed) return block({ title: "Phone alerts", key: "phone", sub: `Couldn't read whether phone alerts are on: ${P.failed}.`, actions: button("Try again", () => loadPhone(ctx), { size: "s" }) });
  const sub = P.on
    ? `On. When something is waiting for you and you haven't seen it, your Hive sends one line to ntfy.sh${P.line ? `: "${P.line}"` : ""}. That is all it sends: no names, no words from your agents${P.link ? ", and a link back to this address" : ""}. Anyone who learns the topic below can read those lines, so keep it to yourself.`
    : `Off. Turn it on to get a line on your phone when your Hive asks you something or finishes a job, even with this page closed. It uses ntfy.sh, a free public service with no account: your Hive sends it one line${P.line ? ` ("${P.line}")` : ""} and nothing else.`;
  return block({ title: "Phone alerts", key: "phone", sub,
    actions: [button(P.on ? "Turn off" : "Turn on phone alerts", () => phoneDo(ctx, P.on ? "off" : "on", P.on ? "Off." : "On."), { size: "s" }),
      P.on ? button("Send a test", () => phoneDo(ctx, "test"), { size: "s" }) : null] },
    P.on ? h("div", { class: "fields" },
      h("p", null, "On your phone: install the ntfy app (free), tap +, choose \"Subscribe to topic\", and enter this topic:"),
      h("p", null, h("code", null, P.topic), " ", copyButton(P.topic)),
      h("p", { class: "field-h" }, P.link ? "Tapping the alert opens " + P.link : "Tapping the alert won't open your Hive: you turned this on from an address a phone can't reach. Turn it off and on again from your Hive's public address to add the link."),
      button(P.runs ? "Stop run-finished alerts" : "Also tell me when a run I started ends", () => phoneDo(ctx, P.runs ? "runs-off" : "runs-on", P.runs ? "Run-finished alerts off." : "You will get a line when a run you started ends."), { size: "s" }),
      button("Make a new topic", () => confirm({ title: "Make a new topic?", body: ["The topic your phone follows stops working. You'd subscribe to the new one on your phone."], ok: "Make a new topic", onOk: () => phoneDo(ctx, "topic", "New topic. Subscribe to it on your phone; the old one is dead.") }), { size: "s" }),
      P.error ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, P.error) : null, msg("phone")) : msg("phone"));
}
// Off until you turn them on here; the browser then asks once. Kept in this browser.
function notifyBlock(ctx) {
  if (!notifySupported()) return null;
  const on = notifyOn(), blocked = notifyBlocked();
  return block({ title: "Notifications", key: "notify",
    sub: blocked ? "Your browser is blocking notifications for this page. Allow them in the browser's site settings, then come back here."
      : on ? "On: this browser shows a notification when your Hive asks you something or finishes a job, while this page isn't in front."
      : "Off. Turn them on and your browser asks once; then you hear about questions and finished jobs without watching this page.",
    actions: blocked ? null : [
      button(on ? "Turn off" : "Turn on notifications", async () => { await setNotify(!on); ctx.update(); }, { size: "s" }),
      on ? button("Send a test", () => notifyTest().catch(() => {}), { size: "s" }) : null] });
}
// Push with the page closed (module conn-webpush). Shows only to a full sign-in, and only once `conn on webpush` has made the key: anything else gets no block.
async function loadPush(ctx) {
  if (!can("config") || !pushSupported()) return;
  try { const P = await jget("/api/webpush"); F.push = P.available ? { ...P, on: await pushOn() } : null; } catch (e) { F.push = { failed: e.message }; }
  ctx.update();
}
async function pushDo(ctx, on) {
  try { await setPush(on, F.push.public_key, post); await loadPush(ctx); say("push", on ? "On." : "Off.", "good"); }
  catch (e) { say("push", e.message, "bad"); }
  ctx.update();
}
function pushBlock(ctx) {
  const P = F.push;
  if (!can("config") || !P || !pushSupported()) return null;
  if (P.failed) return block({ title: "Push notifications", key: "push", sub: `Couldn't read whether push is on for this browser: ${P.failed}.`, actions: button("Try again", () => loadPush(ctx), { size: "s" }) });
  return block({ title: "Push notifications", key: "push",
    sub: P.on ? `On for this browser: it hears from your Hive even with this page closed. The push service only ever carries one fixed line and a link, never what was said.`
      : `Off for this browser. Turn it on to hear from your Hive with this page closed. It goes through your browser's push service (Google, Mozilla, Apple or Microsoft), which only ever carries one line and a link, never what was said.`,
    actions: button(P.on ? "Turn off" : "Turn on push", () => pushDo(ctx, !P.on), { size: "s" }) }, msg("push"));
}
// The name saves when you leave the box; a picture saves as soon as it's picked. Every tab and page picks them up from /api/app.
const LOGO_MAX = 2 * 2 ** 20;
function brandBlocks(ctx) {
  F.name ??= brandName() || "";
  const done = async (work, ok, mark) => {
    try { await work(); await loadApp(); if (mark) { say("brand", ""); flash(mark, ctx.update); } else say("brand", ok, "good"); }
    catch (e) { say("brand", e.message, "bad"); }
    F.name = brandName() || "";
    ctx.update();
  };
  const saveName = () => {
    const v = F.name.trim();
    if (v === (brandName() || "") || v === F.nameSent) return;
    F.nameSent = v;   // a change and the blur after it are one save, not two
    done(() => post("/api/brand", { name: v }), "", "Name").finally(() => { F.nameSent = null; });
  };
  const pick = async file => {
    if (!file) return;
    if (file.size > LOGO_MAX) { say("brand", "A logo can be up to 2 MB.", "bad"); ctx.update(); return; }
    done(async () => {
      const r = await fetch("/api/brand/logo", { method: "POST", headers: { "X-Hive-Dash": "1", "Content-Type": "application/octet-stream" }, body: file });
      if (r.redirected) throw Error("sign in again and reload the page");
      const j = await r.json().catch(() => ({}));
      if (!r.ok) throw Error(j.error || said(r.status));
    }, "Logo saved.");
  };
  const logo = brandLogo();
  const file = h("input", { type: "file", accept: "image/png,image/jpeg,image/gif,image/webp,image/svg+xml", hidden: true, class: "brand-pick", onchange: (e, n) => { pick(n.files[0]); n.value = ""; } });
  return [
    block({ title: "Name", key: "name", sub: "Shown in the top bar and the browser tab. Empty uses the default.",
      actions: [savedMark("Name"), input({ type: "text", maxlength: 40, placeholder: DEFAULT_NAME, "aria-label": "Hive name", value: F.name, oninput: (e, n) => { F.name = n.value; }, onchange: saveName, onfocusout: saveName })] }),
    block({ title: "Logo", sub: "Shown next to the name and as the tab's icon. PNG, JPEG, GIF, WebP or SVG, up to 2 MB. A square picture works best.",
      actions: [logo ? h("img", { class: "brand-logo set-logo", src: logo, alt: "Current logo", "data-key": "preview" }) : h("span", { class: "set-logo set-logo-none", "aria-hidden": "true" }), file,
        button(logo ? "Replace" : "Upload", (e, n) => n.parentNode.querySelector(".brand-pick").click(), { size: "s" }),
        logo ? button("Remove", () => done(() => post("/api/brand", { logo: null }), "Logo removed."), { size: "s", kind: "quiet" }) : null] })];
}

function hiveSection(ctx) {
  if (!can("config")) return null;
  if (!F.ed) return section({ title: "Everyday", note: "what you change in a normal week", level: 1 }, F.cfgErr ? [empty(`Couldn't read the settings: ${F.cfgErr}`, { kind: "error" }), button("Try again", () => { F.cfgErr = null; ctx.update(); return loadCfg(ctx); }, { size: "s" })] : empty("Reading…", { kind: "loading" }));
  const E = F.ed, up = S.app.setup && S.app.setup.starter, brand = brandBlocks(ctx);
  // Each control saves on its own and sends only its own field; a refusal puts the old value back.
  const save = async (fields, undo, mark) => {
    try { await post("/api/config", fields); if (mark) { say("hive", ""); flash(mark, ctx.update); } else say("hive", "Saved. The hive uses it within seconds.", "good"); }
    catch (e) { undo(); say("hive", e.message, "bad"); }
    ctx.update();
  };
  const toggle = (label, on, onChange) => [savedMark(label), h("input", { type: "checkbox", role: "switch", class: "switch", "aria-label": label, checked: on, onchange: (e, n) => onChange(n.checked) })];
  const saveMax = () => {
    const was = F.saved.max, v = String(E.max).trim();
    if (v === String(was)) return;
    if (v !== "" && !(Number.isInteger(+v) && +v >= 1 && +v <= 200)) { E.max = was; say("hive", "Agents at once is a whole number from 1 to 200, or empty for no limit.", "bad"); ctx.update(); return; }
    F.saved.max = v;
    save({ max_sessions: v === "" ? null : +v }, () => { E.max = F.saved.max = was; }, "Agents at once");
  };
  const saveBud = () => {
    const was = F.saved.bud, v = String(E.bud).trim();
    if (v === String(was)) return;
    if (v !== "" && !(+v > 0 && +v < 10000)) { E.bud = was; say("hive", "The most one run may spend is a number of dollars above 0, or empty for no cap.", "bad"); ctx.update(); return; }
    F.saved.bud = v;
    save({ max_budget_usd: v === "" ? null : +v }, () => { E.bud = F.saved.bud = was; }, "Budget per run");
  };
  const saveDay = () => {
    const was = F.saved.day, v = String(E.day).trim();
    if (v === String(was)) return;
    if (v !== "" && !(+v > 0 && +v < 100000)) { E.day = was; say("hive", "The daily cap is a number of dollars above 0, or empty for no cap.", "bad"); ctx.update(); return; }
    F.saved.day = v;
    save({ max_daily_usd: v === "" ? null : +v }, () => { E.day = F.saved.day = was; }, "Daily cap");
  };
  const spent = `Spent today: $${(+(F.cfg.spend_today || 0)).toFixed(2)} at API prices (UTC day). `;
  const starter = async () => {
    say("starter", up ? "Stopping…" : "Starting…");
    try {
      await act("starter", up ? "Stopping the Hive" : "Starting the Hive", async () => {
        await post("/api/starter", { do: up ? "stop" : "start" });
        await new Promise(r => setTimeout(r, up ? 500 : 4000));
        await loadApp();
      }, ctx.update);
      const now = S.app.setup && S.app.setup.starter;
      say("starter", now === up ? (up ? "It hasn't stopped yet." : "It still isn't running. Try once more.") : "", now === up ? "bad" : null);
    } catch (e) { say("starter", e.message, "bad"); }
    ctx.update();
  };
  const pg = S.app.settings ? pageData(ctx.update) : null;
  // R7: the controls you reach for in a normal week first, Stop the Hive as the last of them; what you set up once lives in its own section.
  const everyday = section({ title: "Everyday", note: "what you change in a normal week", level: 1, actions: statusLabel(up ? "working" : "ended", up ? "running" : "not running") },
    blocks(
      block({ title: "Agents at once", sub: "Empty means no limit. Saves when you leave the box.",
        actions: [savedMark("Agents at once"), input({ type: "number", min: 1, max: 200, placeholder: "no limit", "aria-label": "Agents at once", value: E.max, oninput: (e, n) => { E.max = n.value; }, onchange: saveMax, onfocusout: saveMax })] }),
      block({ title: "Keep work moving", sub: E.kp ? `${paceSay(E.kpe)}, pick up work that stopped and start work nobody asked for, and sooner once nothing has run for half an hour. It uses your limits.` : "Off: work that stops waits until someone asks. Turn it on to pick it up by itself.",
        actions: [E.kp ? h("select", { class: "input", value: E.kpe, "aria-label": "How often to keep work moving", title: "How often to keep work moving" ,
            onchange: e => { const was = E.kpe, v = +e.target.value; E.kpe = v; save({ keeper_every_s: v }, () => { E.kpe = was; }, "Keep work moving"); } },
            paceOpts(E.kpe).map(([v, label]) => h("option", { value: v }, label))) : null,
          toggle("Keep work moving", E.kp, v => { E.kp = v; save({ keeper_every_s: v ? E.kpe : 0 }, () => { E.kp = !v; }, "Keep work moving"); })] }),
      block({ title: up ? "Stop the Hive" : "Start the Hive", sub: up ? "Stopping lets agents finish their turn." : "The Hive isn't running.", key: "starter", actions: button(up ? "Stop the Hive…" : "Start the Hive", () => up ? confirm({ title: "Stop the Hive?", body: ["Agents finish their turn, and nothing new starts until you start it again here."], ok: "Stop the Hive", kind: "danger", onOk: starter }) : starter(), { size: "s", kind: up ? "danger" : "primary", busy: isBusy("starter"), title: up ? "Stop the Hive" : "Start the Hive" }) })),
    msg("starter"), msg("hive"), foot(AUTO));
  const setOnce = section({ title: "Set once", note: "the name, the model, updates and spending caps", level: 1 },
    pg ? [h("h3", { class: "set-sub", "data-key": "model-h" }, "Model"), modelPart(ctx.update)] : null,
    blocks(brand[0], brand[1],
      versionBlocks(ctx, save),
      block({ title: "Update by itself", sub: F.up && F.up.installed ? "Installs new releases as often as you chose above. Keeps your settings and goes back if it fails. When it's off, this Hive still looks and says so above, and you choose when to update." : "Installs each new release. Keeps your settings and goes back if it fails.",
        actions: toggle("Update by itself", E.au, v => { E.au = v; save({ auto_update: v }, () => { E.au = !v; }, "Update by itself"); }) }),
      block({ title: "Budget per run", sub: E.bud === "" ? "Empty means no cap. A cap stops a run that reaches it (counted at API prices) and says so in its thread. Applies from each run's next start." : `Each run stops at $${E.bud}, counted at API prices, and says so in its thread. Applies from each run's next start. Empty removes the cap.`,
        actions: [savedMark("Budget per run"), input({ type: "number", min: 0, step: "any", placeholder: "no cap", "aria-label": "Budget per run in dollars", value: E.bud, oninput: (e, n) => { E.bud = n.value; }, onchange: saveBud, onfocusout: saveBud })] }),
      block({ title: "Daily cap", sub: spent + (E.day === "" ? "Empty means no cap. A cap holds new agents from starting once a day's spend reaches it; running ones finish." : `No new agents start once a day's spend reaches $${E.day}; running ones finish. Empty removes the cap.`),
        actions: [savedMark("Daily cap"), input({ type: "number", min: 0, step: "any", placeholder: "no cap", "aria-label": "Daily cap in dollars", value: E.day, oninput: (e, n) => { E.day = n.value; }, onchange: saveDay, onfocusout: saveDay })] })),
    msg("brand"), msg("hive"), foot(AUTO));
  return [everyday, setOnce];
}

// Codex and API keys: same section as Claude, because to the person they're all accounts. With settings.json and config rights they come with the
// settings page; otherwise from /api/accounts, which needs only logins.
function otherAccounts(ctx) {
  const whole = S.app.settings && can("config");
  const acc = whole ? (pageData(ctx.update) || {}).accounts : accountsData(ctx.update);
  if (!acc) return can("logins") ? { rows: [h("div", { class: "blk" }, reading())] } : null;
  const keys = (acc.keys || []).length, codex = (acc.codex.logins || []).length;
  return { note: rootNote(), rows: [h("div", { class: "blk", "data-key": "codex-row" }, codexPart(ctx.update)),
    block({ title: "API keys", sub: keys ? `${plural(keys, "key")} saved, charged per use` : acc.settings_file === false ? "Anthropic, OpenRouter or any Anthropic-compatible API. Charged per use." : "Charged per use. Used only when no Claude or Codex login has room.",
      key: "api-keys", fold: true, open: F.keysOpen ?? keys > 0, onToggle: o => { F.keysOpen = o; } }, keysPart(ctx.update))], codex };
}

// Simple mode's whole Usage page: how full the Claude account's two limits are, and what happens when one fills.
function useSection() {
  const use = simpleOn() && !needsAccount() ? accountUse() : null; if (!use) return null;
  const row = (title, x, word) => x && block({ title, sub: x.back ? `${word} in ${dur(x.back - now())}` : null, state: h("b", null, x.used + "% used"), key: title }, meter({ value: x.used, label: title }));
  return section({ title: "Usage limit", note: "where new work stops", level: 1 },
    h("p", null, "Your Claude account has a limit for the week and a shorter one for every 5 hours. If one fills up, your Hive takes a break until it refills."),
    blocks(row("This week", use.week, "Refills"), row("These 5 hours", use.five, "Refills")));
}

function loginsSection(ctx) {
  if (!can("logins")) return null;
  const logins = (S.app.setup && S.app.setup.logins) || [], yes = logins.filter(l => l.signed_in).length, out = logins.length - yes;
  const state = !logins.length ? statusLabel("off", "none yet") : out ? statusLabel("out", `${out} not signed in`) : statusLabel("working", "signed in");
  if (simpleOn()) {  // one account, no names for it, and nothing about the machinery
    const on = yes > 0, name = (logins.find(l => !l.signed_in) || logins[0] || {}).name || null;
    return section({ title: "Accounts", note: "what your agents sign in with", level: 1 }, blocks(block({ title: "Claude", sub: on ? "Your Hive works through this account." : "Your Hive works through a paid Claude account. Sign in to yours, and it can start.",
      state: statusLabel(on ? "working" : "out", on ? "signed in" : "not signed in"), key: "claude",
      actions: button(on ? "Sign in again" : "Sign in to Claude Code", () => startSignin("login-first", name, false, ctx.update), { size: "s", kind: on ? "quiet" : "primary" }) },
      signinBox("login-first", ctx.update))));
  }
  const other = otherAccounts(ctx) || { rows: [] };
  return section({ title: "Accounts", note: "what your agents sign in with", level: 1 }, other.note,
    blocks(block({ title: "Claude", sub: logins.length ? (out ? `${yes} of ${plural(logins.length, "account")}` : plural(logins.length, "account")) : "a subscription runs the agents", state, key: "claude", fold: true, open: F.acctOpen ?? (out > 0 || !logins.length), onToggle: o => { F.acctOpen = o; } },
      h("p", { class: "field-h" }, "What each has left, pausing it and where it stops are under ", h("a", { href: href("usage") }, "Usage"), "."),
      logins.length ? h("ul", { class: "list" }, logins.map(l => h("li", { class: "lr", "data-key": l.name },
        h("div", { class: "lr-in frow fsplit" }, h("b", null, l.name), l.signed_in ? null : statusLabel("out"),
          h("span", { class: "frow-end" }, button(l.signed_in ? "Sign in again" : "Sign in", () => startSignin("login-" + l.name, l.name, false, ctx.update), { size: "s", kind: l.signed_in ? "quiet" : null }))),
        signinBox("login-" + l.name, ctx.update)))) : null,
      h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.newLogin.trim()) { say("newlogin", null); startSignin("login-new", F.newLogin.trim(), false, ctx.update); } else { say("newlogin", "Give it a name first.", "bad"); ctx.update(); } } },
        h("div", { class: "frow-grow", "data-size": "s" }, field("Add another account", input({ placeholder: "a name for it, like work", value: F.newLogin, oninput: (e, n) => { F.newLogin = n.value; } }))),
        button("Sign in to it", null, { type: "submit" })),
      msg("newlogin"),
      h("p", { class: "field-h" }, "A second subscription gives the Hive more room: new agents go to whichever has the most left. Adding one signs in, checks it isn't one you already have, and makes one short test run."),
      signinBox("login-new", ctx.update)), other.rows));
}

// ---- with settings.json only: the choices the first run made, to change here ----
// When the sandbox's own check fails, say so in red above the power cards. The words are the check's own (sandbox_check.why); the
// full note, with what to do, stays under Sandbox below.
const sandboxBroken = pg => pg.settings.sandbox !== "bare" && pg.sandbox_check.state === "broken"
  ? h("p", { class: "fmsg", "data-tone": "bad", "data-key": "sandbox-broken", role: "status" }, "The sandbox doesn't work on this machine, so these levels are only an instruction to the agents. ", pg.sandbox_check.why || "") : null;
function setupSections(ctx) {
  if (!S.app.settings) return [];
  const pg = pageData(ctx.update);
  if (!can("config")) return [];
  return [
    section({ title: "Power and sandbox", level: 1, note: "who decides what agents may do, and what holds them to it" }, pg ? [sandboxBroken(pg), powerPart(ctx.update), h("h3", { class: "set-sub", "data-key": "sandbox" }, "Sandbox"), sandboxPart(ctx.update), foot(AUTO)] : reading()),
    section({ title: "Data sharing", level: 1, note: "what's sent, and whether it is" }, pg ? [sharePart(ctx.update, true), foot(AUTO)] : reading())];
}
const secretsSection = ctx => can("secrets") ? section({ title: "Secrets", level: 1, note: "send a token to the Hive; it's never shown back" }, h("div", { class: "stack secrets" }, secretParts(ctx))) : null;

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
    if (x.name === "slack-door" && built("slack-door") && !(S.app.slack_door || {}).connected) return slackBtn();
    if (x.name === "slack-door" && built("slack-door")) return h("span", { class: "mod-end" }, // connected: say where it answers, so a reload shows Slack is on
      h("span", { class: "meta" }, "Connected to " + (S.app.slack_door.channel || "Slack")), " ",
      S.app.slack_door.problem ? h("span", { class: "meta mod-problem", "data-tone": "bad", title: "Disconnect, then Set up again with new tokens." }, "but " + S.app.slack_door.problem.split(": ")[0] + ": Disconnect, then Set up again") : null, // a token revoked in Slack after Connect
      S.app.slack_door.problem ? " " : null,
      button("Disconnect", () => confirm({ title: "Disconnect Slack?", body: ["The Hive stops answering in Slack and deletes its two Slack tokens from this computer. You can connect again from this row."], ok: "Disconnect", kind: "danger", onOk: async () => {
        F.busy["slack-door"] = true; ctx.update();
        try { const r = await post("/api/slack-door", { do: "disconnect" }); S.app.slack_door = { connected: r.connected, channel: r.channel }; F.slack = null; say("mods", r.said, "good"); } catch (e) { say("mods", e.message, "bad"); }
        delete F.busy["slack-door"]; ctx.update(); } }), { size: "s", kind: "quiet", busy: F.busy["slack-door"] }));
    if (x.state === "installed" && x.default) return h("span", { class: "meta" }, "comes with the Hive");
    if (x.state === "installed" && inBrowse) return h("span", { class: "meta" }, "installed");
    return x.state === "installed"
      ? button("Remove", () => confirm({ title: `Remove ${x.name}?`, body: ["It comes off this Hive. You can add it back from this page."], ok: "Remove", kind: "danger", onOk: () => act("remove", x.name) }), { size: "s", busy: F.busy[x.name] })
      : button("Add", () => act("add", x.name), { size: "s", busy: F.busy[x.name] });
  };
  const row = (x, inBrowse) => h("li", { class: "lr", "data-key": x.name },
    h("div", { class: "lr-in mod-row" },
      h("b", { class: "mod-name" }, x.repo ? h("a", { href: x.repo, target: "_blank", rel: "noreferrer noopener" }, x.name) : x.name),
      h("span", { class: "muted mod-about" },
        h("span", { class: "mod-line" }, h("span", { class: "mod-one" }, about(x)), infoButton("About " + x.name, about(x))),
        x.source === "own" ? h("span", { class: "mod-where" }, WHERE[x.where] || WHERE.local) : null),
      end(x, inBrowse)));
  const list = (rows, inBrowse) => h("ul", { class: "list" }, rows.map(x => row(x, inBrowse)));
  const group = (key, title, note, ...body) => h("div", { class: "mod-group", "data-group": key }, h("h3", { class: "mod-gt" }, title, note ? h("span", { class: "mod-gn" }, note) : null), body);
  const shelves = () => {
    const pub = F.mods.filter(x => x.source !== "own"), own = F.mods.filter(x => x.source === "own");
    const have = pub.filter(x => x.state === "installed"), rec = pub.filter(x => x.recommended && x.state !== "installed");
    const q = (F.modQ || "").trim().toLowerCase(), shown = pub.filter(x => !q || (x.name + " " + x.about).toLowerCase().includes(q));
    return [
      group("installed", "Installed", null, have.length || own.length ? [have.length ? list(have) : null, own.length ? list(own) : null] : empty("None installed yet.")),
      group("available", "Available", null,
        rec.length ? list(rec) : h("p", { class: "muted mod-none" }, "You have everything we recommend."),
        h("div", { class: "lr-more" }, button(F.browse ? "Hide the full list" : `Browse all ${pub.length} public modules`, () => { F.browse = !F.browse; ctx.update(); }, { size: "s", kind: "quiet" })),
        F.browse ? h("div", { class: "mod-browse" },
          field("Filter", input({ placeholder: "Name or what it does", value: F.modQ || "", oninput: (e, n) => { F.modQ = n.value; ctx.update(); } })),
          shown.length ? list(shown, true) : empty("No module matches that.")) : null,
        h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.url.trim()) act("add", F.url.trim()); else { say("mods", "Paste the module's Git URL first.", "bad"); ctx.update(); } } },
          h("div", { class: "frow-grow" }, field("Add from a Git URL", input({ placeholder: "Git URL of a module", value: F.url, oninput: (e, n) => { F.url = n.value; } }))),
          button("Add", null, { type: "submit", busy: F.busy[F.url.trim()] })),
        h("p", { class: "muted mod-none" }, "A module's code runs with the same rights as your agents, so only add ones you trust.")),
    ];
  };
  return section({ title: "Modules", level: 1, note: "optional tools your agents can use" },
    F.mods == null ? (F.modsErr ? [empty(`Couldn't read the modules: ${F.modsErr}`, { kind: "error" }), button("Try again", () => { F.modsErr = null; ctx.update(); return loadMods(ctx); }, { size: "s" })] : empty("Reading…", { kind: "loading" })) : F.mods.length ? shelves() : empty("No modules listed."),
    F.slack ? slackSetup(ctx) : null,
    msg("mods"));
}

// ---- The Slack door, set up from here instead of a file: POST /api/slack-door {bot_token, app_token, channel} (dash.py slack_door_connect). ----
const MANIFEST = `display_information:
  name: Hive
features:
  bot_user:
    display_name: Hive
oauth_config:
  scopes:
    bot: [chat:write, reactions:write, channels:history, groups:history, im:history, channels:read, groups:read, im:read, im:write, users:read, files:read, files:write]
settings:
  event_subscriptions:
    bot_events: [message.channels, message.groups, message.im]
  socket_mode_enabled: true`;
function slackSetup(ctx) {
  const Z = F.slack, step = (n, title, ...body) => h("li", { class: "sl-step", "data-on": Z.step === n ? "" : null, "data-done": Z.step > n ? "" : null },
    h("b", null, title), Z.step === n ? body : Z.step > n ? h("span", { class: "meta" }, "done") : null);
  const connect = async () => {
    if (Z.busy) return; Z.busy = true; ctx.update();
    try { const r = await post("/api/slack-door", { bot_token: Z.bot.trim(), app_token: Z.app.trim(), channel: Z.channel.trim() }); say("slack", `${r.said} Post in ${r.channel || Z.channel} to test it: an agent answers there.`, "good"); Z.step = 4; Z.bot = Z.app = ""; loadApp().catch(() => {}); }
    catch (e) { say("slack", e.message, "bad"); }
    Z.busy = false; ctx.update();
  };
  return h("div", { class: "signin slk", "data-key": "slack" },
    h("p", null, h("b", null, "Reach the Hive from Slack")),
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
      step(3, "Pick the channel", field("Channel", input({ value: Z.channel, oninput: (e, n) => { Z.channel = n.value; } }), "Invite the app to it in Slack first: /invite @Hive. Everyone in the channel can direct this Hive, so use a small one."),
        h("div", { class: "frow" }, button("Connect", connect, { kind: "primary", busy: Z.busy }), button("Back", () => { Z.step = 2; ctx.update(); }, { kind: "quiet" })))),
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
  const toRows = () => Object.entries(o.sites || {}).map(([host, v]) => {
    const pe = v.people || {}, ks = m => Object.keys(pe).filter(k => pe[k] === m).join(" ");
    return { host, team: v.access?.team || "", aud: v.access?.aud || "", emails: (v.access?.emails || []).join(" "), full: ks("full"), limited: ks("limited"), names: v.names };
  });
  const R = A.rows ||= toRows();
  const saveSites = async () => {
    try {
      const sites = {};
      for (const r of R) {
        const host = r.host.trim().toLowerCase();
        if (!host) throw new Error("Every name needs a hostname.");
        const people = {};
        for (const a of words(r.limited)) people[a] = "limited";
        for (const a of words(r.full)) { if (people[a]) throw new Error(`${a} is on both lists for ${host}. Keep it on one.`); people[a] = "full"; }
        if (!Object.keys(people).length) throw new Error(`${host} needs at least one person, or it would let nobody in.`);
        if (!r.team.trim() || !r.aud.trim()) throw new Error(`${host} needs an Access team and audience.`);
        sites[host] = { access: { team: r.team.trim(), aud: r.aud.trim(), ...(words(r.emails).length ? { emails: words(r.emails) } : {}) }, people, ...(r.names ? { names: r.names } : {}) };
      }
      say("sites", "");
      await act("sites", "Saving the names with their own sign-in", () => post("/api/access", { sites }), ctx.update);
      say("sites", "Saved.", "good");
    }
    catch (e) { say("sites", e.message, "bad"); }
    ctx.update();
  };
  const rowTxt = (r, label, k, ph) => field(label, input({ placeholder: ph, value: r[k], oninput: (e, n) => { r[k] = n.value; } }));
  const siteRows = R.map((r, i) => h("div", { class: "fgrid", "data-key": "site-" + i },
    rowTxt(r, "Hostname", "host", "h3.example.com"), rowTxt(r, "Access team domain", "team", "yourteam.cloudflareaccess.com"),
    rowTxt(r, "Application audience", "aud", "the Access application's AUD tag"), rowTxt(r, "Allowed emails", "emails", "optional: only these emails"),
    rowTxt(r, "People with full access", "full", "you@example.com"), rowTxt(r, "People with limited access", "limited", "friend@example.com"),
    button("Remove", () => { R.splice(i, 1); ctx.update(); }, { kind: "quiet" })));
  const txt = (label, k, ph) => field(label, input({ placeholder: ph, value: A[k], oninput: (e, n) => { A[k] = n.value; } }));
  const sites = Object.keys(o.sites || {}), names = [...sites, ...o.hosts.filter(x => !sites.includes(x))];
  return section({ title: "Access from outside", level: 1, note: "only changeable from this machine" },
    h("p", null, statusLabel(names.length ? "working" : "off", names.length ? "Set up" : "Off"), " ", names.length ? "These names can reach this Hive from the internet, through Cloudflare, once their tunnel is up:" : "Only this computer can open this Hive. You need this only to reach it from a phone or another machine."),
    names.length ? h("ul", { class: "plain", "data-key": "access-names" }, names.map(n => h("li", { "data-key": n }, h("b", { class: "mono" }, n), sites.includes(n) ? h("span", { class: "muted" }, " has its own sign-in rules. Change them under Names with their own sign-in, below.") : null))) : null,
    blocks(block({ title: "How access works", key: "access-how", fold: true, open: F.accessHow, onToggle: o => { F.accessHow = o; } },
      h("p", { class: "field-h" }, "Opening a name takes a Cloudflare tunnel and an Access policy, set up in Cloudflare. If you name people below, each gets limited (read, chat, kill agents) or full (everything here, from signing in until 30 minutes without using the app), and the ticks don't apply. Anyone else is refused, and emptying both lists afterwards refuses everyone. Type addresses here yourself: an agent's tools can refuse your account's address. From this machine, the secret gets everything. Through a tunnel, each request must carry a token signed by Cloudflare Access, and it can do only what's ticked here.")),
      block({ title: o.hosts.length || o.team ? "Change the settings" : "Set it up", key: "access-edit", fold: true, open: F.accessOpen ?? !!(o.hosts.length || o.team || Object.keys(o.people || {}).length), onToggle: v => { F.accessOpen = v; } },
        h("div", { class: "checks", role: "group", "aria-label": "What outside requests may do" }, o.caps.map(c => check(CAPTEXT[c] || c, A.can.has(c), v => { v ? A.can.add(c) : A.can.delete(c); }))),
        h("div", { class: "fgrid" }, txt("Hostnames", "hosts", "hive.example.com"), txt("Access team domain", "team", "yourteam.cloudflareaccess.com"),
          txt("Application audience", "aud", "the Access application's AUD tag"), txt("Allowed emails", "emails", "optional: only these emails"),
          txt("People with full access", "full", "you@example.com"), txt("People with limited access", "limited", "friend@example.com")),
        h("div", { class: "frow" }, button("Save access settings", save, { kind: "primary", busy: isBusy("access") }), msg("access"))),
      block({ title: "Names with their own sign-in", key: "access-sites", fold: true, open: F.sitesOpen ?? R.length > 0, onToggle: v => { F.sitesOpen = v; } },
        h("p", { class: "field-h" }, "A name here has its own Access team, audience and people, apart from the settings above. A sign-in for one name never works on another. Each needs a team, an audience and at least one person, and its tunnel must pass the public name on as the Host. Those above don't change when you save here."),
        ...siteRows,
        h("div", { class: "frow" }, button("Add a name", () => { R.push({ host: "", team: "", aud: "", emails: "", full: "", limited: "" }); F.sitesOpen = true; ctx.update(); }, { kind: "quiet" }),
          button("Save names", saveSites, { kind: "primary", busy: isBusy("sites") }), msg("sites")))),
    foot("Each part below saves as one set with its own button, because a half-typed sign-in rule could lock people out."));
}

// Each section's state, as a dot after its name in the side nav: what the six chips up top used to say.
function navDots(ctx) {
  const s = S.app.setup || {}, logins = s.logins || [], pg = S.app.settings && can("config") ? pageData(ctx.update) : null, o = S.app.route === "local" && S.app.outside, d = {};
  if (simpleOn()) return d;
  // Only what is off or needs action gets a dot; "fine" repeats the section's own state.
  const dot = (title, tone, value) => { if (st(tone).tone !== "good") d[title] = [tone, value]; };
  if (can("config")) dot("Everyday", s.starter ? "working" : "ended", s.starter ? "running" : "not running");
  if (can("logins") && logins.length) dot("Accounts", logins.some(l => !l.signed_in) ? "out" : "working", `${logins.filter(l => l.signed_in).length} of ${logins.length} signed in`);
  if (pg) {
    const c = pg.sandbox_check.state, bare = pg.settings.sandbox === "bare";
    dot("Power and sandbox", bare ? "limited" : c === "works" ? "working" : c === "broken" ? "failed" : "ended", "sandbox " + (bare ? "off" : c === "works" ? "on" : c === "broken" ? "not working" : "unknown"));
  }
  if (o) { const n = Object.keys(o.sites || {}).length + o.hosts.length; dot("Access from outside", n ? "working" : "off", n ? "set up" : "off"); }
  return d;
}

export default {
  id: "settings", label: "Settings", about: "accounts, modules, access", tab: () => S.app.app && (can("config") || can("modules") || can("logins")),
  enter(ctx) { F.keysOpen = null; F.flash = {}; F.ed = null; F.name = null; F.saved = null; F.access = null; F.acctOpen = null; F.accessOpen = null; F.sitesOpen = null; F.say = {}; clearSignin(); forget(); F.up = null; loadCfg(ctx); loadPhone(ctx); loadPush(ctx); loadUpdate(ctx); loadMods(ctx); if (S.app.settings && can("config")) loadPage(ctx.update);
  },
  leave() { forget(); forgetSecrets(); },
  render(ctx) {
    if (S.app.can == null) return h("div", { class: "page" }, waiting(S.failed));   // /api/app hasn't answered: don't say "only in the app" yet
    if (!S.app.app) return h("div", { class: "page" }, empty("Settings show only in the app, opened from this machine.", { kind: "page", title: "Settings" }));
    const hive = hiveSection(ctx), [everyday, setOnce] = Array.isArray(hive) ? hive : [hive, null];
    const secs = [everyday, loginsSection(ctx), useSection(), ...setupSections(ctx), secretsSection(ctx), viewSection(ctx), setOnce, modulesSection(ctx), accessSection(ctx)].filter(Boolean);
    const header = pageHeader({ title: "Settings" });
    if (!secs.length) return form({ header, sections: [empty("This connection can't change any settings.")] });
    const nav = secs.map(n => n.getAttribute("aria-label")), dots = navDots(ctx);
    // One section at a time (#settings/<slug>); #settings, or a slug nobody can see, shows the first. The nav is real links, so Back works.
    const slugOf = t => t.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
    const now = nav.find(t => slugOf(t) === ctx.arg) || nav[0];
    const item = t => h("a", { href: href("settings", slugOf(t)), "data-key": t, "aria-current": t === now ? "page" : null, title: dots[t] ? dots[t][1] : null }, h("span", null, t), dots[t] ? statusDot(dots[t][0]) : null, dots[t] ? h("span", { class: "vh" }, dots[t][1]) : null);
    return h("div", { class: "page set", "data-width": "narrow", "data-key": "settings" }, header,
      h("div", { class: "set-b" },
        h("nav", { class: "set-n", "aria-label": "Settings sections", "data-key": "nav" }, nav.map(t => t === "Set once" ? [h("hr", { class: "set-rule" }), item(t)] : item(t))),
        h("div", { class: "set-s" }, secs.filter(n => n.getAttribute("aria-label") === now))),
      S.app.install ? h("p", { class: "meta", "data-key": "install" }, "Install code ", h("code", { title: "This Hive's id in the reports it sends" }, S.app.install)) : null);
  },
};
