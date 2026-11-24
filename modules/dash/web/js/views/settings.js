// Settings: Everyday, Accounts, Buying, Power and sandbox, Data sharing, Secrets, This device, Set once, Connectors, Modules, Access from outside (R7). Shown only where can() allows each part.
import { status as st } from "../core/status.js";
import { h, s } from "../core/dom.js";
import { S, can, built, jget, needsAccount, post, loadApp, said, fullEnded, unreached, kept, keep, KEPT_NOTE } from "../core/store.js";
import { simpleOn, setSimple } from "../core/simple.js";
import { stillOn, setStill } from "../core/still.js";
import { oneKeyOn, setOneKey } from "../core/onekey.js";
import { deviceFingerprint } from "../core/sign.js";
import { canInstall, installApp, notifyBlocked, notifyOn, notifySupported, notifyTest, onInstallChange, pushOn, pushSupported, setNotify, setPush, standalone } from "../core/notify.js";
import { DEFAULT_NAME, brandLogo, brandName } from "../core/brand.js";
import { form } from "../templates.js";
import { LANGS, chooseLang, getLang } from "../core/lang.js";
import { pageHeader, section, block, blocks, button, field, fieldErr, infoButton, input, invalid, empty, meter, statusLabel, staleNote, statusDot, waiting } from "../ui/kit.js";
import { act, isBusy } from "../ui/activity.js";
import { confirm } from "../ui/dialog.js";
import { tiersSection, loadTiers, pausedTiers } from "../ui/worktiers.js";
import { startSignin, signinBox, clearSignin } from "../ui/signin.js";
import { ago, dur, now, plural, fix } from "../core/format.js";
import { accountUse, threadTitle } from "../core/model.js";
import { href, threadHref } from "../core/router.js";
import { copyButton } from "../ui/refs.js";
import { MODELS, POWER, accountsData, codexPart, forget, keysPart, loadPage, modelPart, pageData, powerPart, reading, rootNote, sandboxPart, sharePart } from "../ui/setup.js";
import { forgetSecrets, secretParts, secretsLost } from "./secrets.js";
import { kbWipeSection } from "../ui/kbwipe.js";
import { buyingSection, loadBuying, forgetBuying } from "../ui/buying.js";

// Everything the person is typing lives here, so a poll never loses it.
// T: words typed here and not saved yet (the style, the Hive name). enter() clears F; T lasts the session, so coming back to Settings shows them
// again instead of asking for them a second time (WCAG 3.3.7). A value the Hive now holds drops out of T when the page is built.
const T = {};
const F = { bad: {}, badIn: {}, cfg: null, ed: null, mods: null, say: {}, newLogin: "", url: "", busy: {}, access: null, saved: null, flash: {}, was: {}, lost: false, kept: {} };
// A switch that saved says so beside it for 1.5 s.
const flash = (k, update) => { F.flash[k] = Date.now(); setTimeout(() => { if (Date.now() - (F.flash[k] || 0) >= 1400) { delete F.flash[k]; update(); } }, 1500); };
const savedMark = k => F.flash[k] ? h("span", { class: "blk-saved field-ok", role: "status" }, "✓ Saved") : null;
const say = (k, text, tone) => { F.say[k] = text ? { text, tone } : null; };
// Beside a box that came back with what was typed before the sign-in: it saves when the box is left, as any change does.
const keptNote = k => F.kept[k] && !F.bad[k] ? h("p", { class: "field-h kept-note", role: "status", "data-key": "kept-" + k }, KEPT_NOTE + " Click in the box, then leave it, to save.") : null;
const msg = k => F.say[k] ? h("p", { class: "fmsg", "data-tone": F.say[k].tone || null, role: "status" }, F.say[k].text) : null;
const CAPTEXT = { read: "Read everything", files: "Read files: notes, modules, scripts, projects", post: "Post as you, which starts agents", stop: "Kill agents", tier: "Change work tiers: pause, move, stop points", limits: "Pause logins and change stop lines", logins: "Sign in and add logins", config: "Change settings", modules: "Add and remove modules", plans: "Edit project plans", secrets: "Store secrets" };

async function loadCfg(ctx) {
  if (!can("config")) return;
  try { F.cfg = await jget("/api/config"); F.cfgErr = null; F.cfgRead = true; if (F.say.hive && F.say.hive.held) say("hive", ""); }
  catch (e) {
    F.cfg = F.cfg || {}; F.cfgErr = e.message;
    // No defaults on a failed read: a toggle would save them over the real values. Values read earlier this session are real, so the
    // page keeps showing them (the Hive went away mid-session), and says they couldn't be refreshed.
    if (!F.cfgRead) { ctx.update(); return; }
    F.say.hive = { text: `Couldn't refresh the settings (${e.message}). These are from the last read.`, tone: "bad", held: true };
  }
  const fresh = !F.ed;
  F.ed ||= { max: F.cfg.max_sessions ?? "", kpe: F.cfg.keeper_every_s || 10800, au: F.cfg.auto_update !== false, bud: F.cfg.max_budget_usd ?? "", day: F.cfg.max_daily_usd ?? "", style: F.cfg.answer_style || "" };
  F.saved ||= { max: F.ed.max, bud: F.ed.bud, day: F.ed.day, style: F.ed.style };
  // A number typed when full mode ended comes back after the sign-in, in its box and marked not saved (store.js keep).
  if (fresh && T.style != null) { if (T.style.trim() !== String(F.saved.style).trim()) F.ed.style = T.style; else delete T.style; }
  if (fresh && can("config")) for (const k of ["max", "bud", "day"]) { const v = kept("set." + k); if (v != null && String(v) !== String(F.saved[k])) { F.ed[k] = v; F.kept[k] = true; } else if (v != null && String(v) === String(F.saved[k])) keep("set." + k, null); }
  ctx.update();
}
let upTimer = null;
/** Keeps the Version block current while Settings is open: a release found or installed in the background shows without a reload. */
function watchUpdate(ctx) {
  clearInterval(upTimer);
  upTimer = setInterval(() => { if (!isBusy("up-check") && !isBusy("up-install")) loadUpdate(ctx); else ctx.update(); }, 20e3);
}
async function loadUpdate(ctx) {
  if (!can("config")) return;
  try { F.up = await jget("/api/update"); } catch (e) { if (!F.up && F.upLast) { F.up = F.upLast; ctx.update(); } return false; }   // an update restarts the app, so a poll can miss; a failed first read on coming back shows the last one (the Hive went away mid-session)
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
      msg("up"),
      h("p", { class: "meta", "data-key": "repo-link" }, "Code, issues and updates are on ", h("a", { href: "https://github.com/ihsan-sa/hive", target: "_blank", rel: "noreferrer noopener" }, "GitHub"), ". A star there helps others find it.")),
    block({ title: "Look for new releases", sub: "How often this Hive looks. It installs what it finds if Update by itself is on.",
      actions: h("select", { class: "input", value: every, "aria-label": "Look for new releases", title: "Look for new releases", onchange: e => { const was = U.update_every_s, v = +e.target.value; U.update_every_s = v; save({ update_every_s: v }, () => { U.update_every_s = was; }); } },
        opts.map(([v, label]) => h("option", { value: v }, label))) }),
    block({ title: "How often to update", sub: "Every release installs each one as soon as it's out. About once a day waits until a release has been out for 12 hours and installs at most one a day. Update now always takes the newest.",
      actions: h("select", { class: "input", value: U.update_pace, "aria-label": "How often to update", title: "How often to update", onchange: e => { const was = U.update_pace, v = e.target.value; U.update_pace = v; save({ update_pace: v }, () => { U.update_pace = was; }); } },
        [["every", "Every release"], ["daily", "About once a day"]].map(([v, label]) => h("option", { value: v }, label))) }),
  ];
}
async function loadMods(ctx) {
  if (!can("modules")) return;
  try { F.mods = await jget("/api/modules"); F.modsErr = null; } catch (e) { F.modsErr = e.message; }
  ctx.update();
}

