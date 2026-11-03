// Start: first run, as the start of the conversation. Hive says what it is, you sign in to Claude, (start the hive if it's
// down), and say what you want done in the same composer Chat uses. Sending opens the new conversation, where an agent answers.
import { h } from "../core/dom.js";
import { S, can, post, loadApp, setupDone } from "../core/store.js";
import { button, statusLabel } from "../ui/kit.js";
import { startSignin, signinBox } from "../ui/signin.js";
import { composer, outFor, actions as outActions } from "../ui/composer.js";
import { localMessage } from "../ui/message.js";

const F = { starterErr: "", chose: null, know: false };

const hive = (key, ...body) => h("article", { class: "msg st-msg", "data-who": "agent", "data-key": key },
  h("div", { class: "msg-h" }, h("b", { class: "st-hive" }, "Hive")), h("div", { class: "msg-b" }, body));
const you = (key, text) => h("article", { class: "msg", "data-who": "me", "data-key": key }, h("div", { class: "msg-b" }, text));
const p = (...t) => h("p", null, ...t);

export default {
  id: "start", label: "Start", tab: () => S.app.app && can("post") && !setupDone(),
  render(ctx) {
    if (!S.app.app) return h("div", { class: "page" }, h("p", null, "Setup shows only in the app, opened from this machine."));
    const s = S.app.setup || {}, signed = s.signed_in || s.api_key;
    const signIn = api => { F.chose = api ? "Use API billing instead" : "Sign in with my Claude subscription"; startSignin("start", null, api, ctx.update); };
    const rows = [
      hive("hi", p("Hi. Hive runs Claude agents on this computer. Tell it what you want done, in your own words, and an agent works on it in the background and answers you here.")),
    ];
    if (s.claude === false) rows.push(hive("claude", p("Claude Code isn't installed for this account yet, and the agents need it. In a terminal, as this account:"),
      h("pre", null, h("code", null, "curl -fsSL https://claude.ai/install.sh | bash")), p("Then restart this app: ", h("code", null, "systemctl --user restart hive-dash"))));
    if (!signed) {
      rows.push(hive("signin", p("First, sign in to Claude. Agents run on your Claude subscription and use its limits. You can use API billing instead, which charges per use."),
        can("logins") && !F.chose ? h("div", { class: "frow" }, button("Sign in with Claude", () => signIn(false), { kind: "primary" }), button("Use API billing instead", () => signIn(true))) : null,
        can("logins") ? null : p(h("span", { class: "muted" }, "This connection can't sign in. Open the app on this computer."))));
      if (F.chose) { rows.push(you("chose", F.chose)); rows.push(hive("box", signinBox("start", ctx.update) || p("Starting Claude's sign-in…"),
        button("Start over", () => { F.chose = null; ctx.update(); }, { kind: "quiet", size: "s" }))); }
    } else rows.push(hive("signed", p(statusLabel("working", "Signed in to Claude."), " You can add a second account later, in Settings.")));
    if (s.starter === false) rows.push(hive("starter", p("The part of Hive that starts agents isn't running, so nothing you say would start one."),
      can("config") ? h("div", { class: "frow" }, button("Start it", async () => {
        try { await post("/api/starter", { do: "start" }); F.starterErr = "Starting…"; setTimeout(async () => { await loadApp(); F.starterErr = ""; ctx.update(); }, 4000); } catch (e) { F.starterErr = e.message; }
        ctx.update();
      }, { kind: "primary" }), F.starterErr ? h("span", { class: "fmsg" }, F.starterErr) : null) : null));
    if (signed) rows.push(hive("ask", p(h("b", null, "What do you want done?"), " Say it the way you'd tell a person: sort these photos, fix my site's contact form, watch this price. One message starts one agent, and it brings in helpers if the job has parts.")));
    const out = outFor(null);
    return h("div", { class: "page st", "data-width": "narrow", "data-key": "start" },
      h("div", { class: "st-log", role: "log", "aria-label": "Setting up" }, rows, out.map(it => localMessage(it, outActions))),
      signed && can("post") ? composer({ thread: null, update: ctx.update, place: "start", autofocus: !out.length }) : h("p", { class: "meta st-wait" }, "Once you're signed in, you can say what you want done."),
      h("div", { class: "st-know" },
        button(F.know ? "Hide what's good to know" : "Good to know before you start", () => { F.know = !F.know; ctx.update(); }, { kind: "quiet", size: "s" }),
        F.know ? h("ul", { class: "plain" },
          h("li", null, "Agents can do anything this computer account can do, so give Hive an account of its own."),
          h("li", null, "They use your Claude limits on their own. New agents stop at a usage line you set in Settings. With API billing, set a spending limit in the Anthropic Console too."),
          h("li", null, "This page answers only on this computer, and only to a browser that opened the address from ", h("code", null, "hive web"), ".")) : null));
  },
};
