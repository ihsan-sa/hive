// Settings › Buying › JLCPCB, and Settings › Connectors › Sign in to a site (any address, in the profile `web` reads with; warden
// 112500380c83): the person signs the machine's browser in from here (GET/POST /api/shop-signin, class ShopSignin in dash.py).
// Thread 11241110ade5, warden 11241330fa1c: only the person's own signed-in app sees or drives it (the server checks, as for Link); the person
// types the password and solves the captcha in the live view; it shows only while a sign-in is open and closes itself once signed in.
// Nothing here keeps, logs or shows back what is typed: each key goes to the server and is dropped.
import { h, tr } from "../core/dom.js";
import { jget, post } from "../core/store.js";
import { block, button, statusLabel, field, input } from "./kit.js";
import { confirm } from "./dialog.js";
import { ago } from "../core/format.js";

const J = { v: null, err: "", loading: false, gen: 0, dlg: null, url: "", say: null, busy: "" };
const SPECIAL = new Set(["Enter", "Backspace", "Tab", "Delete", "ArrowLeft", "ArrowUp", "ArrowRight", "ArrowDown", "Home", "End"]);
const OPEN = new Set(["starting", "open", "checking", "signed"]);

/** Reads /api/shop-signin once (Buying's load calls it when the server says this app is the person's own). */
export async function loadShops(update) {
  if (J.loading) return;
  J.loading = true;
  try { J.v = await jget("/api/shop-signin"); J.err = ""; } catch (e) { J.err = e.message; }
  J.loading = false; update();
}
/** Leaving Settings closes the live view and the browser with it. */
export function forgetShops() { J.url = ""; J.said = null; J.gen++; if (J.dlg) { const d = J.dlg; J.dlg = null; send({ do: "close" }); try { d.close(); } catch (e) {} d.remove(); } J.v = null; J.err = ""; }

// One request at a time, in order: keys sent side by side could arrive out of order and scramble a password (review a31c707b29fe918db).
let chain = Promise.resolve();
const send = body => (chain = chain.then(() => post("/api/shop-signin", body)).catch(e => { if (J.say) J.say(e.message); }));
// Leaving the page (tab closed, reload, phone navigates away) closes the browser too; the server also closes it when the screen stops being asked for.
const closeOnLeave = () => { if (J.dlg) fetch("/api/shop-signin", { method: "POST", keepalive: true, headers: { "Content-Type": "application/json", "X-Hive-Dash": "1" }, body: JSON.stringify({ do: "close" }) }).catch(() => {}); };
addEventListener("pagehide", closeOnLeave);

