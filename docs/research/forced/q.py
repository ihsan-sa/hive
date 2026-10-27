import json,sys,collections,re
from datetime import datetime
def t(s): return datetime.fromisoformat(s[:26].rstrip('Z'))
last={};acc=collections.Counter()
def cat(r):
    if 'touch' in r: return 'file overlap with another brief'
    if 'held by' in r: return 'held by an agent'
    if 'pace' in r or 'max_workers' in r: return 'worker pace / max_workers'
    if 'machine' in r: return 'machine strain'
    if 'person' in r: return 'person'
    return 'other: '+r[:40]
for l in open(sys.argv[1]):
    e=json.loads(l)
    if e['kind']!='task.saved': continue
    d=e['data'];i=d['id'];now=t(e['at'])
    if i in last:
        st,r,ta=last[i]
        if st=='queued': acc[cat(r)]+=(now-ta).total_seconds()/3600
    last[i]=(d.get('stage'),d.get('reason') or '',now)
tot=sum(acc.values())
for k,v in acc.most_common(12): print(f"{v:8.0f} task-hours {100*v/tot:5.1f}%  {k}")
