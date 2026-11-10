// Start: first run. On an install with settings.json, a three-step setup (R7) with a log of what each click did. Otherwise, or once that's done,
// the start of the conversation: Hive says what it is, you sign in, (start the hive if it's down), and say what you want done in the composer Chat uses.
import { h } from "../core/dom.js";
import { S, can, post, loadApp, setupDone } from "../core/store.js";
import { simpleOn } from "../core/simple.js";
import { href } from "../core/router.js";
import { brandName } from "../core/brand.js";
import { button, statusLabel, waiting } from "../ui/kit.js";
import { startSignin, signinBox, signingIn, terminalRoute } from "../ui/signin.js";
import { act, activityLog, isBusy } from "../ui/activity.js";
import { firstJobs, closeLine, FIRST_JOBS } from "../ui/firstjobs.js";
import { composer, draft, focusComposer, outFor, actions as outActions } from "../ui/composer.js";
import { localMessage } from "../ui/message.js";
import { POWER, SHARES, anyAccount, codexPart, finishing, keysPart, local, pageData, reading, rootNote, sandboxPart, saveSettings } from "../ui/setup.js";

const hasSent = () => [...S.threads.values()].some(d => d.posts.some(p => p.from === "person"));
const F = { starterErr: "", chose: null, know: false, step: 1, finished: false, codex: false, keyOpen: false };

const hive = (key, ...body) => h("article", { class: "msg st-msg", "data-who": "agent", "data-key": key },
  h("div", { class: "msg-h" }, h("b", { class: "st-hive" }, brandName() || "Hive")), h("div", { class: "msg-b" }, body));
const you = (key, text) => h("article", { class: "msg", "data-who": "me", "data-key": key }, h("div", { class: "msg-b" }, text));
const p = (...t) => h("p", null, ...t);

// ---- the walkthrough (R7), on an install with settings.json and setup_done false: three short steps, one per screen, then the first message.
// 1 an account (the API key folded; none in simple view, which has no keys), 2 what to share, 3 how much power. The words for sharing and power are the program's own (SHARES and
// POWER in ui/setup.js, the report module's share_text), so this page says nothing Settings doesn't. Each choice saves the moment it's made.
const STEPS = [null,
  ["Add an account", "Hive runs agents on this computer: you say what you want done, and they work on it in the background. First, the account they run on."],
  ["What to share", "Whether anything goes to Hive's developer. You can change this in Settings any time."],
  ["How much power", "How much agents may do on this computer without asking. You can change this in Settings any time."]];
const SIMPLE_LEAD = "Your Hive is a helper you talk to. First, the account it works through.";
const opt = (key, checked, onPick, title, hint, extra) => h("label", { class: "setup-opt", "data-key": key },
  h("input", { type: "radio", name: key.replace(/-.*/, ""), checked, disabled: !local(), onchange: onPick }),
  h("span", { class: "grow" }, h("b", null, title), hint ? h("span", { class: "meta" }, hint) : null), extra || null);
