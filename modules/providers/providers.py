"""Hive providers: run agents on other model APIs and harnesses.

Two kinds of entry in config.json's providers:
  - an Anthropic-compatible API (OpenRouter, a LiteLLM proxy, z.ai, ...): Claude Code runs on it (base_url, key_file, config_dir).
  - "harness": "codex": the run is OpenAI's Codex CLI on its own ChatGPT login (codex_home, default ~/.codex).
  - "harness": "kimi": the run is Moonshot's Kimi Code CLI on its own subscription login (kimi_home, default ~/.kimi-code).
The starter (~/core/starter.py) loads this file when a providers entry names it ("impl") and calls:
  room(c, pc, cache)  -> None if the provider can take a run now, else a short reason (shown on the board)
  model_room(c, pc, model, cache) -> the same for one model (OpenRouter :free models' daily requests); optional
  env(pc, model)      -> env vars for the run (for Claude Code: its API points at the provider, every model slot = model)
  command(pc, model, prompt, system, sid, resume) -> argv for the run, or None for the starter's own `claude` command
pc is that provider's config.json entry. Keys are read from pc["key_file"] and never logged or posted.
status(c) -> {provider: limits} for the dashboard (no keys in it), cached in ~/.hive/providers.json.
"""
import json, os, subprocess, time, urllib.request
from pathlib import Path

STATUS = Path.home() / ".hive" / "providers.json"
LIMITS = Path.home() / ".hive" / "provider_limits.json"   # {name: {"stop_pct": N, "monthly_usd": N}}: the person's lines, set on the dashboard

def lines(c, pc):
    """pc with the person's own stop lines from LIMITS laid over config.json's (stop_pct for Codex, monthly_usd for OpenRouter)."""
    name = next((n for n, v in (c.get("providers") or {}).items() if v is pc or v == pc), None)
    try: over = json.loads(LIMITS.read_text()).get(name) or {}
    except (OSError, ValueError, AttributeError): over = {}
    return {**pc, **{k: v for k, v in over.items() if k in ("stop_pct", "monthly_usd") and isinstance(v, (int, float))}}
INBOX = Path.home() / "core" / "inbox.py"
DENY = Path.home() / "core" / "bin" / "cc-github-deny-hook"

def key(pc):
    try: return Path(pc["key_file"]).expanduser().read_text().strip() or None
    except (OSError, KeyError): return None

def check_openrouter(k, pc):
    """Spend guard for OpenRouter (GET /api/v1/key). Holds the run when the key's usage this month (UTC) has reached
    monthly_usd (the person's monthly limit, shared by every install on that key), when its own hard limit or the account's
    credit (effective_budget) has under min_credit left, or when the check can't be read: no answer means no run, since a run spends money."""
    req = urllib.request.Request(pc["key_check"], headers={"Authorization": f"Bearer {k}"})
    d = json.load(urllib.request.urlopen(req, timeout=15)).get("data") or {}
    month, cap = d.get("usage_monthly"), pc.get("monthly_usd")
    if cap is not None:
        if not isinstance(month, (int, float)): return "the key check gave no usage_monthly"
        if month >= cap: return f"${month:.2f} used this month, at the ${cap:g} monthly limit"
    left = d.get("limit_remaining")
    if isinstance(left, (int, float)) and left < pc.get("min_credit", 1): return f"${left:.2f} left on the key, under min_credit"
    acct = (d.get("effective_budget") or {}).get("remaining_usd")   # the account's credit: runs fail 402 once it's gone
    if isinstance(acct, (int, float)) and acct < pc.get("min_credit", 1): return f"${acct:.2f} credit left on the account, under min_credit"
    if cap is None and d.get("limit") is None: return "no monthly_usd in config and no limit on the key"
    return None

def room(c, pc, cache):
    pc = lines(c, pc)
    if pc.get("harness") == "codex": return codex_room(pc, cache)
    if pc.get("harness") == "kimi": return kimi_room(pc)
    k = key(pc)
    if not k: return f"no key in {pc.get('key_file')}"
    if not pc.get("key_check"): return None
    hit = cache.get((pc["key_check"], pc["key_file"]))
    if not hit or time.time() - hit[0] >= pc.get("check_s", 60):
        try: why = check_openrouter(k, pc)
        except Exception as e: why = f"credit check failed ({type(e).__name__})"
        cache[(pc["key_check"], pc["key_file"])] = hit = (time.time(), why)
    return hit[1]

