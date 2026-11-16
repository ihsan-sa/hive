// Settings > Wipe the knowledge base (the person's ask k2264). Nothing is deleted: the server moves every note but the person's
// priorities and standing directions into a private copy, and Put back merges a copy in again without overwriting anything.
// The wipe window has three steps (the facts, three ticks, the typed name with a 10 s countdown) and sends nothing until its last button.
// The server holds the real checks (dash.py kb_wipe_post); this page only makes them plain. Tests: test_kb_wipe.py, drive_kb_wipe.mjs.
// The window never says "Nothing was moved" on a guess: only a refusal (a 4xx, or a sign-in page) proves it. When the answer is lost or
// broken, it reads the list of copies and says what that shows: a new copy means notes moved (and the copy's own record says whether
// the wipe finished or stopped part way), none means they didn't. A list that shows nothing happened is read a second time a moment
// later, since the request may not have reached the server yet when the first read was answered.
import { h, tr } from "../core/dom.js";
import { S, said, activeNow } from "../core/store.js";
import { section, button, blocks, block, signInAgain } from "./kit.js";
import { confirm } from "./dialog.js";
import { ago } from "../core/format.js";

const K = { copies: null, t: 0, err: "", busy: false, open: false, asking: false };   // asking: the facts are being read (the button shows it); open: the window is up

