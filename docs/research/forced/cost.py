import json,sys,collections
roles={};prev={};cost=collections.Counter();turns=collections.Counter();n=collections.Counter()
since=sys.argv[2] if len(sys.argv)>2 else ''
for l in open(sys.argv[1]):
    e=json.loads(l);k=e['kind'];d=e['data']
    if k=='run.created':
        roles[d['id']]=d.get('role');
        if e['at']>=since: n[d.get('role')]+=1
    elif k=='run.turn_ended':
        c=((d.get('result') or {}).get('cost_usd') or 0);key=(d['id'],d.get('session'))
        delta=c-prev.get(key,0) if c>=prev.get(key,0) else c
        prev[key]=c
        if e['at']>=since:
            r=roles.get(d['id'],'?');cost[r]+=delta;turns[r]+=1
tot=sum(cost.values())
for r,c in cost.most_common(): print(f"{r:10} runs {n[r]:5} turns {turns[r]:5} ${c:8.0f} {100*c/tot:5.1f}%  ${c/max(turns[r],1):.2f}/turn")
print('total',round(tot))
