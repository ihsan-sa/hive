// Phone: voice calls with the Hive (modules/phone). The call screen lives outside the rendered tree, on document.body,
// so a re-render never interrupts audio. Speech is found here in the browser (a level gate with a noise floor), sent as
// 16 kHz PCM to /api/phone/call/<id>/turn, recognised on the machine's GPU, and answered by a brain whose sentences come
// back as wav in the event stream and play in order. Talking over it stops playback (barge-in). Hold music is
// synthesised here, so it needs no file. Agents ring with `phone ring`: rings show as a banner on every page.
// While the person talks, the speech so far goes to /partial every ~1.2 s so their words show as they say them.
// Every page polls /calls, so a call placed on one device shows live on the others: in its thread's strip (callFor, used by
// the comms view), as "On a call" on the agent, and as a card here, where any of them can hang it up.
import { S, jget, post, emit } from "../core/store.js";
import { fix } from "../core/format.js";

const API = "/api/phone";
const C = { id: null, n: 0, ctx: null, mic: null, proc: null, src: null, playing: [], cur: null, hold: null, muted: false,
  seg: null, partial: "", segAt: 0, inflight: false, holdT: null, pre: [], voiced: 0, quiet: 0, floor: 0.004, agent: "", state: "", lines: [], el: null, ended: true, thinking: false };

