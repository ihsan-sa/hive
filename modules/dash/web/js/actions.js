// Things that change the hive. Each is a confirm window; controls appear only where can() allows.
import { h } from "./core/dom.js";
import { S, can, built, post, poll, pollUsage } from "./core/store.js";
import { short } from "./core/format.js";
import { threadTitle, tierOf } from "./core/model.js";
import { TIERS, tier } from "./core/status.js";
import { confirm } from "./ui/dialog.js";
import { button, input, field } from "./ui/kit.js";

// ---- Kill: the window is the first step, typing the exact code the second. The server checks the code again. ----
export function killButton(n) {
  if (!can("stop")) return null;
  if (S.app.app) return button("Stop…", e => { e.stopPropagation(); askStop(n); }, { kind: "quiet", size: "s", title: `Stop ${n}`, cls: "ac-kill" });
  return button("Kill", e => { e.stopPropagation(); askKill(n); }, { kind: "quiet", size: "s", tone: "bad", title: `Stop ${n}'s run now`, cls: "ac-kill" });
}
export function askKill(n) {
  const L = S.running.get(n), say = { 400: "the code didn't match", 403: "this connection isn't allowed to kill", 409: `${n} isn't running any more` };
  confirm({
    title: `Kill ${n}?`, ok: `Kill ${n}`, kind: "danger", typed: n, failText: "Nothing was killed",
    body: [
      L && L.doing ? h("p", { class: "meta" }, "Doing now: " + short(L.doing, 160)) : null,
      "This ends the run and the shells it started, right away, without letting it save its work.",
      "The starter won't resume it, and any wakes queued for it are dropped. A new post that mentions it starts it again. Jobs it detached with nohup keep running, and you'll be told their pids.",
    ],
    onOk: async code => {
      let d;
      try { d = await post("/api/kill", { name: n, confirm: code }); }
      catch (e) { throw Error(say[e.status] || e.message); }
      await poll();
      const stuck = d.stuck || [], det = d.detached || [];
      const left = [stuck.length ? `These didn't die: ${stuck.join(", ")}.` : "", det.length ? `Detached jobs still running: ${det.join(", ")}.` : ""].filter(Boolean).join(" ");
      return left ? `${n} was killed. ${left}` : "";
    },
  });
}

// ---- Stop: the plain way to stop an agent, from the conversation. Not built yet on the server (POST /api/stop). ----
// "After this step" lets it finish what it's doing and save, then end; a message wakes it again. Kill stays the hard stop
// with its typed code, one click further.
export function askStop(n) {
  if (!can("stop")) return;
  if (!built("stop")) return askKill(n);
  const L = S.running.get(n);
  const dlg = confirm({
    title: `Stop ${n}?`, ok: "Stop after this step", failText: "It wasn't stopped",
    body: [
      L && L.doing ? h("p", { class: "meta" }, "Doing now: " + short(L.doing, 160)) : null,
      `${n} finishes the step it's on, saves what it has, and stops. Nothing is lost, and a message in its conversation wakes it again.`,
      h("p", { class: "meta" }, h("span", { class: "nb" }, "not built yet"), " The server needs POST /api/stop; see PART1.md."),
      h("p", { class: "meta" }, "Need it to stop this second? ", h("button", { type: "button", class: "linkbtn", onclick: () => { dlg.close(); askKill(n); } }, "Kill it instead"), ", which ends it at once without saving."),
    ],
    onOk: async () => { await post("/api/stop", { name: n, when: "after-step" }); await poll(); },
  });
}

// ---- Pause or resume a login ----
export function askPause(U, l) {
  const on = !l.paused, others = (U.logins || []).filter(x => x !== l && !x.paused), i = (U.logins || []).indexOf(l);
  confirm({
    title: (on ? "Pause " : "Resume ") + l.name + "?", ok: on ? "Pause account" : "Resume account",
    body: on ? [`The starter gives ${l.name} no new runs, and resumes none on it. Its usage keeps being read and shown here.`,
      "Agents already running on it finish their runs. Runs stopped on a limit move to another account.",
      null,
      !others.length ? "This is the last account not paused. Nothing can start until you resume one, not even runs you ask for." : null]
      : [`The starter can use ${l.name} again. New runs go to whichever account has the most weekly room.`],
    onOk: async () => { await post("/api/login-pause", { name: l.name, paused: on }); await pollUsage(); },
  });
}

const pctInput = (v, ph) => input({ type: "number", min: 0, max: 100, step: 1, value: v ?? "", placeholder: ph, style: "width:7rem" });
const val = (i, hi) => { const v = i.value.trim(); if (v === "") return null; const n = Number(v); if (!isFinite(n) || n < 0 || n > hi) throw Error(`${v} isn't a number from 0 to ${hi}`); return n; };

