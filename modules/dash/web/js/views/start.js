// Start: first run. On an install with settings.json, a one-screen setup with a log of what each click did. Otherwise, or once that's done,
// the start of the conversation: Hive says what it is, you sign in, (start the hive if it's down), and say what you want done in the composer Chat uses.
import { h } from "../core/dom.js";
import { S, can, post, loadApp, setupDone } from "../core/store.js";
import { simpleOn } from "../core/simple.js";
import { href } from "../core/router.js";
import { brandName } from "../core/brand.js";
import { button, statusLabel, waiting } from "../ui/kit.js";
import { startSignin, signinBox, signingIn, terminalRoute } from "../ui/signin.js";
import { act, activityLog, isBusy } from "../ui/activity.js";
import { firstJobs, closeLine } from "../ui/firstjobs.js";
import { composer, outFor, actions as outActions } from "../ui/composer.js";
import { localMessage } from "../ui/message.js";
import { anyAccount, codexPart, finishing, local, pageData, reading, rootNote, sandboxPart, saveSettings } from "../ui/setup.js";

const hasSent = () => [...S.threads.values()].some(d => d.posts.some(p => p.from === "person"));
const F = { starterErr: "", chose: null, know: false };

const hive = (key, ...body) => h("article", { class: "msg st-msg", "data-who": "agent", "data-key": key },
  h("div", { class: "msg-h" }, h("b", { class: "st-hive" }, brandName() || "Hive")), h("div", { class: "msg-b" }, body));
const you = (key, text) => h("article", { class: "msg", "data-who": "me", "data-key": key }, h("div", { class: "msg-b" }, text));
const p = (...t) => h("p", null, ...t);

// ---- the walkthrough, on an install with settings.json and setup_done false: one screen, an account. Sharing, power and the sandbox keep their defaults and live in Settings ----
function walkthrough(ctx, s) {
  const pg = pageData(ctx.update), up = ctx.update;
  const page = (...rows) => h("div", { class: "page wt", "data-key": "start" },
    h("h1", { class: "wt-h" }, "Set up your Hive"),
    h("p", { class: "wt-lead" }, simpleOn() ? "Your Hive is a helper you talk to. Add your account, and you can start." : "Hive runs agents on this computer: you say what you want done, and they work on it in the background. Add an account, and you can start."),
    h("div", { class: "wt-g" }, h("div", { class: "wt-m" }, rows), activityLog()));
  const card = (key, title, ...body) => h("section", { class: "wt-card", "data-key": key }, h("h2", { class: "wt-t" }, title), body);
  const foot = next => h("div", { class: "wt-foot" }, h("span", { class: "wt-next" }, next));
  if (!local() && simpleOn()) return h("div", { class: "page wt", "data-key": "start" }, card("away", "Your Hive isn't ready yet", p("The person setting it up still has a step to finish. There's nothing for you to do here. Check back soon.")));
  if (!local()) return page(card("away", "Finish this on the Hive's computer", p("Setup is only done on this computer, because it decides what the Hive may do. Open the app there to finish it.")));
  if (!pg) return page(card("reading", "Getting ready", reading()));
  const signIn = api => { F.chose = api ? "api" : "claude"; startSignin("start", null, api, up); };
  const logins = s.logins || [], sb = pg.sandbox_check && pg.sandbox_check.state, sbOk = pg.settings.sandbox === "on" && sb === "works";
  const share = pg.settings.share || {}, sharing = [share.failures ? "failure reports and a daily check-in" : null, share.usage ? "usage reports" : null].filter(Boolean);
  // Their own CLAUDE.md: offered, never copied without the tick. MCP servers are listed only.
  const im = pg.import, importCard = !im || !(im.claude_md.found || im.mcp.length) ? null : card("import", "Bring your own instructions",
    im.claude_md.found ? [p("You have a CLAUDE.md of your own. Tick to copy it into the instructions every agent in this Hive gets. Nothing is copied until you tick."),
      h("pre", { class: "wt-pre" }, im.claude_md.preview + (im.claude_md.chars > 400 ? "…" : "")),
      h("label", { class: "frow" }, h("input", { type: "checkbox", "data-key": "import-claude-md", checked: im.claude_md.copied,
        onchange: (e, n) => act("import", n.checked ? "Copying your CLAUDE.md" : "Removing the copy", async () => { pg.import = (await post("/api/import", { claude_md: n.checked })); }, up, n.checked ? "Copied your CLAUDE.md" : "Removed the copy") }), " Copy my CLAUDE.md")] : null,
    im.mcp.length ? p("Your MCP servers (" + im.mcp.join(", ") + ") stay with your own Claude Code. Agents here don't use them yet.") : null);
  return page(
    card("account", "Add an account", p(simpleOn() ? "Your Hive works through your Claude account. Sign in to it here." : "Sign in with your Claude plan (or a ChatGPT plan, below). Agents run on it and use its limits."),
      rootNote(),
      h("div", { class: "signin", "data-key": "claude" },
        h("div", { class: "frow fsplit" }, h("b", null, "Claude"), s.claude === false ? statusLabel("ended", "not installed") : statusLabel(s.signed_in ? "working" : "ended", s.signed_in ? "signed in" : "not signed in"),
          s.claude !== false && !s.signed_in && can("logins") ? h("span", { class: "fsplit-end" }, button("Sign in to Claude Code", () => signIn(false), { size: "s", kind: "primary", busy: signingIn() })) : null),
        s.claude === false ? p("To use it, in a terminal as this account: ", h("code", null, "curl -fsSL https://claude.ai/install.sh | bash"), " Then restart this app.") : null,
        logins.length > 1 ? h("p", { class: "field-h" }, logins.filter(l => l.signed_in).length + " of " + logins.length + " logins signed in.") : null,
        s.claude !== false && !s.signed_in && can("logins") ? terminalRoute(logins) : null,
        signinBox("start", up)),
      pg.accounts && pg.accounts.codex ? [h("p", { class: "field-h" }, "Or sign in with a ChatGPT plan, through Codex."), codexPart(up)] : null),
    importCard,
    // A sandbox that doesn't work stops agents from starting, so that one is said in full here. When it works, it's a line.
    sbOk ? null : card("sandbox-step", "The sandbox on this machine", sandboxPart(up)),
    h("p", { class: "field-h", "data-key": "defaults" },
      sbOk ? (simpleOn() ? "Your Hive works in a protected space, and anything bigger asks first. " : "Agents run in a sandbox, and anything bigger asks first. ") : "",
      pg.share_text ? (sharing.length ? "Sent to the Hive's developer once you press Finish: " + sharing.join(" and ") + ". " : "Nothing is sent to the Hive's developer. ") : "",
      "Sharing, power, the sandbox and the model are in ", h("a", { href: href("settings") }, "Settings"), ", where you can change them any time."),
    foot(anyAccount() ? button("Finish", async () => { if (await saveSettings({ setup_done: true }, up)) up(); }, { kind: "primary", busy: finishing() }) : h("span", { class: "meta" }, "Add your account to go on.")));
}

