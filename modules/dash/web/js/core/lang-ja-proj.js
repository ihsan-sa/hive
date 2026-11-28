// ja: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  // E207: the line in a conversation's reply place while it shows the copy kept on this device (views/comms.js readOnly)
  "Offline. This is the copy kept on this device, read only; replies need a connection.": "オフラインです。この端末に保存されたコピーで、読み取り専用です。返信には接続が必要です。",
  "Showing the chats this device last read. Trying again every few seconds.": "この端末で最後に読んだチャットを表示しています。数秒ごとに再試行しています。",
  "On, with the first line of each reply. It shows on your phone's lock screen and passes through ntfy.sh, a public service. Anyone who learns the topic below can read those lines, so keep it to yourself.": "オン。各返信の最初の1行が付きます。スマホのロック画面に表示され、公開サービスの ntfy.sh を経由します。下のトピックを知っている人は誰でもその行を読めるので、人に教えないでください。",
  "Show the reply's first line in the alert": "通知に返信の最初の1行を表示",
  "Hide the reply's first line": "返信の最初の1行を隠す",
  "The alerts now carry the reply's first line.": "通知に返信の最初の1行が付くようになりました。",
  "The alerts no longer carry any words from a reply.": "通知に返信の言葉は一切含まれなくなりました。",
  "Off: the alert only says something is waiting, so nothing from a reply shows on the lock screen.": "オフ：通知は何かが待っていることだけを伝えるので、ロック画面に返信の内容は表示されません。",
  "Anyone who can see your phone's lock screen can read that line.": "スマホのロック画面を見られる人なら誰でもその行を読めます。",
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
  // the store's own failure when the Hive gives no answer (store.js ask), in "X wasn't exported: …" (drive_app_lang notices, 11-27)
  "can't reach the server": "サーバーに接続できません",
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