/** fetch that keeps the server's own words and its signin flag (store.post drops the flag, store.jget leaves the page on a 401). */
async function ask(path, body) {
  const r = await fetch(path, body === undefined
    ? { cache: "no-store", credentials: "same-origin", headers: activeNow() ? { "X-Hive-Active": "1" } : {} }
    : { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-Hive-Dash": "1" }, body: JSON.stringify(body) });
  if (r.redirected) { const e = Error("sign in again and reload the page"); e.signin = true; throw e; }
  const j = await r.json().catch(() => ({}));
  if (!r.ok) { const e = Error(j.error || said(r.status)); e.status = r.status; e.signin = !!j.signin; throw e; }
  return j;
}

const size = b => b >= 2 ** 20 ? `${(b / 2 ** 20).toFixed(1)} MB` : `${b ? Math.max(1, Math.round(b / 1024)) : 0} KB`;
const files = n => `${n} ${tr(n === 1 ? "file" : "files")}`;
/** A refusal: the server (or the sign-in in front of it) said no before anything could move. Anything else leaves it open. */
const refused = e => !!e.signin || (e.status >= 400 && e.status < 500);
/** The copies once no wipe or put back is running (the server says while one is, and a list read mid-way would be wrong about what moved),
 * or null when they can't be read, or one is still running after half a minute. */
const copiesNow = async () => {
  try {
    for (let i = 0; i < 30; i++) {
      const j = await ask("/api/kb/copies");
      if (!j.busy) return Array.isArray(j.copies) ? j.copies : null;
      await new Promise(r => setTimeout(r, 1000));
    }
  } catch {}
  return null;
};
/** The copies after an answer got lost. A list that shows nothing happened is read once more a moment later: the request may still be
 * on its way to the server's lock, and a list read before it gets there would say nothing moved. null when a read fails. */
const LOST_WAIT = 2500;
const copiesAfter = async happened => {
  let now = await copiesNow();
  if (now && !happened(now)) { await new Promise(r => setTimeout(r, LOST_WAIT)); now = await copiesNow(); }
  return now;
};
/** Which Hive: its account and machine, since two Hives can share one machine. */
const where = plan => (plan.user ? `${plan.user}@${plan.host}` : plan.host);

async function loadCopies(update) {
  if (K.busy) return;
  K.busy = true;
  try { K.copies = (await ask("/api/kb/copies")).copies || []; K.err = ""; K.t = Date.now(); }
  catch (e) { K.err = e.message; K.copies = K.copies || []; K.t = Date.now(); }
  finally { K.busy = false; update && update(); }
}

/** The section, last in Settings. Shown only when /api/app says this sign-in may wipe (app_info kb_wipe). */
export function kbWipeSection(ctx) {
  if (!S.app.kb_wipe) return null;
  const shown = (ctx.arg || "").split("/")[0] === "wipe-the-knowledge-base";   // read the copies only while this section is the one on screen
  if (shown && (K.copies == null || Date.now() - K.t > 30000) && !K.busy) loadCopies(ctx.update);
  const cs = K.copies || [];
  return section({ title: "Wipe the knowledge base", key: "kb-wipe" },
    h("p", { class: "meta" }, "Moves every note in the knowledge base into a private copy on this machine, so agents start again without what they learned. Your priorities and standing directions stay. Nothing is deleted, and a copy can be put back."),
    h("div", { class: "row" }, button("Wipe the knowledge base…", () => openWipe(ctx), { kind: "danger", busy: K.asking })),
    K.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, K.err) : null,
    cs.length ? [h("h3", { class: "set-sub" }, "Copies from earlier wipes"),
      blocks(cs.map(c => block({ key: "copy-" + c.id, title: c.id, sub: [files(c.files), " · ", size(c.bytes), c.at ? [" · ", ago(Date.parse(c.at))] : null,
          c.state === "stopped" ? [" · ", tr("from a wipe that stopped part way")] : c.state === "moving" ? [" · ", tr("from a wipe that hasn't finished")] : null,
          c.back?.length ? [" · ", tr("left from a put back")] : null],
        actions: button("Put back…", () => openPutBack(ctx, c), { size: "s" }) })))] : null);
}

function dialogShell(title) {
  const body = h("div", { class: "kbw-b" }), err = h("div", { class: "dlg-err", role: "status" }), acts = h("div", { class: "dlg-a" });
  const dlg = h("dialog", { class: "dlg", "data-key": "kb-wipe-dlg", "aria-label": title }, h("form", { method: "dialog", novalidate: "", onsubmit: e => e.preventDefault() }, h("h2", null, title), body, err, acts));
  // Escape closes (the browser's own cancel); a click on the dim area outside the window closes it too.
  dlg.addEventListener("click", e => { if (e.target === dlg) dlg.close(); });
  return { dlg, body, err, acts };
}

/** The wipe: read the facts (a GET, which changes nothing), then three steps. Only the last button sends. */
async function openWipe(ctx) {
  if (K.open) return;
  K.open = K.asking = true; ctx.update();
  let plan;
  try { plan = await ask("/api/kb/wipe"); }
  catch (e) { K.open = K.asking = false; K.err = e.message; ctx.update(); return; }
  K.asking = false; ctx.update();   // the window is up from here: the button behind it must not look as if something were still running
  const got = Date.now();
  const { dlg, body, err, acts } = dialogShell("Wipe the knowledge base");
  let timer = null;
  const close = button("Cancel", () => dlg.close(), {});
  dlg.addEventListener("close", () => { clearInterval(timer); dlg.remove(); K.open = false; ctx.update(); });
  const fill = (...kids) => { body.replaceChildren(); for (const k of kids.flat()) if (k) body.append(k); };
  const p = (...kids) => h("p", null, ...kids);

  if (plan.refuse || plan.signin) {
    fill(plan.signin
      ? p("A wipe from outside needs a sign-in from the last 15 minutes. Sign in again, then open this again.")
      : p("Nothing can be wiped now: ", plan.refuse, "."));
    acts.replaceChildren(...[plan.signin ? button("Sign in again", signInAgain, { kind: "primary", href: "/cdn-cgi/access/logout" }) : null, close].filter(Boolean));
    document.body.append(dlg); dlg.showModal(); close.focus();
    return;
  }

  const ticks = { hive: false, lose: false, space: false };
  const TICK = {
    hive: "This is the Hive I mean to wipe.",
    lose: "Agents lose what they learned here until I put the copy back.",
    space: "The copy stays on this machine and still takes up its space.",
  };

  const step1 = () => {
    err.textContent = "";
    fill(
      h("p", { class: "kbw-step" }, "Step 1 of 3: what moves"),
      h("dl", { class: "kbw-facts" },
        h("dt", null, "Hive"), h("dd", null, h("b", null, plan.name), plan.name === where(plan) ? null : [" · ", where(plan)]),
        plan.home ? [h("dt", null, "Its folder"), h("dd", null, h("code", null, `${plan.home}/kb`))] : null,
        h("dt", null, "Moves"), h("dd", null, files(plan.files), " · ", size(plan.bytes), " · ", `${plan.entries} `, tr(plan.entries === 1 ? "top-level entry" : "top-level entries")),
        h("dt", null, "Stays"), h("dd", null, plan.keep.length ? plan.keep.join(", ") : tr("nothing: neither kept file is there")),
        h("dt", null, "Running now"), h("dd", null, plan.agents.length ? plan.agents.join(", ") : tr("no agents"))),
      plan.agents.length ? p("Agents running now may be reading or writing notes while they move. A note one of them writes after the wipe stays in the knowledge base.") : null,
      p("Everything moves into a private copy on this machine, which Settings can put back. Search stops returning the moved notes."),
      p("Only the knowledge base moves. The board, the projects, the core and the modules with their own notes stay as they are."),
      plan.busy ? h("p", { class: "fmsg", "data-tone": "warn" }, "A wipe or a put back is running now.") : null);
    acts.replaceChildren(close, button("Next", step2, { kind: "primary" }));
  };

  const step2 = () => {
    err.textContent = "";
    const next = button("Next", step3, { kind: "primary", disabled: true });
    const ready = () => { next.disabled = !Object.values(ticks).every(Boolean); };
    fill(h("p", { class: "kbw-step" }, "Step 2 of 3: tick each one"),
      h("div", { class: "checks", role: "group", "aria-label": "Tick each one" }, Object.keys(ticks).map(k =>
        h("label", { class: "check", "data-key": "tick-" + k },
          h("input", { type: "checkbox", name: k, checked: ticks[k], onchange: (e, n) => { ticks[k] = n.checked; ready(); } }),
          h("span", null, TICK[k], k === "hive" ? [" ", h("b", null, plan.name), plan.name === where(plan) ? null : [" · ", where(plan)]] : null)))));
    acts.replaceChildren(button("Back", step1, {}), next); ready();
    body.querySelector("input").focus();
  };

  const step3 = () => {
    err.textContent = "";
    const code = h("input", { class: "input", name: "phrase", autocomplete: "off", spellcheck: "false", autocapitalize: "off", "aria-label": "Type the words shown to confirm" });
    const go = button("Wipe now", null, { kind: "danger", disabled: true });
    // Ten seconds at least, and never before the server's token works (15 s after the facts were read).
    const until = Date.now() + Math.max(10000, (plan.wait ?? 15) * 1000 - (Date.now() - got) + 500);
    const left = () => Math.max(0, Math.ceil((until - Date.now()) / 1000));
    const ready = () => {
      const n = left();
      go.disabled = n > 0 || code.value !== plan.phrase || go.dataset.busy === "1";
      go.textContent = n > 0 ? `${tr("Wipe now")} (${n})` : tr("Wipe now");
    };
    // No Tab completion here: the whole name is typed by hand.
    code.addEventListener("input", ready);
    code.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); if (!go.disabled) go.click(); } });
    go.addEventListener("click", async () => {
      if (go.disabled) return;
      go.dataset.busy = "1"; go.setAttribute("aria-busy", "true"); ready(); err.removeAttribute("data-ok"); err.textContent = tr("Moving the notes…");
      try {
        const r = await ask("/api/kb/wipe", { token: plan.token, phrase: code.value, ticks: { ...ticks } });
        if (!r || typeof r.id !== "string") throw Error(tr("the answer wasn't one this page can read"));   // a 200 that isn't the server's answer proves nothing
        clearInterval(timer);
        fill(p(r.ok ? "Done. The notes moved into a private copy, which Settings can put back." : "It stopped part way, so some notes are still in place."),
          p(files(r.files), " · ", size(r.bytes), " · ", tr("copy"), " ", h("code", null, r.id)),
          r.stopped ? p(tr("Where it stopped"), ": ", r.stopped) : null, r.search ? p(tr(r.search)) : null,
          r.noted === false ? p("The line on the board that tells agents about this couldn't be written.") : null);
        err.textContent = "";
        const done = button("Close", () => dlg.close(), { kind: "primary" });
        acts.replaceChildren(done); done.focus();
        K.copies = null;
      } catch (e) {
        clearInterval(timer);
        code.disabled = true; close.textContent = tr("Close");
        // The next window opens from this one's close event, after the listener above has cleared K.open: a timer could run before that event and open nothing.
        const again = button("Start again", () => { dlg.addEventListener("close", () => openWipe(ctx)); dlg.close(); }, { kind: "primary" });
        if (refused(e)) {
          err.textContent = `${tr("Nothing was moved")}: ${tr(e.message)}.`;
          acts.replaceChildren(close, e.signin ? button("Sign in again", signInAgain, { kind: "primary", href: "/cdn-cgi/access/logout" }) : again);
        } else {
          // No refusal and no answer: the notes may have moved. The list of copies settles it, since every wipe that moves a note leaves a new one.
          err.textContent = tr("Checking what happened…");
          const had = new Set((plan.copies || []).map(c => c.id)), fresh = l => l.filter(c => !had.has(c.id));
          const now = await copiesAfter(l => fresh(l).length), made = now && fresh(now);
          K.copies = null;
          if (!dlg.isConnected) return;
          err.textContent = "";
          const got = h("p", { class: "meta" }, tr("What this page got instead of an answer"), ": ", tr(e.message), ".");
          if (!now) {
            fill(p("The answer didn't arrive, so this page can't tell whether the notes moved. Reload the page and look at the copies in this section before you try again."), got);
            acts.replaceChildren(close, button("Reload the page", () => location.reload(), { kind: "primary" }));
          } else if (made.length) {
            // The copy's own record says how its wipe ended. One that stopped part way, or that never got to say, left notes in place (or may have).
            const part = made.find(c => c.state === "stopped"), open = made.find(c => c.state !== "done" && c.state !== "stopped");
            fill(p(part ? "The answer didn't arrive, and the wipe stopped part way: some notes moved into a new copy, and the rest are still in place."
                : open ? "The answer didn't arrive, but notes moved into a new copy. The copy doesn't say whether the wipe finished, so some notes may still be in place."
                : "The answer didn't arrive, but the notes moved: there is a new copy, which Settings can put back."),
              made.map(c => p(files(c.files), " · ", size(c.bytes), " · ", tr("copy"), " ", h("code", null, c.id))),
              part?.error ? p(tr("Where it stopped"), ": ", part.error) : null, got);
            acts.replaceChildren(button("Close", () => dlg.close(), { kind: "primary" }));
          } else {
            fill(p("The answer didn't arrive, and there is no new copy, so nothing seems to have moved."), got);
            acts.replaceChildren(close, again);
          }
          acts.lastChild.focus();
        }
      } finally { delete go.dataset.busy; go.removeAttribute("aria-busy"); }
    });
    fill(h("p", { class: "kbw-step" }, "Step 3 of 3: type the words"),
      p("To wipe, type ", h("b", { class: "kbw-phrase" }, plan.phrase), " exactly."),
      h("label", { class: "field" }, h("span", { class: "field-l" }, "The words"), code));
    acts.replaceChildren(button("Back", () => { clearInterval(timer); step2(); }, {}), go);
    clearInterval(timer); timer = setInterval(ready, 250); ready(); code.focus();
  };

  step1();
  document.body.append(dlg); dlg.showModal(); acts.lastChild.focus();
}

