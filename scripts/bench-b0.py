import argparse
import json
import re
import statistics
import subprocess
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--main', required=True, type=Path)
parser.add_argument('--candidate', required=True, type=Path)
parser.add_argument('--before', required=True, type=Path)
parser.add_argument('--rounds', type=int, default=8)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
if args.rounds < 6:
    parser.error('at least six alternating rounds are required')
output = args.output.resolve()
output.mkdir(parents=True, exist_ok=True)
binaries = {name: getattr(args, name).resolve() for name in ['main', 'candidate', 'before']}
names = ['Loop', 'Fib27', 'LuaCalls', 'TableInsert', 'TableHash', 'Sort', 'GSub', 'StringConcat', 'Metamethod', 'ZZNative', 'ZZPcall', 'ZZIndexMeta', 'ZZCoroutine']


def invoke(version, name, flags, filename):
    command = [str(binaries[version]), '-test.run=^$', '-test.bench=^Benchmark' + name + '$', '-test.count=1', '-test.benchmem'] + flags
    result = subprocess.run(command, capture_output=True, text=True)
    (output / filename).write_text(result.stdout + result.stderr, encoding='utf-8')
    if result.returncode:
        raise RuntimeError('benchmark failed: ' + ' '.join(command))
    match = re.search(r'^Benchmark' + name + r'-\d+\s+(\d+)\s+([\d.e+]+)\s+ns/op\s+([\d.e+]+)\s+B/op\s+([\d.e+]+)\s+allocs/op', result.stdout, re.MULTILINE)
    if not match:
        raise RuntimeError('missing benchmark result: ' + filename)
    return dict(Iterations=int(match[1]), NS=float(match[2]), Bytes=float(match[3]), Allocs=float(match[4]))


results = []
for name in names:
    samples = []
    for round_index in range(args.rounds):
        order = ['main', 'candidate'] if round_index % 2 == 0 else ['candidate', 'main']
        sample = dict(Round=round_index + 1, Order=order)
        for version in order:
            sample[version] = invoke(version, name, ['-test.benchtime=3x'], f'{name}-{round_index + 1:02d}-{version}.txt')
        samples.append(sample)
    baseline = statistics.median(s['main']['NS'] for s in samples)
    candidate = statistics.median(s['candidate']['NS'] for s in samples)
    results.append(dict(Name=name, MainNS=baseline, B0NS=candidate, Ratio=candidate / baseline, Samples=samples))
    print(f'{name}: main={baseline:.0f} ns B0={candidate:.0f} ns ratio={candidate / baseline:.4f}', flush=True)
report = dict(Rounds=args.rounds, Benchtime='3x', Results=results)
(output / 'b0-performance.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
table = '| Scenario | main ms | B0 ms | B0/main |\n| --- | ---: | ---: | ---: |\n'
table += ''.join(f"| {r['Name']} | {r['MainNS'] / 1e6:.3f} | {r['B0NS'] / 1e6:.3f} | {r['Ratio']:.4f} |\n" for r in results)
(output / 'b0-performance.md').write_text(table, encoding='utf-8')
for version in binaries:
    for name in ['ZZNative', 'ZZCoroutine', 'ZZPcall', 'ZZIndexMeta']:
        profile = output / f'{version}-{name}.pprof'
        invoke(version, name, ['-test.benchtime=2s', '-test.cpuprofile=' + str(profile)], f'{version}-{name}-profile.txt')
        for label, flags in [('top', ['-top', '-nodecount=30']), ('lines', ['-list=execute|native|raise|processPanic|pollBinaryHook|pollPreparedHook'])]:
            result = subprocess.run(['go', 'tool', 'pprof'] + flags + [str(binaries[version]), str(profile)], capture_output=True, text=True)
            (output / f'{version}-{name}-{label}.txt').write_text(result.stdout + result.stderr, encoding='utf-8')
            if result.returncode:
                raise RuntimeError('profile decoding failed')
