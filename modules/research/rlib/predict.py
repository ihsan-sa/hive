"""Numbers: forecasts with a backtest, trends, base rates, odds, a scored ledger of predictions, and lead ranking. Standard library only.
Each method tells how far to trust it. A forecast with no backtest is a guess."""
import csv, fcntl, json, math, os, random, statistics as st
from contextlib import contextmanager
from pathlib import Path


# Forecast methods: f(history, horizon) -> [next values].
def naive(h, k, m=1): return [h[-1]] * k
def mean_(h, k, m=1): return [sum(h) / len(h)] * k
def drift(h, k, m=1):
    b = (h[-1] - h[0]) / max(1, len(h) - 1)
    return [h[-1] + b * (i + 1) for i in range(k)]
def snaive(h, k, m=1):
    if m < 2 or len(h) < m: return naive(h, k)
    return [h[-m + (i % m)] for i in range(k)]


def seasonal_drift(h, k, m=1):
    """Same time last season plus the change over the last full season: a season riding on a trend, which plain seasonal always trails."""
    if m < 2 or len(h) < 2 * m: return snaive(h, k, m)
    step = (sum(h[-m:]) - sum(h[-2 * m:-m])) / m
    return [h[-m + i % m] + step * (i // m + 1) for i in range(k)]


def _ses(h, a):
    l = h[0]
    for x in h[1:]: l = a * x + (1 - a) * l
    return l


def ses(h, k, m=1):
    a = min((i / 10 for i in range(1, 10)), key=lambda a: _ses_err(h, a))
    return [_ses(h, a)] * k


def _ses_err(h, a):
    l, e = h[0], 0.0
    for x in h[1:]: e += (x - l) ** 2; l = a * x + (1 - a) * l
    return e


def _holt(h, a, b, phi=0.9):
    l, t, e = h[0], h[1] - h[0], 0.0
    for x in h[1:]:
        e += (x - (l + phi * t)) ** 2
        nl = a * x + (1 - a) * (l + phi * t)
        t = b * (nl - l) + (1 - b) * phi * t
        l = nl
    return l, t, e


def holt(h, k, m=1):
    """Damped trend: a straight line carried far ahead is how forecasts embarrass themselves."""
    if len(h) < 3: return naive(h, k)
    grid = [(a / 10, b / 10) for a in range(1, 10, 2) for b in range(1, 10, 2)]
    a, b = min(grid, key=lambda p: _holt(h, *p)[2])
    l, t, _ = _holt(h, a, b)
    return [l + sum(0.9 ** (j + 1) for j in range(i + 1)) * t for i in range(k)]


METHODS = {"naive": naive, "mean": mean_, "drift": drift, "seasonal": snaive, "seasonal_drift": seasonal_drift, "ses": ses, "holt": holt}


def backtest(series, method, horizon, m=1, min_train=None, relative=False):
    """Rolling origin: stand at each past point, forecast ahead, compare with what happened. {step: [errors]}, or with `relative` each error as a share of its forecast."""
    n, f = len(series), METHODS[method]
    start = min_train or max(4, m + 1, n // 2)
    errs = {i: [] for i in range(horizon)}
    for t in range(start, n):
        fc = f(series[:t], min(horizon, n - t), m)
        for i, v in enumerate(fc):
            if not relative: errs[i].append(series[t + i] - v)
            elif v > 0: errs[i].append((series[t + i] - v) / v)
    return errs


def quantile(xs, q):
    xs = sorted(xs)
    if not xs: return 0.0
    p = q * (len(xs) - 1)
    i = int(p)
    return xs[i] + (xs[min(i + 1, len(xs) - 1)] - xs[i]) * (p - i)


def _finite(xs):
    bad = [x for x in xs if not math.isfinite(x)]
    if bad: raise ValueError(f"the series holds {bad[0]}: every value must be a real number")


def _heldout_skill(series, horizon, m, names):
    """Skill as a user would have had it: the method is chosen on the earlier backtests and scored on the later ones. Choosing and scoring on one set flatters whichever method got lucky."""
    errs = {}
    for name in names:
        by_origin = {}
        n, f = len(series), METHODS[name]
        start = max(4, m + 1, n // 2)
        for t in range(start, n):
            fc = f(series[:t], min(horizon, n - t), m)
            by_origin[t] = sum(abs(series[t + i] - v) for i, v in enumerate(fc)) / len(fc)
        errs[name] = by_origin
    origins = sorted(next(iter(errs.values())))
    if len(origins) < 6 or "naive" not in errs: return None
    cut = origins[len(origins) // 2]
    early = lambda name: sum(e for t, e in errs[name].items() if t < cut)
    late = lambda name: sum(e for t, e in errs[name].items() if t >= cut)
    chosen = min(errs, key=early)
    return 1 - late(chosen) / late("naive") if late("naive") else 0.0


MIN_INTERVAL = 8
WIDEN = 1.25  # past errors overlap and the method was chosen on them, so the plain rank runs narrow; see the coverage test


def _interval(errs):
    """Half-width of an 80% interval from past errors: the conformal rank of the absolute errors, widened for errors that overlap and a method chosen on them."""
    if len(errs) < MIN_INTERVAL: return None
    xs = sorted(abs(e) for e in errs)
    return xs[min(len(xs) - 1, math.ceil(0.8 * (len(xs) + 1)) - 1)] * WIDEN


def forecast(series, horizon=4, m=1):
    """Backtest every method, choose on MAE, report skill against naive on backtests the choice didn't see, and intervals from the method's own past errors. Short series say so."""
    if len(series) < 3: raise ValueError("a forecast needs at least 3 points")
    _finite(series)
    warn = ["intervals are nominal backtest ranges, not calibrated probabilities: shifts, compound growth and changing seasonal amplitude can cause severe undercoverage"]
    if len(series) < 12: warn.append(f"only {len(series)} points: treat this as a rough guess")
    table = {}
    for name in METHODS:
        if name in ("seasonal", "seasonal_drift") and m < 2: continue
        errs = backtest(series, name, horizon, m)
        flat = [abs(e) for v in errs.values() for e in v]
        if flat: table[name] = {"mae": round(sum(flat) / len(flat), 4), "n": len(flat)}
    if not table: raise ValueError("the series is too short to backtest")
    best = min(table, key=lambda k: table[k]["mae"])
    base = table.get("naive", {}).get("mae") or 0
    skill = 1 - table[best]["mae"] / base if base else 0.0
    held = _heldout_skill(series, horizon, m, list(table))
    if held is None: warn.append("too few backtests to test the choice of method on points it didn't see: the skill figure flatters it")
    elif held <= 0.1: warn.append("the chosen method didn't clearly beat 'same as last value' on backtests it wasn't chosen on: treat the method as no better than that")
    errs = backtest(series, best, horizon, m)
    # On a series that is all positive, errors tend to grow with the level (compound growth, a season whose swing grows), so past errors
    # are also taken as shares of their forecast and scaled to today's: the wider of the two is used.
    rel = backtest(series, best, horizon, m, relative=True) if min(series) > 0 else {}
    fc = METHODS[best](series, horizon, m)
    rows = []
    for i, v in enumerate(fc):
        e = errs.get(i) or []
        q = _interval(e)
        qr = _interval(rel.get(i) or []) if v > 0 else None
        if q is not None and qr is not None: q = max(q, qr * v)
        rows.append({"step": i + 1, "forecast": round(v, 4), "lo80": None if q is None else round(v - q, 4), "hi80": None if q is None else round(v + q, 4), "backtests": len(e)})
    if any(r["lo80"] is None for r in rows): warn.append(f"no interval where there are under {MIN_INTERVAL} backtests: too few errors to say how wide")
    if any(r["backtests"] < 30 and r["lo80"] is not None for r in rows): warn.append("intervals rest on under 30 overlapping backtests and on a method chosen with them: they can run narrow, so don't read 80% as exact")
    return {"method": best, "skill_vs_naive": round(skill, 3), "skill_heldout": None if held is None else round(held, 3), "methods": table, "forecast": rows, "warnings": warn}


def _norm_cdf(z): return 0.5 * math.erfc(-z / math.sqrt(2))


WANDERS = 0.5


def _lag_corrs(xs):
    """Lags whose correlation stands clear of noise, by rank so one wild value can't decide it, and the first lag's correlation whatever it is."""
    n = len(xs)
    ranks = [sum(y < x for y in xs) + (sum(y == x for y in xs) - 1) / 2 for x in xs]
    m = sum(ranks) / n
    den = sum((r - m) ** 2 for r in ranks)
    if not den: return [], 0.0
    corr = [sum((ranks[i] - m) * (ranks[i + k] - m) for i in range(n - k)) / den for k in range(1, n - 2)]
    return [(k + 1, r) for k, r in enumerate(corr) if abs(r) > 1.96 / math.sqrt(n - k - 1)], (corr[0] if corr else 0.0)


def trend(xs):
    """Mann-Kendall (is there a monotonic trend?) with Theil-Sen (how steep?). No assumption of a straight line or normal noise."""
    n = len(xs)
    if n < 4: raise ValueError("a trend needs at least 4 points")
    _finite(xs)
    s = sum((xs[j] > xs[i]) - (xs[j] < xs[i]) for i in range(n) for j in range(i + 1, n))
    groups = {}
    for x in xs: groups[x] = groups.get(x, 0) + 1
    var = (n * (n - 1) * (2 * n + 5) - sum(g * (g - 1) * (2 * g + 5) for g in groups.values())) / 18
    slope = st.median((xs[j] - xs[i]) / (j - i) for i in range(n) for j in range(i + 1, n))
    # Values that follow on from each other look like a trend far more often than chance says, so the variance is corrected for that (Hamed and Rao 1998).
    spread = (max(xs) - min(xs)) or 1.0
    lags, wander = _lag_corrs([round((x - slope * i) / spread, 9) for i, x in enumerate(xs)])
    var *= max(1.0, 1 + 2 / (n * (n - 1) * (n - 2)) * sum((n - k) * (n - k - 1) * (n - k - 2) * r for k, r in lags))
    z = 0.0 if var <= 0 or s == 0 else (s - (1 if s > 0 else -1)) / math.sqrt(var)
    p = 2 * (1 - _norm_cdf(abs(z)))
    out = {"n": n, "s": s, "z": round(z, 3), "p": round(p, 4), "slope_per_step": round(slope, 5), "lag1_corr": round(wander, 3), "direction": "none clear" if p >= 0.05 else ("up" if s > 0 else "down")}
    # A series that wanders can still get past that: nothing in one run of it tells drift from a real trend.
    if wander > WANDERS and p < 0.05:
        changes = [b - a for a, b in zip(xs, xs[1:])]
        se = st.stdev(changes) / math.sqrt(len(changes))
        drift_clear = abs(st.mean(changes)) > 1.96 * se
        if not drift_clear: out["direction"] = "none clear"
        out["warning"] = f"residual correlation {wander:.2f} after a straight line: curvature or persistent noise may explain this. Direction describes this sample, not evidence it will continue"
    return out


def _betacf(a, b, x):
    c, d = 1.0, 1 - (a + b) * x / (a + 1)
    d = 1 / (d if abs(d) > 1e-30 else 1e-30)
    h = d
    for m in range(1, 300):
        for num in (m * (b - m) * x / ((a + 2 * m - 1) * (a + 2 * m)), -(a + m) * (a + b + m) * x / ((a + 2 * m) * (a + 2 * m + 1))):
            d = 1 + num * d; d = 1 / (d if abs(d) > 1e-30 else 1e-30)
            c = 1 + num / c; c = c if abs(c) > 1e-30 else 1e-30
            step = d * c; h *= step
        if abs(step - 1) < 1e-10: break
    return h


def beta_cdf(x, a, b):
    if x <= 0: return 0.0
    if x >= 1: return 1.0
    front = math.exp(math.lgamma(a + b) - math.lgamma(a) - math.lgamma(b) + a * math.log(x) + b * math.log(1 - x))
    return front * _betacf(a, b, x) / a if x < (a + 1) / (a + b + 2) else 1 - front * _betacf(b, a, 1 - x) / b


def beta_quantile(q, a, b):
    lo, hi = 0.0, 1.0
    for _ in range(60):
        mid = (lo + hi) / 2
        if beta_cdf(mid, a, b) < q: lo = mid
        else: hi = mid
    return (lo + hi) / 2


def estimate(k, n, prior=(1.0, 1.0)):
    """A rate from k of n, with its uncertainty. 3 of 3 isn't 100%."""
    if not 0 <= k <= n: raise ValueError("need 0 <= hits <= total")
    a, b = prior[0] + k, prior[1] + n - k
    return {"k": k, "n": n, "raw": round(k / n, 4) if n else None, "estimate": round(a / (a + b), 4), "lo90": round(beta_quantile(0.05, a, b), 4), "hi90": round(beta_quantile(0.95, a, b), 4), "prior": list(prior)}


def _prob(o): return 1.0 if math.isinf(o) else round(o / (1 + o), 4)


def odds(p, ratios):
    """Bayes on the odds scale. The ratios must be independent: two reports of one source count as one."""
    if not 0 < p < 1: raise ValueError("the base rate must be between 0 and 1")
    o = p / (1 - p)
    steps = [{"start": round(p, 4)}]
    for r in ratios:
        if not (0 < r < math.inf): raise ValueError("a likelihood ratio must be a number above 0")
        o *= r
        steps.append({"lr": r, "p": _prob(o)})
    return {"p": _prob(o), "steps": steps}


class Ledger:
    """Predictions written down before the answer is known, then scored. The only honest test of a forecaster."""
    def __init__(self, path=None):
        self.path = Path(path or os.environ.get("RESEARCH_LEDGER") or Path.home() / ".cache" / "research" / "predictions.jsonl")

    def all(self):
        try: return [json.loads(l) for l in self.path.read_text().splitlines() if l.strip()]
        except OSError: return []

    def _write(self, rows):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_name(f"{self.path.name}.{os.getpid()}.tmp")
        tmp.write_text("".join(json.dumps(r) + "\n" for r in rows))
        os.replace(tmp, self.path)

    @contextmanager
    def _locked(self):
        """Agents share this file, and a lost row is a prediction that can never be scored."""
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with open(self.path.with_name(self.path.name + ".lock"), "w") as f:
            fcntl.flock(f, fcntl.LOCK_EX)
            yield

    def add(self, question, p, due="", base=None, at=""):
        if not 0 <= p <= 1: raise ValueError("a probability is between 0 and 1")
        with self._locked():
            rows = self.all()
            r = {"id": max((x["id"] for x in rows), default=0) + 1, "q": question, "p": p, "base": base, "due": due, "at": at, "outcome": None}
            self._write(rows + [r])
        return r

    def resolve(self, id_, outcome):
        with self._locked():
            rows = self.all()
            for r in rows:
                if r["id"] == id_:
                    if r["outcome"] is not None: raise ValueError(f"prediction {id_} is already resolved; the record stays as it was")
                    r["outcome"] = int(outcome)
                    self._write(rows)
                    return r
        raise ValueError(f"no prediction {id_}")

    def score(self):
        done = [r for r in self.all() if r["outcome"] is not None]
        res = {"open": sum(r["outcome"] is None for r in self.all()), "resolved": len(done)}
        if not done: return res
        brier = sum((r["p"] - r["outcome"]) ** 2 for r in done) / len(done)
        rate = sum(r["outcome"] for r in done) / len(done)
        ref = sum(((r["base"] if r["base"] is not None else rate) - r["outcome"]) ** 2 for r in done) / len(done)
        bins = {}
        for r in done:
            b = min(int(r["p"] * 5), 4)
            x = bins.setdefault(b, [0, 0, 0.0])
            x[0] += 1; x[1] += r["outcome"]; x[2] += r["p"]
        res.update(brier=round(brier, 4), reference_brier=round(ref, 4), skill=round(1 - brier / ref, 3) if ref else None, calibration=[{"bin": f"{b * 20}-{b * 20 + 20}%", "n": x[0], "said": round(x[2] / x[0], 3), "happened": round(x[1] / x[0], 3)} for b, x in sorted(bins.items())])
        if len(done) < 30: res["warning"] = f"only {len(done)} resolved: too few to judge calibration"
        return res


def auc(scores, ys):
    pos = [s for s, y in zip(scores, ys) if y]
    neg = [s for s, y in zip(scores, ys) if not y]
    if not pos or not neg: return None
    return sum((p > n) + 0.5 * (p == n) for p in pos for n in neg) / (len(pos) * len(neg))


def logistic(X, y, l2=1.0, steps=800, lr=0.2):
    d = len(X[0])
    w, b = [0.0] * d, 0.0
    n = len(X)
    for _ in range(steps):
        gw, gb = [0.0] * d, 0.0
        for xi, yi in zip(X, y):
            p = 1 / (1 + math.exp(-max(-30, min(30, b + sum(a * c for a, c in zip(w, xi))))))
            for j in range(d): gw[j] += (p - yi) * xi[j]
            gb += p - yi
        for j in range(d): w[j] -= lr * (gw[j] + l2 * w[j]) / n
        b -= lr * gb / n
    return w, b


_NOT_VERIFIED = {"secondary", "partial", "not-found", "not-a-person", "blocked", "refused", "no-source"}  # the check's own statuses


def _num(v, where=""):
    v = (v or "").strip().lower()
    if v in ("yes", "true", "y", "verified"): return 1.0
    if v in ("no", "false", "n", "") or v in _NOT_VERIFIED: return 0.0
    try: x = float(v.replace(",", "").rstrip("%"))
    except ValueError: x = math.nan
    if not math.isfinite(x): raise ValueError(f"{where}{v!r} isn't a number or yes/no: recode that column first")
    return x


def _outcome(v, where):
    x = _num(v, where)
    if x not in (0.0, 1.0): raise ValueError(f"{where}{v!r}: an outcome has to be yes/no, true/false or 1/0")
    return x


def _scaler(X, idx):
    """Mean and spread from these rows only: a test row mustn't help set the scale it's judged on."""
    cols = list(zip(*(X[i] for i in idx)))
    means, sds = [sum(c) / len(c) for c in cols], [st.pstdev(c) or 1.0 for c in cols]
    return lambda x: [(v - m) / s for v, m, s in zip(x, means, sds)]


MIN_EACH = 10


def _auc_low(a, pos, neg):
    """Lower end of a one-sided 95% bound on an AUC (Hanley and McNeil 1982)."""
    q1, q2 = a / (2 - a), 2 * a * a / (1 + a)
    var = (a * (1 - a) + (pos - 1) * (q1 - a * a) + (neg - 1) * (q2 - a * a)) / (pos * neg)
    return a - 1.645 * math.sqrt(max(var, 0.0))


def rank(rows, features, outcome=None, weights=None, seed=1):
    """Order rows. With a column of past outcomes: fit a small regularised logistic model and report cross-validated AUC, so the ranking says whether it beats chance. Without: the weights you give, which are your opinion and are labelled as that."""
    missing = [f for f in features if rows and f not in rows[0]]
    if missing: raise ValueError(f"no column {', '.join(missing)}. Columns: {', '.join(rows[0])}")
    X = [[_num(r.get(f), f"column {f}, row {i + 1}: ") for f in features] for i, r in enumerate(rows)]
    Z = [_scaler(X, range(len(X)))(x) for x in X]
    info = {"rows": len(rows)}
    if outcome:
        known = [i for i, r in enumerate(rows) if (r.get(outcome) or "").strip() != ""]
        if rows and outcome not in rows[0]: raise ValueError(f"no column {outcome}. Columns: {', '.join(rows[0])}")
        y = [_outcome(rows[i].get(outcome), f"column {outcome}, row {i + 1}: ") for i in known]
        pos = int(sum(y))
        if pos < MIN_EACH or len(y) - pos < MIN_EACH: raise ValueError(f"{pos} good and {len(y) - pos} bad outcomes: too few to fit. Need at least {MIN_EACH} of each; use --weights and call it opinion")
        order = list(range(len(known))); random.Random(seed).shuffle(order)
        folds, held, truth = 5, [], []
        for f in range(folds):
            test = set(order[f::folds])
            tr = [k for k in range(len(known)) if k not in test]
            scale = _scaler(X, [known[k] for k in tr])
            w, b = logistic([scale(X[known[k]]) for k in tr], [y[k] for k in tr])
            for k in test:
                held.append(b + sum(a * c for a, c in zip(w, scale(X[known[k]]))))
                truth.append(y[k])
        w, b = logistic([Z[i] for i in known], y)
        a = auc(held, truth)
        low = _auc_low(a, pos, len(y) - pos)
        info.update(model="logistic", cv_auc=round(a, 3), cv_auc_low95=round(low, 3), outcomes=len(y), weights={f: round(v, 3) for f, v in zip(features, w)})
        if a < 0.4: info["warning"] = f"held-out AUC {a:.2f}: this order runs backwards on rows it didn't see, which with this few rows is usually noise. Don't trust it"
        elif low <= 0.5: info["warning"] = f"held-out AUC {a:.2f}, but with {len(y)} outcomes its low end is {low:.2f}: this can't be told from chance. Don't trust this order"
        info["validation_note"] = "Choose features before inspecting outcomes; cross-validation does not correct feature selection or repeated subjects."
        signatures = [tuple(X[i]) + (y[k],) for k, i in enumerate(known)]
        if len(set(signatures)) != len(signatures):
            info["warning"] = (info.get("warning", "") + "; repeated feature/outcome rows can leak between folds: do not trust this AUC without grouping independent subjects").lstrip("; ")
        score = lambda z: 1 / (1 + math.exp(-(b + sum(p * q for p, q in zip(w, z)))))
    else:
        w = [(weights or {}).get(f, 1.0) for f in features]
        info.update(model="your weights (an opinion, untested)", weights=dict(zip(features, w)))
        score = lambda z: sum(p * q for p, q in zip(w, z))
    ranked = sorted(zip(rows, Z), key=lambda t: -score(t[1]))
    return [dict(r, score=round(score(z), 4)) for r, z in ranked], info


def read_series(path, column=None, said=None):
    """Numbers from a CSV column (the last numeric one by default), or one per line. `said`, a dict, is told which column was used and what else could have been."""
    text = Path(path).read_text(encoding="utf-8-sig") if path != "-" else __import__("sys").stdin.read()
    rows = list(csv.reader(text.splitlines()))
    rows = [r for r in rows if r]
    if not rows: raise ValueError("no data")
    def isnum(v):
        try: return math.isfinite(float(v.replace(",", "")))
        except ValueError: return False
    head = None if any(isnum(c) for c in rows[0]) else rows[0]
    body = rows[1:] if head else rows
    if column:
        if not head or column not in head: raise ValueError(f"no column {column}")
        i = head.index(column)
    else:
        i = next((j for j in reversed(range(len(body[0]))) if all(isnum(r[j]) for r in body if len(r) > j and r[j])), None)
        if i is None: raise ValueError("no numeric column: nan and inf don't count as numbers")
        others = [j for j in range(len(body[0])) if j != i and all(isnum(r[j]) for r in body if len(r) > j and r[j])]
        if said is not None and others:
            names = [head[j] if head and j < len(head) else f"column {j + 1}" for j in others]
            said["note"] = f"no column named: used the last numeric one, {head[i] if head else f'column {i + 1}'}. Others are {', '.join(names)}: choose with --column"
    if said is not None and head: said["column"] = head[i]
    out = []
    for n, r in enumerate(body):
        if len(r) > i and r[i].strip():
            if not isnum(r[i]): raise ValueError(f"row {n + 1}: {r[i]!r} isn't a number")
            out.append(float(r[i].replace(",", "")))
    return out
