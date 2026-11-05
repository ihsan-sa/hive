// Phone: voice calls with the Hive (modules/phone). The call screen lives outside the rendered tree, on document.body,
// so a re-render never interrupts audio. Speech is found here in the browser (a level gate with a noise floor), sent as
// 16 kHz PCM to /api/phone/call/<id>/turn, recognised on the machine's GPU, and answered by a brain whose sentences come
// back as wav in the event stream and play in order. Talking over it stops playback (barge-in). Hold music is
// synthesised here, so it needs no file. Agents ring with `phone ring`: rings show as a banner on every page.
// While the person talks, the speech so far goes to /partial every ~1.2 s so their words show as they say them.
// Every page polls /calls, so a call placed on one device shows live on the others: in its thread's strip (callFor, used by
// the comms view), as "On a call" on the agent, and as a read-only card here.
import { S, jget, post, emit } from "../core/store.js";

const API = "/api/phone";
const C = { id: null, n: 0, ctx: null, mic: null, proc: null, src: null, playing: [], nextT: 0, hold: null, muted: false,
  seg: null, partial: "", segAt: 0, inflight: false, holdT: null, pre: [], voiced: 0, quiet: 0, floor: 0.004, agent: "", state: "", lines: [], el: null, ended: true, thinking: false };

// ---- audio out ----
function ctx() { if (!C.ctx) C.ctx = new (window.AudioContext || window.webkitAudioContext)(); if (C.ctx.state === "suspended") C.ctx.resume(); return C.ctx; }
const isPlaying = () => C.playing.length > 0 && C.nextT > ctx().currentTime;
async function play(b64) {
  const a = ctx(), buf = Uint8Array.from(atob(b64), c => c.charCodeAt(0)).buffer;
  const ab = await a.decodeAudioData(buf);
  const s = a.createBufferSource(); s.buffer = ab; s.connect(a.destination);
  const t = Math.max(a.currentTime + 0.03, C.nextT); s.start(t); C.nextT = t + ab.duration;
  C.playing.push(s); s.onended = () => { C.playing = C.playing.filter(x => x !== s); draw(); };
  draw();
}
function stopPlayback() { for (const s of C.playing) try { s.stop(); } catch {} C.playing = []; C.nextT = 0; }

// Soft hold music: a slow electric-piano arpeggio over a quiet pad, I–vi–IV–V in C, looping until pickup.
function holdStart() {
  if (C.hold) return;
  const a = ctx(), out = a.createGain(); out.gain.value = 0.0; out.gain.linearRampToValueAtTime(0.09, a.currentTime + 1.5);
  const lp = a.createBiquadFilter(); lp.type = "lowpass"; lp.frequency.value = 2400; lp.connect(out); out.connect(a.destination);
  const chords = [[60, 64, 67, 71], [57, 60, 64, 67], [53, 57, 60, 64], [55, 59, 62, 67]], hz = m => 440 * 2 ** ((m - 69) / 12);
  const note = (m, t, len, v, type = "sine") => {
    const o = a.createOscillator(), g = a.createGain(); o.type = type; o.frequency.value = hz(m);
    g.gain.setValueAtTime(0, t); g.gain.linearRampToValueAtTime(v, t + 0.02); g.gain.exponentialRampToValueAtTime(0.0008, t + len);
    o.connect(g); g.connect(lp); o.start(t); o.stop(t + len + 0.05);
  };
  let bar = 0, t = a.currentTime + 0.2;
  const tick = () => {
    while (t < a.currentTime + 1.5) {
      const ch = chords[bar % 4], beat = 0.42;
      note(ch[0] - 12, t, beat * 8, 0.18, "triangle"); for (const m of ch) note(m, t, beat * 8, 0.035, "triangle");
      const pat = [0, 1, 2, 3, 2, 1, 2, 3];
      pat.forEach((k, i) => { note(ch[k] + 12, t + i * beat, 1.6, 0.16); note(ch[k] + 24, t + i * beat, 0.6, 0.025); });
      t += beat * 8; bar++;
    }
  };
  tick(); C.hold = { out, iv: setInterval(tick, 400) };
}
function holdStop() {
  if (!C.hold) return;
  const { out, iv } = C.hold, a = ctx(); clearInterval(iv); C.hold = null;
  out.gain.cancelScheduledValues(a.currentTime); out.gain.setValueAtTime(out.gain.value, a.currentTime); out.gain.linearRampToValueAtTime(0, a.currentTime + 0.6);
  setTimeout(() => out.disconnect(), 800);
}
function chime(n = 2) {
  const a = ctx(); [0, 0.18].slice(0, n).forEach((d, i) => {
    const o = a.createOscillator(), g = a.createGain(); o.type = "sine"; o.frequency.value = i ? 784 : 659;
    const t = a.currentTime + d; g.gain.setValueAtTime(0, t); g.gain.linearRampToValueAtTime(0.12, t + 0.01); g.gain.exponentialRampToValueAtTime(0.001, t + 0.9);
    o.connect(g); g.connect(a.destination); o.start(t); o.stop(t + 1);
  });
}

