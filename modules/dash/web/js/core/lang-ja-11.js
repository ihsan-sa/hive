// Japanese for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-ja-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
export default {
  "Bought": "購入済み",
  "Canceled": "キャンセル済み",
  "Card": "カード",
  "Card expired": "カードの有効期限切れ",
  "Charged": "請求済み",
  "Code, issues and updates are on": "コード、イシュー、更新情報はこちら:",
  "Declined": "拒否",
  "Expired": "期限切れ",
  "It has no merchant or amount.": "販売者も金額もありません。",
  "It isn't JSON.": "JSON ではありません。",
  "It isn't a buy.": "購入ではありません。",
  "Item": "商品",
  "Merchant": "販売者",
  "One-time card from Link, made on approval": "Link の使い捨てカード(承認時に作成)",
  "Receipt": "領収書",
  "Recording": "録画",
  "Shipping": "配送",
  "Should have lapsed": "失効しているはずです",
  "Still open from rev": "未解決(この版から: rev)",
  "Tax": "税",
  "Waiting for Link": "Link を待っています",
  "Waiting in Link": "Link で待機中",
  "test mode": "テストモード",
  "Stop and send": "停止して送信",
  "Stopped and sent": "停止して送信しました",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "送信は今のステップが終わるまで待ちます。「停止して送信」はそのステップを打ち切ろうとします。",
  __patterns: [
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "ステップの終了を待たず、今すぐ停止してこれを送信します"],
    [/^Still open from rev (.+)$/, (_, r) => "未解決(この版から: rev" + " " + r + ")"],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
