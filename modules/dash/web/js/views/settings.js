// Settings: the hive, logins, modules, access from outside. Shown only where can() allows each part.
import { h } from "../core/dom.js";
import { S, can, jget, post, loadApp } from "../core/store.js";
import { form } from "../templates.js";
import { pageHeader, section, button, field, input, empty, statusLabel } from "../ui/kit.js";
import { startSignin, signinBox } from "../ui/signin.js";

// Everything the person is typing lives here, so a poll never loses it.
const F = { cfg: null, ed: null, mods: null, say: {}, newLogin: "", url: "", busy: {}, access: null };
const say = (k, text, tone) => { F.say[k] = text ? { text, tone } : null; };
const msg = k => F.say[k] ? h("p", { class: "fmsg", "data-tone": F.say[k].tone || null, role: "status" }, F.say[k].text) : null;
const CAPTEXT = { read: "Read everything", post: "Post as you, which starts agents", stop: "Kill agents", tier: "Change a thread's tier", limits: "Pause logins and change stop lines", logins: "Sign in and add logins", config: "Change settings", modules: "Add and remove modules", plans: "Edit project plans", secrets: "Store secrets" };

async function loadCfg(ctx) {
  if (!can("config")) return;
  try { F.cfg = await jget("/api/config"); } catch (e) { F.cfg = {}; }
  F.ed ||= { max: F.cfg.max_sessions ?? "", us: F.cfg.usage_stop_pct ?? "", kp: !!F.cfg.keeper_every_s, au: F.cfg.auto_update !== false };
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
  if (!F.ed) return section({ title: "The hive" }, empty("Reading…", { kind: "loading" }));
  const E = F.ed, up = S.app.setup && S.app.setup.starter;
  const save = async () => {
    try {
      await post("/api/config", { max_sessions: E.max === "" ? null : +E.max, keeper_every_s: E.kp ? (F.cfg.keeper_every_s || 10800) : 0, usage_stop_pct: +E.us, auto_update: E.au });
      say("hive", "Saved. The hive uses it within seconds.", "good");
    } catch (e) { say("hive", e.message, "bad"); }
    ctx.update();
  };
  const starter = async () => {
    try { await post("/api/starter", { do: up ? "stop" : "start" }); say("starter", up ? "Stopping…" : "Starting…"); ctx.update(); setTimeout(async () => { await loadApp(); say("starter", ""); ctx.update(); }, up ? 500 : 4000); }
    catch (e) { say("starter", e.message, "bad"); ctx.update(); }
  };
  return section({ title: "The hive" },
    h("div", { class: "fgrid" },
      field("Session limit", input({ type: "number", min: 1, max: 200, value: E.max, oninput: (e, n) => { E.max = n.value; } }), "How many agents run at once. Empty means no limit."),
      field("Stop at weekly usage, %", input({ type: "number", min: 1, max: 100, value: E.us, oninput: (e, n) => { E.us = n.value; } }), "No new agents past this, except ones you ask for. Subscription logins only."),
      check("Keeper", E.kp, v => { E.kp = v; }, "A round every 3 hours that revives stalled work and starts work nobody asked for. It spends usage."),
      check("Update automatically", E.au, v => { E.au = v; }, "Installs each new release a few times a day, keeps your settings and goes back if it fails.")),
    h("div", { class: "frow" }, button("Save", save, { kind: "primary" }), msg("hive")),
    h("div", { class: "frow fsplit" }, h("span", null, "Starter: ", statusLabel(up ? "working" : "ended", up ? "running" : "not running")),
      button(up ? "Stop (running agents finish their turn)" : "Start", starter), msg("starter")));
}

function loginsSection(ctx) {
  if (!can("logins")) return null;
  const logins = (S.app.setup && S.app.setup.logins) || [];
  return section({ title: "Logins", note: "pausing and stop lines are on the Usage tab" },
    logins.length ? h("ul", { class: "list" }, logins.map(l => h("li", { class: "lr", "data-key": l.name },
      h("div", { class: "lr-in frow fsplit" }, h("b", null, l.name), statusLabel(l.signed_in ? "working" : "failed", l.signed_in ? "signed in" : "not signed in"),
        h("span", { style: "margin-left:auto" }, button(l.signed_in ? "Sign in again" : "Sign in", () => startSignin("login-" + l.name, l.name, false, ctx.update), { size: "s" })))))) : empty("No logins yet."),
    logins.map(l => signinBox("login-" + l.name, ctx.update)),
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.newLogin.trim()) startSignin("login-new", F.newLogin.trim(), false, ctx.update); } },
      h("div", { style: "flex:1;min-width:12rem;max-width:20rem" }, field("New login", input({ placeholder: "name, like work", value: F.newLogin, oninput: (e, n) => { F.newLogin = n.value; } }))),
      button("Add a login", null, { type: "submit" })),
    h("p", { class: "field-h" }, "Adding one signs in, checks it isn't an account you already have, and makes one short test run."),
    signinBox("login-new", ctx.update));
}

