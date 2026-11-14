import sys,re
def strip(s):
    out=[];i=0;n=len(s)
    while i<n:
        c=s[i]
        if s.startswith("//",i):
            j=s.find("\n",i); i=n if j<0 else j; continue
        if s.startswith("/*",i):
            j=s.find("*/",i+2); i=n if j<0 else j+2; continue
        if c=='`':
            j=s.find('`',i+1); i=n if j<0 else j+1; out.append('""'); continue
        if c=='"':
            i+=1
            while i<n and s[i]!='"':
                i+= 2 if s[i]=='\\' else 1
            i+=1; out.append('""'); continue
        if c=="'":
            i+=1
            while i<n and s[i]!="'":
                i+= 2 if s[i]=='\\' else 1
            i+=1; out.append("'x'"); continue
        out.append(c); i+=1
    return "".join(out)
bad=0
for f in sys.argv[1:]:
    t=strip(open(f).read()); st=[]; line=1
    pairs={')':'(',']':'[','}':'{'}
    for ch in t:
        if ch=="\n": line+=1
        if ch in "([{": st.append((ch,line))
        elif ch in ")]}":
            if not st or st[-1][0]!=pairs[ch]:
                print(f"{f}:{line}: unbalanced {ch}"); bad+=1; break
            st.pop()
    else:
        if st: print(f"{f}: unclosed {st[-1]}"); bad+=1
print("files:",len(sys.argv)-1,"problems:",bad)