/** Put back one copy: merge it into the knowledge base without overwriting; one typed confirmation. */
function openPutBack(ctx, c) {
  const words = c.phrase || `put back ${c.id}`;
  const last = c.back?.length ? c.back[c.back.length - 1] : null;
  // A whole sentence of this page's own, which ends the window (dialog.js shows x.message as it is when x.whole is set).
  const whole = text => { const e = Error(text); e.whole = true; return e; };
  const dlg = confirm({ title: "Put a copy back", ok: "Put back", typed: words, failText: "Nothing was put back",
    body: [h("p", null, files(c.files), " · ", size(c.bytes), " · ", tr("copy"), " ", h("code", null, c.id)),
      "Each note goes back where it was. A note of the same name that is in the knowledge base now stays as it is, and the one from the copy stays in the copy.",
      last ? (last.stopped ? "The last put back of this copy couldn't move everything, and what it couldn't move is still here."
        : "This copy was put back before. What is in it now stayed here because notes of the same names were in the knowledge base.") : null],
    busyText: "Putting the notes back…",
    onOk: async typed => {
      let r;
      try {
        r = await ask("/api/kb/putback", { id: c.id, confirm: typed });
        if (!r || typeof r.files !== "number") throw Error(tr("the answer wasn't one this page can read"));
      } catch (e) {
        if (e.signin) throw Error("this sign-in is too old, so sign in again first");
        if (refused(e)) throw e;
        // No refusal and no answer: what the copy holds now says whether notes went back.
        const went = l => { const x = l.find(y => y.id === c.id); return !x || x.files < c.files; };
        const now = await copiesAfter(went), same = now && now.find(x => x.id === c.id);
        K.copies = null; ctx.update();
        throw whole(!now ? tr("The answer didn't arrive, so this page can't tell whether the notes went back. Reload the page and look at the copies in this section before you try again.")
          : !same ? tr("The answer didn't arrive, but the notes went back: nothing is left in this copy.")
          : same.files < c.files ? `${tr("The answer didn't arrive, but some notes went back. Still in the copy")}: ${files(same.files)}.`
          : tr("The answer didn't arrive, and the copy is as it was, so nothing seems to have gone back."));
      }
      K.copies = null; ctx.update();
      const count = `${files(r.files)} ${tr("returned")}` + (r.left ? `; ${r.left} ${tr("stayed in the copy because a note of the same name is there now")}` : "");
      const more = (r.search ? ` ${tr(r.search)}` : "") + (r.noted === false ? ` ${tr("The line on the board that tells agents about this couldn't be written.")}` : "");
      // It went through the whole copy: r.stopped is the first entry that couldn't move (its path and the system's reason), r.stuck how many couldn't.
      if (r.stopped) throw whole(`${tr("Not everything could be moved, and the rest is still in the copy")}: ${count}. ${tr("What couldn't be moved")}: ${tr(r.stopped)}.`
        + (r.stuck > 1 ? ` ${tr("Others that couldn't be moved")}: ${r.stuck - 1}.` : "") + more);
      return `${tr("Done")}: ${count}.${more}`;
    } });
  dlg.addEventListener("click", e => { if (e.target === dlg) dlg.close(); });
}