function openView(site, name, update, url) {
  const web = site === "web";
  if (J.dlg) return;
  const gen = ++J.gen;
  const img = h("img", { class: "shop-screen", alt: `The ${name} page on the Hive's browser`, draggable: "false", tabindex: "0" });
  const line = h("p", { class: "fmsg", role: "status" }, "Starting the browser…");
  const typed = h("input", { class: "input", type: "password", autocomplete: "off", autocapitalize: "off", spellcheck: "false", "aria-label": "Type into the page", placeholder: "Type here, then Send (phones)" });
  const show = button("Show", () => { const hid = typed.type === "password"; typed.type = hid ? "text" : "password"; show.textContent = tr(hid ? "Hide" : "Show"); show.setAttribute("aria-pressed", String(hid)); }, { size: "s", kind: "quiet" });
  show.setAttribute("aria-pressed", "false");
  const finish = () => {
    if (J.dlg !== dlg) return;
    J.dlg = null; J.gen++; send({ do: "close" });
    try { dlg.close(); } catch (e) {} dlg.remove(); loadShops(update);
  };
  const sendTyped = () => { const s = typed.value; typed.value = ""; if (s) send({ do: "input", t: "text", s: s.slice(0, 500) }); };
  const dlg = h("dialog", { class: "dlg dlg-wide shop-dlg", "aria-label": `Sign in to ${name}` },
    h("h2", null, `Sign in to ${name}`),
    h("p", { class: "field-h" }, web
      ? "This is the Hive's own browser, and it doesn't save passwords. Click the page and type your password and any captcha here; agents never see or keep what you type. Drag works for slider captchas. Press Done once you're signed in."
      : "This is the Hive's own browser. Click the page and type your password and any captcha here; agents never see or keep what you type. Drag works for slider captchas. Esc leaves; it closes by itself once you're signed in."),
    line, img,
    h("form", { class: "frow", onsubmit: e => { e.preventDefault(); sendTyped(); } }, typed, show, button("Send", null, { size: "s", type: "submit" }),
      button("Enter", () => send({ do: "input", t: "key", key: "Enter" }), { size: "s", kind: "quiet" }),
      button("Back", () => send({ do: "input", t: "back" }), { size: "s", kind: "quiet" })),
    h("div", { class: "dlg-a" }, web ? button("Done, I'm signed in", () => { line.textContent = tr("Saving the sign-in…"); send({ do: "done" }); }, { kind: "primary" }) : null,
      button(web ? "Cancel" : "Close", finish, web ? { kind: "quiet" } : {})));
  J.dlg = dlg; J.say = t => { line.textContent = t; line.dataset.tone = "bad"; };
  dlg.addEventListener("cancel", e => { e.preventDefault(); finish(); });
  const at = e => { const r = img.getBoundingClientRect(); return { fx: (e.clientX - r.left) / r.width, fy: (e.clientY - r.top) / r.height }; };
  // A press that barely moves is a click; one that moves is a drag along the same path (slider captchas). On a touch screen a mostly
  // up-and-down swipe scrolls the page instead, as it would on the phone itself.
  let down = null;
  img.addEventListener("pointerdown", e => { if (e.button) return; e.preventDefault(); img.focus(); try { img.setPointerCapture(e.pointerId); } catch (x) {} down = { e, pts: [[...Object.values(at(e)), 0]], t: e.timeStamp, x: e.clientX, y: e.clientY }; });
  img.addEventListener("pointermove", e => {
    if (!down || e.pointerId !== down.e.pointerId) return;
    const p = at(e), last = down.pts[down.pts.length - 1];
    if (Math.hypot(p.fx - last[0], p.fy - last[1]) > 0.004 && down.pts.length < 200) { down.pts.push([p.fx, p.fy, Math.round(e.timeStamp - down.t)]); down.t = e.timeStamp; }
  });
  const up = e => {
    if (!down || e.pointerId !== down.e.pointerId) return;
    const d = down; down = null;
    const dx = e.clientX - d.x, dy = e.clientY - d.y, far = Math.hypot(dx, dy) > 6;
    if (!far) return send({ do: "input", t: "click", ...at(d.e) });
    if (e.pointerType === "touch" && Math.abs(dy) > 2 * Math.abs(dx)) {
      const r = img.getBoundingClientRect(); return send({ do: "input", t: "wheel", ...at(d.e), dy: Math.max(-2000, Math.min(2000, -dy * (img.naturalHeight || r.height) / r.height)) });
    }
    let pts = d.pts; const end = at(e); pts.push([end.fx, end.fy, Math.round(e.timeStamp - d.t)]);
    if (pts.length > 80) { const k = (pts.length - 1) / 79; pts = Array.from({ length: 80 }, (_, i) => pts[Math.round(i * k)]); }   // keep 80, first and last kept
    let total = 0; pts = pts.map((q, i) => { const ms = i ? Math.min(200, Math.max(0, q[2])) : 0; total += ms; return [q[0], q[1], ms]; });
    if (total > 5000) pts = pts.map(q => [q[0], q[1], Math.floor(q[2] * 5000 / total)]);
    send({ do: "input", t: "drag", pts });
  };
  img.addEventListener("pointerup", up);
  img.addEventListener("pointercancel", () => { down = null; });
  img.addEventListener("wheel", e => { e.preventDefault(); send({ do: "input", t: "wheel", ...at(e), dy: e.deltaY }); }, { passive: false });
  img.addEventListener("keydown", e => {   // typing with the screen focused goes straight to the page
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (SPECIAL.has(e.key)) { e.preventDefault(); send({ do: "input", t: "key", key: e.key }); }
    else if (e.key.length === 1) { e.preventDefault(); send({ do: "input", t: "text", s: e.key }); }
  });
  img.addEventListener("paste", e => { const s = (e.clipboardData && e.clipboardData.getData("text")) || ""; if (s) { e.preventDefault(); send({ do: "input", t: "text", s: s.slice(0, 500) }); } });
  document.body.append(dlg); dlg.showModal();

  let seq = 0;
  const loop = async () => {
    while (gen === J.gen && J.dlg === dlg) {
      let r;
      try { r = await jget("/api/shop-signin/frame?after=" + seq); }
      catch (e) { line.textContent = tr("Lost the Hive: " + e.message); line.dataset.tone = "bad"; await new Promise(ok => setTimeout(ok, 2000)); continue; }
      if (gen !== J.gen) return;
      if (r.img) { img.src = "data:image/jpeg;base64," + r.img; seq = r.seq; }
      const left = r.left ? ` · closes in ${Math.ceil(r.left / 60)} min` : "";
      if (r.state === "signed") { line.textContent = tr(`Signed in to ${name}. Closing…`); line.dataset.tone = "good"; if (web) { J.said = { text: `Signed in to ${name}. Agents can read its pages now.`, tone: "good" }; J.url = ""; } setTimeout(finish, 2500); return; }
      if (r.state === "open") { line.textContent = tr("Click the page to start" + left); delete line.dataset.tone; }
      else if (r.state === "checking") { line.textContent = tr(web ? "That looks signed in. Press Done if it is." : "That looks signed in. Checking it holds, then this closes by itself…"); line.dataset.tone = "good"; }
      else if (!OPEN.has(r.state)) { line.textContent = tr(r.state === "failed" ? "It didn't start: " + (r.why || "unknown") : "Closed" + (r.why ? ": " + r.why : "") + "."); line.dataset.tone = "bad"; return; }
    }
  };
  send(web ? { do: "start", site, url } : { do: "start", site }).then(v => { if (v) loop(); else if (line.dataset.tone !== "bad") line.textContent = tr("It didn't start."); });
}

