// A voice memo (or any audio file) in a message: a play button, a waveform you can click to seek, and the time, played in place.
// The card is data-own, so a re-render never rebuilds it and playback carries on. The bars are drawn from the file's own samples
// (decoded in the browser the first time it is shown; a file too big or undecodable keeps a flat row of bars).
import { h } from "../core/dom.js";
import { icon } from "./kit.js";

export const VOICE = /^voice-.*\.(webm|ogg|oga|opus|m4a|mp4|wav|mp3)$|\.(mp3|wav|ogg|oga|opus|m4a|aac|flac|weba)$/i;
const BARS = 48, MAXB = 8 * 1048576, PEAKS = new Map();
const mmss = s => (isFinite(s) ? Math.floor(s / 60) + ":" + String(Math.floor(s % 60)).padStart(2, "0") : "0:00");

async function peaks(url, size) {
  if (PEAKS.has(url)) return PEAKS.get(url);
  if (size > MAXB) return null;
  try {
    const buf = await (await fetch(url)).arrayBuffer();
    const AC = window.AudioContext || window.webkitAudioContext, c = new AC();
    const ab = await c.decodeAudioData(buf); c.close && c.close();
    const d = ab.getChannelData(0), step = Math.max(1, Math.floor(d.length / BARS)), out = [];
    for (let i = 0; i < BARS; i++) { let m = 0; for (let j = i * step; j < Math.min(d.length, (i + 1) * step); j += 8) m = Math.max(m, Math.abs(d[j])); out.push(m); }
    const top = Math.max(...out, 1e-6), r = out.map(x => Math.max(.08, x / top));
    PEAKS.set(url, r); return r;
  } catch { return null; }
}

/** The card for file f of post p; url is where its bytes are, name and meta label it. */
export function voiceMemo(url, name, meta, size, { key, on, open } = {}) {
  const audio = h("audio", { src: url, preload: "metadata" });
  const bars = Array.from({ length: BARS }, () => h("i", { style: "height:30%" }));
  const wave = h("div", { class: "vm-wave", role: "slider", tabindex: "0", "aria-label": "Seek", title: "Click to jump to a point" }, bars);
  const time = h("span", { class: "vm-t" }, "0:00");
  const btn = h("button", { type: "button", class: "vm-play", "aria-label": "Play " + name, title: "Play" }, icon("play"));
  const paint = () => {
    const f = audio.duration ? audio.currentTime / audio.duration : 0, n = Math.round(f * BARS);
    bars.forEach((b, i) => b.classList.toggle("on", i < n));
    time.textContent = audio.duration && (audio.currentTime || !audio.paused) ? mmss(audio.currentTime) + " / " + mmss(audio.duration) : audio.duration ? mmss(audio.duration) : "0:00";
  };
  const state = () => { btn.replaceChildren(icon(audio.paused ? "play" : "pause")); btn.title = audio.paused ? "Play" : "Pause"; card.dataset.playing = audio.paused ? "" : "1"; };
  const seek = e => { const r = wave.getBoundingClientRect(); if (audio.duration) audio.currentTime = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) * audio.duration; paint(); };
  btn.onclick = e => { e.stopPropagation(); if (audio.paused) { document.querySelectorAll(".vm audio").forEach(a => a !== audio && a.pause()); audio.play().catch(() => {}); } else audio.pause(); };
  wave.onclick = e => { e.stopPropagation(); seek(e); };
  wave.onkeydown = e => { if ((e.key === "ArrowRight" || e.key === "ArrowLeft") && audio.duration) { audio.currentTime += e.key === "ArrowRight" ? 5 : -5; paint(); e.preventDefault(); } };
  for (const ev of ["timeupdate", "loadedmetadata", "durationchange", "ended"]) audio.addEventListener(ev, paint);
  for (const ev of ["play", "pause", "ended"]) audio.addEventListener(ev, state);
  const card = h("div", { class: "vm", "data-own": "", "data-sig": url, title: name },
    btn, h("div", { class: "vm-mid" }, wave, h("span", { class: "vm-meta" }, time, h("span", { class: "meta" }, meta))),
    key ? h("button", { type: "button", class: "vm-open", title: "Open " + name + " beside the conversation", "data-pv-key": key, "data-pv-on": on, onclick: e => { e.stopPropagation(); open && open(); } }, "Open") : null, audio);
  peaks(url, size).then(p => (p ? bars.forEach((b, i) => (b.style.height = Math.round(p[i] * 100) + "%")) : (wave.dataset.flat = "")));
  return card;
}