function walkthrough(ctx, s) {
  const pg = pageData(ctx.update), up = ctx.update, n = Math.min(Math.max(F.step || 1, 1), 3);
  const go = k => { F.step = k; up(); scrollTo(0, 0); };
  const page = (...rows) => h("div", { class: "page wt", "data-key": "start" },
    h("div", { class: "ph" }, h("div", { class: "ph-t" }, h("span", { class: "ph-k" }, `Set up · step ${n} of 3`), h("h1", { class: "wt-h" }, STEPS[n][0]),
      h("p", { class: "wt-lead" }, n === 1 && simpleOn() ? SIMPLE_LEAD : STEPS[n][1]))),
    h("div", { class: "setup-steps", "aria-hidden": "true" }, [1, 2, 3].map(i => h("i", { "data-on": i <= n ? "" : null }))),
    h("div", { class: "wt-g" }, h("div", { class: "wt-m" }, rows), activityLog()));
  const card = (key, title, ...body) => h("section", { class: "wt-card", "data-key": key }, title ? h("h2", { class: "wt-t" }, title) : null, body);
  const foot = (next, back = true) => h("div", { class: "wt-foot btn-row" }, back && n > 1 ? button("Back", () => go(n - 1), { kind: "quiet" }) : null, h("span", { class: "wt-next" }, next));
  if (!local() && simpleOn()) return h("div", { class: "page wt", "data-key": "start" }, card("away", "Your Hive isn't ready yet", p("The person setting it up still has a step to finish. There's nothing for you to do here. Check back soon.")));
  if (!local()) return h("div", { class: "page wt", "data-key": "start" }, card("away", "Finish this on the Hive's computer", p("Setup is only done on this computer, because it decides what the Hive may do. Open the app there to finish it.")));
  if (!pg) return page(card("reading", "Getting ready", reading()));
  const signIn = api => { F.chose = api ? "api" : "claude"; startSignin("start", null, api, up); };
  const logins = s.logins || [], sb = pg.sandbox_check && pg.sandbox_check.state, sbOk = pg.settings.sandbox === "on" && sb === "works";
  if (n === 2) {
    // The three choices are the first two of Settings' own (failure reports, usage reports); the rest stay in Settings.
    const sh = pg.settings.share || {}, pick = (failures, usage) => () => saveSettings({ share: { failures, usage } }, up);
    const [f, u] = SHARES, cur = sh.failures ? (sh.usage ? 2 : 1) : 0;
    return page(card("share", null,
      pg.share_text ? h("div", { class: "share-text", "data-key": "share-text" }, pg.share_text.split(/\n+/).map(t => p(t)))
        : h("p", { "data-key": "share-text" }, "The report module isn't installed here, so nothing can be sent, whatever you choose."),
      h("div", { class: "setup-list", role: "radiogroup", "aria-label": "What to share" },
        opt("share-none", cur === 0, pick(false, false), "Nothing", "Nothing goes to Hive's developer."),
        opt("share-failures", cur === 1, pick(true, false), f[1], f[2]),
        opt("share-usage", cur === 2, pick(true, true), f[1] + ", and " + u[1].toLowerCase(), u[2])),
      h("p", { class: "field-h", "data-key": "defaults" },
        pg.share_text ? (cur ? "Sent to the Hive's developer once you press Finish: " + [sh.failures ? "failure reports and a daily check-in" : null, sh.usage ? "usage reports" : null].filter(Boolean).join(" and ") + ". " : "Nothing is sent to the Hive's developer. ") : "",
        "Sending a copy of everything, and GitHub issues, are in ", h("a", { href: href("settings", "data-sharing") }, "Settings"), ".")),
      foot(button("Continue", () => go(3), { kind: "primary" })));
  }
  if (n === 3) {
    const cur = pg.settings.power;
    return page(card("power", null,
      h("div", { class: "setup-list", role: "radiogroup", "aria-label": "How much power" }, POWER.map(([k, label, hint, loose]) =>
        opt("power-" + k, cur === k, () => saveSettings({ power: k }, up), `${k} · ${label}`, (!sbOk && loose) || hint))),
      sbOk ? h("p", { class: "meta", "data-key": "defaults-sb" }, statusLabel("working", "Sandbox on"), simpleOn() ? " Your Hive works in a protected space, and anything bigger asks first." : " Agents run in a sandbox, and anything bigger asks first.") : null,
      h("p", { class: "field-h" }, "What stays your call at every level, and the model, are in ", h("a", { href: href("settings", "power-and-sandbox") }, "Settings"), ".")),
      // A sandbox that doesn't work stops agents from starting, so that one is said in full here.
      sbOk ? null : card("sandbox-step", "The sandbox on this machine", sandboxPart(up)),
      foot(button("Finish", async () => { if (await saveSettings({ setup_done: true }, up)) { F.finished = true; up(); } }, { kind: "primary", busy: finishing() })));
  }
  // Their own CLAUDE.md: offered, never copied without the tick. MCP servers are listed only.
  const im = pg.import, importCard = !im || !(im.claude_md.found || im.mcp.length) ? null : card("import", "Bring your own instructions",
    im.claude_md.found ? [p("You have a CLAUDE.md of your own. Tick to copy it into the instructions every agent in this Hive gets. Nothing is copied until you tick."),
      h("pre", { class: "wt-pre" }, im.claude_md.preview + (im.claude_md.chars > 400 ? "…" : "")),
      h("label", { class: "frow" }, h("input", { type: "checkbox", "data-key": "import-claude-md", checked: im.claude_md.copied,
        onchange: (e, n) => act("import", n.checked ? "Copying your CLAUDE.md" : "Removing the copy", async () => { pg.import = (await post("/api/import", { claude_md: n.checked })); }, up, n.checked ? "Copied your CLAUDE.md" : "Removed the copy") }), " Copy my CLAUDE.md")] : null,
    im.mcp.length ? p("Your MCP servers: tick the ones agents here may use. Unticked ones stay with your own Claude Code.") : null,
    ...im.mcp.map(n => h("label", { class: "frow" }, h("input", { type: "checkbox", "data-key": "import-mcp-" + n, checked: (im.mcp_copied || []).includes(n),
      title: (im.mcp_secrets || {})[n] ? "Copies these values in clear into the Hive's own file: " + im.mcp_secrets[n].join(", ") : null, onchange: (e, node) => { const on = new Set(im.mcp_copied || []); node.checked ? on.add(n) : on.delete(n);
        act("import", "Saving your MCP choice", async () => { pg.import = (await post("/api/import", { mcp: [...on] })); }, up, "Saved your MCP choice"); } }), " " + n, (im.mcp_secrets || {})[n] ? h("small", { class: "muted", "data-key": "import-mcp-secret-" + n }, " carries " + im.mcp_secrets[n].join(", ") + ": ticking copies the values, in clear, into the Hive's own file") : null)));
  const claudeIn = s.signed_in, codex = pg.accounts && pg.accounts.codex, codexIn = codex && (codex.signed_in || (codex.logins || []).some(l => l.signed_in));
  return page(
    card("account", null, rootNote(),
      h("div", { class: "setup-list" },
        h("div", { class: "setup-opt signin", "data-key": "claude" },
          h("span", { class: "av", "data-s": "l" }, "C"),
          h("span", { class: "grow" }, h("b", null, "Claude"), h("span", { class: "meta" }, simpleOn() ? "Your Hive works through your Claude account." : "Use your Claude plan. Agents run on it and use its limits.")),
          s.claude === false ? statusLabel("ended", "not installed") : claudeIn ? statusLabel("working", "signed in")
            : can("logins") ? button("Sign in", () => signIn(false), { kind: "primary", busy: signingIn(), title: "Sign in to Claude Code" }) : statusLabel("ended", "not signed in")),
        s.claude === false ? p("To use it, in a terminal as this account: ", h("code", null, "curl -fsSL https://claude.ai/install.sh | bash"), " Then restart this app.") : null,
        logins.length > 1 ? h("p", { class: "field-h" }, logins.filter(l => l.signed_in).length + " of " + logins.length + " logins signed in.") : null,
        s.claude !== false && !claudeIn && can("logins") ? terminalRoute(logins) : null,
        signinBox("start", up),
        codex ? h("div", { class: "setup-opt", "data-key": "codex-opt" },
          h("span", { class: "av", "data-s": "l" }, "G"),
          h("span", { class: "grow" }, h("b", null, "ChatGPT, through Codex"), h("span", { class: "meta" }, "Use a ChatGPT plan instead, or as well.")),
          codexIn ? statusLabel("working", "signed in") : button(F.codex ? "Hide" : "Sign in", () => { F.codex = !F.codex; up(); })) : null,
        codex && (F.codex || codexIn) ? h("div", { class: "setup-sub" }, codexPart(up)) : null),
      simpleOn() ? null : h("details", { class: "fold-row", "data-key": "api-key", open: F.keyOpen || null, ontoggle: (e, n) => { F.keyOpen = n.open; } },
        h("summary", null, "I have an API key instead", h("small", null, "Anthropic or OpenRouter")), h("div", { class: "setup-sub" }, keysPart(up)))),
    importCard,
    foot(anyAccount() ? button("Continue", () => go(2), { kind: "primary" }) : h("span", { class: "meta" }, "Add your account to go on.")));
}