// ---- audio out ----
function ctx() { if (!C.ctx) C.ctx = new (window.AudioContext || window.webkitAudioContext)(); if (C.ctx.state === "suspended") C.ctx.resume(); return C.ctx; }
// Speech plays one chunk at a time so the speed slider can act on what is already playing: each chunk carries the speed it
// was synthesised at, and plays at playbackRate = slider / that speed. An <audio> element keeps the pitch while the rate
// changes (preservesPitch); moving the slider retunes the running chunk, and queued ones take the live rate when they start.
// An element can be refused where Web Audio isn't (a page policy, a browser's autoplay rule, and the hold music still plays): a refused
// chunk plays through the AudioContext instead, with the pitch following the rate, and the service hears about it so a person needn't.
let SLIDER = 1.05;
const rate = c => Math.min(4, Math.max(0.25, SLIDER / (c.spd || SLIDER)));
const isPlaying = () => !!C.cur || C.playing.length > 0;
const left = c => Math.max(0, (c.a.duration || c.dur || 0) - (c.a.currentTime || 0)) / rate(c);
const remaining = () => (C.cur ? left(C.cur) : 0) + C.playing.reduce((t, c) => t + (c.dur || 0) / rate(c), 0);
const wavSeconds = b => (b.length - 44) / (new DataView(b.buffer).getUint32(28, true) || 1);
async function play(b64, spd) {
  const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0));
  const a = new Audio(URL.createObjectURL(new Blob([bytes], { type: "audio/wav" })));
  a.preservesPitch = a.mozPreservesPitch = a.webkitPreservesPitch = true;
  C.playing.push({ a, spd, bytes, dur: wavSeconds(bytes) }); if (!C.cur) next(); draw();
}
function next() {
  const c = C.playing.shift(); if (!c) { C.cur = null; draw(); return; }
  C.cur = c; c.a.playbackRate = rate(c);
  c.a.onended = () => { URL.revokeObjectURL(c.a.src); if (C.cur === c) next(); };
  c.a.onerror = () => playBuffer(c, "element error " + (c.a.error ? c.a.error.code : ""));
  c.a.play().catch(e => playBuffer(c, e.name));
}
function reportPlayback(why) {
  if (C.playbackSaid || !C.id) return; C.playbackSaid = true;
  post(`${API}/call/${C.id}/playback`, { why }).catch(() => {});
}
async function playBuffer(c, why) {
  if (C.cur !== c || c.buffered) return; c.buffered = true;
  const el = c.a, url = el.src; el.onended = el.onerror = null; el.removeAttribute("src"); el.load(); URL.revokeObjectURL(url);
  const ac = ctx(); reportPlayback(why + ", audio context " + ac.state);
  let buf;
  try { buf = await ac.decodeAudioData(c.bytes.buffer.slice(0)); } catch (e) { reportPlayback("no voice: " + e.name); }
  if (C.cur !== c) return;
  if (!buf) return next();
  const s = ac.createBufferSource(); s.buffer = buf; s.playbackRate.value = rate(c); s.connect(ac.destination);
  let pos = 0, t0 = ac.currentTime;
  const was = () => pos + (ac.currentTime - t0) * s.playbackRate.value;
  c.a = {   // what the rest of this file asks of an element
    src: "", onended: null, duration: buf.duration,
    get currentTime() { return was(); },
    get playbackRate() { return s.playbackRate.value; },
    set playbackRate(r) { pos = was(); t0 = ac.currentTime; s.playbackRate.value = r; },
    pause() { s.onended = null; try { s.stop(); } catch {} },
  };
  s.onended = () => { if (C.cur === c) next(); };
  s.start();
}
export function setLiveSpeed(v) { SLIDER = +v; if (C.cur) C.cur.a.playbackRate = rate(C.cur); }   // the slider moved
function stopPlayback() { const c = C.cur; C.cur = null; for (const q of C.playing) URL.revokeObjectURL(q.a.src); C.playing = []; if (c) { c.a.onended = c.a.onerror = null; c.a.pause(); URL.revokeObjectURL(c.a.src); } }
export const __t = { play, remaining, cur: () => C.cur };   // for test/drive_phone_speed.mjs

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
    if (C.muted || C.state === "hold") {  // muting mid-sentence sends what was said so far; it never drops it or touches the call
      C.pre = []; C.voiced = 0;
      if (C.seg) { const seg = C.seg, len = seg.length * frameS; C.seg = null; if (C.muted && len - C.quiet > 0.6) sendTurn(seg, a.sampleRate); else C.partial = ""; draw(); }
      return;
    }
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
    C.quiet = rms > gate * 0.6 ? 0 : C.quiet + frameS;  // hysteresis: a softer trailing word still counts as speech, so a pause isn't taken for the end
    const len = C.seg.length * frameS;
    // End of turn: a shorter pause after a finished sentence (the live transcript ends in . ? !, not "..." or a filler like
    // "uh" or "so") or a short reply, a longer one mid-thought, so a pause to think doesn't cut the person off. Whisper
    // puts a full stop on most fragments, so even "finished" waits over a second (the person: "please stretch that").
    const p = (C.partial || "").trim(), done = len < 1.6 || (/[^.][.?!]["')]?$/.test(p) && !/\b(uh|um|so|and|but|like|or|the|a|to)\W*$/i.test(p));
    if (C.quiet >= (done ? 1.5 : 2.6) || len > 120) { const seg = C.seg; C.seg = null; C.voiced = 0; if (len - C.quiet > 0.25) sendTurn(seg, a.sampleRate); else C.partial = ""; draw(); return; }
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
  Object.assign(C, { ended: false, n: 0, lines: [], state: "connecting", agent: to || (thread ? "a new agent" : "the Hive"), muted: false, err: "", playbackSaid: false });
  draw(); ctx(); chime(1);
  try {
    await micStart();
    const r = await post(`${API}/call`, { to, ring, quiet, thread });
    C.id = r.id; C.agent = r.agent;
    events(r.id);
  } catch (e) { C.err = e.name === "NotAllowedError" ? "The browser didn't allow the microphone." : e.name === "NotFoundError" ? "No microphone found on this device." : String(e.message || e); end(false); }
  draw();
}
async function events(id) {
  while (!C.ended && C.id === id) {
    let r;
    try { r = await jget(`${API}/call/${id}/events?after=${C.n}`); } catch { await new Promise(f => setTimeout(f, 1500)); continue; }
    for (const e of r.events || []) {
      C.n = e.n + 1;
      if (e.kind === "say") { C.thinking = false; C.lines.push({ who: e.agent, text: e.text }); if (e.audio) play(e.audio, e.speed); }
      else if (e.kind === "heard") { C.lines.push({ who: "you", text: e.text }); if (!C.seg) C.partial = ""; }
      else if (e.kind === "connected") { C.state = "talking"; C.agent = e.agent; }
      else if (e.kind === "hold") {   // let the last voice finish its sentence, then the music
        C.state = "hold"; C.holdFor = e.agent; clearTimeout(C.holdT);
        C.holdT = setTimeout(() => { if (C.state === "hold") holdStart(); }, remaining() * 1000 + 300);
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
  setTimeout(() => { if (C.ended) { C.id = null; C.err = ""; draw(); } }, C.err ? 6000 : 2500);   // the error goes too: it kept the card up for good, with no button (round 18)
  draw();
}

// ---- the screen ----
const esc = s => String(s).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
// Every draw rebuilds the card, which would put the transcript back at its top: it stays on the newest words unless the person has scrolled up to reread.
// The reading-speed slider (saved on the server, applies live, to the sentence being spoken too). One element kept across redraws, so a drag isn't cut off.
let SPEED = null;
export function speedControl() {
  if (SPEED) return SPEED;
  SPEED = document.createElement("label"); SPEED.className = "ph-speed";
  SPEED.innerHTML = `<span>Voice speed <b></b></span><input type="range" min="0.6" max="1.6" step="0.05" value="1.05" aria-label="Voice speed">`;
  const inp = SPEED.querySelector("input"), b = SPEED.querySelector("b"), show = () => { b.textContent = `${fix((+inp.value), 2)}×`; };
  let t = null;
  inp.oninput = () => { show(); setLiveSpeed(inp.value); clearTimeout(t); t = setTimeout(() => post(`${API}/settings`, { speed: +inp.value }).catch(() => {}), 250); };
  jget(`${API}/settings`).then(r => { inp.min = r.min; inp.max = r.max; inp.value = r.speed; SLIDER = +r.speed; show(); }).catch(() => {}); show();
  return SPEED;
}
function fill(html) {
  const was = C.el.querySelector(".ph-lines"), back = was && was.scrollTop + was.clientHeight < was.scrollHeight - 24 ? was.scrollTop : null;
  C.el.innerHTML = html;
  const slot = C.el.querySelector(".ph-speed-slot"); if (slot) slot.replaceWith(speedControl());
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
    ${C.ended ? "" : `<div class="ph-speed-slot"></div>`}
    <div class="ph-btns">${C.ended ? (C.err ? `<button class="btn" data-a="close">Close</button>` : "") : `<button class="btn ph-mute" data-a="mute" aria-pressed="${C.muted}">${C.muted ? "Unmute" : "Mute"}</button><button class="btn ph-end" data-a="end">Hang up</button>`}</div>`);
}
function onClick(e) {
  const a = e.target.closest("[data-a]"); if (!a) return;
  if (a.dataset.a === "end") end();
  else if (a.dataset.a === "close") { C.err = ""; C.id = null; draw(); }
  else if (a.dataset.a === "hangup") hangUp(a.dataset.call);
  else if (a.dataset.a === "mute") { C.muted = !C.muted; post(`${API}/call/${C.id}/mute`, { muted: C.muted }).catch(() => {}); draw(); }
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
const LIVE = { calls: [], sig: "", hidden: new Set(), ending: new Set(), failed: null, iv: null };
/** Live calls, each {id, agent, thread, state, started, partial, lines: [[speaker, text]]}. */
export const liveCalls = () => LIVE.calls;
export const callFor = th => (th ? LIVE.calls.find(c => c.thread === th) : null);
export const onCallWith = name => LIVE.calls.find(c => c.agent === name);
/** End a call from this window, wherever it was placed. The device on the call hears "end" in its event stream and stops its mic. */
export function hangUp(id) {
  if (id === C.id && !C.ended) return end();
  LIVE.ending.add(id); LIVE.failed = null; LIVE.calls = LIVE.calls.filter(c => c.id !== id); draw(); emit({ calls: true });
  post(`${API}/call/${id}/end`, {}).catch(() => { LIVE.ending.delete(id); LIVE.hidden.delete(id); LIVE.failed = id; LIVE.sig = ""; pollCalls(); });
}
function drawOther(c) {
  let prev = null;
  const lines = c.lines.filter(([w]) => w !== "system").slice(c.partial ? -3 : -4).map(([w, t]) => { const same = w === prev; prev = w; return `<div class="ph-line${w === "person" ? " ph-you" : ""}">${same ? "" : `<b>${esc(w === "person" ? "You" : w)}</b> `}${esc(t)}</div>`; }).join("")
    + (c.partial ? `<div class="ph-line ph-you ph-partial">${prev === "person" ? "" : "<b>You</b> "}<i>${esc(c.partial)}…</i></div>` : "");
  const st = LIVE.failed === c.id ? "Couldn't hang up. Try again." : c.state === "hold" ? "On hold" : `On a call, ${Math.max(1, Math.round((Date.now() / 1000 - c.started) / 60))} min · on another device`;
  fill(`<div class="ph-top"><span class="ph-dot ${esc(c.state)}"></span><div><div class="ph-who">${esc(c.agent)}</div><div class="ph-st">${st}</div></div></div>
    <div class="ph-lines" aria-live="polite">${lines}</div>
    <div class="ph-btns"><button class="btn" data-a="hide" data-call="${esc(c.id)}">Hide</button><button class="btn ph-end" data-a="hangup" data-call="${esc(c.id)}">Hang up</button></div>`);
}
async function pollCalls() {
  if (!(S.app && S.app.phone) || document.hidden) return;
  try {
    const r = await jget(`${API}/calls${window.H2_PHONE_ALL ? "?all=1" : ""}`), calls = (r.calls || []).filter(c => !LIVE.ending.has(c.id));
    const sig = JSON.stringify(calls.map(c => [c.id, c.agent, c.thread, c.state, c.partial, c.lines.length]));
    LIVE.calls = calls;
    if (sig !== LIVE.sig) { LIVE.sig = sig; draw(); emit({ calls: true }); }
  } catch {}
}