/** Once, at a section's foot: whether its changes need a Save. */
const foot = text => h("p", { class: "field-h set-foot", "data-key": "saves" }, text);
const check = (label, on, onChange, hint) => h("label", { class: "check" }, h("input", { type: "checkbox", checked: on, onchange: (e, n) => onChange(n.checked) }), h("span", null, label, hint ? h("span", { class: "field-h" }, hint) : null));

// Kept in this browser, like the theme: another person signed in elsewhere keeps theirs.
function viewSection(ctx) {
  const on = simpleOn();
  return section({ title: "This device", note: "this browser only", level: 1 },
    blocks(block({ title: "Simple view", key: "simple", sub: on ? "On: you see a chat, and your Hive talks to you in plain words." : "Off: you see every tab, and what the agents say to each other. Turn it on for just a chat and what's been made, with agents told to use plain words.",
      actions: button(on ? "Show everything" : "Turn on simple view", () => { setSimple(!on); ctx.update(); }, { size: "s" }) }),
    block({ title: "Still screen", key: "still", sub: stillOn() ? "On: nothing moves by itself, not the live dots, the Map's rings or the loading shimmer." : "Off: live agents breathe, the Map shows each post, and loading shimmers. Turn it on to stop everything that moves by itself.",
      actions: button(stillOn() ? "Let things move" : "Stop all motion", () => { setStill(!stillOn()); ctx.update(); }, { size: "s" }) }),
    langBlock(), keysBlock(ctx), voiceBlock(ctx), installBlock(ctx), phoneLinkBlock(), notifyBlock(ctx), pushBlock(ctx), phoneBlock(ctx), signBlock(ctx)));
}
// Voice calls and memos: where they stand on this install (dash.py voice_view, projects/everyday/VOICE.md V1). The build runs in the
// background after install, so this says Setting up, Didn't finish (with Try again) or Not on this install (with why) instead of a Call
// button that is simply missing. Shown with the modules cap, which Try again needs: it starts a multi-GB build.
let voiceTimer = null;
async function loadVoice(ctx) {
  if (!can("modules")) return;
  try { F.voice = await jget("/api/voice"); } catch (e) { F.voice = { failed: e.message }; }
  clearTimeout(voiceTimer); voiceTimer = null;
  if (["building", "starting"].includes(F.voice.state)) voiceTimer = setTimeout(() => loadVoice(ctx), 5000);
  ctx.update();
}
async function voiceRetry(ctx) {
  F.busy.voice = true; ctx.update();
  try { await post("/api/voice", { do: "retry" }); say("voice", "Started. It takes a few minutes; this line follows it.", "good"); }
  catch (e) { say("voice", e.message, "bad"); }
  F.busy.voice = false; await loadVoice(ctx);
}
const VOICE_WHY = {
  "own-account": "your Hive lives in a folder of your own account, so it doesn't start a multi-GB speech build there by itself. Ask an agent to set up voice calls if you want them.",
  wsl: "Windows (WSL) can't run the call service yet.",
  "not-linux": "voice calls need Linux.",
  "no-service": "this Hive was installed without its background service, which runs the calls.",
  off: "it was turned off when this Hive was installed.",
  older: "this Hive was installed before voice came with new installs.",
  removed: "the voice modules were removed.",
  "failed-add": "the voice modules didn't install.",
};
function voiceBlock(ctx) {
  const V = F.voice;
  if (!can("modules") || !V) return null;
  const again = label => button(label, () => voiceRetry(ctx), { size: "s", disabled: !!F.busy.voice });
  const mic = window.isSecureContext ? null : h("p", { class: "field-h", "data-key": "voice-mic" }, "On this address the browser won't lend the page a microphone: that needs https, or opening your Hive on the computer it runs on (localhost).");
  const log = V.log ? h("pre", { class: "voice-log", "data-key": "voice-log" }, V.log) : null;
  const since = V.since ? ago(V.since * 1000) : "";
  let state, sub, actions = null, body = [];
  if (V.failed) { state = "Unknown"; sub = `Couldn't read whether voice calls are set up: ${V.failed}.`; actions = button("Try again", () => loadVoice(ctx), { size: "s" }); }
  else if (V.state === "ready") {
    state = "Ready";
    sub = V.memos ? "Talk to your Hive: Voice call and Voice memo are in the + menu of any chat. Your voice is turned into words on this machine and never leaves it."
      : `Voice call is in the + menu of any chat. Voice memos aren't ready yet${V.ffmpeg ? "" : ": they need ffmpeg installed on the machine"}.`;
  } else if (V.state === "building") {
    state = "Setting up";
    sub = `Building speech recognition and a voice on this machine${since ? `, started ${since}` : ""}. It takes a few minutes; Voice call shows in the + menu when it's done.`;
    body = [log];
  } else if (V.state === "starting") { state = "Setting up"; sub = "Built. The call service is starting."; }
  else if (V.state === "failed" || V.state === "stopped") {
    state = "Didn't finish";
    sub = V.state === "stopped" ? "It was built, but the call service isn't answering." : V.rc ? "The build stopped with an error. The last lines of its log:" : "The build stopped before it finished (the machine restarted, or it was stopped). The last lines of its log:";
    actions = again("Try again"); body = [log];
  } else if (V.state === "unset") { state = "Not set up"; sub = "The voice modules are here, but speech recognition and the voice aren't built yet."; actions = again("Set up voice"); }
  else {
    state = "Not on this install";
    const why = V.why === "small" ? `this machine had too little room: it needs ${V.need_gb} GB of free disk and 3 GB of memory, and had ${V.free_gb} GB and ${V.mem_gb} GB.` : VOICE_WHY[V.why] || "it wasn't set up.";
    sub = "Voice calls aren't on this install: " + why;
    if (V.can_retry) actions = again("Set up voice");
  }
  const chip = h("span", { class: "chip", "data-key": "voice-state", "data-state": V.state || "failed", "data-tone": { ready: "good", building: "warn", starting: "warn", failed: "bad", stopped: "bad" }[V.state] || null }, state);
  return block({ title: "Voice calls", key: "voice", sub, live: !!V.failed, ...(actions ? { actions: [chip, actions] } : { state: chip }) },   // the head grid has one column for both
    ...body, mic, msg("voice"));
}
// The interface language (core/lang.js): the app's own words; agents already answer in whatever language you write.
function langBlock() {
  const cur = getLang();
  return block({ title: "Language", key: "lang", sub: "The words on the screens. Agents answer in the language you write in, whatever this says.",
    actions: LANGS.map(([code, name]) => { const b = button(name, () => chooseLang(code), { size: "s", ...(code === cur ? { kind: "primary" } : {}) }); b.lang = code; return b; }) });   // each name in its own language, marked so (WCAG 3.1.2)
}
// One-key shortcuts (WCAG 2.1.4, core/onekey.js): ?, F and / act on one key press with nothing held; this turns them off here.
function keysBlock(ctx) {
  const on = oneKeyOn(), label = "One-key shortcuts";
  return block({ title: label, key: "onekey",
    sub: on ? "On: ? lists the shortcuts, F goes full screen, and / jumps to search in Knowledge. Turn them off if they go off by accident, for example when you talk to the page." : "Off: ?, F and / type nothing special. Shortcuts with Ctrl or ⌘ still work.",
    actions: [savedMark(label), h("input", { type: "checkbox", role: "switch", class: "switch", "aria-label": label, checked: on,
      onchange: (e, n) => { setOneKey(n.checked); flash(label, ctx.update); ctx.update(); } })] });
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
  if (P.failed) return block({ live: true, title: "Phone alerts", key: "phone", sub: `Couldn't read whether phone alerts are on: ${P.failed}.`, actions: button("Try again", () => loadPhone(ctx), { size: "s" }) });
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
// Open on your phone (E122): a QR code for the address a phone can actually reach (dash.py phone_link: the outside address, else the
// tailnet name), drawn black on white in any theme so a camera reads it. With neither set, plain words and a way to Access from outside.
function qrSvg(rows, label) {
  const n = rows.length, q = 4, d = rows.flatMap((r, y) => [...r].flatMap((c, x) => c === "1" ? [`M${x + q} ${y + q}h1v1h-1z`] : [])).join("");
  return s("svg", { class: "qr", viewBox: `0 0 ${n + 2 * q} ${n + 2 * q}`, width: 200, height: 200, role: "img", "aria-label": label, "shape-rendering": "crispEdges", "data-key": "qr" },
    s("rect", { width: n + 2 * q, height: n + 2 * q, fill: "#fff" }), s("path", { d, fill: "#000" }));
}
function phoneLinkBlock() {
  const L = S.app.phone_link;
  if (S.app.phone_link === undefined) return null;   // an older server, or the open dash
  if (!L) return block({ title: "Open on your phone", key: "phone-link",
    sub: "Your phone can't open this Hive yet: only this computer can. It needs an address the phone can reach, either a name on the internet set up under Access from outside, or this computer on your tailnet (Tailscale).",
    actions: S.app.route === "local" && S.app.outside ? h("a", { class: "btn", "data-size": "s", href: href("settings", "access-from-outside"), "data-key": "phone-link-go" }, "Access from outside") : null });
  return block({ title: "Open on your phone", key: "phone-link",
    sub: L.via === "outside" ? "Point your phone's camera at the code. It opens your Hive at its internet address, where you sign in as set up under Access from outside."
      : "Point your phone's camera at the code. This address works only on your tailnet: the phone needs the Tailscale app signed in, and to be one of the devices let in." },
    h("div", { class: "qr-box" }, qrSvg(L.qr, "QR code"), h("p", null, h("code", { "data-key": "phone-link-url" }, L.url), " ", copyButton(L.url))));
}
// Add to home screen (E56c): how to put the Hive on a phone's home screen, so it opens full screen like an app. Hidden once it is running
// that way. The Install app button shows only where the browser said it can install the page (beforeinstallprompt, core/notify.js).
function installBlock(ctx) {
  if (standalone()) return null;
  onInstallChange(() => ctx.update());
  // E56f: only the line for the phone in use (an iPad says Macintosh but has touch); a computer gets both.
  const ua = navigator.userAgent || "", os = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1) ? "ios" : /Android/.test(ua) ? "android" : "";
  return block({ title: "Add to home screen", key: "install", sub: "Put your Hive on your home screen and it opens full screen, like an app.",
    actions: canInstall() ? button("Install app", async () => { await installApp().catch(() => false); ctx.update(); }, { size: "s" }) : null },
    os !== "android" ? h("p", { class: "field-h", "data-key": "install-ios" }, "iPhone or iPad: in Safari, tap Share, then Add to Home Screen.") : null,
    os !== "ios" ? h("p", { class: "field-h", "data-key": "install-android" }, "Android or Chrome: open the browser menu, then Install app.") : null);
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
  if (P.failed) return block({ live: true, title: "Push notifications", key: "push", sub: `Couldn't read whether push is on for this browser: ${P.failed}.`, actions: button("Try again", () => loadPush(ctx), { size: "s" }) });
  return block({ title: "Push notifications", key: "push",
    sub: P.on ? `On for this browser: it hears from your Hive even with this page closed. An alert names the conversation and gives the first words of the reply, locked so only this browser can read them; the push service passes it on unread.`
      : `Off for this browser. Turn it on to hear from your Hive with this page closed. An alert names the conversation and gives the first words of the reply. It goes through your browser's push service (Google, Mozilla, Apple or Microsoft), locked so only this browser can read it.`,
    actions: button(P.on ? "Turn off" : "Turn on push", () => pushDo(ctx, !P.on), { size: "s" }) }, msg("push"));
}
// The name saves when you leave the box; a picture saves as soon as it's picked. Every tab and page picks them up from /api/app.
const LOGO_MAX = 2 * 2 ** 20;
function brandBlocks(ctx) {
  if (T.name != null && T.name.trim() === (brandName() || "")) delete T.name;
  F.name ??= T.name ?? brandName() ?? "";
  const done = async (work, ok, mark) => {
    try { await work(); await loadApp(); if (mark) { say("brand", ""); flash(mark, ctx.update); } else say("brand", ok, "good"); if (T.name == null || T.name.trim() === (brandName() || "")) { delete T.name; F.name = brandName() || ""; } }   // the box shows what the Hive now holds; words it didn't take stay
    catch (e) { say("brand", e.message, "bad"); }   // a refused save keeps the typed name in the box
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
      actions: [savedMark("Name"), input({ type: "text", maxlength: 40, placeholder: DEFAULT_NAME, "aria-label": "Hive name", value: F.name, oninput: (e, n) => { F.name = T.name = n.value; }, onchange: saveName, onfocusout: saveName })] }),
    block({ title: "Logo", sub: "Shown next to the name and as the tab's icon. PNG, JPEG, GIF, WebP or SVG, up to 2 MB. A square picture works best.",
      actions: [logo ? h("img", { class: "brand-logo set-logo", src: logo, alt: "Current logo", "data-key": "preview" }) : h("span", { class: "set-logo set-logo-none", "aria-hidden": "true" }), file,
        button(logo ? "Replace" : "Upload", (e, n) => n.parentNode.querySelector(".brand-pick").click(), { size: "s" }),
        logo ? button("Remove", () => done(() => post("/api/brand", { logo: null }), "Logo removed."), { size: "s", kind: "quiet" }) : null] })];
}

