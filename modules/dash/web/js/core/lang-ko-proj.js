// ko: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  // E207: the line in a conversation's reply place while it shows the copy kept on this device (views/comms.js readOnly)
  "Offline. This is the copy kept on this device, read only; replies need a connection.": "오프라인입니다. 이 기기에 저장된 사본이며 읽기 전용입니다. 답장하려면 연결이 필요합니다.",
  "Showing the chats this device last read. Trying again every few seconds.": "이 기기에서 마지막으로 읽은 채팅을 보여 줍니다. 몇 초마다 다시 시도합니다.",
  "On, with the first line of each reply. It shows on your phone's lock screen and passes through ntfy.sh, a public service. Anyone who learns the topic below can read those lines, so keep it to yourself.": "켜짐, 각 답장의 첫 줄과 함께. 휴대폰 잠금 화면에 보이고 공개 서비스인 ntfy.sh를 거칩니다. 아래 주제를 아는 사람은 누구나 그 줄을 읽을 수 있으니 혼자만 알고 계세요.",
  "Show the reply's first line in the alert": "알림에 답장 첫 줄 보이기",
  "Hide the reply's first line": "답장 첫 줄 숨기기",
  "The alerts now carry the reply's first line.": "이제 알림에 답장의 첫 줄이 들어갑니다.",
  "The alerts no longer carry any words from a reply.": "이제 알림에 답장의 어떤 말도 들어가지 않습니다.",
  "Off: the alert only says something is waiting, so nothing from a reply shows on the lock screen.": "꺼짐: 알림은 기다리는 것이 있다고만 알려서 잠금 화면에 답장 내용이 보이지 않습니다.",
  "Anyone who can see your phone's lock screen can read that line.": "휴대폰 잠금 화면을 볼 수 있는 사람은 누구나 그 줄을 읽을 수 있습니다.",
  "it stopped without saying why": "이유를 밝히지 않고 멈췄습니다",
  "your Hive restarted while this ran; start it again": "실행 중에 Hive가 다시 시작되었습니다. 다시 시작하세요",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "이 로그인으로는 더 이상 따라갈 수 없습니다(한동안 활동이 없어 전체 접근이 끝났습니다). Hive에서는 계속 실행 중이니 다시 로그인한 뒤 프로젝트를 확인하세요",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "Hive가 응답하지 않습니다. 아직 실행 중일 수 있으니 잠시 후 프로젝트를 확인하세요",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "프로젝트를 기록과 함께 다른 Hive로 옮깁니다. 내보내기는 이 기기에 zip으로 저장하고, 가져오기는 그런 zip에서 추가합니다.",
  "Project to export": "내보낼 프로젝트",
  "No projects yet": "아직 프로젝트가 없습니다",
  "Couldn't read the projects": "프로젝트를 읽지 못했습니다",
  "Export": "내보내기",
  "Export the project as a zip": "프로젝트를 zip으로 내보내기",
  "its name here (optional)": "여기에 이름(선택)",
  "Name for the project brought in": "가져올 프로젝트의 이름",
  "Bring in a project…": "프로젝트 가져오기…",
  "Bring in a project from a zip": "zip에서 프로젝트 가져오기",
  "What it did": "한 일",
  "Bring in a project": "프로젝트 가져오기",
  "Exporting": "내보내는 중",
  "Bringing in": "가져오는 중",
  // the store's own failure when the Hive gives no answer (store.js ask), in "X wasn't exported: …" (drive_app_lang notices, 11-27)
  "can't reach the server": "서버에 연결할 수 없습니다",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `${n} 내보내는 중` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `${n} 가져오는 중` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} 다운로드 중입니다. 다른 Hive의 설정에서 "프로젝트 가져오기"로 가져오세요.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n}을(를) 내보내지 못했습니다: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `${n}(으)로 가져왔습니다. 이제 프로젝트에 있습니다.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n}을(를) 가져오지 못했습니다: ${e}`; }],
  ],
};