// Right after Finish: the first message, with two jobs to try (R7 "You're set up").
function setUp(ctx) {
  return h("div", { class: "page wt", "data-width": "narrow", "data-key": "start" },
    h("div", { class: "ph" }, h("div", { class: "ph-t" }, h("span", { class: "ph-k" }, "Set up · done"), h("h1", { class: "wt-h" }, "You're set up"),
      h("p", { class: "wt-lead" }, h("b", null, "What do you want done?"), " Say it in your own words. An agent starts on it and answers you here."))),
    h("div", { class: "setup-steps", "aria-hidden": "true" }, h("i", { "data-on": "" }), h("i", { "data-on": "" }), h("i", { "data-on": "" })),
    h("p", { class: "fmsg", "data-tone": "good", "data-key": "set-note" }, "You can change sharing, power and accounts in ", h("a", { href: href("settings") }, "Settings"), " any time."),
    composer({ thread: null, update: ctx.update, place: "start", autofocus: true }),
    h("div", { class: "btn-row", "data-key": "starters", role: "group", "aria-label": "Jobs to try" }, h("span", { class: "meta" }, "Try one:"),
      FIRST_JOBS.slice(0, 2).map(([label, text]) => button(label, () => { draft(null).text = text; focusComposer(); ctx.update(); }, { size: "s" }))),
    activityLog({ hideEmpty: true }));
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
    if (S.app.can == null) return h("div", { class: "page", "data-width": "narrow" }, waiting(S.failed));   // /api/app hasn't answered: don't say "only in the app" yet
    if (!S.app.app) return h("div", { class: "page", "data-width": "narrow" }, h("p", null, "Setup shows only in the app, opened from this machine."));
    const s = S.app.setup || {}, signed = s.signed_in || s.api_key || s.other, cfgd = S.app.settings;
    if (cfgd && cfgd.setup_done === false) return walkthrough(ctx, s);
    if (F.finished && !hasSent() && can("post")) return setUp(ctx);
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
    return h("div", { class: "page stp", "data-width": "narrow", "data-key": "start" },
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
