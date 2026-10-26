import json,sys,collections
from datetime import datetime
def t(s): return datetime.fromisoformat(s[:26].rstrip('Z')) 
tasks={}
for l in open(sys.argv[1]):
    e=json.loads(l)
    if e['kind']!='task.saved': continue
    d=e['data'];tk=tasks.setdefault(d['id'],{'title':d.get('title',''),'seq':[], 'from':d.get('from',''),'aim':None,'kind':d.get('kind')})
    st=d.get('stage','');tk['aim']=d.get('aim') or tk['aim']
    if not tk['seq'] or tk['seq'][-1][0]!=st: tk['seq'].append((st,t(e['at'])))
tot=collections.defaultdict(list);fin=collections.Counter()
fu=sum(1 for k in tasks.values() if k['title'].startswith('Follow up the review'))
fl=sum(1 for k in tasks.values() if k['title'].startswith('Fix the flaky'))
print('tasks',len(tasks),'review follow-ups',fu,'flaky fixes',fl)
print('final stages',collections.Counter(k['seq'][-1][0] for k in tasks.values()).most_common())
print('aims',collections.Counter(k['aim'] for k in tasks.values()).most_common())
print('from',collections.Counter((k['from'] or '').split('-')[0] for k in tasks.values()).most_common(10))
for k in tasks.values():
    s=k['seq']
    if s[-1][0] not in ('merged','done'): continue
    per=collections.Counter()
    for (a,ta),(b,tb) in zip(s,s[1:]): per[a]+=(tb-ta).total_seconds()/60
    for a,v in per.items(): tot[a].append(v)
    fin['n']+=1
print('finished',fin['n'])
for a,v in sorted(tot.items(),key=lambda x:-sum(x[1])):
    v=sorted(v);print(f"{a:15} n {len(v):4} median {v[len(v)//2]:7.1f}m  mean {sum(v)/len(v):7.1f}m  p90 {v[int(len(v)*.9)]:7.1f}m")
