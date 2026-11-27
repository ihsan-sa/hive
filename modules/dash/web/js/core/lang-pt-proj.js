// pt: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  "it stopped without saying why": "parou sem dizer porquê",
  "your Hive restarted while this ran; start it again": "seu Hive reiniciou enquanto isto rodava; comece de novo",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "esta sessão não consegue mais acompanhar (o acesso completo terminou após um tempo sem atividade); continua rodando no seu Hive, então entre de novo e veja em Projetos",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "seu Hive parou de responder; talvez ainda esteja rodando, então veja em Projetos daqui a pouco",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "Mova um projeto para outro Hive, com seu histórico. Exportar salva um como zip neste dispositivo; Trazer adiciona um a partir desse zip.",
  "Project to export": "Projeto a exportar",
  "No projects yet": "Ainda não há projetos",
  "Couldn't read the projects": "Não foi possível ler os projetos",
  "Export": "Exportar",
  "Export the project as a zip": "Exportar o projeto como zip",
  "its name here (optional)": "o nome dele aqui (opcional)",
  "Name for the project brought in": "Nome do projeto trazido",
  "Bring in a project…": "Trazer um projeto…",
  "Bring in a project from a zip": "Trazer um projeto de um zip",
  "What it did": "O que ele fez",
  "Bring in a project": "Trazer um projeto",
  "Exporting": "Exportando",
  "Bringing in": "Trazendo",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `Exportando ${n}` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `Trazendo ${n}` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} está baixando. Traga-o no outro Hive com “Trazer um projeto”, nas Configurações dele.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} não foi exportado: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `Trazido como ${n}: já está em Projetos.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} não foi trazido: ${e}`; }],
  ],
};
