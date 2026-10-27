package lua

import (
	"errors"
	"fmt"
)

type GoFunction func(s *State, args []Value) ([]Value, error)

type Math struct {
	Sin, Cos, Tan, Asin, Acos, Atan func(float64) float64
	Atan2                           func(y, x float64) float64
}

type Options struct {
	Hook      func() error
	HookEvery int
	MaxString int
	Memory    *Budget
	Math      *Math
}

type function struct {
	proto    *Proto
	native   GoFunction
	name     string
	env      *Table
	upvalues []*upvalue
}

type upvalue struct {
	thread *thread
	index  int
	value  Value
	next   *upvalue
}

func (u *upvalue) get() Value {
	if u.thread != nil {
		return u.thread.stack[u.index]
	}
	return u.value
}

func (u *upvalue) set(value Value) {
	if u.thread != nil {
		u.thread.stack[u.index] = value
	} else {
		u.value = value
	}
}

type frame struct {
	function                          *function
	site                              *instruction
	start, base, pc, returnDest, want int32
	metacallTop                       int32
	tailcalls                         int64
}

type thread struct {
	object        object
	stack         []Value
	frames        []frame
	used          int
	top           int
	open          *upvalue
	status        string
	entry         Value
	started       bool
	nativeDepth   int
	nativeRoots   []nativeRoot
	hookRoots     []Value
	cDepth        int
	boundary      int
	resumeDest    int
	resumeWant    int
	resumeReturn  bool
	parent        *thread
	tail          []Value
	handler       Value
	handlerID     uint64
	handlerActive bool
	handlerRoots  []Value
	callLimit     int
}

const (
	freshCalls = 16384
	maxCalls   = 20000
)

func (s *State) growFrames() {
	t := s.current
	if t.callLimit > maxCalls {
		panic(s.handlerError())
	}
	t.callLimit *= 2
	if t.callLimit > maxCalls {
		s.runtimeError("stack overflow")
	}
}

func (t *thread) restoreCallLimit(depth int) {
	if t.callLimit > maxCalls && depth+1 < maxCalls {
		t.callLimit = maxCalls
	}
}

type State struct {
	binaryCache [6]binaryCache
	options     Options
	globals     *Table
	stringMeta  *Table
	main        *thread
	current     *thread
	threads     []*thread
	nextID      uint64
	steps       int
	running     int
	randomState uint64
	handlers    *handlerOwner
	hostRoots   map[uint64]Value
	rootID      uint64
}

func NewState(options Options) *State {
	if options.HookEvery <= 0 {
		options.HookEvery = 10000
	}
	if options.MaxString <= 0 {
		options.MaxString = 512 << 20
	}
	s := &State{options: options}
	s.main = &thread{status: "running", callLimit: maxCalls}
	s.main.object.thread = s.main
	s.current = s.main
	s.globals = s.NewTable(0, 32)
	s.stringMeta = s.NewTable(0, 1)
	s.stringMeta.set(String("__index"), s.NewTable(0, 0).Value())
	s.stringMeta.readonly = true
	s.openBase()
	s.openCoroutine()
	s.openMath()
	s.openTable()
	s.openString()
	return s
}

func (s *State) Globals() *Table { return s.globals }

func (s *State) SetGlobals(globals *Table) { s.globals = globals }

func (s *State) NewTable(arraySize, hashSize int) *Table {
	s.reserve(192)
	s.nextID++
	t := &Table{}
	if s.running > 0 {
		t.memory = s.options.Memory
	}
	t.object = object{kind: TypeTable, id: s.nextID, table: t}
	t.resize(arraySize, hashSize)
	return t
}

func (s *State) Function(name string, native GoFunction) Value {
	s.nextID++
	s.reserve(128)
	return Value{object: &object{kind: TypeFunction, id: s.nextID, function: &function{native: native, name: name, env: s.globals}}}
}

func (s *State) Userdata(data any) Value {
	s.nextID++
	return Value{object: &object{kind: TypeUserdata, id: s.nextID, data: data}}
}

func (v Value) Userdata() any {
	if v.Type() == TypeUserdata {
		return v.object.data
	}
	return nil
}

func (s *State) Load(proto *Proto, env *Table) Value {
	if env == nil {
		env = s.globals
	}
	return s.closure(proto, env, nil)
}

func (s *State) closure(proto *Proto, env *Table, upvalues []*upvalue) Value {
	s.nextID++
	s.reserve(128 + int64(len(upvalues))*8)
	return Value{object: &object{kind: TypeFunction, id: s.nextID, function: &function{proto: proto, env: env, upvalues: upvalues}}}
}

