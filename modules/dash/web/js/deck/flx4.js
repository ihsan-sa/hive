// The DDJ-FLX4's MIDI, as data: bytes in, a plain event out. No DOM, no state beyond the 14-bit pairs, so node tests it alone.
// Numbers checked against Mixxx's Pioneer-DDJ-FLX4.midi.xml (test/test_flx4.mjs). Marked "unchecked" where Mixxx doesn't list them.
const DECK_NOTES = {
  0x0B: ["play"], 0x0E: ["play", 1], 0x47: ["play", 1], 0x0C: ["cue"], 0x48: ["cue", 1], 0x3F: ["shift"],
  0x58: ["sync"], 0x60: ["sync", 1], 0x5C: ["syncLong"], 0x10: ["loopIn"], 0x4C: ["loopIn", 1], 0x11: ["loopOut"], 0x4E: ["loopOut", 1],
  0x4D: ["reloop"], 0x50: ["reloop", 1], 0x51: ["callPrev"], 0x3E: ["callPrev", 1], 0x53: ["callNext"], 0x3D: ["callNext", 1],
  0x36: ["jogTouch"], 0x67: ["jogTouch", 1], 0x54: ["headCue"], 0x68: ["headCue", 1],
};
export const PAD_MODES = { 0x1B: "hotcue", 0x6D: "beatloop", 0x20: "beatjump", 0x22: "sampler", 0x69: "keyboard", 0x1E: "padfx1", 0x6B: "padfx2", 0x6F: "keyshift" };
const PAD_BASE = { hotcue: 0x00, padfx1: 0x10, beatjump: 0x20, sampler: 0x30, keyboard: 0x40, padfx2: 0x50, beatloop: 0x60, keyshift: 0x70 };   // padfx1 and padfx2 unchecked
const BY_BASE = Object.fromEntries(Object.entries(PAD_BASE).map(([k, v]) => [v, k]));
const MIXER_NOTES = { 0x41: ["browsePress"], 0x42: ["browsePress", 1], 0x46: ["load", 0, 1], 0x47: ["load", 0, 2], 0x58: ["load", 1, 1], 0x59: ["load", 1, 2] };   // shifted loads unchecked
const FX_NOTES = { 0x47: ["fxOn"], 0x43: ["fxOn", 1], 0x63: ["fxSelect"], 0x64: ["fxSelect", 1], 0x4A: ["beatPrev"], 0x4B: ["beatNext"], 0x10: ["fxChannel"], 0x11: ["fxChannel"], 0x14: ["fxChannel"] };
const DECK_CC = { 0x00: "tempo", 0x13: "fader", 0x04: "trim", 0x07: "eqHi", 0x0B: "eqMid", 0x0F: "eqLow" };   // MSB; its LSB is +0x20
const MIXER_CC = { 0x1F: ["crossfader"], 0x17: ["filter", 1], 0x18: ["filter", 2], 0x0C: ["headMix"] };
const JOGS = { 0x22: ["platter"], 0x23: ["platter"], 0x21: ["side"], 0x29: ["platter", 1] };

/** A decoder keeps the high byte of each 14-bit control until its low byte comes. decode(status, d1, d2) returns an event or null. */
export function decoder() {
  const msb = new Map(), lsb = new Map();
  const wide = (key, d1, v, base, make) => {
    const low = d1 >= base + 0x20; if (low) lsb.set(key, v); else msb.set(key, v);
    return { ...make, v: ((msb.get(key) ?? 0) * 128 + (lsb.get(key) ?? 0)) / 16383 };
  };
  return function decode(status, d1, d2) {
    const hi = status & 0xF0, ch = status & 0x0F;
    if (hi === 0x80 || hi === 0x90) {
      const on = hi === 0x90 && d2 > 0;
      if (ch <= 1) {
        const deck = ch + 1;
        if (PAD_MODES[d1]) return { c: "padMode", deck, mode: PAD_MODES[d1], on };
        const n = DECK_NOTES[d1];
        return n ? { c: n[0], deck, shift: !!n[1], on } : null;
      }
      if (ch === 6) { const n = MIXER_NOTES[d1]; return n ? { c: n[0], shift: !!n[1], deck: n[2], on } : null; }
      if (ch === 4 || ch === 5) { const n = FX_NOTES[d1]; return n ? { c: n[0], shift: !!n[1], on } : null; }
      if (ch >= 7 && ch <= 10) {
        const mode = BY_BASE[d1 & 0xF0], i = d1 & 0x0F;
        return mode && i < 8 ? { c: "pad", deck: ch < 9 ? 1 : 2, shift: ch === 8 || ch === 10, mode, i, on } : null;
      }
      return null;
    }
    if (hi !== 0xB0) return null;
    if (ch <= 1) {
      const deck = ch + 1, j = JOGS[d1];
      if (j) return { c: "jog", deck, part: j[0], shift: !!j[1], d: d2 - 64 };
      const base = d1 >= 0x20 ? d1 - 0x20 : d1, name = DECK_CC[base];
      return name ? wide(`${ch}:${base}`, d1, d2, base, { c: name, deck }) : null;
    }
    if (ch === 6) {
      if (d1 === 0x40 || d1 === 0x64) return { c: "browse", shift: d1 === 0x64, d: d2 < 64 ? d2 : d2 - 128 };
      const base = d1 >= 0x20 ? d1 - 0x20 : d1, m = MIXER_CC[base];
      return m ? wide(`6:${base}`, d1, d2, base, { c: m[0], deck: m[1] }) : null;
    }
    if (ch === 4 && (d1 === 0x02 || d1 === 0x22)) return wide("4:2", d1, d2, 0x02, { c: "fxLevel" });
    return null;
  };
}

// ---- out: lights. Every light is the note its button sends, value 0x7F or 0. ----
export const LED = {
  play: d => [0x8F + d, 0x0B], cue: d => [0x8F + d, 0x0C], sync: d => [0x8F + d, 0x58], headCue: d => [0x8F + d, 0x54],
  loopIn: d => [0x8F + d, 0x10], loopOut: d => [0x8F + d, 0x11], reloop: d => [0x8F + d, 0x4D], fxOn: () => [0x94, 0x47],
  padMode: (d, mode) => [0x8F + d, +Object.keys(PAD_MODES).find(k => PAD_MODES[k] === mode)],
  pad: (d, mode, i) => [d === 1 ? 0x97 : 0x99, PAD_BASE[mode] + i],
};
export const vuMsg = (d, v) => [0xAF + d, 0x02, Math.max(0, Math.min(127, Math.round(v * 127)))];
export const HELLO = [[0x9F, 0x00, 0x7F], [0x9F, 0x01, 0x7F]];   // the unit's own "track loaded" sweep on both jogs
export const KEEP_ALIVE = [0xF0, 0x00, 0x40, 0x05, 0x00, 0x00, 0x04, 0x05, 0x00, 0x50, 0x02, 0xF7];   // from Mixxx, sent every 200 ms; needs sysex
export const PAGES = ["hotcue", "beatloop", "beatjump", "sampler"];   // pad-mode buttons turn the 8 pads into pages, in this order
export const isFlx4 = name => /DDJ[- ]?FLX ?4/i.test(name || "");
