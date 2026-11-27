// ko: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
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
