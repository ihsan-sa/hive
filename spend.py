"""What the Hive has spent, from the `cost` on each run's end event in log.jsonl (a run's total_cost_usd, counted at API prices).
A resumed session's total is cumulative, so a run's spend is its rise over the last end of the same session."""
import datetime as dt, json

def spend_on(log_path, day=None):
    """Dollars spent on `day` (YYYY-MM-DD, default today UTC)."""
    day = day or dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d")
    last, tot = {}, 0.0
    try: f = open(log_path, errors="replace")
    except OSError: return 0.0
    with f:
        for ln in f:
            if '"end"' not in ln or '"cost"' not in ln: continue
            try: e = json.loads(ln)
            except ValueError: continue
            c = e.get("cost")
            if e.get("event") != "end" or not isinstance(c, (int, float)) or isinstance(c, bool): continue
            k = e.get("session") or e.get("name")
            rise = max(0.0, c - last.get(k, 0.0)); last[k] = max(c, last.get(k, 0.0))
            if str(e.get("at", ""))[:10] == day: tot += rise
    return round(tot, 4)

def over_cap(c, log_path, day=None):
    """The daily cap if today's spend has reached it, else None."""
    cap = c.get("max_daily_usd")
    return cap if cap and spend_on(log_path, day) >= cap else None