// ---- A login's stop lines: weekly and 5-hour. Empty means the default. ----
export function askLimits(U, l) {
  const st = l.stop || {}, g = st.global ?? U.stop_pct;
  const w = pctInput(st.weekly, String(g)), f = pctInput(st.five_hour, "none");
  confirm({
    title: "Stop lines for " + l.name, ok: "Save",
    body: [`The starter stops giving ${l.name} new runs once it passes either line. Runs already on it finish. Your own posts still start runs past the lines, up to the real limit.`,
      field("Weekly, %", w, `Empty: the global line, ${g}%.`),
      l.no_five_hour ? null : field("5-hour, %", f, "Empty: no 5-hour stop. Running sessions report it every turn; the usage API is read every 30 s at best, slower when it rate-limits.")],
    onOk: async () => { await post("/api/login-limit", { name: l.name, weekly: val(w, 100), five_hour: l.no_five_hour ? st.five_hour ?? null : val(f, 100) }); await pollUsage(); },
  });
}

// ---- The line every account stops at unless it has its own. ----
export function askStopLine(U) {
  const i = pctInput(U.stop_pct, "98");
  confirm({
    title: "Where new agents stop", ok: "Save",
    body: ["Past this share of its weekly limit, an account gets no new agents unless you ask for them. An account with its own line uses that instead. Subscriptions only.",
      field("Weekly, %", i)],
    onOk: async () => { const v = val(i, 100); if (!v) throw Error("Give a number from 1 to 100."); await post("/api/config", { usage_stop_pct: v }); await pollUsage(); },
  });
}

// ---- A provider's line: Codex's stop percent, OpenRouter's monthly dollars. Empty: back to config.json. ----
export function askProviderLine(name, p) {
  const cx = p.kind === "codex", cur = cx ? p.stop_pct : p.monthly_cap;
  const i = input({ type: "number", min: 0, max: cx ? 100 : 1000, step: cx ? 1 : 0.5, value: cur ?? "", placeholder: cx ? "95" : "10", style: "width:7rem" });
  confirm({
    title: (cx ? "Stop line for " : "Monthly limit for ") + name, ok: "Save",
    body: [cx ? `The starter sends no new runs to ${name} once any of its windows (5-hour, weekly) passes this line. Runs already on it finish.`
      : `The starter sends no new runs to ${name} once this month's spend (UTC) reaches this many dollars. It is real money: raising it lets the hive spend more.`,
      field(cx ? "Stop at, %" : "Limit, $", i, "Empty: back to the value in config.json.")],
    onOk: async () => { await post("/api/provider-limit", { name, key: cx ? "stop_pct" : "monthly_usd", value: val(i, cx ? 100 : 1000) }); setTimeout(pollUsage, 2500); },
  });
}

// ---- A thread's tier. Writes kb/priorities.md, the file the keeper follows. ----
export function askTier(th) {
  const P = (S.live && S.live.priorities) || {}, was = tierOf(th), setNow = (P.set || {})[th]; let pick = was;
  const desc = { tier1: "goes first; its jobs use the heavy fast lane", tier2: "next", held: "agents save it and stop; nothing new starts", other: "after tier 2" };
  const warn = h("p", { class: "meta", style: "color:var(--warn-ink)" });
  const opt = (k, l, d, onPick) => h("label", { class: "opt" }, h("input", { type: "radio", name: "tierpick", value: k, checked: k === was || null, onchange: onPick }), h("b", null, l), h("span", { class: "meta" }, d));
  confirm({
    title: "Tier for " + threadTitle(th), ok: "Save",
    body: [h("p", { class: "meta" }, `Now: ${tier(was).label}${setNow ? ", set here on the dashboard" : ", from the list in kb/priorities.md"}. Your choice is saved there, in its own section, and beats the list for this thread.`),
      ...TIERS.map(t => opt(t.key, t.key === "other" ? "Not in the order" : t.label, desc[t.key], () => { pick = t.key; warn.textContent = was === "held" && pick !== "held" ? "This takes it off hold. The keeper may start work on it again." : ""; })),
      setNow ? opt("", "Back to the list", "remove your dashboard setting", () => { pick = null; warn.textContent = "This goes back to what the list in kb/priorities.md says."; }) : null,
      warn],
    onOk: async () => { if (pick === was && setNow) return; await post("/api/tier", { thread: th, tier: pick }); await poll(); },
  });
}
