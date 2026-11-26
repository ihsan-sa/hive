// Korean for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-ko-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
export default {
  "Bought": "구매함",
  "Canceled": "취소됨",
  "Card": "카드",
  "Card expired": "카드 만료됨",
  "Charged": "청구됨",
  "Code, issues and updates are on": "코드, 이슈, 업데이트는 여기에 있습니다:",
  "Declined": "거절됨",
  "Expired": "만료됨",
  "It has no merchant or amount.": "판매자도 금액도 없습니다.",
  "It isn't JSON.": "JSON이 아닙니다.",
  "It isn't a buy.": "구매가 아닙니다.",
  "Item": "품목",
  "Merchant": "판매자",
  "One-time card from Link, made on approval": "Link 일회용 카드, 승인 시 생성",
  "Receipt": "영수증",
  "Recording": "녹화",
  "Shipping": "배송",
  "Should have lapsed": "만료되었어야 함",
  "Still open from rev": "미해결, 시작 버전: rev",
  "Tax": "세금",
  "Waiting for Link": "Link 대기 중",
  "Waiting in Link": "Link에서 대기 중",
  "test mode": "테스트 모드",
  "Stop and send": "중지하고 보내기",
  "Stopped and sent": "중지하고 보냄",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "보내기는 지금 단계가 끝날 때까지 기다립니다. ‘중지하고 보내기’는 그 단계를 바로 끊으려 시도합니다.",
  __patterns: [
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "단계가 끝나기를 기다리지 않고 지금 중지한 뒤 이 메시지를 보냅니다"],
    [/^Still open from rev (.+)$/, (_, r) => "미해결, 시작 버전: rev" + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
