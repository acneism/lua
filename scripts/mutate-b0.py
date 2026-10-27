import json
import shutil
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parent.parent
target = root / 'work' / 'b0-mutation-source'
evidence = root / 'work' / 'b0-mutation-evidence'
target.mkdir(parents=True, exist_ok=True)
evidence.mkdir(parents=True, exist_ok=True)
for name in subprocess.check_output(['git', 'ls-files'], cwd=root, text=True).splitlines():
    source = root / name
    if source.is_file():
        destination = target / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
originals = {p.name: p.read_text(encoding='utf-8') for p in target.glob('*.go')}


def replace(name, old, new, count=1):
    path = target / name
    text = path.read_text(encoding='utf-8')
    if text.count(old) != count:
        raise RuntimeError('mutation anchor changed: ' + name + ': ' + old)
    path.write_text(text.replace(old, new), encoding='utf-8', newline='\n')


def mutate(name):
    if name == 'handler-after-unwind':
        replace('state.go', 'panic(s.processPanic(err))', 'panic(err)')
        replace('vm.go', 'panic(s.processPanic(failure))', 'panic(failure)')
        replace('base.go', 'panic(s.processPanic(&Error{Value: value, Message: value.String(), Line: s.currentLine()}))', 'panic(&Error{Value: value, Message: value.String(), Line: s.currentLine()})')
        replace('state.go', '\t\t\traised = s.processPanic(raised)\n', '')
        anchor = '\t\t\tt.used, t.top, t.nativeDepth, t.boundary = used, top, nativeDepth, boundary\n'
        replace('state.go', anchor, anchor + '\t\t\terr = s.processPanic(err).(error)\n')
    elif name == 'unprotected-reader':
        path = target / 'base.go'
        text = path.read_text(encoding='utf-8')
        start = text.index('\n\tt := s.current\n', text.index('func (s *State) loadReader'))
        end = text.index('\n\tvar source strings.Builder', start)
        path.write_text(text[:start] + text[end:], encoding='utf-8', newline='\n')
    else:
        filename, old, new = edits[name]
        replace(filename, old, new)


