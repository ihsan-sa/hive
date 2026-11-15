// Read aloud (projects/everyday/VOICE.md V2): a reply spoken with its markdown taken out. With the phone module on, the words go
// through its Kokoro voice (POST /api/phone/say -> wav), the same voice as a call; without it, or if that fails, the browser's own speech.
import { S } from "../core/store.js";
import { getLang } from "../core/lang.js";

/** Markdown -> the words a listener should hear: no code, links, URLs, footnotes, marks, tables or maths signs. */
export function speechText(s) {
  return String(s || "")
    .replace(/^ {0,3}(```|~~~)[^\n]*\n[\s\S]*?(\n {0,3}\1[^\n]*|$)/gm, " (code left out) ")
    .replace(/^\[\^[^\]]+\]:.*$/gm, "")
    .replace(/\[\^[^\]]+\]/g, "")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]\n]+)\]\([^)\s]+(\s+"[^"]*")?\)/g, "$1")
    .replace(/<\/?[a-z][^>\n]*>/gi, " ")
    .replace(/https?:\/\/\S+/g, "a link")
    .replace(/\$\$([\s\S]*?)\$\$/g, "$1").replace(/\$([^$\n]+)\$/g, "$1")
    .replace(/^\s*\|(.*)\|\s*$/gm, (m, c) => (/^[\s|:-]+$/.test(c) ? "" : c.split("|").map(x => x.trim()).filter(Boolean).join(", ") + "."))
    .replace(/^ {0,3}#{1,6}[ \t]+(.*)$/gm, "$1.")
    .replace(/^ {0,3}>+ ?/gm, "")
    .replace(/^\s*([-*+]|\d+[.)])\s+(\[[ xX]\]\s+)?/gm, "")
    .replace(/^ {0,3}([-*_])( *\1){2,} *$/gm, "")
    .replace(/(\*\*|__|~~|`+)/g, "")
    .replace(/(^|[\s(])[*_]([^*_\n]+)[*_](?=[\s).,;:!?]|$)/gm, "$1$2")
    .replace(/[*#`|]/g, " ")
    .replace(/[ \t]+/g, " ").replace(/\n{2,}/g, "\n").trim();
}

const NOW = { id: null, audio: null, url: null };
export const reading = id => NOW.id === id;
let onChange = () => {};
export function onReading(fn) { onChange = fn || (() => {}); }

/** Stop whatever is being read. */
export function stopReading() {
  const was = NOW.id;
  if (NOW.audio) { try { NOW.audio.pause(); } catch {} }
  if (NOW.url) URL.revokeObjectURL(NOW.url);
  try { if (window.speechSynthesis) speechSynthesis.cancel(); } catch {}
  NOW.id = NOW.audio = NOW.url = null;
  if (was) onChange(was);
}

function browserSay(id, text) {
  if (!window.speechSynthesis || !window.SpeechSynthesisUtterance) { NOW.id = null; onChange(id); return false; }
  const u = new SpeechSynthesisUtterance(text);
  u.lang = getLang() === "en" ? "en-US" : getLang();
  u.onend = u.onerror = () => { if (NOW.id === id) { NOW.id = null; onChange(id); } };
  speechSynthesis.cancel(); speechSynthesis.speak(u);
  return true;
}

/** Read message `id` (its markdown `md`) aloud. Kokoro when the phone module is ready and the words are English, else the browser. */
export async function readAloud(id, md) {
  stopReading();
  const text = speechText(md);
  if (!text) return false;
  NOW.id = id; onChange(id);
  if (S.app && S.app.phone && getLang() === "en") {
    try {
      const r = await fetch("/api/phone/say", { method: "POST", headers: { "Content-Type": "application/json", "X-Hive-Dash": "1" }, body: JSON.stringify({ text }) });
      if (NOW.id !== id) return true;   // stopped while it was being made
      if (r.ok && (r.headers.get("Content-Type") || "").startsWith("audio/")) {
        NOW.url = URL.createObjectURL(await r.blob());
        if (NOW.id !== id) { URL.revokeObjectURL(NOW.url); NOW.url = null; return true; }
        NOW.audio = new Audio(NOW.url);
        NOW.audio.onended = NOW.audio.onerror = () => { if (NOW.id === id) stopReading(); };
        await NOW.audio.play();
        return true;
      }
    } catch {}
    if (NOW.id !== id) return true;
    if (NOW.audio) { try { NOW.audio.pause(); } catch {} NOW.audio = null; }
  }
  return browserSay(id, text);
}