func (s *State) Call(fn Value, args ...Value) (values []Value, err error) {
	return s.call(fn, args, Nil(), false)
}

func (s *State) Index(value, key Value) (Value, error) {
	values, err := s.Call(s.Function("index", func(s *State, args []Value) ([]Value, error) {
		return []Value{s.get(args[0], args[1], nameInfo{})}, nil
	}), value, key)
	if err != nil {
		return Nil(), err
	}
	return values[0], nil
}

func (s *State) call(fn Value, args []Value, handler Value, active bool) (values []Value, err error) {
	outer := s.running == 0
	s.running++
	if outer {
		s.steps = 0
	}
	defer func() {
		s.running--
		if outer {
			s.cleanup()
		}
	}()
	if !outer {
		s.current.boundary++
		defer func() { s.current.boundary-- }()
	}
	values, err = s.protectedHandler(fn, args, handler, active)
	if fn.Type() != TypeFunction || fn.object.function.proto == nil {
		values = append([]Value(nil), values...)
	}
	return values, err
}

func (s *State) protected(fn Value, args []Value) (values []Value, err error) {
	return s.protectedCall(fn, args, true)
}

func (s *State) protectedCall(fn Value, args []Value, enter bool) (values []Value, err error) {
	t := s.current
	cDepth := t.cDepth
	depth, used, top, nativeDepth, boundary := len(t.frames), t.used, t.top, t.nativeDepth, t.boundary
	defer func() {
		if raised := recover(); raised != nil {
			raised = s.processPanic(raised)
			failure, ok := raised.(*Error)
			if !ok {
				cause := fmt.Errorf("internal Lua error: %v", raised)
				failure = &Error{Message: cause.Error(), Value: String(cause.Error()), Abort: cause, Internal: true}
			}
			if failure.Line == 0 {
				failure.Line = s.currentLine()
			}
			if failure.cDepth == 0 {
				failure.cDepth = t.cDepth
			}
			err = failure
		}
		t.cDepth = cDepth
		if err != nil {
			t.close(used)
			clear(t.stack[used:t.used])
			t.frames = t.frames[:depth]
			t.used, t.top, t.nativeDepth, t.boundary = used, top, nativeDepth, boundary
			t.restoreCallLimit(depth)
		}
	}()
	if enter {
		t.cDepth++
		if t.cDepth == 200 {
			s.runtimeError("C stack overflow")
		}
		if t.cDepth >= 225 {
			panic(plainError("error in error handling"))
		}
	}
	if fn.Type() != TypeFunction {
		fn, args = s.callable(fn, args, nameInfo{})
	}
	if fn.object.function.native != nil {
		return s.native(fn.object.function, args, nil), nil
	}
	s.push(fn.object.function, args, -1, -1, nil)
	return s.execute(depth), nil
}

func (s *State) invoke(fn Value, args []Value) []Value {
	s.current.boundary++
	defer func() { s.current.boundary-- }()
	values, err := s.protected(fn, args)
	if err != nil {
		panic(err)
	}
	return values
}

func (s *State) invokeOne(fn Value, args []Value) Value {
	t := s.current
	cDepth := t.cDepth
	t.cDepth++
	t.boundary++
	defer func() { t.cDepth = cDepth; t.boundary-- }()
	if t.cDepth == 200 {
		s.runtimeError("C stack overflow")
	}
	if t.cDepth >= 225 {
		panic(plainError("error in error handling"))
	}
	if fn.Type() != TypeFunction {
		fn, args = s.callable(fn, args, nameInfo{})
	}
	if fn.object.function.native != nil {
		return first(s.native(fn.object.function, args, nil))
	}
	depth, used, top := len(t.frames), t.used, t.top
	dest := max(used, top)
	s.push(fn.object.function, args, dest, 1, nil)
	s.execute(depth)
	value := t.stack[dest]
	t.stack[dest] = Nil()
	t.used, t.top = used, top
	return value
}

func (s *State) cleanup() {
	clear(s.binaryCache[:])
	for _, t := range s.threads {
		t.close(0)
		clear(t.stack)
		clear(t.frames[:cap(t.frames)])
		clear(t.tail)
		t.tail = t.tail[:0]
		t.entry = Nil()
		t.frames = t.frames[:0]
		t.used, t.top, t.nativeDepth, t.boundary = 0, 0, 0, 0
		t.cDepth = 0
		t.status = "dead"
		t.parent = nil
		t.handler, t.handlerID, t.handlerActive = Nil(), 0, false
	}
	s.threads = s.threads[:0]
	s.current = s.main
	s.main.close(0)
	clear(s.main.stack)
	clear(s.main.frames[:cap(s.main.frames)])
	clear(s.main.tail)
	s.main.tail = s.main.tail[:0]
	s.main.frames = s.main.frames[:0]
	s.main.used, s.main.top, s.main.nativeDepth, s.main.boundary = 0, 0, 0, 0
	s.main.cDepth = 0
	s.main.handler, s.main.handlerID, s.main.handlerActive = Nil(), 0, false
}

