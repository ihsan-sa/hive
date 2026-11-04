// Easter egg: typing /67 in the composer makes the whole screen do the 67 meme (two hands going up and down, out of step). Nothing is sent.
let busy = false;
export function sixSeven() {
  if (busy) return; busy = true;
  const st = document.createElement("style");
  st.textContent = `@keyframes s7a{0%,100%{transform:translateY(-18px)}50%{transform:translateY(18px)}}
@keyframes s7b{0%,100%{transform:translateY(18px)}50%{transform:translateY(-18px)}}
@keyframes s7n{0%,100%{transform:translateY(-40px) scale(1)}50%{transform:translateY(40px) scale(1.15)}}
#s7{position:fixed;inset:0;z-index:99999;pointer-events:none;display:flex;justify-content:center;align-items:center;gap:12vw;font:900 28vmin/1 system-ui,sans-serif;color:#fff;text-shadow:0 0 .1em #000,0 0 .3em #f0f}
#s7 b{animation:s7a .32s ease-in-out infinite}#s7 b+b{animation-name:s7b}
body.s7on>*:not(#s7){animation:s7n .32s ease-in-out infinite;transform-origin:center}
@media (prefers-reduced-motion:reduce){body.s7on>*:not(#s7),#s7 b{animation-duration:1.6s}}`;
  const ov = document.createElement("div"); ov.id = "s7"; ov.setAttribute("aria-hidden", "true");
  ov.innerHTML = "<b>6</b><b>7</b>";
  document.head.append(st); document.body.append(ov); document.body.classList.add("s7on");
  setTimeout(() => { document.body.classList.remove("s7on"); ov.remove(); st.remove(); busy = false; }, 6700);
}
