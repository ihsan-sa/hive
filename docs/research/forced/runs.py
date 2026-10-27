import json,sys,collections,re
roles={};names={};sysw={};created={}
cost=collections.Counter();turns=collections.Counter();dur=collections.Counter()
start={}
first=last=None
for l in open(sys.argv[1]):
    e=json.loads(l);k=e['kind'];d=e['data'];first=first or e['at'];last=e['at']
    if k=='run.created':
        r=d.get('role');roles[d['id']]=r;names[d['id']]=d.get('name','')
        sysw.setdefault(r,[]).append(len(d.get('system','').split()))
        bw=len(d.get('brief','').split()) if isinstance(d.get('brief'),str) else 0
        sysw.setdefault(r+':brief',[]).append(bw)
    elif k=='run.turn_ended':
        r=roles.get(d['id'],'?');turns[r]+=1
        cost[r]+=((d.get('result') or {}).get('cost_usd') or 0)
print(first,last)
c=collections.Counter(roles.values())
tot=sum(cost.values())
for r,n in c.most_common():
    s=sysw.get(r,[0]);b=sysw.get(r+':brief',[0])
    print(f"{r:18} runs {n:5} turns {turns[r]:5} cost ${cost[r]:9.0f} ({100*cost[r]/tot:4.1f}%) sys words med {sorted(s)[len(s)//2]:5} last {s[-1]:5} brief med {sorted(b)[len(b)//2]}")
print('total',tot)
# reviewer name kinds
nm=collections.Counter(re.sub(r'[-\d]+$','',names[i]) if names[i] else i.split('-')[0] for i in roles if roles[i]=='reviewer')
print(nm.most_common(10))