function hiveSection(ctx) {
  // Full mode can end while a number is being typed (dash.py FULL_IDLE_S): the section stays, with what was typed and the way back beside it (WCAG 3.3.1).
  if (!can("config") && !(F.lost && F.ed)) return null;
  if (!F.ed) return section({ title: "Everyday", note: "what you change in a normal week; changes save as you make them", level: 1 }, F.cfgErr ? [empty(`Couldn't read the settings: ${F.cfgErr}`, { kind: "error" }), button("Try again", () => { F.cfgErr = null; ctx.update(); return loadCfg(ctx); }, { size: "s" })] : empty("Reading…", { kind: "loading" }));
  const E = F.ed, up = S.app.setup && S.app.setup.starter, brand = brandBlocks(ctx);
  // Each control saves on its own and sends only its own field; a refusal puts the old value back.
  // key: the number box it came from. If full mode ended, the box keeps what was typed and says so beside itself; the next save tries again.
  const save = async (fields, undo, mark, key) => {
    try { await post("/api/config", fields); F.lost = false; if (key) { keep("set." + key, null); F.kept[key] = false; } if (mark) { say("hive", ""); flash(mark, ctx.update); } else say("hive", "Saved. The hive uses it within seconds.", "good"); }
    catch (e) {
      if (key && fullEnded(e)) { F.lost = true; F.saved[key] = F.was[key]; F.bad[key] = e.message; keep("set." + key, String(F.ed[key])); }
      // No network: the typed number stays in the box with the reason beside it, and leaving the box again saves it (WCAG 3.3.1).
      else if (key && unreached(e)) { F.saved[key] = F.was[key]; F.bad[key] = "Couldn't reach the Hive, so this wasn't saved. What you typed is kept: click in the box, then leave it, to save once you're back online."; keep("set." + key, String(F.ed[key])); }
      else { undo(); say("hive", e.message, "bad"); }
    }
    ctx.update();
  };
  const toggle = (label, on, onChange) => [savedMark(label), h("input", { type: "checkbox", role: "switch", class: "switch", "aria-label": label, checked: on, onchange: (e, n) => onChange(n.checked) })];
  const saveMax = () => {
    const was = F.saved.max, v = F.badIn.max ? "?" : String(E.max).trim();   // a number box holding letters reads as empty: that's not "no limit"
    if (v === String(was)) { if (F.kept.max) { F.kept.max = false; keep("set.max", null); } if (F.bad.max) { F.bad.max = ""; ctx.update(); } return; }
    // A bad number stays in the box, marked, with the reason beside it (WCAG 3.3.1); nothing is saved until it's fixed or emptied.
    if (v !== "" && !(Number.isInteger(+v) && +v >= 1 && +v <= 200)) { F.bad.max = "Agents at once is a whole number from 1 to 200, or empty for no limit."; ctx.update(); return; }
    F.bad.max = "";
    F.saved.max = v; F.was.max = was;
    save({ max_sessions: v === "" ? null : +v }, () => { E.max = F.saved.max = was; }, "Agents at once", "max");
  };
  const saveBud = () => {
    const was = F.saved.bud, v = F.badIn.bud ? "?" : String(E.bud).trim();   // a number box holding letters reads as empty: that's not "no limit"
    if (v === String(was)) { if (F.kept.bud) { F.kept.bud = false; keep("set.bud", null); } if (F.bad.bud) { F.bad.bud = ""; ctx.update(); } return; }
    if (v !== "" && !(+v > 0 && +v < 10000)) { F.bad.bud = "The most one run may spend is a number of dollars above 0 and under 10,000, or empty for no cap."; ctx.update(); return; }
    F.bad.bud = "";
    F.saved.bud = v; F.was.bud = was;
    save({ max_budget_usd: v === "" ? null : +v }, () => { E.bud = F.saved.bud = was; }, "Budget per run", "bud");
  };
  const saveDay = () => {
    const was = F.saved.day, v = F.badIn.day ? "?" : String(E.day).trim();   // a number box holding letters reads as empty: that's not "no limit"
    if (v === String(was)) { if (F.kept.day) { F.kept.day = false; keep("set.day", null); } if (F.bad.day) { F.bad.day = ""; ctx.update(); } return; }
    if (v !== "" && !(+v > 0 && +v < 100000)) { F.bad.day = "The daily cap is a number of dollars above 0 and under 100,000, or empty for no cap."; ctx.update(); return; }
    F.bad.day = "";
    F.saved.day = v; F.was.day = was;
    save({ max_daily_usd: v === "" ? null : +v }, () => { E.day = F.saved.day = was; }, "Daily cap", "day");
  };
  // E33: how the person wants answers, given to every new chat (the starter adds ~/answer-style.md to each agent's instructions).
  const saveStyle = () => {
    const was = F.saved.style, v = String(E.style).trim();
    if (v === String(was).trim()) return;
    if (v.length > 2000) { say("hive", "How you want answers fits in 2000 characters.", "bad"); ctx.update(); return; }
    F.saved.style = v;
    save({ answer_style: v }, () => { F.saved.style = was; }, "How I want answers");   // a refused save keeps what was typed; the next focus-out tries again
  };
  // E39b (projects/everyday MEMORY.md): what new chats are told about the person. core/memory.py writes ~/memory.md; here it's read, deleted and turned off.
  const mem = F.cfg.memory || {};
  const memSet = async (fields, label) => {
    try { const c = await act("memory", label, () => post("/api/config", fields), ctx.update); if (c && c.memory) F.cfg.memory = c.memory; say("memory", ""); if (fields.memory_on != null) flash("Use memory in new chats", ctx.update); }
    catch (e) { say("memory", e.message, "bad"); loadCfg(ctx); }
    ctx.update();
  };
  const memWhere = i => i.chat === "settings" ? "added in Settings" : i.gone ? "chat deleted" : h("a", { href: threadHref(i.chat) }, i.title || threadTitle(i.chat) || "the chat");
  const memDay = d => { const t = new Date(d + "T12:00:00"); return isNaN(t) ? d : t.toLocaleDateString(undefined, { day: "numeric", month: "short", year: t.getFullYear() === new Date().getFullYear() ? undefined : "numeric" }); };
  const DELETE_SAYS = "Removed from memory: new chats won't be given it. The chat where you said it still has it, and an agent can still find it there.";
  const memN = (mem.items || []).length;
  const memory = !mem.available ? null : block({ title: "Memory", sub: mem.on ? "New chats are given what you told other chats, like \"I'm vegetarian\". A chat says \"Saved to memory\" when it keeps something." : "Off: new chats aren't given these, and nothing new is kept. Turn it on to use them again.",
      key: "memory", actions: toggle("Use memory in new chats", !!mem.on, v => memSet({ memory_on: v }, v ? "Turning memory on" : "Turning memory off")) },
    memN ? h("ul", { class: "list mem-list", "aria-label": "What new chats are told about you" }, mem.items.map(i => h("li", { class: "lr", "data-key": "mem-" + i.id },
      h("div", { class: "lr-in frow fsplit" }, h("span", { class: "mem-t" }, i.text, h("span", { class: "lr-s" }, " · ", memDay(i.date), " · ", i.how === "asked" ? "you asked" : "kept by the agent", " · ", memWhere(i))),
        h("span", { class: "frow-end" }, button("Delete", () => memSet({ memory_forget: [i.id] }, "Deleting from memory"), { size: "s", kind: "quiet", busy: isBusy("memory"), title: "Delete: " + i.text })))))) :
      h("p", { class: "field-h" }, mem.on ? "Nothing yet. Tell a chat \"remember that…\" and it shows here." : "Nothing kept."),
    memN ? h("p", { class: "field-h" }, "Delete: " + DELETE_SAYS) : null,
    memN ? h("div", { class: "frow" }, button("Delete all…", () => confirm({ title: "Delete all of memory?", body: [`All ${plural(memN, "line")} go: new chats won't be given any of them. The chats where you said them still have them, as does the Hive's backup, and an agent can still find them there.`],
      ok: "Delete all", kind: "danger", onOk: () => memSet({ memory_forget: "all" }, "Deleting all of memory") }), { size: "s", kind: "danger", busy: isBusy("memory"), title: "Delete all of memory" })) : null,
    msg("memory"));
  const spent = `Spent today: $${fix((+(F.cfg.spend_today || 0)), 2)} at API prices (UTC day). `;
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
  const everyday = section({ title: "Everyday", note: "what you change in a normal week; changes save as you make them", level: 1, actions: statusLabel(up ? "working" : "ended", up ? "running" : "not running") },
    blocks(
      block({ title: "How I want answers", sub: "Every chat follows this from its next reply, like \"short answers, metric units, no bullet lists\". Saves when you leave the box.", key: "answer-style", actions: savedMark("How I want answers") },
        h("textarea", { class: "input set-style", rows: 3, maxlength: 2000, "aria-label": "How I want answers", placeholder: "Anything every answer should follow", value: E.style,
          oninput: (e, n) => { E.style = T.style = n.value; }, onchange: saveStyle, onfocusout: saveStyle })),
      memory,
      // E34 (GAPS row 19): every chat as plain text with its files, in one zip, made by the server (/api/export).
      can("config") ? block({ title: "Download my chats", sub: "Every chat as plain text with its files, in one zip. A big Hive takes a minute.", key: "export",
        actions: button("Download", null, { size: "s", href: "/api/export", icon: "download", title: "Download my chats" }) }) : null,
      block({ title: "Agents at once", sub: "Empty means no limit. Saves when you leave the box.",
        actions: [savedMark("Agents at once"), input({ type: "number", min: 1, max: 200, placeholder: "no limit", "aria-label": "Agents at once", value: E.max, ...invalid("err-max", F.bad.max), oninput: (e, n) => { E.max = n.value; F.badIn.max = n.validity.badInput; }, onchange: saveMax, onfocusout: saveMax })] },
        fieldErr("err-max", F.bad.max), keptNote("max")),
      // Always on: picking up work that stopped is the Hive's promise, not a switch (the person, 2026-11-08, post 110818074c18). Only the pace is chosen.
      block({ title: "Keep work moving", sub: `${paceSay(E.kpe)}, pick up work that stopped and start work nobody asked for, and sooner once nothing has run for half an hour. It uses your limits.`,
        actions: [h("select", { class: "input", value: E.kpe, "aria-label": "How often to keep work moving", title: "How often to keep work moving",
            onchange: e => { const was = E.kpe, v = +e.target.value; E.kpe = v; save({ keeper_every_s: v }, () => { E.kpe = was; }, "Keep work moving"); } },
            paceOpts(E.kpe).map(([v, label]) => h("option", { value: v }, label)))] }),
      block({ title: up ? "Stop the Hive" : "Start the Hive", sub: up ? "Stopping lets agents finish their turn." : "The Hive isn't running.", key: "starter", actions: button(up ? "Stop the Hive…" : "Start the Hive", () => up ? confirm({ title: "Stop the Hive?", body: ["Agents finish their turn, and nothing new starts until you start it again here."], ok: "Stop the Hive", kind: "danger", onOk: starter }) : starter(), { size: "s", kind: up ? "danger" : "primary", busy: isBusy("starter"), title: up ? "Stop the Hive" : "Start the Hive" }) })),
    msg("starter"), msg("hive"));
  const setOnce = section({ title: "Set once", note: "the name, the model, updates and spending caps; changes save as you make them", level: 1 },
    pg ? [h("h3", { class: "set-sub", "data-key": "model-h" }, "Model"), modelPart(ctx.update)] : null,
    blocks(brand[0], brand[1],
      versionBlocks(ctx, save),
      block({ title: "Update by itself", sub: F.up && F.up.installed ? "Installs new releases as often as you chose above. Keeps your settings and goes back if it fails. When it's off, this Hive still looks and says so above, and you choose when to update." : "Installs each new release. Keeps your settings and goes back if it fails.",
        actions: toggle("Update by itself", E.au, v => { E.au = v; save({ auto_update: v }, () => { E.au = !v; }, "Update by itself"); }) }),
      block({ title: "Budget per run", sub: E.bud === "" ? "Empty means no cap. A cap stops a run that reaches it (counted at API prices) and says so in its thread. Applies from each run's next start." : `Each run stops at $${E.bud}, counted at API prices, and says so in its thread. Applies from each run's next start. Empty removes the cap.`,
        actions: [savedMark("Budget per run"), input({ type: "number", min: 0, step: "any", placeholder: "no cap", "aria-label": "Budget per run in dollars", value: E.bud, ...invalid("err-bud", F.bad.bud), oninput: (e, n) => { E.bud = n.value; F.badIn.bud = n.validity.badInput; }, onchange: saveBud, onfocusout: saveBud })] },
        fieldErr("err-bud", F.bad.bud), keptNote("bud")),
      block({ title: "Daily cap", sub: spent + (E.day === "" ? "Empty means no cap. A cap holds new agents from starting once a day's spend reaches it; running ones finish." : `No new agents start once a day's spend reaches $${E.day}; running ones finish. Empty removes the cap.`),
        actions: [savedMark("Daily cap"), input({ type: "number", min: 0, step: "any", placeholder: "no cap", "aria-label": "Daily cap in dollars", value: E.day, ...invalid("err-day", F.bad.day), oninput: (e, n) => { E.day = n.value; F.badIn.day = n.validity.badInput; }, onchange: saveDay, onfocusout: saveDay })] },
        fieldErr("err-day", F.bad.day), keptNote("day"))),
    msg("brand"), msg("hive"));
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
      h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.newLogin.trim()) { say("newlogin", null); F.bad.login = ""; startSignin("login-new", F.newLogin.trim(), false, ctx.update, () => { F.newLogin = ""; }); } else { F.bad.login = "Give it a name first, like work."; ctx.update(); } } },
        h("div", { class: "frow-grow", "data-size": "s" }, field("Add another account", input({ placeholder: "a name for it, like work", value: F.newLogin, ...invalid("err-login", F.bad.login), oninput: (e, n) => { F.newLogin = n.value; } }))),
        button("Sign in to it", null, { type: "submit" })),
      fieldErr("err-login", F.bad.login), msg("newlogin"),
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
    section({ title: "Power and sandbox", level: 1, note: "who decides what agents may do, and what holds them to it; changes save as you make them" }, pg ? [sandboxBroken(pg), powerPart(ctx.update), h("h3", { class: "set-sub", "data-key": "sandbox" }, "Sandbox"), sandboxPart(ctx.update)] : reading()),
    section({ title: "Data sharing", level: 1, note: "what's sent, and whether it is; changes save as you make them" }, pg ? [sharePart(ctx.update, true)] : reading())];
}
const secretsSection = ctx => can("secrets") || secretsLost() ? section({ title: "Secrets", level: 1, note: "send a token to the Hive; it's never shown back" }, h("div", { class: "stack secrets" }, secretParts(ctx))) : null;