// ---- audio in: find speech, send each utterance ----
async function micStart() {
  const a = ctx();
  C.mic = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 } });
  C.src = a.createMediaStreamSource(C.mic);
  C.proc = a.createScriptProcessor(2048, 1, 1);
  const mute = a.createGain(); mute.gain.value = 0;
  C.src.connect(C.proc); C.proc.connect(mute); mute.connect(a.destination);
  const frameS = 2048 / a.sampleRate;
  C.proc.onaudioprocess = e => {
    if (C.ended) return;
    const x = new Float32Array(e.inputBuffer.getChannelData(0));
    let s = 0; for (let i = 0; i < x.length; i++) s += x[i] * x[i];
    const rms = Math.sqrt(s / x.length);
    C.level = rms;
    if (C.muted || C.state === "hold") { C.seg = null; C.pre = []; return; }
    if (!C.seg) C.floor = rms < C.floor ? C.floor * 0.9 + rms * 0.1 : C.floor * 0.995 + rms * 0.005;
    const gate = Math.max(0.012, C.floor * 3.2) * (isPlaying() ? 2.6 : 1);  // the voice's own echo needs a higher bar
    const loud = rms > gate;
    if (!C.seg) {
      C.pre.push(x); if (C.pre.length > Math.ceil(0.3 / frameS)) C.pre.shift();
      C.voiced = loud ? C.voiced + 1 : 0;
      if (C.voiced >= Math.ceil(0.12 / frameS)) {
        C.seg = [...C.pre]; C.quiet = 0; C.pre = []; C.partial = ""; C.segAt = a.currentTime;
        if (isPlaying()) { stopPlayback(); post(`${API}/call/${C.id}/interrupt`, {}).catch(() => {}); }
        draw();
      }
      return;
    }
    C.seg.push(x);
    C.quiet = loud ? 0 : C.quiet + frameS;
    const len = C.seg.length * frameS;
    // End of turn: a shorter pause after a finished sentence (the live transcript ends in . ? !, not "..." or a filler like
    // "uh" or "so") or a short reply, a longer one mid-thought, so a pause to think doesn't cut the person off. Whisper
    // puts a full stop on most fragments, so even "finished" waits over a second (the person: "please stretch that").
    const p = (C.partial || "").trim(), done = len < 1.6 || (/[^.][.?!]["')]?$/.test(p) && !/\b(uh|um|so|and|but|like|or|the|a|to)\W*$/i.test(p));
    if (C.quiet >= (done ? 1.1 : 2.2) || len > 120) { const seg = C.seg; C.seg = null; C.voiced = 0; if (len - C.quiet > 0.25) sendTurn(seg, a.sampleRate); else C.partial = ""; draw(); return; }
    if (!C.inflight && a.currentTime - C.segAt >= 1.2 && len > 0.6) { C.segAt = a.currentTime; sendPartial(C.seg, a.sampleRate); }
  };
}
function micStop() {
  try { C.proc && (C.proc.onaudioprocess = null, C.proc.disconnect()); C.src && C.src.disconnect(); } catch {}
  if (C.mic) C.mic.getTracks().forEach(t => t.stop());
  C.mic = C.proc = C.src = null;
}
function toPcm(frames, rate) {
  const n = frames.reduce((k, f) => k + f.length, 0), all = new Float32Array(n);
  let o = 0; for (const f of frames) { all.set(f, o); o += f.length; }
  const ratio = rate / 16000, m = Math.floor(n / ratio), pcm = new Int16Array(m);
  for (let i = 0; i < m; i++) {  // average over each output sample's span: a cheap low-pass before decimating
    const a = Math.floor(i * ratio), b = Math.max(a + 1, Math.floor((i + 1) * ratio)); let s = 0;
    for (let j = a; j < b; j++) s += all[j];
    pcm[i] = Math.max(-1, Math.min(1, s / (b - a))) * 32767;
  }
  return pcm;
}
const sendPcm = (kind, pcm) => fetch(`${API}/call/${C.id}/${kind}`, { method: "POST", headers: { "Content-Type": "application/octet-stream", "X-Hive-Dash": "1" }, body: pcm.buffer }).then(r => r.json()).catch(() => ({}));
async function sendPartial(frames, rate) {
  const seg = frames; C.inflight = true;
  try { const j = await sendPcm("partial", toPcm(frames.slice(), rate)); if (j.text && C.seg === seg) { C.partial = j.text; draw(); } }
  finally { C.inflight = false; }
}
async function sendTurn(frames, rate) {
  const pcm = toPcm(frames, rate);
  C.thinking = true; draw();
  try {
    const j = await sendPcm("turn", pcm);
    if (!j.text) { C.thinking = false; C.partial = ""; draw(); }  // nothing heard (noise, a cough): back to listening
  } catch { C.thinking = false; C.partial = ""; draw(); }
}

// ---- the call ----
export async function startCall({ to = null, ring = null, quiet = false, thread = null } = {}) {
  if (!C.ended) return;
  Object.assign(C, { ended: false, n: 0, lines: [], state: "connecting", agent: to || (thread ? "a new agent" : "the Hive"), muted: false, err: "" });
  draw(); ctx(); chime(1);
  try {
    await micStart();
    const r = await post(`${API}/call`, { to, ring, quiet, thread });
    C.id = r.id; C.agent = r.agent;
    events(r.id);
  } catch (e) { C.err = e.name === "NotAllowedError" ? "The browser didn't allow the microphone." : String(e.message || e); end(false); }
  draw();
}
async function events(id) {
  while (!C.ended && C.id === id) {
    let r;
    try { r = await jget(`${API}/call/${id}/events?after=${C.n}`); } catch { await new Promise(f => setTimeout(f, 1500)); continue; }
    for (const e of r.events || []) {
      C.n = e.n + 1;
      if (e.kind === "say") { C.thinking = false; C.lines.push({ who: e.agent, text: e.text }); if (e.audio) play(e.audio); }
      else if (e.kind === "heard") { C.lines.push({ who: "you", text: e.text }); if (!C.seg) C.partial = ""; }
      else if (e.kind === "connected") { C.state = "talking"; C.agent = e.agent; }
      else if (e.kind === "hold") {   // let the last voice finish its sentence, then the music
        C.state = "hold"; C.holdFor = e.agent; clearTimeout(C.holdT);
        C.holdT = setTimeout(() => { if (C.state === "hold") holdStart(); }, Math.max(0, C.nextT - ctx().currentTime) * 1000 + 300);
      }
      else if (e.kind === "pickup") { C.state = "talking"; C.agent = e.agent; clearTimeout(C.holdT); holdStop(); }
      else if (e.kind === "thinking") C.thinking = e.on;
      else if (e.kind === "turn_done") C.thinking = false;
      else if (e.kind === "error") C.lines.push({ who: "", text: e.text });
      else if (e.kind === "end") { end(false); return; }
    }
    draw();
  }
}
export function end(tell = true) {
  if (tell && C.id && !C.ended) post(`${API}/call/${C.id}/end`, {}).catch(() => {});
  C.ended = true; C.state = "ended"; C.partial = ""; clearTimeout(C.holdT); holdStop(); micStop();
  setTimeout(() => { if (C.ended) { C.id = null; draw(); } }, C.err ? 6000 : 2500);
  draw();
}

// ---- the screen ----
const esc = s => String(s).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
// Every draw rebuilds the card, which would put the transcript back at its top: it stays on the newest words unless the person has scrolled up to reread.
function fill(html) {
  const was = C.el.querySelector(".ph-lines"), back = was && was.scrollTop + was.clientHeight < was.scrollHeight - 24 ? was.scrollTop : null;
  C.el.innerHTML = html;
  const box = C.el.querySelector(".ph-lines"); box.scrollTop = back == null ? box.scrollHeight : back;
}
function draw() {
  if (!C.el) { C.el = document.createElement("div"); C.el.className = "ph-call"; C.el.setAttribute("role", "dialog"); C.el.setAttribute("aria-label", "Voice call"); document.body.append(C.el); C.el.addEventListener("click", onClick); }
  const show = !C.ended || C.id || C.err;
  const other = !show && LIVE.calls.find(c => c.id !== C.id && !LIVE.hidden.has(c.id));
  C.el.hidden = !show && !other; if (other) return drawOther(other); if (!show) return;
  const who = C.agent === "the Hive" ? "the Hive" : C.agent;
  const st = C.err ? C.err : C.state === "connecting" ? "Connecting…" : C.state === "hold" ? `On hold: putting you through to ${esc(C.holdFor || "")}` :
    C.state === "ended" ? "Call ended" : C.muted ? "Muted" : C.seg ? "Listening…" : C.thinking ? "Thinking…" : isPlaying() ? "Speaking" : "Go ahead, I'm listening";
  let prev = null;   // a name shows once per run of lines by the same speaker
  const lines = C.lines.slice(C.partial ? -3 : -4).map(l => { const same = l.who === prev; prev = l.who; return `<div class="ph-line${l.who === "you" ? " ph-you" : ""}">${same ? "" : `<b>${esc(l.who === "you" ? "You" : l.who || "")}</b> `}${esc(l.text)}</div>`; }).join("")
    + (C.partial ? `<div class="ph-line ph-you ph-partial">${prev === "you" ? "" : "<b>You</b> "}<i>${esc(C.partial)}…</i></div>` : "");
  fill(`<div class="ph-top"><span class="ph-dot ${C.state}${C.seg ? " live" : ""}"></span><div><div class="ph-who">${esc(who)}</div><div class="ph-st" aria-live="polite">${st}</div></div></div>
    <div class="ph-lines">${lines}</div>
    <div class="ph-btns">${C.ended ? "" : `<button class="btn ph-mute" data-a="mute" aria-pressed="${C.muted}">${C.muted ? "Unmute" : "Mute"}</button><button class="btn ph-end" data-a="end">Hang up</button>`}</div>`);
}
function onClick(e) {
  const a = e.target.closest("[data-a]"); if (!a) return;
  if (a.dataset.a === "end") end();
  else if (a.dataset.a === "mute") { C.muted = !C.muted; draw(); }
  else if (a.dataset.a === "hide") { LIVE.hidden.add(a.dataset.call); draw(); }
  else if (a.dataset.a === "answer") { const id = a.dataset.ring; RINGS.seen.add(id); RINGS.list = []; drawRings(); startCall({ ring: id }); }
  else if (a.dataset.a === "decline") { const id = a.dataset.ring; RINGS.seen.add(id); post(`${API}/ring/${id}/decline`, {}).catch(() => {}); RINGS.list = RINGS.list.filter(r => r.id !== id); drawRings(); }
}

// ---- rings from agents ----
const RINGS = { list: [], seen: new Set(), el: null, iv: null };
function drawRings() {
  if (!RINGS.el) { RINGS.el = document.createElement("div"); RINGS.el.className = "ph-rings"; RINGS.el.setAttribute("aria-live", "assertive"); document.body.append(RINGS.el); RINGS.el.addEventListener("click", onClick); }
  const rs = C.ended ? RINGS.list.filter(r => !RINGS.seen.has(r.id)) : [];
  RINGS.el.hidden = !rs.length;
  RINGS.el.innerHTML = rs.map(r => `<div class="ph-ring"><span class="ph-dot ringing"></span><div><b>${esc(r.from)} is calling</b><div class="ph-st">${esc(r.why || "")}</div></div>
    <button class="btn ph-answer" data-a="answer" data-ring="${esc(r.id)}">Answer</button><button class="btn" data-a="decline" data-ring="${esc(r.id)}">Decline</button></div>`).join("");
}
async function pollRings() {
  if (!(S.app && S.app.phone) || document.hidden) return;
  try {
    const r = await jget(`${API}/rings`), was = new Set(RINGS.list.map(x => x.id));
    RINGS.list = r.rings || [];
    if (RINGS.list.some(x => !was.has(x.id) && !RINGS.seen.has(x.id)) && C.ended) { try { chime(2); } catch {} }
    drawRings();
  } catch {}
}
export function phoneInit() {
  if (!RINGS.iv) { RINGS.iv = setInterval(pollRings, 8000); setTimeout(pollRings, 1500); }
  if (!LIVE.iv) { LIVE.iv = setInterval(pollCalls, 2000); setTimeout(pollCalls, 1200); }
}
export const phoneReady = () => !!(S.app && S.app.phone && navigator.mediaDevices);
export const inCall = () => !C.ended;

// ---- calls in progress anywhere (another device, another tab) ----
const LIVE = { calls: [], sig: "", hidden: new Set(), iv: null };
/** Live calls, each {id, agent, thread, state, started, partial, lines: [[speaker, text]]}. */
export const liveCalls = () => LIVE.calls;
export const callFor = th => (th ? LIVE.calls.find(c => c.thread === th) : null);
export const onCallWith = name => LIVE.calls.find(c => c.agent === name);
function drawOther(c) {
  let prev = null;
  const lines = c.lines.filter(([w]) => w !== "system").slice(c.partial ? -3 : -4).map(([w, t]) => { const same = w === prev; prev = w; return `<div class="ph-line${w === "person" ? " ph-you" : ""}">${same ? "" : `<b>${esc(w === "person" ? "You" : w)}</b> `}${esc(t)}</div>`; }).join("")
    + (c.partial ? `<div class="ph-line ph-you ph-partial">${prev === "person" ? "" : "<b>You</b> "}<i>${esc(c.partial)}…</i></div>` : "");
  const st = c.state === "hold" ? "On hold" : `On a call, ${Math.max(1, Math.round((Date.now() / 1000 - c.started) / 60))} min · on another device`;
  fill(`<div class="ph-top"><span class="ph-dot ${esc(c.state)}"></span><div><div class="ph-who">${esc(c.agent)}</div><div class="ph-st">${st}</div></div></div>
    <div class="ph-lines" aria-live="polite">${lines}</div>
    <div class="ph-btns"><button class="btn" data-a="hide" data-call="${esc(c.id)}">Hide</button></div>`);
}
async function pollCalls() {
  if (!(S.app && S.app.phone) || document.hidden) return;
  try {
    const r = await jget(`${API}/calls${window.H2_PHONE_ALL ? "?all=1" : ""}`), calls = r.calls || [];
    const sig = JSON.stringify(calls.map(c => [c.id, c.agent, c.thread, c.state, c.partial, c.lines.length]));
    LIVE.calls = calls;
    if (sig !== LIVE.sig) { LIVE.sig = sig; draw(); emit({ calls: true }); }
  } catch {}
}
