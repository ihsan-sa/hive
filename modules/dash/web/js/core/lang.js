// The interface language, kept in this browser like the theme. English is the app's own words; another language is a table from
// those exact words (a label, a button, a placeholder) to its own, applied by h() as elements are built (dom.js setLang).
// Only whole strings in the table change, so what agents and the person write is left alone; a word missing from the table shows in English.
// Design: projects/everyday/LANGUAGES.md. To add a language, add a table to TABLES (and its name to LANGS), format.js's LOC and the Language buttons follow; see LANGUAGES.md, "Adding a language"; words from new screens go in as they're found.
import { setLang } from "./dom.js";
import ES1 from "./lang-es.js";
import ES2 from "./lang-es-2.js";
import ES3 from "./lang-es-3.js";
import ES4 from "./lang-es-4.js";
import ES5 from "./lang-es-5.js";   // the screen words that were still English (product-test rung "Spanish on the screens")
import ES6 from "./lang-es-6.js";   // where a conversation stands: the line in its head (model.js threadStand)
import ES7 from "./lang-es-7.js";   // the wall display and the shared views at its size (product-test rung "Spanish on the display page")
import ES8 from "./lang-es-8.js";   // the rest of lang-left.js, view by view (product-test rung "Spanish in the rest of lang-left.js")
import ES9 from "./lang-es-9.js";   // part 2: Settings, first-run setup and the design-system page (rung "Spanish in the rest of lang-left.js, part 2")
import ES10 from "./lang-es-10.js";   // part 3: Usage, a message, Claude Design, the model's labels and Knowledge (rung "Spanish in the rest of lang-left.js, part 3")
import ES11 from "./lang-es-11.js";   // part 4: Now, an agent, Mesh, the deck, the shell, Progress and the store's lines (rung "Spanish in the rest of lang-left.js, part 4")
import ES12 from "./lang-es-12.js";   // part 5: feedback, Agents, header cards, the deck, sign-in, rootwarden's word, kb history, tiers, Flow, quick answers and a focus (rung "Spanish in the rest of lang-left.js, part 5")
import ES13 from "./lang-es-13.js";   // part 6: the tail, 1 to 10 strings from each of about 60 files (rung "Spanish in the rest of lang-left.js, part 6")
import ES14 from "./lang-es-14.js";
import ES15 from "./lang-es-15.js";   // helpers (ui/helpers.js, Now, Agents, the Map) and the board's flash and port lines, 2026-11-20   // Settings > Connectors and connectors.json's rows (rung E55)
import ESKB from "./lang-es-kbwipe.js";
import ES16 from "./lang-es-16.js";   // Documents and the document viewer (views/docs.js, ui/docviewer.js), 2026-11-22   // Settings > Wipe the knowledge base (thread kb-wipe)
import FR1 from "./lang-fr.js";   // French (rung E58): the main screens, the same four files as Spanish
import FR2 from "./lang-fr-2.js";
import FR3 from "./lang-fr-3.js";
import FR4 from "./lang-fr-4.js";
import FR5 from "./lang-fr-5.js";
import FR6 from "./lang-fr-6.js";   // the tail, as Spanish lang-es-5 to -16 (rung E110, 2026-11-22)
import FR7 from "./lang-fr-7.js";
import FR8 from "./lang-fr-8.js";
import FR9 from "./lang-fr-9.js";
import FR10 from "./lang-fr-10.js";
import FR11 from "./lang-fr-11.js";
import DE1 from "./lang-de.js";   // German (rung E111, 2026-11-22): the whole app, built from the Spanish tables
import DE2 from "./lang-de-2.js";
import DE3 from "./lang-de-3.js";
import DE4 from "./lang-de-4.js";
import DE5 from "./lang-de-5.js";
import DE6 from "./lang-de-6.js";
import DE7 from "./lang-de-7.js";
import DE8 from "./lang-de-8.js";
import DE9 from "./lang-de-9.js";
import IT1 from "./lang-it.js";   // Italian (rung E112, 2026-11-22): the whole app, built from the Spanish tables as German was
import IT2 from "./lang-it-2.js";
import IT3 from "./lang-it-3.js";
import IT4 from "./lang-it-4.js";
import IT5 from "./lang-it-5.js";
import IT6 from "./lang-it-6.js";
import IT7 from "./lang-it-7.js";
import IT8 from "./lang-it-8.js";
import IT9 from "./lang-it-9.js";
import PT1 from "./lang-pt.js";
import PT2 from "./lang-pt-2.js";
import PT3 from "./lang-pt-3.js";
import PT4 from "./lang-pt-4.js";
import PT5 from "./lang-pt-5.js";
import PT6 from "./lang-pt-6.js";
import PT7 from "./lang-pt-7.js";
import PT8 from "./lang-pt-8.js";
import PT9 from "./lang-pt-9.js";
import JA1 from "./lang-ja.js";
import JA2 from "./lang-ja-2.js";
import JA3 from "./lang-ja-3.js";
import JA4 from "./lang-ja-4.js";
import JA5 from "./lang-ja-5.js";
import JA6 from "./lang-ja-6.js";
import JA7 from "./lang-ja-7.js";
import JA8 from "./lang-ja-8.js";
import JA9 from "./lang-ja-9.js";
import KO1 from "./lang-ko.js";
import KO2 from "./lang-ko-2.js";
import KO3 from "./lang-ko-3.js";
import KO4 from "./lang-ko-4.js";
import KO5 from "./lang-ko-5.js";
import KO6 from "./lang-ko-6.js";
import KO7 from "./lang-ko-7.js";
import KO8 from "./lang-ko-8.js";
import KO9 from "./lang-ko-9.js";
import HI1 from "./lang-hi.js";
import HI2 from "./lang-hi-2.js";
import HI3 from "./lang-hi-3.js";
import HI4 from "./lang-hi-4.js";
import HI5 from "./lang-hi-5.js";
import HI6 from "./lang-hi-6.js";
import HI7 from "./lang-hi-7.js";
import HI8 from "./lang-hi-8.js";
import HI9 from "./lang-hi-9.js";
import ID1 from "./lang-id.js";
import ID2 from "./lang-id-2.js";
import ID3 from "./lang-id-3.js";
import ID4 from "./lang-id-4.js";
import ID5 from "./lang-id-5.js";
import ID6 from "./lang-id-6.js";
import ID7 from "./lang-id-7.js";
import ID8 from "./lang-id-8.js";
import ID9 from "./lang-id-9.js";
import LEFT from "./lang-left.js";   // the app's words with no Spanish yet, generated by test/lang_left.mjs