// Simple mode once setup is finished: at most one thing left to do, signing in to Claude.
function simpleStart(ctx, signed) {
  const card = (key, title, ...body) => h("div", { class: "page wt", "data-key": "start" }, h("section", { class: "wt-card", "data-key": key }, h("h2", { class: "wt-t" }, title), body));
  if (signed) return card("ready", "Your Hive is ready", p("Tell it what you want done, in your own words."), button("Start chatting", null, { href: href("comms"), kind: "primary" }));
  if (!can("logins")) return card("away", "Your Hive isn't ready yet", p("The person setting it up still has a step to finish. There's nothing for you to do here. Check back soon."));
  return card("account", "One step left: add your Claude account", p("Your Hive works through a paid Claude account. Sign in to yours, and it can start."),
    signinBox("start", ctx.update) || button("Sign in to Claude Code", () => startSignin("start", null, false, ctx.update), { kind: "primary", busy: signingIn() }), terminalRoute(S.app.setup && S.app.setup.logins));
}

export default {
  id: "start", label: "Start", tab: () => S.app.app && can("post") && !setupDone(),
  enter: () => { F.arrived = true; },
  render(ctx) {
    if (S.app.can == null) return h("div", { class: "page" }, waiting(S.failed));   // /api/app hasn't answered: don't say "only in the app" yet
    if (!S.app.app) return h("div", { class: "page" }, h("p", null, "Setup shows only in the app, opened from this machine."));
    const s = S.app.setup || {}, signed = s.signed_in || s.api_key || s.other, cfgd = S.app.settings;
    if (cfgd && cfgd.setup_done === false) return walkthrough(ctx, s);
    if (simpleOn()) return simpleStart(ctx, signed);
    const signIn = api => { F.chose = api ? "Use API billing instead" : "Sign in with my Claude subscription"; startSignin("start", null, api, ctx.update); };
    const rows = [
      hive("hi", p(cfgd ? "You're set up." : "Hi.", " Hive runs ", cfgd ? "" : "Claude ", "agents on this computer. Tell it what you want done, in your own words, and an agent works on it in the background and answers you here.")),
    ];
    if (s.claude === false) rows.push(hive("claude", p("Claude Code isn't installed for this account yet, and the agents need it. In a terminal, as this account:"),
      h("pre", null, h("code", null, "curl -fsSL https://claude.ai/install.sh | bash")), p("Then restart this app: ", h("code", null, "systemctl --user restart hive-dash"))));
    if (!signed) {
      rows.push(hive("signin", p("First, sign in to Claude. Agents run on your Claude subscription and use its limits. You can use API billing instead, which charges per use."),
        can("logins") && !F.chose ? h("div", { class: "frow" }, button("Sign in to Claude Code", () => signIn(false), { kind: "primary" }), button("Use API billing instead", () => signIn(true))) : null,
        can("logins") && !F.chose ? terminalRoute(s.logins) : null,
        can("logins") ? null : p(h("span", { class: "muted" }, "This connection can't sign in. Open the app on this computer."))));
      if (F.chose) { rows.push(you("chose", F.chose)); rows.push(hive("box", signinBox("start", ctx.update) || p("Starting Claude's sign-in…"),
        button("Start over", () => { F.chose = null; ctx.update(); }, { kind: "quiet", size: "s" }))); }
    } else rows.push(hive("signed", p(statusLabel("working", s.signed_in || s.api_key ? "Signed in to Claude." : "Your account is ready."))));
    if (s.starter === false) rows.push(hive("starter", p("The part of Hive that starts agents isn't running, so nothing you say would start one."),
      can("config") ? h("div", { class: "frow" }, button("Start it", async () => {
        F.starterErr = "";
        try {
          await act("starter", "Starting the Hive", async () => {
            await post("/api/starter", { do: "start" });
            await new Promise(r => setTimeout(r, 4000));
            await loadApp();
            if ((S.app.setup || {}).starter === false) throw new Error("it still isn't running after 4 seconds. Try once more, or look at: systemctl --user status h2-starter");
          }, ctx.update, "The Hive is running");
        } catch (e) { F.starterErr = e.message; }
        ctx.update();
      }, { kind: "primary", busy: isBusy("starter") }), F.starterErr ? h("span", { class: "fmsg" }, F.starterErr) : null) : null));
    if (signed) rows.push(hive("ask", p(h("b", null, "What do you want done?"), " Say it the way you'd tell a person: sort these photos, fix my site's contact form, watch this price. One message starts one agent, and it brings in helpers if the job has parts.")));
    const out = outFor(null), box = signed && can("post");
    // Only on arriving, or when the box first appears after signing in: taking focus on every render pulled the page back to the box after any real click elsewhere on it.
    const arrived = F.arrived; if (box) F.arrived = false;
    return h("div", { class: "page st", "data-width": "narrow", "data-key": "start" },
      h("div", { class: "st-log", role: "log", "aria-label": "Setting up" }, rows, out.map(it => localMessage(it, outActions))),
      signed && can("post") ? composer({ thread: null, update: ctx.update, place: "start", autofocus: arrived && !out.length }) : h("p", { class: "meta st-wait" }, "Once you're signed in, you can say what you want done."),
      signed && can("post") ? (out.some(it => it.state !== "failed") ? closeLine() : !out.length && !hasSent() ? firstJobs() : null) : null,
      activityLog({ hideEmpty: true }),
      h("div", { class: "st-know" },
        button(F.know ? "Hide what's good to know" : "Good to know before you start", () => { F.know = !F.know; ctx.update(); }, { kind: "quiet", size: "s" }),
        F.know ? h("ul", { class: "plain" },
          h("li", null, cfgd ? "What agents may do on this computer, and whether the sandbox works here, is under Power in Settings." : "Agents can do anything this computer account can do, so give Hive an account of its own."),
          h("li", null, "They use your account's limits on their own. New agents stop at a usage line you set in Settings. With API billing, set a spending limit in the Anthropic Console too."),
          h("li", null, "This page answers only on this computer, and only to a browser that opened the address from ", h("code", null, "hive web"), ".")) : null));
  },
};
