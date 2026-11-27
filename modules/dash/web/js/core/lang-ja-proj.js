// ja: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  "it stopped without saying why": "理由を示さずに止まりました",
  "your Hive restarted while this ran; start it again": "実行中に Hive が再起動しました。もう一度始めてください",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "このサインインでは追えなくなりました(しばらく操作がなかったためフルアクセスが終了しました)。Hive では実行が続いているので、もう一度サインインしてプロジェクトを確認してください",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "Hive が応答しなくなりました。まだ実行中かもしれないので、しばらくしてからプロジェクトを確認してください",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "プロジェクトを履歴ごと別の Hive に移します。エクスポートはこの端末に zip で保存し、取り込みはその zip から追加します。",
  "Project to export": "エクスポートするプロジェクト",
  "No projects yet": "プロジェクトはまだありません",
  "Couldn't read the projects": "プロジェクトを読み込めませんでした",
  "Export": "エクスポート",
  "Export the project as a zip": "プロジェクトを zip でエクスポート",
  "its name here (optional)": "ここに名前(任意)",
  "Name for the project brought in": "取り込むプロジェクトの名前",
  "Bring in a project…": "プロジェクトを取り込む…",
  "Bring in a project from a zip": "zip からプロジェクトを取り込む",
  "What it did": "実行した内容",
  "Bring in a project": "プロジェクトを取り込む",
  "Exporting": "エクスポート中",
  "Bringing in": "取り込み中",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `${n} をエクスポート中` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `${n} を取り込み中` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} をダウンロードしています。もう一方の Hive の設定で「プロジェクトを取り込む」から取り込んでください。`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} をエクスポートできませんでした: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `${n} として取り込みました。プロジェクトに表示されています。`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} を取り込めませんでした: ${e}`; }],
  ],
};
