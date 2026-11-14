import re,sys,glob,os
bad=0
for f in sys.argv[1:]:
    s=open(f).read()
    m=re.search(r'^import \((.*?)^\)',s,re.S|re.M)
    imps=[]
    if m:
        for line in m.group(1).splitlines():
            line=line.strip()
            if not line or line.startswith('//'): continue
            mm=re.match(r'(?:(\w+|_|\.)\s+)?"([^"]+)"',line)
            if mm: imps.append((mm.group(1) or mm.group(2).split('/')[-1],mm.group(2)))
    else:
        mm=re.search(r'^import (?:(\w+)\s+)?"([^"]+)"',s,re.M)
        if mm: imps.append((mm.group(1) or mm.group(2).split('/')[-1],mm.group(2)))
    body=s[m.end():] if m else s
    # strip strings and comments crudely
    body2=re.sub(r'//[^\n]*','',body)
    body2=re.sub(r'`[^`]*`','``',body2)
    body2=re.sub(r'"(?:\\.|[^"\\\n])*"','""',body2)
    for name,path in imps:
        if name in('_','.'): continue
        if not re.search(r'\b'+re.escape(name)+r'\.',body2):
            print(f"{f}: unused import {path}"); bad+=1
    known={n for n,_ in imps}
    for pkg in ['fmt','os','strings','errors','context','json','sort','sync','time','filepath','http','io','bufio','regexp','strconv','exec','url','net','hex','sha256','utf8','fs','atomic','bytes','flag','tabwriter','httptest','syscall']:
        if pkg in known: continue
        if re.search(r'(?<![\w.])'+pkg+r'\.[A-Z]',body2):
            # could be a local variable; report softly
            print(f"{f}: uses {pkg}. without import?"); bad+=1
print("checked",len(sys.argv)-1,"files, problems:",bad)
