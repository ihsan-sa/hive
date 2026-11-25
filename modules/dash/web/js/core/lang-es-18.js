// es: the Knowledge file page's Save as and leave-unsaved words (views/knowledge.js), and the "can't reach" band's reason
// (core/store.js failWords: "The <what> read got no answer." / "read failed: <why>."). Exact English keys.
const READ = { activity: "la actividad", agents: "los agentes", conversations: "las conversaciones" };
const what = w => READ[w] || w;
const SELF = {
  "Collapse": "Contraer",   // a drawing or page folded to its bar (rich/figure.js)
  "Fold this to its title bar, or open it again": "Pliégalo a su barra de título, o ábrelo otra vez",
  "Text file": "Archivo de texto",
  "Your edits aren't saved. Leave them?": "Tus cambios no están guardados. ¿Salir sin guardarlos?",
  __patterns: [
    [/^The (\w+) read got no answer(\.?)$/, (_, w, d) => `La lectura de ${what(w)} no obtuvo respuesta${d}`],
    [/^The (\w+) read failed: (.*)$/, (_, w, r) => `La lectura de ${what(w)} falló: ${r}`],
  ],
};
export default SELF;
