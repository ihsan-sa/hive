// The agent hover card: rest the pointer on an agent's name or avatar (a.ref[data-kind=agent], a.av) and a small card
// offers Call (a voice call with that agent itself: it pauses its work and answers) and DM (the composer, addressed to it).
// One delegated listener for the whole app; touch screens get the same two buttons on the agent page.
import { h } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { agentStatus, agentThread, isAgent, isRunning, isWarden } from "../core/model.js";
import { go } from "../core/router.js";
import { status } from "../core/status.js";
import { button, statusDot } from "./kit.js";
import { draft, focusComposer } from "./composer.js";
import { inCall, onCallWith, phoneReady, startCall } from "./phone.js";

let card = null, owner = null, openT = 0, closeT = 0;
const SEL = 'a.ref[data-kind=agent][data-name], a.av[data-name]';

/** Call and DM for agent n, as buttons (the hover card and the agent page share them). */
export function agentActions(n, { size = "s" } = {}) {
  if (!isAgent(n) || isWarden(n)) return [];
  const busy = onCallWith(n);
  return [
    phoneReady() ? button("Call", () => { hide(); if (!inCall()) startCall({ to: n }); }, { size, icon: "phone", disabled: !!busy || inCall(),
      title: busy ? `${n} is on a call already.` : `Voice call with ${n} itself. It pauses what it's doing and picks up; if it hasn't in a minute, a copy that knows its work answers.` }) : null,
    can("post") ? button("DM", () => { hide(); dm(n); }, { size, icon: "arrow-up", title: `Write to ${n}: opens its thread with the message addressed to it.` }) : null,
  ].filter(Boolean);
}

/** Open the agent's thread with the composer addressed to it. */
export function dm(n) {
  const th = agentThread(n) || "@" + n;
  draft(th).to = n;
  go("comms", th); focusComposer();
}

function show(a) {
  const n = a.dataset.name;
  if (!isAgent(n) || isWarden(n)) return;
  const acts = agentActions(n);
  if (!acts.length) return;
  hide(); owner = a;
  const s = agentStatus(n);
  card = h("div", { class: "ahc", role: "dialog", "aria-label": `${n}: call or message` },
    h("div", { class: "ahc-h" }, s.key !== "none" ? statusDot(s.key, { live: isRunning(n) }) : null, h("b", null, n),
      s.label ? h("span", { class: "meta" }, s.key === "ended" ? s.label : status(s.key).label.toLowerCase()) : null),
    h("div", { class: "ahc-b" }, acts));
  card.addEventListener("pointerenter", () => clearTimeout(closeT)); card.addEventListener("pointerleave", later);
  document.body.appendChild(card);
  const r = a.getBoundingClientRect(), w = card.offsetWidth, hgt = card.offsetHeight;
  const below = r.bottom + 6 + hgt < innerHeight;
  card.style.left = Math.max(6, Math.min(innerWidth - w - 6, r.left)) + "px";
  card.style.top = (below ? r.bottom + 6 : r.top - hgt - 6) + "px";
}
function hide() { clearTimeout(openT); clearTimeout(closeT); if (card) card.remove(); card = owner = null; }
function later() { clearTimeout(closeT); closeT = setTimeout(hide, 250); }

export function agentHoverInit() {
  document.addEventListener("pointerover", e => {
    if (e.pointerType !== "mouse") return;   // touch and pen: the agent page has the buttons
    const a = e.target.closest && e.target.closest(SEL);
    if (!a) return;
    if (a === owner) { clearTimeout(closeT); return; }
    clearTimeout(openT); openT = setTimeout(() => { if (a.isConnected && a.matches(":hover")) show(a); }, 450);
  });
  document.addEventListener("pointerout", e => {
    if (e.pointerType !== "mouse") return;
    const a = e.target.closest && e.target.closest(SEL);
    if (!a || (e.relatedTarget && a.contains(e.relatedTarget))) return;
    clearTimeout(openT); if (a === owner) later();
  });
  addEventListener("hashchange", hide); addEventListener("scroll", hide, true);
  document.addEventListener("keydown", e => { if (e.key === "Escape") hide(); });
}
