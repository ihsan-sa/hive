import json,sys,collections
last={};merged=set();prs={}
for l in open(sys.argv[1]):
    e=json.loads(l);d=e['data']
    if e['kind']=='task.saved': last[d['id']]=d
for i,d in last.items():
    pass
c=collections.Counter()
for d in last.values():
    fu=d.get('title','').startswith('Follow up the review')
    st=d.get('stage');haspr=bool(d.get('pr'))
    c[(fu,st,haspr)]+=1
for k,v in sorted(c.items()): print(v,k)