export const LANGS = [["en", "English"], ["es", "Español"], ["fr", "Français"], ["de", "Deutsch"], ["it", "Italiano"], ["pt", "Português (Brasil)"], ["ja", "日本語"], ["ko", "한국어"], ["hi", "हिन्दी"], ["id", "Bahasa Indonesia"]];
// Spanish comes in two files (the main screens, then the rest); the first wins on a shared key, and its patterns are tried first.
const ES = { ...ES16, ...ESKB, ...ES15, ...ES14, ...ES13, ...ES12, ...ES11, ...ES10, ...ES9, ...ES8, ...ES7, ...ES6, ...ES5, ...ES4, ...ES3, ...ES2, ...ES1, __patterns: [...ES1.__patterns, ...ES2.__patterns, ...ES3.__patterns, ...ES4.__patterns, ...ES5.__patterns, ...ES6.__patterns, ...ES7.__patterns, ...ES8.__patterns, ...ES9.__patterns, ...ES10.__patterns, ...ES11.__patterns, ...ES12.__patterns, ...ES13.__patterns, ...ES14.__patterns, ...ES15.__patterns, ...ESKB.__patterns, ...ES16.__patterns] };
// French covers the whole app as Spanish does (E58, then the tail in E110); a word it lacks shows in English. Same merge rule: the first file wins on a shared key.
const FR = { ...FR11, ...FR10, ...FR9, ...FR8, ...FR7, ...FR6, ...FR5, ...FR4, ...FR3, ...FR2, ...FR1, __patterns: [...FR1.__patterns, ...FR2.__patterns, ...FR3.__patterns, ...FR4.__patterns, ...FR5.__patterns, ...FR6.__patterns, ...FR7.__patterns, ...FR8.__patterns, ...FR9.__patterns, ...FR10.__patterns, ...FR11.__patterns] };
// German, the same way (E111).
const DE = { ...DE9, ...DE8, ...DE7, ...DE6, ...DE5, ...DE4, ...DE3, ...DE2, ...DE1, __patterns: [...DE1.__patterns, ...DE2.__patterns, ...DE3.__patterns, ...DE4.__patterns, ...DE5.__patterns, ...DE6.__patterns, ...DE7.__patterns, ...DE8.__patterns, ...DE9.__patterns] };
const IT = { ...IT9, ...IT8, ...IT7, ...IT6, ...IT5, ...IT4, ...IT3, ...IT2, ...IT1, __patterns: [...IT1.__patterns, ...IT2.__patterns, ...IT3.__patterns, ...IT4.__patterns, ...IT5.__patterns, ...IT6.__patterns, ...IT7.__patterns, ...IT8.__patterns, ...IT9.__patterns] };
// Brazilian Portuguese, the same way (E112b-pt).
const PT = { ...PT9, ...PT8, ...PT7, ...PT6, ...PT5, ...PT4, ...PT3, ...PT2, ...PT1, __patterns: [...PT1.__patterns, ...PT2.__patterns, ...PT3.__patterns, ...PT4.__patterns, ...PT5.__patterns, ...PT6.__patterns, ...PT7.__patterns, ...PT8.__patterns, ...PT9.__patterns] };
const JA = { ...JA9, ...JA8, ...JA7, ...JA6, ...JA5, ...JA4, ...JA3, ...JA2, ...JA1, __patterns: [...JA1.__patterns, ...JA2.__patterns, ...JA3.__patterns, ...JA4.__patterns, ...JA5.__patterns, ...JA6.__patterns, ...JA7.__patterns, ...JA8.__patterns, ...JA9.__patterns] };
// Korean, the same way (E112b-ko).
const KO = { ...KO9, ...KO8, ...KO7, ...KO6, ...KO5, ...KO4, ...KO3, ...KO2, ...KO1, __patterns: [...KO1.__patterns, ...KO2.__patterns, ...KO3.__patterns, ...KO4.__patterns, ...KO5.__patterns, ...KO6.__patterns, ...KO7.__patterns, ...KO8.__patterns, ...KO9.__patterns] };
// Hindi, the same way (E112b-hi).
const HI = { ...HI9, ...HI8, ...HI7, ...HI6, ...HI5, ...HI4, ...HI3, ...HI2, ...HI1, __patterns: [...HI1.__patterns, ...HI2.__patterns, ...HI3.__patterns, ...HI4.__patterns, ...HI5.__patterns, ...HI6.__patterns, ...HI7.__patterns, ...HI8.__patterns, ...HI9.__patterns] };
const ID = { ...ID9, ...ID8, ...ID7, ...ID6, ...ID5, ...ID4, ...ID3, ...ID2, ...ID1, __patterns: [...ID1.__patterns, ...ID2.__patterns, ...ID3.__patterns, ...ID4.__patterns, ...ID5.__patterns, ...ID6.__patterns, ...ID7.__patterns, ...ID8.__patterns, ...ID9.__patterns] };
const TABLES = { es: ES, fr: FR, de: DE, it: IT, pt: PT, ja: JA, ko: KO, hi: HI, id: ID };

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
  }, code === "es" ? (left => s => left.has(s.trim()))(new Set(LEFT)) : null);   // shown in English, so marked lang="en" (dom.js)
}