edits = {
    'lost-tail-count': ('vm.go', 'tailcalls := f.tailcalls + 1', 'tailcalls := int64(0)'),
    'skip-native-frames': ('frame.go', '\t\tframe := &s.current.frames[i]\n', '\t\tframe := &s.current.frames[i]\n\t\tif frame.function.proto == nil { continue }\n'),
    'skip-weak-clearing': ('gc.go', 'for _, weak := range g.weak {', 'for _, weak := range []weakTable(nil) {'),
    'weak-strings': ('gc.go', 'func (g *collector) dead(value Value) bool {\n\tswitch value.Type() {', 'func (g *collector) dead(value Value) bool {\n\tswitch value.Type() {\n\tcase TypeString: return true'),
    'ephemeron-keys': ('gc.go', '\t\tif !values {\n\t\t\tg.value(node.value)', '\t\tif !values && !keys {\n\t\t\tg.value(node.value)'),
    'skip-host-roots': ('gc.go', 'for _, value := range s.hostRoots {', 'for _, value := range map[uint64]Value(nil) {'),
    'retain-dead-registers': ('gc.go', 'limit = int(f.base) + site.a + 1', 'limit = int(f.base) + p.maxRegisters'),
    'skip-native-arguments': ('gc.go', 'for _, value := range t.stack[f.start:f.base] {', 'for _, value := range []Value(nil) {'),
    'skip-native-relocation': ('state.go', 't.nativeRoots = append(t.nativeRoots, nativeRoot{frame: index, values: t.stack[f.start:f.base]})', ''),
    'skip-masked-handler-roots': ('gc.go', 'for _, handler := range t.handlerRoots {', 'for _, handler := range []Value(nil) {'),
    'untruncated-chunk': ('compiler.go', 'if end := strings.IndexByte(chunk, 0); end >= 0 {', 'if end := strings.IndexByte(chunk, 0); end < -1 {'),
    'omit-pending-hook-arguments': ('state.go', 't.hookRoots = append(t.hookRoots, t.pendingCall()...)', ''),
    'omit-prepared-hook-slots': ('state.go', 't.stack[base], t.stack[base+1] = fn, arg', '_ = base'),
    'omit-hook-root-tracing': ('gc.go', 'for _, value := range t.hookRoots {', 'for _, value := range []Value(nil) {'),
    'omit-metamethod-hook-snapshots': ('state.go', 't.hookRoots = append(t.hookRoots, method, left, right)', ''),
    'step-false-without-collection': ('base.go', 'case "step":\n\t\ts.collect()\n\t\treturn []Value{Bool(true)}, nil', 'case "step":\n\t\treturn []Value{Bool(false)}, nil'),
    'retain-pending-result': ('gc.go', 'if slot >= resultStart && slot < resultEnd && slot < limit-1 {', 'if false && slot >= resultStart && slot < resultEnd && slot < limit-1 {'),
    'alias-native-results': ('state.go', 'values = append([]Value(nil), values...)', ''),
}
cases = [
    ('handler-after-unwind', '^(TestXpcallHandlerSeesLiveFrames|TestHandlerSeesErrorPoint)$'),
    ('lost-tail-count', '^(TestFrameLevels|TestDirectHandlerTailFrame|TestCorpus)$/^(4186|4187|4193|4194)$'),
    ('skip-native-frames', '^(TestFrameGoCallsGo|TestHandlerSeesErrorPoint)$'),
    ('skip-weak-clearing', '^TestCorpus$/^4195$'),
    ('weak-strings', '^TestCorpus$/^4199$'),
    ('ephemeron-keys', '^TestCorpus$/^4200$'),
    ('skip-host-roots', '^TestWeakHostRoots$'),
    ('retain-dead-registers', '^TestCorpus$/^(4195|4196|4208|4209|4210)$'),
    ('skip-native-arguments', '^TestWeakNativeArguments$'),
    ('skip-native-relocation', '^TestWeakNativeArgumentsAfterGrowth$'),
    ('skip-masked-handler-roots', '^TestWeakMaskedHandlerRoot$'),
    ('unprotected-reader', '^(TestLoadReaderPreservesHandler|TestCorpus)$/^(4215|4216|4217|4218|4219|4222)$'),
    ('untruncated-chunk', '^(TestCompileTruncatesChunkName|TestHandlerSeesErrorPoint)$'),
    ('omit-pending-hook-arguments', '^TestWeakPendingHookArguments$'),
    ('omit-prepared-hook-slots', '^TestWeakPreparedHookCallee$'),
    ('omit-hook-root-tracing', '^TestWeakMetamethodHookRoots$'),
    ('omit-metamethod-hook-snapshots', '^TestWeakMetamethodHookRoots$'),
    ('step-false-without-collection', '^TestCorpus$/^(4229|4230)$'),
    ('retain-pending-result', '^TestCorpus$/^4230$'),
    ('alias-native-results', '^TestNativeResultsSurviveCleanup$'),
]
report = []
try:
    for name, tests in cases:
        for filename, text in originals.items():
            (target / filename).write_text(text, encoding='utf-8', newline='\n')
        mutate(name)
        result = subprocess.run(['go', 'test', '.', '-run', tests, '-count=1', '-timeout=2m'], cwd=target, capture_output=True, text=True)
        log = result.stdout + result.stderr
        (evidence / (name + '.log')).write_text(log, encoding='utf-8')
        failures = [line.strip() for line in result.stdout.splitlines() if '--- FAIL:' in line]
        report.append(dict(Mutation=name, Test=tests, ExitCode=result.returncode, Failures=failures))
        (evidence / 'mutations.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
        if result.returncode == 0 or not failures:
            raise RuntimeError('mutation did not fail a test: ' + name)
        if name == 'step-false-without-collection' and not all('TestCorpus/' + str(n) in log for n in [4229, 4230]):
            raise RuntimeError('both step probes must fail')
        print(name + ': ' + '; '.join(failures[:5]), flush=True)
finally:
    for filename, text in originals.items():
        (target / filename).write_text(text, encoding='utf-8', newline='\n')
