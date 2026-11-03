// Start: first run. Sign in, start the hive, say what you want done.
import { h } from "../core/dom.js";
import { S, can, post, loadApp, poll, setupDone } from "../core/store.js";
import { go } from "../core/router.js";
import { form } from "../templates.js";
import { pageHeader, section, button, field, statusLabel } from "../ui/kit.js";
import { startSignin, signinBox } from "../ui/signin.js";

const F = { text: "", err: "", busy: false, starterErr: "" };

const step = (n, done, title, ...body) => section({ title: `${n}. ${title}`, note: done ? "done" : null, key: "step" + n }, ...body);
const done = text => h("p", { class: "muted" }, statusLabel("working", "Done."), " ", text);

export default {
  id: "start", label: "Start", tab: () => S.app.app && can("post") && !setupDone(),
  render(ctx) {
    if (!S.app.app) return h("div", { class: "page" }, h("p", null, "Setup shows only in the app, opened from this machine."));
    const s = S.app.setup || {}, signed = s.signed_in || s.api_key;
    const begin = async () => {
      if (!F.text.trim() || F.busy) return;
      F.busy = true; F.err = ""; ctx.update();
      try { const r = await post("/api/post", { text: F.text }); F.text = ""; await loadApp(); go("comms", r.thread); poll(); }
      catch (e) { F.err = e.message; }
      F.busy = false; ctx.update();
    };
    const steps = [];
    let num = 0;   // step 2 only shows when the starter is down, so count rather than hard-code
    if (s.claude === false) steps.push(section({ title: "Install Claude Code", key: "claude" },
      h("p", null, "It isn't on this account's PATH. In a terminal, as this account: ", h("code", null, "curl -fsSL https://claude.ai/install.sh | bash"), ". Then restart this app: ", h("code", null, "systemctl --user restart hive-dash"))));
    steps.push(step(++num, signed, "Sign in to Claude", signed ? done("More logins and their limits are in Settings.") : [
      h("p", { class: "muted" }, "Agents run on your Claude subscription, or on API billing."),
      can("logins") ? h("div", { class: "frow" }, button("Sign in with a Claude subscription", () => startSignin("start", null, false, ctx.update), { kind: "primary" }), button("Use API billing instead", () => startSignin("start", null, true, ctx.update))) : h("p", { class: "muted" }, "This connection can't sign in."),
      signinBox("start", ctx.update)]));
    if (s.starter === false) steps.push(step(++num, false, "Start the hive", h("p", { class: "muted" }, "The service that starts agents isn't running, so nothing you post would start one."),
      can("config") ? h("div", { class: "frow" }, button("Start it", async () => {
        try { await post("/api/starter", { do: "start" }); F.starterErr = "Starting…"; setTimeout(async () => { await loadApp(); F.starterErr = ""; ctx.update(); }, 4000); } catch (e) { F.starterErr = e.message; }
        ctx.update();
      }, { kind: "primary" }), F.starterErr ? h("span", { class: "fmsg" }, F.starterErr) : null) : null));
    steps.push(step(++num, s.has_posts, "Say what you want done", s.has_posts ? done("Your threads are under Comms.") : [
      h("p", { class: "muted" }, "One message starts one agent. It can start others if the job has separate parts."),
      field("What do you want done?", h("textarea", { class: "input", rows: 3, value: F.text, placeholder: "For example: set up a repo for a small web scraper and get its tests passing", oninput: (e, n) => { F.text = n.value; } })),
      h("div", { class: "frow" }, button(F.busy ? "Starting…" : "Start", begin, { kind: "primary", disabled: !signed || F.busy }), F.err ? h("span", { class: "fmsg", "data-tone": "bad", role: "alert" }, F.err) : null)]));
    steps.push(section({ title: "Good to know", key: "know" }, h("ul", { class: "plain" },
      h("li", null, "Agents can do anything this account can do, so it should be an account of its own."),
      h("li", null, "They spend your usage on their own. New agents stop at the usage line in Settings, but only on a subscription: with API billing, set a limit in the Anthropic Console."),
      h("li", null, "This page answers only on this machine, and only to a browser that opened the address from ", h("code", null, "hive web"), "."))));
    return form({ header: pageHeader({ title: "Set up your hive", lead: "A hive is a team of Claude Code agents on this machine. You say what you want here, and they work on it in the background and report back." }), sections: steps });
  },
};
