// The interface language, kept in this browser like the theme. English is the app's own words; another language is a table from
// those exact words (a label, a button, a placeholder) to its own, applied by h() as elements are built (dom.js setLang).
// Only whole strings in the table change, so what agents and the person write is left alone; a word missing from the table shows in English.
// Design: projects/everyday/LANGUAGES.md. To add a language, add a table to TABLES (and its name to LANGS); words from new screens go in as they're found.
import { setLang } from "./dom.js";
import ES1 from "./lang-es.js";
import ES2 from "./lang-es-2.js";
import ES3 from "./lang-es-3.js";
import ES4 from "./lang-es-4.js";

export const LANGS = [["en", "English"], ["es", "Español"]];
// Spanish comes in two files (the main screens, then the rest); the first wins on a shared key, and its patterns are tried first.
const ES = { ...ES4, ...ES3, ...ES2, ...ES1, __patterns: [...ES1.__patterns, ...ES2.__patterns, ...ES3.__patterns, ...ES4.__patterns] };
const TABLES = { es: ES };

export function getLang() {
  let v = null; try { v = localStorage.getItem("lang"); } catch {}
  return TABLES[v] ? v : "en";
}
/** Switch and reload: every screen is built again in the new words. */
export function chooseLang(code) {
  try { if (code === "en") localStorage.removeItem("lang"); else localStorage.setItem("lang", code); } catch {}
  location.reload();
}

const code = getLang(), table = TABLES[code];
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
  });
}
