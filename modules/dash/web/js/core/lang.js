// The interface language, kept in this browser like the theme. English is the app's own words; another language is a table from
// those exact words (a label, a button, a placeholder) to its own, applied by h() as elements are built (dom.js setLang).
// Only whole strings in the table change, so what agents and the person write is left alone; a word missing from the table shows in English.
// Design: projects/everyday/LANGUAGES.md. The tables are fetched by langtables.js, only the chosen one. To add a language, add its row to FILES there (and its name to LANGS), format.js's LOC and the Language buttons follow; see LANGUAGES.md, "Adding a language"; words from new screens go in as they're found.
import { setLang } from "./dom.js";
import { code, table, left, getLang } from "./langtables.js";
export { getLang };
export const LANGS = [["en", "English"], ["es", "Español"], ["fr", "Français"], ["de", "Deutsch"], ["it", "Italiano"], ["pt", "Português (Brasil)"], ["ja", "日本語"], ["ko", "한국어"], ["hi", "हिन्दी"], ["id", "Bahasa Indonesia"]];
/** Switch and reload: every screen is built again in the new words. */
export function chooseLang(code) {
  try { if (code === "en") localStorage.removeItem("lang"); else localStorage.setItem("lang", code); } catch {}
  location.reload();
}

export const TABLE = table || null;   // the live table, read by test/drive_app_lang.mjs (its plant=drop takes one word out)
document.documentElement.lang = code;
if (table) {
  // Patterns for the few labels with a number or name in them; each is [RegExp on the whole trimmed string, replacement].
  const pats = table.__patterns || [];
  setLang(s => {
    const k = s.trim();
    if (!k) return s;
    let v = table[k];
    if (v === undefined) for (const [re, to] of pats) if (re.test(k)) { v = k.replace(re, to); break; }
    return v === undefined ? s : s.replace(k, () => v);   // a function, so a "$" in the words stays a "$"
  }, code === "es" ? (left => s => left.has(s.trim()))(new Set(left)) : null);   // shown in English, so marked lang="en" (dom.js)
}
