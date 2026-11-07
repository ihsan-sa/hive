// Easter egg: click the logo three times quickly and a bee flies a looping path around it for a few seconds. Display only; the click still works.
export function beeAround(a) {
  const el = a.querySelector(".brand-logo") || a, r = el.getBoundingClientRect();
  if (!r.width) return;
  const cx = r.left + r.width / 2, cy = r.top + r.height / 2, R = Math.max(r.width, r.height) * 0.9 + 10;
  const bee = document.createElement("div"); bee.setAttribute("aria-hidden", "true");
  bee.style.cssText = "position:fixed;left:0;top:0;z-index:99999;pointer-events:none;font-size:20px;line-height:1;will-change:transform";
  bee.textContent = "🐝"; document.body.append(bee);
  const T = 3200, t0 = performance.now();
  const step = now => {
    const p = (now - t0) / T;
    if (p >= 1) { bee.remove(); return; }
    const ang = p * Math.PI * 2 * 2.5, rr = R * (1 + 0.35 * Math.sin(p * Math.PI * 6)) * (p > 0.85 ? 1 + (p - 0.85) * 12 : 1);
    const x = cx + rr * Math.cos(ang), y = cy + rr * 0.7 * Math.sin(ang), dx = -Math.sin(ang);
    bee.style.opacity = p > 0.9 ? String((1 - p) * 10) : "1";
    bee.style.transform = `translate(${x - 10}px,${y - 10}px) scaleX(${dx > 0 ? -1 : 1}) rotate(${Math.sin(p * 90) * 10}deg)`;
    requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}
