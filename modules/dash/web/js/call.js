// The one-tap call page (/call/): a home-screen app for the phone. The call itself is ui/phone.js (its card has Mute and Hang up);
// this adds the Call button, the recent-calls list from the phone service, and a chat pane on the board thread "phone-chat".
import { S, jget, post, loadApp } from "./core/store.js";
import { startCall, end, phoneInit, phoneReady, inCall } from "./ui/phone.js";

const $ = id => document.getElementById(id);
const esc = s => String(s ?? "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const link = s => esc(s).replace(/https?:\/\/[^\s<]+/g, u => `<a href="${u}" target="_blank" rel="noopener">${u}</a>`);
const open = new Set();

async function boot() {
  await loadApp();
  phoneInit();
  const b = $("cp-call");
  if (!phoneReady()) { $("cp-msg").textContent = !(S.app && S.app.phone) ? "The call service isn't running, or this sign-in can't call." : "This browser can't use the microphone here."; }
  else { b.disabled = false; }
  b.onclick = () => { if (!inCall()) startCall({}); };
  recent(); chat(); setInterval(recent, 10000); setInterval(chat, 4000);
  $("cp-send").onsubmit = async e => {
    e.preventDefault(); const t = $("cp-text"), text = t.value.trim(); if (!text) return;
    t.value = "";
    try { await post("/api/post", { text, thread: "phone-chat" }); chat(); } catch (x) { $("cp-msg").textContent = String(x.message || x); t.value = text; }
  };
  $("cp-recent").onclick = e => {
    const a = e.target.closest("[data-call]"), t = e.target.closest("[data-open]");
    if (a) { if (!inCall()) startCall(a.dataset.call && a.dataset.call !== "Hive" && a.dataset.call !== "the Hive" ? { to: a.dataset.call } : {}); }
    else if (t) { open.has(t.dataset.open) ? open.delete(t.dataset.open) : open.add(t.dataset.open); recent(true); }
  };
}
let lastRecent = "";
async function recent(force) {
  if (document.hidden && !force) return;
  try {
    const r = await jget("/api/phone/history"), sig = JSON.stringify(r.calls.map(c => c.id)) + [...open];
    if (sig === lastRecent && !force) return; lastRecent = sig;
    $("cp-recent").innerHTML = r.calls.length ? r.calls.map(c => {
      const when = new Date(c.started * 1000).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" }), mins = Math.max(1, Math.round((c.ended - c.started) / 60));
      const tr = open.has(c.id) ? `<div class="cp-tr">${esc(c.lines.map(([s, t]) => `${s === "person" ? "You" : s}: ${t}`).join("\n"))}</div>` : "";
      return `<div><div class="cp-row"><div data-open="${esc(c.id)}"><b>${esc(c.agent)}</b><small>${esc(when)} · ${mins} min · transcript</small></div><button data-call="${esc(c.agent)}">Call</button></div>${tr}</div>`;
    }).join("") : `<small>No calls yet.</small>`;
  } catch {}
}
let lastChat = "";
async function chat() {
  if (document.hidden) return;
  try {
    const r = await jget("/api/call-chat"), sig = r.posts.map(p => p.id).join();
    if (sig === lastChat) return; const box = $("cp-chat"), stick = box.scrollTop + box.clientHeight >= box.scrollHeight - 30; lastChat = sig;
    box.innerHTML = r.posts.length ? r.posts.map(p => {
      const files = (p.files || []).map(f => { const m = /files\/([0-9a-f]{12})\//.exec(f.path || ""); return m ? `<div><a href="/files/${m[1]}/${encodeURIComponent(f.name)}" target="_blank">${esc(f.name)}</a></div>` : ""; }).join("");
      return `<div class="cp-post${p.from === "person" ? " me" : ""}"><small>${esc(p.from === "person" ? "You" : p.from)} · ${esc(new Date(p.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }))}</small>${link(p.text)}${files}</div>`;
    }).join("") : `<small>Agents can send you links and files here: hive post --thread phone-chat</small>`;
    if (stick) window.scrollTo(0, document.body.scrollHeight);
  } catch {}
}
boot();