def model_room(c, pc, model, cache):
    """None if this one model can take a run now, else why. OpenRouter free models (id ends in :free) share a daily request
    count (1000/day on this account); a tool-using run takes 20-100, so hold below free_min_left (default 100)."""
    if not model.endswith(":free") or not pc.get("key_check"): return None
    k = key(pc)
    if not k: return f"no key in {pc.get('key_file')}"
    ck = ("free", pc["key_check"], pc["key_file"]); hit = cache.get(ck)
    if not hit or time.time() - hit[0] >= pc.get("check_s", 60):
        try:
            req = urllib.request.Request(pc["key_check"], headers={"Authorization": f"Bearer {k}"})
            f = (json.load(urllib.request.urlopen(req, timeout=15)).get("data") or {}).get("free_model_daily_requests") or {}
            left = f.get("remaining")
            why = None if not isinstance(left, (int, float)) or left >= pc.get("free_min_left", 100) else f"{left} free requests left today"
        except Exception as e: why = f"free check failed ({type(e).__name__})"
        cache[ck] = hit = (time.time(), why)
    return hit[1]

def env(pc, model):
    if pc.get("harness") == "codex": return {"CODEX_HOME": str(codex_home(pc))}
    if pc.get("harness") == "kimi": return {"KIMI_CODE_HOME": str(kimi_home(pc)), "KIMI_CODE_NO_AUTO_UPDATE": "1", "KIMI_DISABLE_TELEMETRY": "1"}
    e = {"ANTHROPIC_BASE_URL": pc["base_url"], "ANTHROPIC_AUTH_TOKEN": key(pc) or "", "ANTHROPIC_API_KEY": ""}
    if pc.get("key_env") == "api": e.update(ANTHROPIC_API_KEY=key(pc) or "", ANTHROPIC_AUTH_TOKEN="")  # Anthropic's own API takes x-api-key, not a bearer token
    for v in ("ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
              "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"): e[v] = model
    return e

def command(pc, model, prompt, system, sid, resume):
    if pc.get("harness") == "kimi": return kimi_command(pc, model, prompt, system, sid, resume)
    if pc.get("harness") != "codex": return None
    codex_hooks(pc)
    note = ("You are running in OpenAI's Codex CLI, not Claude Code: the Agent, ToolSearch and Skill tools named below don't exist "
            "here; use the shell for everything (hive, git, gh). Board posts for you arrive after tool calls as hook context.\n\n")
    return ["codex", "exec", *(["resume"] if resume else []), "--json", "--skip-git-repo-check",
            "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust",
            *(["-m", model] if model and model != "default" else []),  # codex:default = the login's own default model
            "-c", "developer_instructions=" + json.dumps(note + system), *([sid] if resume else []), prompt]

# ---- codex: its own ChatGPT login (`codex login --device-auth` as this account), limits read through `codex app-server`

def codex_home(pc): return Path(pc.get("codex_home") or "~/.codex").expanduser()

def codex_hooks(pc):
    """The inbox hook (mid-run posts) and the GitHub deny hook in CODEX_HOME/hooks.json; Codex reads Claude Code's hook format, and a PreToolUse exit 2 stops the call
    (checked on codex-cli 0.153.4). Each is added on its own, so an install from before the deny hook gets it."""
    f = codex_home(pc) / "hooks.json"
    try: s = json.loads(f.read_text()) if f.exists() else {}
    except ValueError: return
    had = json.dumps(s)
    hooks = s.setdefault("hooks", {})
    if str(INBOX) not in had:
        h = {"hooks": [{"type": "command", "command": str(INBOX), "timeout": 10}]}
        hooks.setdefault("PostToolUse", []).append({"matcher": "*", **h}); hooks.setdefault("Stop", []).append(h)
    if str(DENY) not in had:
        hooks.setdefault("PreToolUse", []).append({"matcher": "Bash|WebFetch", "hooks": [{"type": "command", "command": str(DENY), "timeout": 10}]})
    if json.dumps(s) == had: return
    f.parent.mkdir(parents=True, exist_ok=True); f.write_text(json.dumps(s, indent=2) + "\n")