func (t *thread) ensure(size int) {
	if size > len(t.stack) {
		t.growStack(size)
	}
}

type nativeRoot struct {
	frame  int
	values []Value
}

func (t *thread) growStack(size int) {
	for index := range t.frames {
		f := &t.frames[index]
		if f.function.proto == nil && f.pc == 0 && f.base > f.start {
			t.nativeRoots = append(t.nativeRoots, nativeRoot{frame: index, values: t.stack[f.start:f.base]})
			f.pc = 1
		}
	}
	t.stack = append(t.stack, make([]Value, size-len(t.stack))...)
}

func (t *thread) capture(index int) *upvalue {
	link := &t.open
	for *link != nil && (*link).index > index {
		link = &(*link).next
	}
	if *link != nil && (*link).index == index {
		return *link
	}
	current := &upvalue{thread: t, index: index, next: *link}
	*link = current
	return current
}

func (t *thread) close(base int) {
	for t.open != nil && t.open.index >= base {
		current := t.open
		t.open = current.next
		current.value = t.stack[current.index]
		current.thread = nil
		current.next = nil
	}
}

func (s *State) currentLine() int {
	for i := len(s.current.frames) - 1; i >= 0; i-- {
		if s.current.frames[i].function.proto != nil {
			return s.current.frames[i].sourceLine()
		}
	}
	return 0
}

func (s *State) raise(message string, level int) {
	t := s.current
	err := plainError(message)
	if frame, ok := s.Frame(level); ok && (frame.What == "main" || frame.What == "Lua") {
		err = locatedError(frame.Source, frame.Line, message)
	}
	err.Line = s.currentLine()
	err.cDepth = t.cDepth
	panic(s.processPanic(err))
}

func (s *State) runtimeError(message string) { s.raise(message, 0) }

func (s *State) typeError(action string, value Value, info nameInfo) {
	info = s.resolveName(info)
	if info.name != "" {
		s.runtimeError(fmt.Sprintf("attempt to %s %s '%s' (a %s value)", action, info.kind, info.name, value.Type()))
	}
	s.runtimeError(fmt.Sprintf("attempt to %s a %s value", action, value.Type()))
}

func (s *State) tick() {
	s.steps++
	if s.steps < s.options.HookEvery {
		return
	}
	s.pollHook()
}

func (s *State) pollHook() {
	s.pollHookRoots(nil)
}

func (s *State) pollBinaryHook(method, left, right Value) {
	t := s.current
	base := len(t.hookRoots)
	t.hookRoots = append(t.hookRoots, method, left, right)
	defer func() {
		clear(t.hookRoots[base:])
		t.hookRoots = t.hookRoots[:base]
	}()
	s.pollHook()
}

func (s *State) pollPreparedHook(frameIndex, pc, register int, fn, arg Value) {
	t := s.current
	f := &t.frames[frameIndex]
	base := int(f.base) + register
	t.stack[base], t.stack[base+1] = fn, arg
	f.pc = int32(pc)
	s.pollHookRoots(nil)
}

func (s *State) pollHookRoots(roots []Value) {
	s.steps = 0
	if s.options.Hook != nil {
		t := s.current
		base := len(t.hookRoots)
		t.hookRoots = append(t.hookRoots, roots...)
		t.hookRoots = append(t.hookRoots, t.pendingCall()...)
		defer func() {
			clear(t.hookRoots[base:])
			t.hookRoots = t.hookRoots[:base]
		}()
		if err := s.options.Hook(); err != nil {
			panic(&Error{Message: err.Error(), Value: String(err.Error()), Line: s.currentLine(), Abort: err})
		}
	}
}

func (s *State) reserve(size int64) {
	if s.running > 0 {
		s.options.Memory.reserve(size)
	}
}

func (s *State) checkString(size int) {
	if size < 0 || size > s.options.MaxString {
		s.runtimeError("string length overflow")
	}
	s.options.Memory.reserve(int64(size))
}

func abortError(err error) bool {
	var failure *Error
	return errors.As(err, &failure) && (failure.Abort != nil || failure.Internal)
}
