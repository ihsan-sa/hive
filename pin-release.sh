# Run as a Hive's own user before the rerun checks. A rig installs the test candidate (HIVE_BRANCH), which isn't built on the
# published release, so the rerun's `hive update` would swap it for that older release ("the release wins") and the checks after
# would test the release, not the candidate. Recording the newest published tag as installed puts the Hive where a real install of
# the newest release is: the rerun then finds nothing newer, which is the case the check is about.
set -e
t=$(git -C "$HOME/core" ls-remote --tags --refs origin 'v2.*' | sed 's#.*refs/tags/##' | grep -Ex 'v2\.[0-9]+\.[0-9]+' | sort -V | tail -n 1)
[ -n "$t" ]
mkdir -p "$HOME/.hive"  # where update.py keeps update.json in a Hive (STATE)
python3 -c "import json,sys,pathlib as P;p=P.Path.home()/'.hive/update.json';d=json.loads(p.read_text()) if p.exists() else {};d['tag']=sys.argv[1];p.write_text(json.dumps(d))" "$t"
echo "$t"