function modulesSection(ctx) {
  if (!can("modules")) return null;
  const act = async (what, name) => {
    F.busy[name] = true; say("mods", (what === "add" ? "Adding " : "Removing ") + name + "…"); ctx.update();
    try { const r = await post("/api/module", { do: what, name }); say("mods", r.said); } catch (e) { say("mods", e.message, "bad"); }
    delete F.busy[name]; await loadMods(ctx);
  };
  return section({ title: "Modules", note: "optional tools your agents can use" },
    F.mods == null ? empty("Reading…", { kind: "loading" }) : F.mods.length ? h("ul", { class: "list" }, F.mods.map(x => h("li", { class: "lr", "data-key": x.name },
      h("div", { class: "lr-in frow fsplit" }, h("b", { style: "flex:none" }, x.name), h("span", { class: "muted", style: "flex:1;min-width:10rem" }, x.about, x.needs ? ` Needs ${x.needs}.` : ""),
        x.name === "dash" ? h("span", { class: "meta" }, "this app") : button(x.state === "installed" ? "Remove" : "Add", () => act(x.state === "installed" ? "remove" : "add", x.name), { size: "s", disabled: F.busy[x.name] }))))) : empty("No modules listed."),
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (F.url.trim()) act("add", F.url.trim()); } },
      h("div", { style: "flex:1;min-width:12rem" }, field("Add from a Git URL", input({ placeholder: "Git URL of a module", value: F.url, oninput: (e, n) => { F.url = n.value; } }))),
      button("Add", null, { type: "submit" })),
    msg("mods"));
}

function accessSection(ctx) {
  const o = S.app.route === "local" && S.app.outside;
  if (!o) return null;
  const A = F.access ||= { can: new Set(o.can), hosts: o.hosts.join(" "), team: o.team || "", aud: o.aud || "", emails: o.emails.join(" ") };
  const words = s => s.split(/\s+/).filter(Boolean);
  const save = async () => {
    try { await post("/api/access", { can: o.caps.filter(c => A.can.has(c)), hosts: words(A.hosts), access: { team: A.team, aud: A.aud, emails: words(A.emails) } }); say("access", "Saved.", "good"); }
    catch (e) { say("access", e.message, "bad"); }
    ctx.update();
  };
  const txt = (label, k, ph) => field(label, input({ placeholder: ph, value: A[k], oninput: (e, n) => { A[k] = n.value; } }));
  return section({ title: "Access from outside", note: "only changeable from this machine" },
    h("p", { class: "field-h" }, "Off until set up. If web.json lists outside.people, each listed person gets limited (read, chat, kill agents) or full (everything here, for an hour after signing in), and the ticks below don't apply. From this machine, the secret gets everything. Through a tunnel, each request must carry a token signed by Cloudflare Access, and it can do only what's ticked here. Opening a name also takes a Cloudflare tunnel and an Access policy, set up in Cloudflare."),
    h("div", { class: "checks", role: "group", "aria-label": "What outside requests may do" }, o.caps.map(c => check(CAPTEXT[c] || c, A.can.has(c), v => { v ? A.can.add(c) : A.can.delete(c); }))),
    h("div", { class: "fgrid" }, txt("Hostnames", "hosts", "hive.example.com"), txt("Access team domain", "team", "yourteam.cloudflareaccess.com"),
      txt("Application audience", "aud", "the Access application's AUD tag"), txt("Allowed emails", "emails", "optional: only these emails")),
    h("div", { class: "frow" }, button("Save", save, { kind: "primary" }), msg("access")));
}

export default {
  id: "settings", label: "Settings", tab: () => S.app.app && (can("config") || can("modules") || can("logins")),
  enter(ctx) { F.ed = null; F.access = null; F.say = {}; loadCfg(ctx); loadMods(ctx); },
  render(ctx) {
    if (!S.app.app) return h("div", { class: "page" }, empty("Settings show only in the app, opened from this machine.", { kind: "page", title: "Settings" }));
    const secs = [hiveSection(ctx), loginsSection(ctx), modulesSection(ctx), accessSection(ctx)].filter(Boolean);
    return form({ header: pageHeader({ title: "Settings", lead: "How the hive runs, who it signs in as, and what it can use." }), sections: secs.length ? secs : [empty("This connection can't change any settings.")] });
  },
};
