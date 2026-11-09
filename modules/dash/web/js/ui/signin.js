// Signing in to Claude with no terminal. One sign-in at a time; its state lives here so the page can re-render freely.
// The code the person pastes is held only until it is sent: it is never logged, stored or shown back.
import { h } from "../core/dom.js";
import { jget, post, loadApp } from "../core/store.js";
import { simpleOn } from "../core/simple.js";
import { button, input, field } from "./kit.js";
import { act, begin, finish, isBusy, note } from "./activity.js";

const LOST = "Can't reach the Hive to follow the sign-in. It keeps trying.";
const SI = { key: null, v: null, err: "", code: "", note: "", gen: 0 };

/** Begin a sign-in for a place on the page (key), a login name or null, and API billing (console); onDone(v) runs once when it has signed in. */
export function startSignin(key, name, api, update, onDone) {
  const gen = ++SI.gen;
  Object.assign(SI, { key, v: null, err: "", code: "", note: "", name: name || null, seen: null, got: false });
  begin("signin", "Starting the sign-in" + (name ? " for " + name : ""), update);
  let misses = 0;
  const tick = async () => {
    if (SI.gen !== gen) return;
    let v; try { v = await jget("/api/signin"); } catch (e) {
      if (++misses === 4 && !SI.err) { SI.err = LOST; update(); }   // about 6 s without an answer: say so instead of "Starting..." for ever
      return setTimeout(tick, 1500);
    }
    if (SI.gen !== gen) return;
    misses = 0; if (SI.err === LOST) SI.err = "";
    SI.v = v;
    if (v.state === "waiting" && SI.note === "Checking…") {
      const m = (v.out || "").match(/[^\r\n]*(invalid|error|failed|expired)[^\r\n]*/ig);
      if (m) { if (!SI.err) note("bad", "Claude didn't accept that code", update); SI.err = m[m.length - 1].trim(); }
    }
    tell(v, update);
    update();
    if (v.state === "done") { if (onDone) onDone(v); loadApp(); return; }
    if (v.state === "failed" || v.state === "cancelled") return;
    setTimeout(tick, 1500);
  };
  post("/api/signin", { name: name || null, console: !!api }).then(tick).catch(e => {
    if (SI.gen !== gen) return;
    if (/under way/.test(e.message)) return tick();
    SI.err = e.message; SI.v = { state: "failed", why: e.message }; tell(SI.v, update); update();
  });
}
// The log follows the state and nothing else: v.out is raw terminal output and stays out of it.
function tell(v, update) {
  if (v.state === SI.seen) return;
  SI.seen = v.state;
  if (v.state === "waiting") {
    if (!SI.got) { SI.got = true; finish("signin", "ok", "Got the sign-in page from Claude", update); }
    begin("signin", "Waiting for you to sign in", update);
  }
  else if (v.state === "checking") begin("signin", "Checking that it worked", update);
  else if (v.state === "done") finish("signin", "ok", "Signed in" + (v.name ? " as " + v.name : ""), update);
  else if (v.state === "failed") finish("signin", "bad", "Sign-in failed: " + (v.why || "no reason given"), update);
  else if (v.state === "cancelled") finish("signin", "info", "Sign-in cancelled", update);
}
export const signingIn = () => isBusy("signin");

const send = async update => {
  const code = SI.code; SI.code = ""; SI.err = ""; SI.note = "Checking…"; update();
  try { await act("signin-code", "Sending your code to Claude", () => post("/api/signin/code", { code }), update, "Sent your code to Claude"); } catch (e) { SI.err = e.message; SI.note = ""; }
  update();
};
const cancel = async update => { try { await act("signin-cancel", "Cancelling the sign-in", () => post("/api/signin/cancel", {}), update, "Asked the sign-in to stop"); } catch (e) { SI.err = e.message; } update(); };

/** Forget a sign-in that has finished, so an old result doesn't sit on the page. One still under way stays. */
export function clearSignin() {
  if (SI.key && SI.v && ["done", "failed", "cancelled"].includes(SI.v.state)) { SI.gen++; SI.key = null; SI.v = null; }
}

/** The sign-in box for one place on the page; null when this place isn't the one signing in. */
export function signinBox(key, update) {
  if (SI.key !== key) return null;
  const v = SI.v, say = (text, tone) => h("p", { class: "fmsg", "data-tone": tone || null, role: "status" }, text);
  const fallback = v && v.command && !simpleOn() ? h("p", { class: "field-h" }, "Doesn't work here? In a terminal, as this account: ", h("code", null, v.command)) : null;
  let body;
  if (!v || v.state === "idle" || !v.state) body = say("Starting Claude's sign-in…");
  else if (v.name && SI.name && v.name !== SI.name) body = say("Another sign-in is under way. Finish or cancel it first.", "warn");
  else if (v.state === "waiting") body = [
    h("ol", { class: "steps" },
      h("li", null, /^https:\/\//.test(v.url) ? h("a", { href: v.url, target: "_blank", rel: "noreferrer noopener" }, "Open the sign-in page") : "The sign-in page didn't give a usable link.", " and sign in."),
      h("li", null, "If it shows a code, paste it here.")),
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); if (SI.code.trim()) send(update); } },
      h("div", { style: "flex:1;min-width:12rem" }, field("Code", input({ placeholder: "Paste the code here", autocomplete: "off", spellcheck: "false", value: SI.code, oninput: (e, n) => { SI.code = n.value; } }))),
      button("Send", null, { kind: "primary", type: "submit", busy: isBusy("signin-code") }), button("Cancel", () => cancel(update), { kind: "quiet", busy: isBusy("signin-cancel") })),
    SI.err ? say(SI.err, "bad") : SI.note ? say(SI.note) : null, fallback];
  // After the code: Claude Code finishes, then a new account is added and makes one short test run (dash.py SignIn.watch, up to
  // 5 minutes). Before, this fell to "Starting Claude's sign-in…" and read as stuck (consumer feedback, 2026-11-08).
  else if (v.state === "checking") body = say(SI.key === "login-new" ? "Claude took the code. Now adding the account and making one short test run; this can take a minute or two." : "Claude took the code. Checking that the sign-in worked…");
  else if (v.state === "done") body = say(simpleOn() ? "Signed in. Your Hive is ready." : `Signed in${v.name ? " as login " + v.name : ""}.${v.said ? " " + v.said : ""}`, "good");
  else if (v.state === "failed") body = [say(v.why || "That didn't work.", "bad"), fallback];
  else if (v.state === "cancelled") body = say("Cancelled.");
  else body = say("Starting Claude's sign-in…");
  const over = v && ["done", "failed", "cancelled"].includes(v.state);
  return h("div", { class: "signin", "data-key": "signin-" + key }, body, over ? h("div", { class: "frow" }, button("Close", () => { clearSignin(); update(); }, { size: "s", kind: "quiet" })) : null);
}

/** The same sign-in by hand: Claude Code's own `claude auth login`, in the Hive's account (its own login folder when it has one). */
export function terminalRoute(logins) {
  if (simpleOn()) return null;   // simple view has no terminal
  const path = ((logins || [])[0] || {}).path, home = /^\/home\/[^/]+\/\.claude$/.test(path || "") || !path;
  return h("p", { class: "field-h", "data-key": "terminal-route" }, "Or in a terminal, as this account: ", h("code", null, (home ? "" : "CLAUDE_CONFIG_DIR=" + path + " ") + "claude auth login"));
}