// ---- Connectors (projects/everyday/CONNECTORS.md, E55): the reach bundle's connectors, one row each, read from GET /api/connectors. ----
// Nothing here turns a connector on: no route runs `conn`, and a two-way one (it talks back into the Hive) has no On at all until it runs
// under a separate account (warden ruling). The only writes are Add/Remove of `reach` (/api/module) and a pasted token (/api/secret).
async function loadConns(ctx, fresh) {
  if (!can("modules")) return;
  try { F.conns = await jget("/api/connectors" + (fresh ? "?fresh=1" : "")); F.connsErr = null; } catch (e) { F.connsErr = e.message; }
  try { F.mcpUrls = await jget("/api/mcp-url"); } catch (e) { F.mcpUrls = null; }
  ctx.update();
}
// A connector added by its address (E120a): a remote MCP server, as Claude's custom connectors. The list and mcp.json are dash.py's mcp_url_do;
// the token goes into mcp.json for the agents and is never sent back, so a row says only that it has one.
function mcpUrlBlock(ctx) {
  const M = F.mcpUrls, A = F.mcpAdd ||= { name: "", url: "", token: "" };
  if (!M || !Array.isArray(M.servers)) return null;   // the consumer preview answers without a list (sweep 11-23: "reading length" crashed Settings)
  const run = async (body, key, done) => {
    F.busy[key] = true; ctx.update();
    try { F.mcpUrls = await post("/api/mcp-url", body); if (done) done(); say("mcpurl", body.do === "add" ? "Added. Agents can use it from their next run." : "Removed.", "good"); } catch (e) { say("mcpurl", e.message, "bad"); }
    delete F.busy[key]; ctx.update();
  };
  // E129: the dash runs the server's sign-in and keeps the token for the agents; this tab goes to the server's page and comes back to Settings
  const signin = async name => {
    F.busy["mcpin-" + name] = true; ctx.update();
    try { const r = await post("/api/mcp-url", { do: "signin", name });
      if (r && typeof r.go === "string" && /^https?:\/\//.test(r.go)) { location.href = r.go; return; }
      if (r && r.need_client && typeof r.redirect === "string") F.mcpNeed = { name, redirect: r.redirect, id: "", secret: "" };   // E135: no self-registration
      else say("mcpurl", "The server sent no sign-in page.", "bad"); }
    catch (e) { say("mcpurl", e.message, "bad"); }
    delete F.busy["mcpin-" + name]; ctx.update();
  };
  // E135: the client ID (and secret) of an app the person registered at the server's maker; the secret goes to dash.py and never comes back
  const saveClient = async () => {
    const N = F.mcpNeed; F.busy["mcpcli"] = true; ctx.update();
    try { F.mcpUrls = await post("/api/mcp-url", { do: "client", name: N.name, client_id: N.id.trim(), client_secret: N.secret }); F.mcpNeed = null; delete F.busy["mcpcli"]; return signin(N.name); }
    catch (e) { say("mcpurl", e.message, "bad"); }
    delete F.busy["mcpcli"]; ctx.update();
  };
  const N = F.mcpNeed && M.servers.some(x => x.name === F.mcpNeed.name) ? F.mcpNeed : null;
  // E137: a listed server that doesn't register clients itself carries a short guide for registering the app (mcp-known.json)
  const G = N ? ((M.known || []).find(k => k.guide && M.servers.some(x => x.name === N.name && x.url === k.url)) || {}).guide : null;
  const need = N ? h("form", { class: "mcp-client", onsubmit: e => { e.preventDefault(); saveClient(); } },
    h("p", { class: "muted" }, "This server needs an app you register yourself on its maker's site. Give that app this return address, then paste its client ID here."),
    h("p", null, h("code", null, N.redirect)),
    G ? h("div", { class: "mcp-guide" }, h("ol", null, G.steps.map(t => h("li", null, t))),
      h("p", null, h("a", { href: G.where, target: "_blank", rel: "noreferrer noopener" }, "Open the setup page"))) : null,
    h("div", { class: "frow" },
      h("div", { class: "frow-grow" }, field("Client ID", input({ value: N.id, autocomplete: "off", oninput: (e, n) => { N.id = n.value; } }))),
      h("div", { class: "frow-grow" }, field("Client secret (if it gave one)", input({ type: "password", value: N.secret, autocomplete: "new-password", oninput: (e, n) => { N.secret = n.value; } }))),
      button("Save and sign in", null, { type: "submit", size: "s", kind: "primary", busy: F.busy["mcpcli"] }))) : null;
  const add = () => run({ do: "add", name: A.name.trim(), url: A.url.trim(), token: A.token }, "mcpurl-add", () => { F.mcpAdd = { name: "", url: "", token: "" }; });
  // E132: official servers from mcp-known.json; picking one sends only its id, and one that needs a sign-in goes straight on to it
  const pick = async k => {
    F.busy["mcpk-" + k.id] = true; ctx.update();
    try { F.mcpUrls = await post("/api/mcp-url", { do: "add", known: k.id }); } catch (e) { say("mcpurl", e.message, "bad"); delete F.busy["mcpk-" + k.id]; ctx.update(); return; }
    delete F.busy["mcpk-" + k.id];
    if (k.signin) return signin(k.id);
    say("mcpurl", "Added. Agents can use it from their next run.", "good"); ctx.update();
  };
  const known = Array.isArray(M.known) && M.known.length ? block({ title: "Known servers", sub: "Official servers from the companies that make them. Pick one to add it; one that needs your account takes you to its sign-in page." },
    h("ul", { class: "list mcp-known-list" }, M.known.map(k => h("li", { class: "lr mcp-known-row", "data-key": k.id },
      h("div", { class: "lr-in" }, h("b", null, k.label), " ", h("span", { class: "meta" }, k.guide ? "needs an app you register" : k.signin ? "needs your account" : "no account needed"), h("p", { class: "muted mcp-known-line" }, k.line)),
      k.added ? h("span", { class: "meta" }, "added") : button(k.signin ? "Add and sign in" : "Add", () => pick(k), { size: "s", kind: "quiet", busy: F.busy["mcpk-" + k.id], disabled: M.servers.length >= M.max })))) ) : null;
  return [known, block({ title: "Add a connector by address", sub: "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust." },
    M.servers.length ? h("ul", { class: "list mcp-url-list" }, M.servers.map(x => h("li", { class: "lr mcp-url-row", "data-key": x.name },
      h("div", { class: "lr-in" }, h("b", null, x.name), " ", h("span", { class: "meta mcp-url" }, x.url), x.token ? [" ", h("span", { class: "meta" }, "has a token")] : null, x.client ? [" ", h("span", { class: "meta" }, "has its own app")] : null,
        x.signed === "in" ? [" ", h("span", { class: "meta" }, "signed in")] : x.signed === "lapsed" ? [" ", h("span", { class: "meta" }, "sign-in ended")] : null),
      x.token || x.open ? null : button(x.signed === "in" ? "Sign in again" : "Sign in", () => signin(x.name), { size: "s", kind: x.signed === "lapsed" ? "primary" : "quiet", busy: F.busy["mcpin-" + x.name] }),
      button("Remove", () => confirm({ title: `Remove ${x.name}?`, body: ["Agents stop seeing its tools from their next run."], ok: "Remove", kind: "danger", onOk: () => run({ do: "remove", name: x.name }, "mcpurl-" + x.name) }), { size: "s", kind: "quiet", busy: F.busy["mcpurl-" + x.name] })))) : null,
    need,
    M.servers.length < M.max ? h("form", { class: "mcp-url-add", onsubmit: e => { e.preventDefault(); add(); } },
      h("div", { class: "frow" },
        h("div", null, field("Name", input({ value: A.name, autocomplete: "off", maxlength: 40, placeholder: "tools", oninput: (e, n) => { A.name = n.value; } }))),
        h("div", { class: "frow-grow" }, field("Address", input({ type: "url", value: A.url, autocomplete: "off", placeholder: "https://example.com/mcp", oninput: (e, n) => { A.url = n.value; } })))),
      h("div", { class: "frow" },
        h("div", { class: "frow-grow" }, field("Token (if the server needs one)", input({ type: "password", value: A.token, autocomplete: "new-password", oninput: (e, n) => { A.token = n.value; } }))),
        button("Add", null, { type: "submit", size: "s", kind: "primary", busy: F.busy["mcpurl-add"] }))) : h("p", { class: "muted" }, `${M.max} is the most. Remove one to add another.`),
    msg("mcpurl"))];
}
const CONN_STATE = { on: ["working", "Running"], ready: ["queued", "Ready to turn on"], usable: ["working", "Ready to use"], nothing: ["off", "Nothing to turn on"],
  off: ["off", "Not set up"], two_way: ["off", "Needs a separate account first"], cant_load: ["failed", "Can't load"], token_missing: ["stuck", "Token missing"],
  restarting: ["stuck", "Keeps restarting"] };
const CONN_HINT = { on: "Its service is running.", ready: "It has what it needs from you. An agent or a terminal turns it on.", off: "It isn't set up yet. Its row says what it needs.",
  usable: "Nothing to turn on: the agent can use it now.", nothing: "It has no service to turn on. An agent sets it up when you ask.",
  restarting: "Its service keeps starting over, so it may be failing. Ask an agent to look.",
  two_way: "It would let messages from outside reach the Hive, so it stays off until it can run under a separate account on this computer.",
  cant_load: "Its code is missing or its service failed. Remove the connectors and add them again, or ask an agent.", token_missing: "It was on, and its saved token is gone. Paste it again." };
function connectorsSection(ctx) {
  if (!can("modules")) return null;
  const C = F.conns, F2 = F.connIn ||= {};
  const bundle = async what => {
    F.busy.reach = true; say("conns", what === "add" ? "Adding the connectors…" : "Removing the connectors…"); ctx.update();
    try { await post("/api/module", { do: what, name: "reach" }); say("conns", what === "add" ? "Added the connectors. Each row below says what it still needs." : "Removed.", "good"); /* never the reply's own text: that is the install's output, with this machine's paths */ } catch (e) { say("conns", e.message, "bad"); }
    delete F.busy.reach; await loadConns(ctx, true); loadMods(ctx);
  };
  const paste = async x => {
    const v = (F2[x.name] || "").trim();
    if (!v) { say("conn-" + x.name, "Paste it first.", "bad"); ctx.update(); return; }
    F.busy["conn-" + x.name] = true; ctx.update();
    try { await post("/api/secret", { name: x.secret, value: v }); F2[x.name] = ""; say("conn-" + x.name, "Saved. It can't be shown again.", "good"); } catch (e) { say("conn-" + x.name, e.message, "bad"); }
    delete F.busy["conn-" + x.name]; await loadConns(ctx, true);
  };
  const step = x => x.two_way || x.link || x.state === "usable" ? null : x.can_finish_here ? h("span", { class: "conn-step", "data-tone": "good" }, "Finish here") : h("span", { class: "conn-step" }, "Needs an agent or a terminal");
  const row = x => {
    const [key, word] = CONN_STATE[x.state] || CONN_STATE.off;
    const box = x.secret && !x.two_way && !x.link && can("secrets") && x.state !== "on" && !(x.shared && x.saved);   // a shared secret (Telnyx: the phone line's key) is never replaced from here
    return h("li", { class: "lr conn-row", "data-key": x.name, "data-state": x.state },
      h("div", { class: "lr-in conn-in" },
        h("div", { class: "conn-head" },
          h("b", { class: "conn-name" }, x.label),
          h("span", { class: "conn-kind meta" }, x.kind === "arm" ? "Let the agent read this" : "Reach you from another app"),
          statusLabel(key, word, { title: CONN_HINT[x.state] })),
        h("p", { class: "conn-line" }, x.line),
        h("p", { class: "conn-needs muted" }, step(x), step(x) ? " " : null, x.needs),
        h("div", { class: "conn-acts" },
          x.link ? button(x.name === "conn-ntfy" ? "Open Phone alerts" : "Open Push notifications", null, { size: "s", href: href("settings", x.link) }) : null,
          x.readme && h("a", { class: "conn-readme", href: x.readme, target: "_blank", rel: "noreferrer noopener" }, "How to set it up")),
        box ? h("form", { class: "frow conn-paste", onsubmit: e => { e.preventDefault(); paste(x); } },
          h("div", { class: "frow-grow" }, field(x.can_finish_here ? "Paste the link" : "Paste the token", input({ type: "password", autocomplete: "new-password", value: F2[x.name] || "", placeholder: x.secret, oninput: (e, n) => { F2[x.name] = n.value; } }))),
          button("Save", null, { type: "submit", size: "s", busy: F.busy["conn-" + x.name] })) : null,
        x.shared ? h("p", { class: "conn-shared muted" }, x.saved ? "Its key is saved. The phone line uses the same key, so it isn't changed from here: use Secrets." : "The phone line uses this key too: saving it here changes the phone line's key.") : null,
        msg("conn-" + x.name)));
  };
  const body = () => !C.installed
    ? block({ title: "Connect other apps", sub: "Set up phone alerts with ntfy, Gotify or web push, and let the agent read a calendar, feeds or Home Assistant. Two-way chat apps like Telegram or email come too, but they need a separate account before they can be on. One Add brings them all; each stays off until you set it up.",
        actions: button("Add", () => bundle("add"), { size: "s", kind: "primary", busy: F.busy.reach }) },
        h("p", { class: "muted mod-none" }, "A module's code runs with the same rights as your agents, so only add ones you trust."))
    : [h("ul", { class: "list conn-list" }, C.rows.map(row)),
       C.removable ? h("div", { class: "lr-more" }, button("Remove the connectors", () => confirm({ title: "Remove every connector?", body: ["They all come off this Hive, with any that are on. You can add them back from this page."], ok: "Remove", kind: "danger", onOk: () => bundle("remove") }), { size: "s", kind: "quiet", busy: F.busy.reach })) : null];
  return section({ title: "Connectors", level: 1, note: "other apps that can reach you, or that the agent can read" },
    msg("conns"),   // above the list: after Add the line shows where the person is looking, not under 24 rows
    C == null ? (F.connsErr ? [empty(`Couldn't read the connectors: ${F.connsErr}`, { kind: "error" }), button("Try again", () => { F.connsErr = null; ctx.update(); return loadConns(ctx, true); }, { size: "s" })] : empty("Reading…", { kind: "loading" })) : body(),
    mcpUrlBlock(ctx));
}

function modulesSection(ctx) {
  if (!can("modules")) return null;
  const act = async (what, name) => {
    F.busy[name] = true; say("mods", (what === "add" ? "Adding " : "Removing ") + name + "…"); ctx.update();
    try { const r = await post("/api/module", { do: what, name }); say("mods", r.said); } catch (e) { say("mods", e.message, "bad"); }
    delete F.busy[name]; await loadMods(ctx);
  };
  const about = x => x.about.replace(/`/g, "") + (x.needs ? ` Needs ${x.needs}.` : "");
  const WHERE = { github: "On GitHub", git: "In a Git repository elsewhere", local: "Only on this machine" };
  const slackBtn = () => button(F.slack ? "Close" : "Set up", () => { F.slack = F.slack ? null : { step: 1, bot: "", app: "", channel: "" }; ctx.update(); }, { size: "s", kind: F.slack ? "quiet" : null });
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
  // A link to one module's row (#settings/modules/<name>, F.goRow from render): once the list is read, open the full list if the row
  // is only there, and scroll the row into view, so a link to the Slack row shows its "Set up" with no press.
  if (F.goRow && F.mods) {
    const name = F.goRow; F.goRow = null;
    if (F.mods.some(x => x.name === name && x.source !== "own" && x.state !== "installed" && !x.recommended)) F.browse = true;
    setTimeout(() => { const el = document.querySelector(`.set-s li[data-key="${CSS.escape(name)}"]`); if (el) el.scrollIntoView({ block: "center" }); }, 0);
  }
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
    F.mods == null ? (F.modsErr ? [empty(`Couldn't read the modules: ${F.modsErr}`, { kind: "error" }), button("Try again", () => { F.modsErr = null; ctx.update(); return loadMods(ctx); }, { size: "s" })] : empty("Reading…", { kind: "loading" })) : [F.modsErr ? staleNote("Couldn't refresh the modules, so this shows what it read last. It keeps trying.") : null, F.mods.length ? shelves() : empty("No modules listed.")],
    F.slack ? slackSetup(ctx) : null,
    msg("mods"));
}

// ---- The Slack door, set up from here instead of a file: POST /api/slack-door {bot_token, app_token, channel} (dash.py slack_door_connect). ----
const MANIFEST = `display_information:
  name: Hive
features:
  bot_user:
    display_name: Hive
  app_home:
    messages_tab_enabled: true
    messages_tab_read_only_enabled: false
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
    try { const r = await post("/api/slack-door", { bot_token: Z.bot.trim(), app_token: Z.app.trim(), channel: Z.channel.trim() }); say("slack", r.channel && r.channel[0] === "#" ? `${r.said} Post in ${r.channel} to test it: an agent answers there.` : r.said, "good"); Z.step = 4; Z.bot = Z.app = ""; loadApp().catch(() => {}); }
    catch (e) { say("slack", e.message, "bad"); }
    Z.busy = false; ctx.update();
  };
  return h("div", { class: "signin slk", "data-key": "slack" },
    h("p", null, h("b", null, "Reach the Hive from Slack")),
    h("p", { class: "field-h" }, "You make a small Slack app of your own, so messages go only between your Slack and this computer. You write to it in its Messages tab, or in a channel."),
    h("ol", { class: "sl-steps" },
      step(1, "Make the Slack app", h("p", { class: "field-h" }, "Slack's page opens with this app text filled in: pick your workspace, then Next and Create. If the box is empty, choose From a manifest and paste this:"),
        h("div", { class: "sl-man" }, h("pre", null, MANIFEST), copyButton(MANIFEST)),
        h("div", { class: "frow" }, h("a", { class: "btn", href: "https://api.slack.com/apps?new_app=1&manifest_yaml=" + encodeURIComponent(MANIFEST), target: "_blank", rel: "noopener noreferrer" }, "Open Slack's app page"), button("Done", () => { Z.step = 2; ctx.update(); }, { kind: "primary" }))),
      step(2, "Paste its two keys", h("p", { class: "field-h" }, "Install the app to your workspace. Then copy the Bot token (OAuth & Permissions, starts xoxb-) and make an App token with connections:write (Basic Information, starts xapp-)."),
        h("div", { class: "fgrid" },
          field("Bot token", input({ placeholder: "xoxb-…", autocomplete: "off", spellcheck: "false", value: Z.bot, oninput: (e, n) => { Z.bot = n.value; } })),
          field("App token", input({ placeholder: "xapp-…", autocomplete: "off", spellcheck: "false", value: Z.app, oninput: (e, n) => { Z.app = n.value; } }))),
        h("div", { class: "frow" }, button("Next", () => { if (/^xoxb-/.test(Z.bot.trim()) && /^xapp-/.test(Z.app.trim())) { say("slack", ""); Z.step = 3; } else say("slack", "The bot token starts xoxb- and the app token xapp-.", "bad"); ctx.update(); }, { kind: "primary" }))),
      step(3, "Pick the channel", field("Channel (optional)", input({ value: Z.channel, placeholder: "empty: talk to the app directly", oninput: (e, n) => { Z.channel = n.value; } }), "Leave it empty to write to the app in Slack's Messages tab. If others share your Slack, type your name as Slack shows it. For a #channel, /invite @Hive in it first: everyone there can direct this Hive."),
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
      say("access", "Saved.", "good"); F.accessTyped = false;
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
      say("sites", "Saved.", "good"); F.accessTyped = false;
    }
    catch (e) { say("sites", e.message, "bad"); }
    ctx.update();
  };
  const rowTxt = (r, label, k, ph) => field(label, input({ placeholder: ph, value: r[k], oninput: (e, n) => { r[k] = n.value; F.accessTyped = true; } }));
  const siteRows = R.map((r, i) => h("div", { class: "fgrid", "data-key": "site-" + i },
    rowTxt(r, "Hostname", "host", "h3.example.com"), rowTxt(r, "Access team domain", "team", "yourteam.cloudflareaccess.com"),
    rowTxt(r, "Application audience", "aud", "the Access application's AUD tag"), rowTxt(r, "Allowed emails", "emails", "optional: only these emails"),
    rowTxt(r, "People with full access", "full", "you@example.com"), rowTxt(r, "People with limited access", "limited", "friend@example.com"),
    button("Remove", () => { R.splice(i, 1); ctx.update(); }, { kind: "quiet" })));
  const txt = (label, k, ph) => field(label, input({ placeholder: ph, value: A[k], oninput: (e, n) => { A[k] = n.value; F.accessTyped = true; } }));
  const sites = Object.keys(o.sites || {}), names = [...sites, ...o.hosts.filter(x => !sites.includes(x))];
  return section({ title: "Access from outside", level: 1, note: "only changeable from this machine" },
    h("p", null, statusLabel(names.length ? "working" : "off", names.length ? "Set up" : "Off"), " ", names.length ? "These names can reach this Hive from the internet, through Cloudflare, once their tunnel is up:" : "Only this computer can open this Hive. You need this only to reach it from a phone or another machine."),
    names.length ? h("ul", { class: "plain", "data-key": "access-names" }, names.map(n => h("li", { "data-key": n }, h("b", { class: "mono" }, n), sites.includes(n) ? h("span", { class: "muted" }, " has its own sign-in rules. Change them under Names with their own sign-in, below.") : null))) : null,
    foot("Each part below saves as one set with its own button, because a half-typed sign-in rule could lock people out."),   // E56f: above the parts it speaks of
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
          button("Save names", saveSites, { kind: "primary", busy: isBusy("sites") }), msg("sites")))));
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
  if (can("tier")) { const p = pausedTiers(); if (p) dot("Work tiers", "paused", p); }
  if (o) { const n = Object.keys(o.sites || {}).length + o.hosts.length; dot("Access from outside", n ? "working" : "off", n ? "set up" : "off"); }
  return d;
}

export default {
  id: "settings", label: "Settings", about: "accounts, modules, access", tab: () => S.app.app && (can("config") || can("modules") || can("logins")),
  enter(ctx) { F.navShown = null; F.keysOpen = null; F.flash = {}; F.ed = null; F.lost = false; F.name = null; F.saved = null; if (!F.accessTyped) F.access = null;   /* unsaved access edits stay for the session (3.3.7) */ F.acctOpen = null; F.accessOpen = null; F.sitesOpen = null; F.say = {}; clearSignin(); forget(); F.upLast = F.up || F.upLast; F.up = null; loadCfg(ctx); loadPhone(ctx); loadVoice(ctx); loadPush(ctx); loadUpdate(ctx); watchUpdate(ctx); loadMods(ctx); loadConns(ctx); loadBuying(ctx.update); if (can("tier")) loadTiers(ctx.update, true); if (S.app.settings && can("config")) loadPage(ctx.update);
  },
  back(ctx) { loadCfg(ctx); loadPhone(ctx); loadVoice(ctx); loadPush(ctx); loadUpdate(ctx); loadMods(ctx); loadConns(ctx); loadBuying(ctx.update); if (can("tier")) loadTiers(ctx.update, true); if (S.app.settings && can("config")) loadPage(ctx.update); },   // what enter reads, without enter's clearing: what the person is typing stays
  leave() { F.goTo = null; clearInterval(upTimer); upTimer = null; clearTimeout(voiceTimer); voiceTimer = null; forget(); forgetSecrets(); forgetBuying(); F.connIn = {}; },   // tokens typed in Connectors but not saved don't outlive the screen
  render(ctx) {
    if (S.app.can == null) return h("div", { class: "page", "data-width": "narrow" }, waiting(S.failed));   // /api/app hasn't answered: don't say "only in the app" yet
    if (!S.app.app) return h("div", { class: "page", "data-width": "narrow" }, empty("Settings show only in the app, opened from this machine.", { kind: "page", title: "Settings" }));
    const [slug, row] = (ctx.arg || "").split("/");   // #settings/modules/slack-door: the section, then a row to open on (modulesSection's F.goRow)
    if (row && slug === "modules" && F.goTo !== ctx.arg) { F.goTo = ctx.arg; F.goRow = row; }   // once per link, so "Hide the full list" stays hidden
    const hive = hiveSection(ctx), [everyday, setOnce] = Array.isArray(hive) ? hive : [hive, null];
    const secs = [everyday, loginsSection(ctx), buyingSection(ctx), useSection(), tiersSection(ctx), ...setupSections(ctx), secretsSection(ctx), viewSection(ctx), setOnce, connectorsSection(ctx), modulesSection(ctx), accessSection(ctx), kbWipeSection(ctx)].filter(Boolean);
    const header = pageHeader({ title: "Settings" });
    if (!secs.length) return form({ header, sections: [empty("This connection can't change any settings.")] });
    const nav = secs.map(n => n.dataset.name || n.getAttribute("aria-label")),   // data-name: the English title, which the interface language (core/lang.js) leaves alone, so #settings/this-device works in any language
    dots = navDots(ctx);
    // One section at a time (#settings/<slug>); #settings, or a slug nobody can see, shows the first. The nav is real links, so Back works.
    const slugOf = t => t.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
    const now = nav.find(t => slugOf(t) === slug) || nav[0];
    if (F.navShown !== now) { F.navShown = now; requestAnimationFrame(() => { const n = document.querySelector(".set-n"), a = n && n.querySelector("a[aria-current=page]"); if (n && a && n.scrollWidth > n.clientWidth) n.scrollLeft = a.offsetLeft - (n.clientWidth - a.offsetWidth) / 2; }); }   // a phone's one-row chips: the open section's chip comes into view, once per section, so a hand scroll stays put
    const item = t => h("a", { href: href("settings", slugOf(t)), "data-key": t, "aria-current": t === now ? "page" : null, title: dots[t] ? dots[t][1] : null }, h("span", null, t), dots[t] ? statusDot(dots[t][0]) : null, dots[t] ? h("span", { class: "vh" }, dots[t][1]) : null);
    return h("div", { class: "page set", "data-width": "narrow", "data-key": "settings" }, header,
      h("div", { class: "set-b" },
        h("nav", { class: "set-n", "aria-label": "Settings sections", "data-key": "nav" }, nav.map(t => t === "Set once" ? [h("hr", { class: "set-rule" }), item(t)] : item(t))),
        h("div", { class: "set-s" }, secs.filter(n => (n.dataset.name || n.getAttribute("aria-label")) === now))),
      S.app.install ? h("p", { class: "meta", "data-key": "install" }, "Install code ", h("code", { title: "This Hive's id in the reports it sends" }, S.app.install), " · this Hive's id in the reports it sends") : null);
  },
};
