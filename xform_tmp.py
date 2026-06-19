import re, sys

GOESC = {'n':'\n','t':'\t','r':'\r','"':'"','\\':'\\'}

def decode_gostr(body):
    out=[]; i=0
    while i<len(body):
        c=body[i]
        if c=='\\':
            nx=body[i+1]
            out.append(GOESC.get(nx, nx)); i+=2; continue
        out.append(c); i+=1
    return ''.join(out)

def encode_gostr(s):
    out=[]
    for c in s:
        if c=='\\': out.append('\\\\')
        elif c=='"': out.append('\\"')
        elif c=='\n': out.append('\\n')
        elif c=='\t': out.append('\\t')
        else: out.append(c)
    return ''.join(out)

def deindent(text):
    out=[]
    for line in text.split('\n'):
        if line.startswith('  '):
            out.append(line[2:])
        else:
            out.append(line)
    return '\n'.join(out)

strlit=re.compile(r'"((?:[^"\\]|\\.)*)"')

def process(fn):
    lines=open(fn).read().split('\n')
    out=[]
    raw=False
    prev_continues=False
    state={'in_cmd':False}
    flagged=[]
    for lineno,ln in enumerate(lines, 1):
        stripped=ln.rstrip()
        line_starts_in_raw=raw
        if not prev_continues:
            state['in_cmd']=False
        if not line_starts_in_raw:
            def repl(m):
                inner=m.group(1)
                dec=decode_gostr(inner)
                if 'command:\n' in dec:
                    idx=dec.index('command:\n')
                    before=dec[:idx]
                    after=dec[idx+len('command:\n'):]
                    ded=deindent(after)
                    state['in_cmd']=True
                    return '"'+encode_gostr(before+ded)+'"'
                elif state['in_cmd']:
                    if dec and not dec.startswith(' ') and not dec.startswith('\n'):
                        state['in_cmd']=False
                        return m.group(0)
                    return '"'+encode_gostr(deindent(dec))+'"'
                else:
                    return m.group(0)
            newln=strlit.sub(repl, ln)
            if state['in_cmd']:
                bare=strlit.sub('', newln)
                if re.search(r'\+\s*[A-Za-z_]', bare) or re.search(r'[A-Za-z_0-9\]\)]\s*\+\s*$', bare):
                    flagged.append((lineno, ln))
            out.append(newln)
        else:
            out.append(ln)
        bt=ln.count('`')
        if bt%2==1:
            raw=not raw
        prev_continues = stripped.endswith('+') and not raw
    open(fn,'w').write('\n'.join(out))
    return flagged

for fn in ['internal/validator_test.go','internal/generator_test.go']:
    fl=process(fn)
    print("==",fn,"flagged",len(fl))
    for n,l in fl:
        print("  ",n, l.strip()[:100])
