import json,pathlib

pathlib.Path('work').mkdir(exist_ok=True)
cases=[]
for name in ['exp','log','log10','pow','sinh','cosh','tanh']:
    for chunk in range(100):
        start=chunk*1000
        init=f'local state = {((chunk+1)*2654435761)%4294967296} local out = {{}} '
        loop=f'for i = {start}, {start+999} do state = (1664525 * state + 1013904223) % 4294967296 local u = state / 4294967296 '
        if name in ['exp','sinh','cosh']:
            expression='local x = (u - 0.5) * 1500 '
            expression+='if i % 4 == 1 then x = (u - 0.5) * 4 elseif i % 4 == 2 then x = math.ldexp(u - 0.5, (i % 1100) - 1075) end '
        elif name=='tanh':
            expression='local x = (u - 0.5) * 80 if i % 3 == 1 then x = (u - 0.5) * 4 elseif i % 3 == 2 then x = math.ldexp(u - 0.5, (i % 1100) - 1075) end '
        elif name in ['log','log10']:
            expression='local x = math.ldexp(0.5 + u * 0.5, i % 2099 - 1074) if i % 4 == 0 then x = 1 + (u - 0.5) * 0.15 elseif i % 4 == 1 then x = u * 10000 + 0.00001 end '
        else:
            expression='state = (1664525 * state + 1013904223) % 4294967296 local v = state / 4294967296 local x = 0.01 + 10 * u local y = (v - 0.5) * 200 '
            expression+='if i % 4 == 1 then x = math.ldexp(0.5 + u * 0.5, i % 2099 - 1074) y = (v - 0.5) * 4 elseif i % 4 == 2 then x = 1 + (u - 0.5) * 0.000001 y = (v - 0.5) * 2000000000 elseif i % 4 == 3 then x = -x y = math.floor(y) end '
        output=f'out[#out + 1] = string.format("%.17g", math.{name}(x'+(', y' if name=='pow' else '')+')) '
        if name=='pow': output+='out[#out + 1] = string.format("%.17g", x ^ y) '
        cases.append({'src':init+loop+expression+output+'end return table.concat(out, ",")'})
pathlib.Path('work/a2-math-grid-input.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2))
print(len(cases),'snippets; 100000 inputs per function and operator ^')