def codex_limits(pc, timeout=20):
    """{"account": ..., "limits": {...}} from `codex app-server` (account/read, account/rateLimits/read). Uses no model usage."""
    msgs = [{"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "hive", "version": "1"}}}, {"method": "initialized"},
            {"id": 2, "method": "account/read", "params": {}}, {"id": 3, "method": "account/rateLimits/read"}]
    p = subprocess.Popen(["codex", "app-server"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                         env={**os.environ, "CODEX_HOME": str(codex_home(pc))}, text=True)
    got, t = {}, time.time() + timeout
    try:
        p.stdin.write("".join(json.dumps(m) + "\n" for m in msgs)); p.stdin.flush()
        while time.time() < t and not {2, 3} <= set(got):
            line = p.stdout.readline()
            if not line: break
            try: m = json.loads(line)
            except ValueError: continue
            if m.get("id") in (2, 3): got[m["id"]] = m
    finally: p.kill(); p.wait()
    acct = (got.get(2) or {}).get("result") or {}
    lim = got.get(3) or {}
    return {"account": acct.get("account"), "limits": lim.get("result"), "error": (lim.get("error") or {}).get("message"),
            "answered": 2 in got}   # no account/read reply (slow app-server, busy box) is not "logged out"

def windows(limits):
    """[(used percent, resets_at epoch or None, minutes)] for each window Codex reports (5-hour, weekly), however it nests them."""
    out = []
    def walk(d):
        if isinstance(d, dict):
            u = d.get("usedPercent", d.get("used_percent"))
            if isinstance(u, (int, float)):
                out.append((float(u), d.get("resetsAt", d.get("resets_at")), d.get("windowDurationMins", d.get("window_minutes")))); return
            for v in d.values(): walk(v)
        elif isinstance(d, list):
            for v in d: walk(v)
    walk(limits)
    return list(dict.fromkeys(out))   # app-server can report the same window under two keys

def codex_room(pc, cache):
    hit = cache.get(("codex", str(codex_home(pc))))
    if not hit or time.time() - hit[0] >= pc.get("check_s", 120):
        try:
            s = codex_limits(pc)
            if not s.get("answered", True): why = "Codex check failed (no reply from codex app-server)"
            elif not s["account"]: why = f"Codex isn't logged in (as this account: CODEX_HOME={codex_home(pc)} codex login --device-auth)"
            else:
                full = [w for w in windows(s["limits"]) if w[0] >= pc.get("stop_pct", 95)]
                why = f"Codex limit at {full[0][0]:.0f}% (stop at {pc.get('stop_pct', 95)}%)" if full else None
        except Exception as e: why = f"Codex check failed ({type(e).__name__})"
        cache[("codex", str(codex_home(pc)))] = hit = (time.time(), why)
    return hit[1]

# ---- dashboard

# ---- kimi: its own subscription login (`kimi login` as this account, the person's). Tested on 2.4.1: HIVE_NAME reaches hooks; PostToolUse
# and PreToolUse stdout never reach the model, only a PreToolUse deny and a Stop exit 2 do (kimi_inbox.py turns posts into those).

def kimi_home(pc): return Path(pc.get("kimi_home") or "~/.kimi-code").expanduser()

def kimi_hooks(pc):
    """The inbox hook in KIMI_CODE_HOME/config.toml, appended once; Kimi reads hooks only from there."""
    f, hook = kimi_home(pc) / "config.toml", Path(__file__).resolve().parent / "kimi_inbox.py"
    old = f.read_text() if f.exists() else ""
    if str(hook) in old: return
    f.parent.mkdir(parents=True, exist_ok=True)
    f.write_text(old + "".join(f'\n[[hooks]]\nevent = "{e}"\ncommand = "{hook}"\ntimeout = 10\n' for e in ("PreToolUse", "Stop")))

def kimi_command(pc, model, prompt, system, sid, resume):
    kimi_hooks(pc)
    if not resume:  # no system-prompt flag: the orientation opens the session, and resuming keeps it
        prompt = ("You are running in Moonshot's Kimi Code CLI, not Claude Code: the Agent, ToolSearch and Skill tools named below may "
                  "not exist here; use the shell for hive, git and gh. A board post for you arrives as the refusal of a tool call: read "
                  "it, then repeat that call.\n\n" + system + "\n\n# Your task\n\n" + prompt)
    return [str(pc.get("kimi_bin") and Path(pc["kimi_bin"]).expanduser() or "kimi"), *(["-S", sid] if resume else []), "-p", prompt, "--output-format", "stream-json",
            *(["-m", model] if model and model != "default" else [])]

def kimi_logged_in(pc): return any((kimi_home(pc) / "credentials").glob("*.json"))

def kimi_live_runs(name):
    n = 0
    for f in (Path.home() / "run").glob("*.json"):
        try:
            r = json.loads(f.read_text())
            if r.get("provider") == name: os.kill(r["pid"], 0); n += 1
        except (OSError, ValueError, KeyError, TypeError, AttributeError): pass
    return n

def kimi_room(pc):
    """Hold when signed out or at max_runs (the person's trial: a few agents at a time). Kimi publishes no usage API
    Hive knows of: a 5-hour limit shows up as the run's own error, and the starter resumes it (limit_hit)."""
    if not kimi_logged_in(pc): return "Kimi isn't logged in (as this account: kimi login)"
    m = pc.get("max_runs", 3)
    if kimi_live_runs(pc.get("name", "kimi")) >= m: return f"Kimi is at its {m} runs"
    return None

def status(c, max_age=300):
    """{"_checked": t, name: {...}} for every provider: OpenRouter spend, Codex login and limits. Never holds a key."""
    try:
        old = json.loads(STATUS.read_text())
        if time.time() - old.get("_checked", 0) < max_age: return old
    except (OSError, ValueError): pass
    out = {"_checked": time.time()}
    for name, pc in (c.get("providers") or {}).items():
        pc = lines(c, pc)
        try:
            if pc.get("harness") == "codex":
                s = codex_limits(pc)
                out[name] = {"kind": "codex", "logged_in": bool(s["account"]), "plan": (s["account"] or {}).get("planType"),
                             "stop_pct": pc.get("stop_pct", 95), "windows": [{"used": u, "resets": r, "minutes": m} for u, r, m in windows(s["limits"])],
                             "error": None if s["account"] else "not logged in" if s.get("answered", True) else "no reply from codex app-server"}
            elif pc.get("harness") == "kimi":
                out[name] = {"kind": "kimi", "logged_in": kimi_logged_in(pc), "runs": kimi_live_runs(name), "max_runs": pc.get("max_runs", 3),
                             "error": None if kimi_logged_in(pc) else "not logged in"}
            elif pc.get("key_check"):
                k = key(pc)
                if not k: out[name] = {"kind": "key", "error": "no key yet"}; continue
                req = urllib.request.Request(pc["key_check"], headers={"Authorization": f"Bearer {k}"})
                d = json.load(urllib.request.urlopen(req, timeout=15)).get("data") or {}
                out[name] = {"kind": "key", "monthly": d.get("usage_monthly"), "monthly_cap": pc.get("monthly_usd"), "daily": d.get("usage_daily"),
                             "limit": d.get("limit"), "remaining": d.get("limit_remaining"), "rate_limit": d.get("rate_limit"),
                             # free-model requests today (UTC): 50/day, or 1000/day once $10 of credit was ever bought
                             "free": d.get("free_model_daily_requests"),
                             # the budget that binds first (often the account's credit, not the key's own limit)
                             "budget": {x: (d.get("effective_budget") or {}).get(x) for x in ("scope", "limit_usd", "spend_usd", "remaining_usd")}
                                       if d.get("effective_budget") else None}
            else: out[name] = {"kind": "plain"}
        except Exception as e: out[name] = {"error": f"check failed ({type(e).__name__})"}
    try: STATUS.parent.mkdir(parents=True, exist_ok=True); STATUS.write_text(json.dumps(out, indent=1))
    except OSError: pass
    return out

if __name__ == "__main__":  # python3 providers.py: print the dashboard's view, fresh
    print(json.dumps(status(json.loads((Path.home() / "core" / "config.json").read_text()), 0), indent=1))
