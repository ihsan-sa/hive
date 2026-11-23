#!/usr/bin/env python3
"""A stand-in for link-cli in test_buy.py: logs its argv, answers like link-cli --format json (a list), writes a fake card file."""
import json, os, sys
d = os.environ["FAKE_LINK_DIR"]; a = sys.argv[1:]
open(os.path.join(d, "argv.jsonl"), "a").write(json.dumps(a) + "\n")
def opt(k): return a[a.index(k) + 1] if k in a else None
st = lambda: open(os.path.join(d, "status")).read().strip() if os.path.exists(os.path.join(d, "status")) else "pending_approval"
if a[:2] == ["auth", "status"]: print(json.dumps([{"authenticated": True}]))
elif a[:2] == ["spend-request", "create"]:
    if int(opt("--amount")) > 50000: print(json.dumps({"code": "INVALID_INPUT", "message": "too much"})); sys.exit(1)
    print(json.dumps([{"id": "lsrq_test" + str(len(open(os.path.join(d, "argv.jsonl")).readlines())), "status": "pending_approval",
                       "approval_url": "https://app.link.com/approve/x", "expires_at": "2099-01-01T00:00:00Z"}]))
elif a[:2] == ["spend-request", "retrieve"]:
    pt = os.path.join(d, "poll_timeouts")  # answer POLLING_TIMEOUT this many times first, as the real one does after --timeout
    left = int(open(pt).read() or 0) if os.path.exists(pt) else 0
    if left > 0:
        open(pt, "w").write(str(left - 1))
        print(json.dumps({"code": "POLLING_TIMEOUT", "message": "Timed out after 600s waiting for a status change"})); sys.exit(1)
    s = st(); out = {"id": a[2], "status": s}
    if s == "requires_action": out["status_details"] = {"requires_action": {"next_action": {"action_url": "https://app.link.com/verify/x"}}}
    if s == "approved" and opt("--output-file"):
        f = opt("--output-file")
        if os.path.exists(f): print(json.dumps({"code": "OUTPUT_FILE_EXISTS", "message": "exists"})); sys.exit(1)
        fd = os.open(f, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.write(fd, json.dumps({"card": {"number": "4242424242424242", "cvc": "123", "exp_month": 12, "exp_year": 2030}}).encode()); os.close(fd)
        # the real link-cli leaves card out of stdout when it writes the file
    print(json.dumps([out]))
elif a[:2] == ["spend-request", "cancel"]:
    if os.path.exists(os.path.join(d, "cancel_fail")): print(json.dumps({"code": "INVALID_STATE", "message": "cannot cancel"})); sys.exit(1)
    print(json.dumps([{"id": a[2], "status": "canceled"}]))
else: print(json.dumps({"code": "UNKNOWN", "message": "fake: " + " ".join(a)})); sys.exit(1)