/** One row per shop under Buying. Only on the person's own app (v.here from /api/buy); elsewhere the server would refuse anyway. */
export function shopsBlock(here, update) {
  if (!here) return null;
  if (!J.v) { if (!J.loading && !J.err) loadShops(update); return J.err ? block({ title: "Shop sign-ins", key: "shop-signin" }, h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, J.err)) : null; }
  return (J.v.sites || []).map(s => {
    const busy = OPEN.has(J.v.state) && J.v.site === s.site;
    return block({ title: s.name, key: "shop-" + s.site,
      sub: s.signed_at ? `Signed in here ${ago(s.signed_at * 1000)}. Agents fill the cart and check out in that browser; you pay.` : "Sign the Hive's browser in once, so agents can fill the cart without pasted cookies.",
      state: statusLabel(s.signed_at ? "working" : "off", s.signed_at ? "signed in" : "not signed in"),
      actions: button(s.signed_at ? "Sign in again" : `Sign in to ${s.name}`, () => openView(s.site, s.name, update), { size: "s", kind: s.signed_at ? "quiet" : "primary", busy: !!J.dlg && busy }) });
  });
}

/** Settings › Connectors: sign the Hive's browser in to any site the person names, so `web` reads it signed in (thread 11241110ade5).
 *  here: the server says this app is the person's own (v.here from /api/buy); null while that is still being read. */
export function sitesBlock(here, update) {
  if (here == null) return null;
  if (here && !J.v && !J.loading && !J.err) loadShops(update);
  const sites = (J.v && J.v.web) || [];
  const say = (text, tone) => { J.said = text ? { text, tone } : null; update(); };
  const go = (u, label) => {
    if (J.dlg) return;
    const t = String(u || "").trim();
    if (!t) return say("Type the site's address first, like etsy.com.", "bad");
    J.said = null; J.busy = "start"; update();
    // Started here first, so a refusal (a bank, a number address, another sign-in open) shows under the box, not in an empty window;
    // the window's own start then finds it running and just shows it
    post("/api/shop-signin", { do: "start", site: "web", url: t }).then(v => { J.busy = ""; openView("web", label || v.name || t, update, t); },
      e => { J.busy = ""; say(e.message, "bad"); });
  };
  const forget = x => confirm({ title: `Forget ${x.base}?`, body: [`The Hive's browser signs out of ${x.base} and clears what the site kept there. Agents then read it as a visitor.`], ok: "Forget", kind: "danger",
    onOk: async () => {
      J.busy = x.base; update();
      try { const r = await post("/api/shop-signin", { do: "forget", base: x.base }); J.v = r; say(r.cleared ? `Forgot ${x.base}: the browser is signed out of it.` : `Took ${x.base} off the list, but its cookies weren't cleared: ${r.why || "try again"}.`, r.cleared ? "good" : "bad"); }
      catch (e) { say(e.message, "bad"); }
      J.busy = ""; update();
    } });
  const row = x => h("li", { class: "lr conn-row", "data-key": "web-" + x.base },
    h("div", { class: "lr-in conn-in" },
      h("div", { class: "conn-head" }, h("b", { class: "conn-name" }, x.base), statusLabel("working", "signed in")),
      h("p", { class: "conn-line muted" }, x.signed_at ? `Signed in here ${ago(x.signed_at * 1000)}` : "Signed in here"),
      here ? h("div", { class: "conn-acts" },
        button("Sign in again", () => go(x.base, x.base), { size: "s", kind: "quiet" }),
        button("Forget", () => forget(x), { size: "s", kind: "quiet", busy: J.busy === x.base })) : null));
  return block({ title: "Sign in to a site", key: "web-signin",
      sub: "For a site that turns agents away, like Etsy, eBay, Best Buy or Mouser. You sign the Hive's browser in once; agents then read its pages signed in. They never buy, post, message or change anything there. Banks and payment sites stay out." },
    here ? h("form", { class: "frow", "data-key": "web-signin-form", onsubmit: e => { e.preventDefault(); go(J.url); } },
      h("div", { class: "frow-grow" }, field("Site address", input({ type: "text", inputmode: "url", enterkeyhint: "go", autocomplete: "off", autocapitalize: "off", spellcheck: "false", placeholder: "etsy.com", value: J.url, oninput: (e, n) => { J.url = n.value; } }))),
      button("Sign in", null, { type: "submit", size: "s", kind: "primary", busy: !!J.dlg || J.busy === "start" }))
      : h("p", { class: "field-h", "data-key": "web-signin-where" }, "Signing in to a site works only from your own app opened through Cloudflare or Tailscale, not from this connection."),
    J.err && here ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, J.err) : null,
    J.said ? h("p", { class: "fmsg", "data-tone": J.said.tone || null, role: "status", "data-key": "web-signin-say" }, J.said.text) : null,
    sites.length ? h("ul", { class: "list conn-list" }, sites.map(row)) : null);
}
