import re,sys,os,collections
# duplicate top-level declarations inside each Go package directory (test files are separate: a
# package's test files share names with each other, and with the package itself)
decl=re.compile(r'^(?:func\s+(?:\((\w+)\s+\*?(\w+)(?:\[[^\]]*\])?\)\s+)?(\w+)|type\s+(\w+)|(?:var|const)\s+(\w+))',re.M)
blk=re.compile(r'^(?:var|const)\s*\((.*?)^\)',re.M|re.S)
bad=0
for root,_,files in os.walk('.'):
    if '/bin' in root or '.git' in root: continue
    gos=[f for f in files if f.endswith('.go')]
    if not gos: continue
    seen=collections.defaultdict(list)
    for f in sorted(gos):
        s=open(os.path.join(root,f)).read()
        s=re.sub(r'//[^\n]*','',s)
        build=re.search(r'^//go:build (.*)$',open(os.path.join(root,f)).read(),re.M)
        tag=build.group(1) if build else ''
        names=[]
        for m in decl.finditer(s):
            recv,typ,fn,ty,v=m.groups()
            if fn and not recv: names.append(('func',fn))
            elif fn and recv: names.append(('method',typ+'.'+fn))
            elif ty: names.append(('type',ty))
            elif v: names.append(('var',v))
        for b in blk.finditer(s):
            for line in b.group(1).splitlines():
                mm=re.match(r'\t(\w+)\b',line)
                if mm and mm.group(1)!='_': names.append(('var',mm.group(1)))
        for kind,name in set(names):
            seen[(kind if kind=='method' else 'ident',name)].append((f,tag))
    for (k,name),where in seen.items():
        if name=='init' or name=='main' or name=='_': continue
        if len(where)>1:
            tags={t for _,t in where}
            if len(tags)>1 and '' not in tags: continue   # excluded from each other by build tags
            print(f"{root}: {name} declared in {', '.join(f for f,_ in where)}"); bad+=1
print("duplicate declarations:",bad)
