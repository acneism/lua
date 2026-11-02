package redis

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/acneism/lua"
)

const (
	functionLoadTimeout = 500 * time.Millisecond
	functionOpcode      = 0xf5
	functionPreGAOpcode = 246
	rdbVersion          = 11
	nameRules           = "Library names can only contain letters, numbers, or underscores(_) and must be at least one character long"
)

var errFunctionLoadTimeout = errors.New("FUNCTION LOAD timeout")

var scriptFlagNames = []struct {
	flag Flags
	name string
}{{NoWrites, "no-writes"}, {AllowOOM, "allow-oom"}, {AllowStale, "allow-stale"}, {NoCluster, "no-cluster"}, {AllowCrossSlotKeys, "allow-cross-slot-keys"}}

type functionInfo struct {
	name           string
	description    string
	hasDescription bool
	flags          Flags
	library        *library
}

type library struct {
	name      string
	code      string
	proto     *lua.Proto
	functions []*functionInfo
}

type libraryContext struct {
	libraries map[string]*library
	functions map[string]*functionInfo
	order     []*library
}

func newLibraryContext() *libraryContext {
	return &libraryContext{libraries: make(map[string]*library), functions: make(map[string]*functionInfo)}
}

func (c *libraryContext) link(lib *library) {
	c.libraries[lib.name] = lib
	c.order = append(c.order, lib)
	for _, function := range lib.functions {
		c.functions[function.name] = function
	}
}

func (c *libraryContext) unlink(lib *library) {
	delete(c.libraries, lib.name)
	for index, item := range c.order {
		if item == lib {
			c.order = append(c.order[:index:index], c.order[index+1:]...)
			break
		}
	}
	for _, function := range lib.functions {
		delete(c.functions, function.name)
	}
}

type functionRegistry struct {
	mu      sync.RWMutex
	current *libraryContext
	free    []*session
}

type loadContext struct {
	functions []*functionInfo
	callbacks map[string]lua.Value
	deadline  time.Time
}

func validFunctionName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		if c := name[index]; !isAlphanumeric(c) && c != '_' {
			return false
		}
	}
	return true
}

func (s *session) registerFunction(_ *lua.State, args []lua.Value) ([]lua.Value, error) {
	load := s.loading
	if load == nil {
		return nil, s.failure("redis.register_function can only be called on FUNCTION LOAD command")
	}
	var function functionInfo
	var callback lua.Value
	var failure string
	switch len(args) {
	case 1:
		callback, failure = s.namedFunctionArguments(args[0], &function)
	case 2:
		switch {
		case args[0].Type() != lua.TypeString && args[0].Type() != lua.TypeNumber:
			failure = "first argument to redis.register_function must be a string"
		case args[1].Type() != lua.TypeFunction:
			failure = "second argument to redis.register_function must be a function"
		default:
			function.name, callback = args[0].String(), args[1]
		}
	default:
		failure = "wrong number of arguments to redis.register_function"
	}
	if failure != "" {
		return nil, s.failure(failure)
	}
	if !validFunctionName(function.name) {
		return nil, s.failure(nameRules)
	}
	if _, exists := load.callbacks[function.name]; exists {
		return nil, s.failure("Function already exists in the library")
	}
	load.functions = append(load.functions, &function)
	load.callbacks[function.name] = callback
	return nil, nil
}

func (s *session) namedFunctionArguments(value lua.Value, function *functionInfo) (lua.Value, string) {
	table := value.Table()
	if table == nil {
		return lua.Nil(), "calling redis.register_function with a single argument is only applicable to Lua table (representing named arguments)."
	}
	callback := lua.Nil()
	named := false
	for key, item, err := table.Next(lua.Nil()); !key.IsNil() && err == nil; key, item, err = table.Next(key) {
		if key.Type() != lua.TypeString && key.Type() != lua.TypeNumber {
			return lua.Nil(), "named argument key given to redis.register_function is not a string"
		}
		text := item.Type() == lua.TypeString || item.Type() == lua.TypeNumber
		switch name := cstring(key.String()); {
		case strings.EqualFold(name, "function_name"):
			if !text {
				return lua.Nil(), "function_name argument given to redis.register_function must be a string"
			}
			function.name, named = item.String(), true
		case strings.EqualFold(name, "description"):
			if !text {
				return lua.Nil(), "description argument given to redis.register_function must be a string"
			}
			function.description, function.hasDescription = item.String(), true
		case strings.EqualFold(name, "callback"):
			if item.Type() != lua.TypeFunction {
				return lua.Nil(), "callback argument given to redis.register_function must be a function"
			}
			callback = item
		case strings.EqualFold(name, "flags"):
			if item.Table() == nil {
				return lua.Nil(), "flags argument to redis.register_function must be a table representing function flags"
			}
			flags, ok := s.functionFlags(item)
			if !ok {
				return lua.Nil(), "unknown flag given"
			}
			function.flags = flags
		default:
			return lua.Nil(), "unknown argument given to redis.register_function"
		}
	}
	if !named {
		return lua.Nil(), "redis.register_function must get a function name argument"
	}
	if callback.IsNil() {
		return lua.Nil(), "redis.register_function must get a callback argument"
	}
	return callback, ""
}

func (s *session) functionFlags(value lua.Value) (Flags, bool) {
	var flags Flags
	for index := 1; ; index++ {
		item, err := s.state.Index(value, lua.Number(float64(index)))
		if err != nil {
			return 0, false
		}
		if item.IsNil() {
			return flags, true
		}
		if item.Type() != lua.TypeString && item.Type() != lua.TypeNumber {
			return 0, false
		}
		found := false
		for _, known := range scriptFlagNames {
			if strings.EqualFold(known.name, cstring(item.String())) {
				flags |= known.flag
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
}

func (s *session) setFunctionGlobals(index lua.Value) {
	s.functionMeta.SetReadOnly(false)
	_ = s.functionMeta.RawSet(lua.Intern("__index"), index)
	s.functionMeta.SetReadOnly(true)
}

func (s *session) runLibrary(lib *library, timeout time.Duration) (*loadContext, string) {
	load := &loadContext{callbacks: make(map[string]lua.Value)}
	if timeout > 0 {
		load.deadline = time.Now().Add(timeout)
	}
	s.loading = load
	s.active = &execution{resp: 2}
	s.setFunctionGlobals(s.loadAPI)
	_, err := s.state.Call(s.state.Load(lib.proto, s.globals))
	s.setFunctionGlobals(s.functionAPI)
	s.loading, s.active = nil, nil
	if err == nil {
		return load, ""
	}
	var failure *lua.Error
	if !errors.As(err, &failure) {
		return nil, "Error registering functions: ERR " + err.Error()
	}
	if errors.Is(failure.Abort, errFunctionLoadTimeout) {
		return nil, "Error registering functions: ERR FUNCTION LOAD timeout"
	}
	if failure.Value.Type() == lua.TypeString || failure.Value.Type() == lua.TypeNumber {
		return nil, "Error registering functions: ERR " + cstring(failure.Value.String())
	}
	message, found := s.field(failure.Value, "err")
	if !found {
		message = "(null)"
	}
	return nil, "Error registering functions: " + message
}

func parseLibraryMetadata(code string) (name, engine, body, failure string) {
	if !strings.HasPrefix(cstring(code), "#!") {
		return "", "", "", "Missing library metadata"
	}
	end := strings.IndexByte(cstring(code), '\n')
	if end < 0 {
		return "", "", "", "Invalid library metadata"
	}
	parts, ok := splitShebang(code[:end])
	if !ok || len(parts) == 0 {
		return "", "", "", "Invalid library metadata"
	}
	engine = strings.TrimPrefix(parts[0], parts[0][:min(2, len(parts[0]))])
	hasName := false
	for _, part := range parts[1:] {
		if len(part) >= 5 && strings.EqualFold(part[:5], "name=") {
			if hasName {
				return "", "", "", "Invalid metadata value, name argument was given multiple times"
			}
			name, hasName = part[5:], true
			continue
		}
		return "", "", "", "Invalid metadata value given: " + cstring(part)
	}
	if !hasName {
		return "", "", "", "Library name was not given"
	}
	return name, engine, code[end:], ""
}

func (e *Engine) createLibrary(code string, replace bool, context *libraryContext, timeout time.Duration) (string, string) {
	name, engine, body, failure := parseLibraryMetadata(code)
	if failure != "" {
		return "", failure
	}
	if !validFunctionName(name) {
		return "", nameRules
	}
	if !strings.EqualFold(engine, "lua") {
		return "", "Engine '" + engine + "' not found"
	}
	previous := context.libraries[name]
	if previous != nil && !replace {
		return "", "Library '" + name + "' already exists"
	}
	if previous != nil {
		context.unlink(previous)
	}
	restore := func() {
		if previous != nil {
			context.link(previous)
		}
	}
	proto, err := lua.Compile([]byte(body), "@user_function")
	if err != nil {
		restore()
		return "", "Error compiling function: " + err.Error()
	}
	lib := &library{name: name, code: code, proto: proto}
	s := e.acquireFunctionSession()
	defer e.releaseFunctionSession(s)
	load, message := s.runLibrary(lib, timeout)
	if message != "" {
		restore()
		return "", message
	}
	if len(load.functions) == 0 {
		restore()
		return "", "No functions registered"
	}
	for _, function := range load.functions {
		if context.functions[function.name] != nil {
			restore()
			return "", "Function " + function.name + " already exists"
		}
		function.library = lib
	}
	lib.functions = load.functions
	s.libraries[lib] = load.callbacks
	context.link(lib)
	return name, ""
}

func (e *Engine) FunctionLoad(code []byte, replace bool) Reply {
	e.functions.mu.Lock()
	defer e.functions.mu.Unlock()
	name, failure := e.createLibrary(string(code), replace, e.functions.current, functionLoadTimeout)
	if failure != "" {
		return Error("ERR " + failure)
	}
	return Bulk(name)
}

func (e *Engine) FunctionDelete(name string) Reply {
	e.functions.mu.Lock()
	defer e.functions.mu.Unlock()
	lib := e.functions.current.libraries[name]
	if lib == nil {
		return Error("ERR Library not found")
	}
	e.functions.current.unlink(lib)
	return Status("OK")
}

func (e *Engine) FunctionFlush() {
	e.functions.mu.Lock()
	defer e.functions.mu.Unlock()
	e.functions.current = newLibraryContext()
}

func (e *Engine) FunctionFlags(name string) (Flags, bool) {
	e.functions.mu.RLock()
	defer e.functions.mu.RUnlock()
	function := e.functions.current.functions[name]
	if function == nil {
		return 0, false
	}
	return function.flags, true
}

func (e *Engine) Libraries() map[string][]byte {
	e.functions.mu.RLock()
	defer e.functions.mu.RUnlock()
	result := make(map[string][]byte, len(e.functions.current.order))
	for _, lib := range e.functions.current.order {
		result[lib.name] = []byte(lib.code)
	}
	return result
}

func (e *Engine) FunctionList(pattern string, withCode bool) Reply {
	return e.functionList(pattern, pattern != "", withCode)
}

func (e *Engine) functionList(pattern string, filter, withCode bool) Reply {
	e.functions.mu.RLock()
	defer e.functions.mu.RUnlock()
	result := Array()
	for _, lib := range e.functions.current.order {
		if filter && !stringMatch(pattern, lib.name, true) {
			continue
		}
		functions := Array()
		for _, function := range lib.functions {
			description := Null()
			if function.hasDescription {
				description = Bulk(function.description)
			}
			flags := Set()
			for _, known := range scriptFlagNames {
				if function.flags&known.flag != 0 {
					flags.Elements = append(flags.Elements, Status(known.name))
				}
			}
			functions.Elements = append(functions.Elements, Map(Pair{Bulk("name"), Bulk(function.name)}, Pair{Bulk("description"), description}, Pair{Bulk("flags"), flags}))
		}
		entry := Map(Pair{Bulk("library_name"), Bulk(lib.name)}, Pair{Bulk("engine"), Bulk("LUA")}, Pair{Bulk("functions"), functions})
		if withCode {
			entry.Pairs = append(entry.Pairs, Pair{Bulk("library_code"), Bulk(lib.code)})
		}
		result.Elements = append(result.Elements, entry)
	}
	return result
}

func (e *Engine) FunctionStats() Reply {
	running := Null()
	e.mu.Lock()
	for x := range e.running {
		if x.info.Kind == Script {
			e.mu.Unlock()
			return Busy(Script)
		}
		if x.info.Kind == Function {
			command := Array()
			for _, arg := range x.caller {
				command.Elements = append(command.Elements, Bulk(string(arg)))
			}
			running = Map(Pair{Bulk("name"), Bulk(x.info.Name)}, Pair{Bulk("command"), command}, Pair{Bulk("duration_ms"), Integer(time.Since(x.info.Started).Milliseconds())})
		}
	}
	e.mu.Unlock()
	e.functions.mu.RLock()
	defer e.functions.mu.RUnlock()
	stats := Map(Pair{Bulk("libraries_count"), Integer(int64(len(e.functions.current.order)))}, Pair{Bulk("functions_count"), Integer(int64(len(e.functions.current.functions)))})
	return Map(Pair{Bulk("running_script"), running}, Pair{Bulk("engines"), Map(Pair{Bulk("LUA"), stats})})
}

func rdbAppendLength(dst []byte, length uint64) []byte {
	switch {
	case length < 1<<6:
		return append(dst, byte(length))
	case length < 1<<14:
		return append(dst, byte(length>>8)|0x40, byte(length))
	case length <= 0xffffffff:
		return binary.BigEndian.AppendUint32(append(dst, 0x80), uint32(length))
	}
	return binary.BigEndian.AppendUint64(append(dst, 0x81), length)
}

func (e *Engine) FunctionDump() []byte {
	e.functions.mu.RLock()
	defer e.functions.mu.RUnlock()
	var payload []byte
	for _, lib := range e.functions.current.order {
		payload = append(payload, functionOpcode)
		payload = rdbAppendLength(payload, uint64(len(lib.code)))
		payload = append(payload, lib.code...)
	}
	payload = binary.LittleEndian.AppendUint16(payload, rdbVersion)
	return binary.LittleEndian.AppendUint64(payload, crc64Jones(payload))
}

var crc64Table = func() [256]uint64 {
	var table [256]uint64
	for index := range table {
		value := uint64(index)
		for range 8 {
			if value&1 != 0 {
				value = value>>1 ^ 0x95ac9329ac4bc9b5
			} else {
				value >>= 1
			}
		}
		table[index] = value
	}
	return table
}()

func crc64Jones(data []byte) uint64 {
	crc := uint64(0)
	for _, b := range data {
		crc = crc64Table[byte(crc)^b] ^ crc>>8
	}
	return crc
}

type rdbReader struct {
	data []byte
	at   int
}

func (r *rdbReader) take(count uint64) ([]byte, bool) {
	if count > uint64(len(r.data)-r.at) {
		return nil, false
	}
	part := r.data[r.at : r.at+int(count)]
	r.at += int(count)
	return part, true
}

func (r *rdbReader) length() (value uint64, encoded, ok bool) {
	first, ok := r.take(1)
	if !ok {
		return 0, false, false
	}
	switch first[0] >> 6 {
	case 0:
		return uint64(first[0] & 0x3f), false, true
	case 1:
		next, ok := r.take(1)
		if !ok {
			return 0, false, false
		}
		return uint64(first[0]&0x3f)<<8 | uint64(next[0]), false, true
	case 3:
		return uint64(first[0] & 0x3f), true, true
	}
	switch first[0] {
	case 0x80:
		part, ok := r.take(4)
		if !ok {
			return 0, false, false
		}
		return uint64(binary.BigEndian.Uint32(part)), false, true
	case 0x81:
		part, ok := r.take(8)
		if !ok {
			return 0, false, false
		}
		return binary.BigEndian.Uint64(part), false, true
	}
	return 0, false, false
}

func (r *rdbReader) stringValue() ([]byte, bool) {
	length, encoded, ok := r.length()
	if !ok {
		return nil, false
	}
	if !encoded {
		return r.take(length)
	}
	switch length {
	case 0:
		part, ok := r.take(1)
		if !ok {
			return nil, false
		}
		return strconv.AppendInt(nil, int64(int8(part[0])), 10), true
	case 1:
		part, ok := r.take(2)
		if !ok {
			return nil, false
		}
		return strconv.AppendInt(nil, int64(int16(binary.LittleEndian.Uint16(part))), 10), true
	case 2:
		part, ok := r.take(4)
		if !ok {
			return nil, false
		}
		return strconv.AppendInt(nil, int64(int32(binary.LittleEndian.Uint32(part))), 10), true
	case 3:
		compressed, _, ok := r.length()
		if !ok {
			return nil, false
		}
		size, _, ok := r.length()
		if !ok || size == 0 {
			return nil, false
		}
		part, ok := r.take(compressed)
		if !ok {
			return nil, false
		}
		return lzfDecompress(part, size)
	}
	return nil, false
}

func lzfDecompress(input []byte, size uint64) ([]byte, bool) {
	if size > 512<<20 || len(input) == 0 {
		return nil, false
	}
	output := make([]byte, 0, size)
	for at := 0; at < len(input); {
		control := int(input[at])
		at++
		if control < 32 {
			count := control + 1
			if at+count > len(input) || uint64(len(output)+count) > size {
				return nil, false
			}
			output = append(output, input[at:at+count]...)
			at += count
			continue
		}
		count := control >> 5
		if at >= len(input) {
			return nil, false
		}
		if count == 7 {
			count += int(input[at])
			at++
			if at >= len(input) {
				return nil, false
			}
		}
		reference := len(output) - (control&0x1f)<<8 - 1 - int(input[at])
		at++
		if uint64(len(output)+count+2) > size || reference < 0 {
			return nil, false
		}
		for offset := range count + 2 {
			output = append(output, output[reference+offset])
		}
	}
	if uint64(len(output)) != size {
		return nil, false
	}
	return output, true
}

func (e *Engine) FunctionRestore(payload []byte, policy string) Reply {
	mode := strings.ToLower(policy)
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "replace" && mode != "flush" {
		return Error("ERR Wrong restore policy given, value should be either FLUSH, APPEND or REPLACE.")
	}
	if len(payload) < 10 {
		return Error("ERR DUMP payload version or checksum are wrong")
	}
	footer := payload[len(payload)-10:]
	version := binary.LittleEndian.Uint16(footer)
	if version > rdbVersion || crc64Jones(payload[:len(payload)-8]) != binary.LittleEndian.Uint64(footer[2:]) {
		return Error("ERR DUMP payload version or checksum are wrong")
	}
	e.functions.mu.Lock()
	defer e.functions.mu.Unlock()
	incoming := newLibraryContext()
	reader := rdbReader{data: payload}
	for len(payload)-reader.at > 10 {
		opcode, _ := reader.take(1)
		if opcode[0] == functionPreGAOpcode {
			return Error("ERR Pre-GA function format not supported")
		}
		if opcode[0] != functionOpcode {
			return Error("ERR given type is not a function")
		}
		code, ok := reader.stringValue()
		if !ok {
			return Error("ERR Failed loading library payload")
		}
		if _, failure := e.createLibrary(string(code), false, incoming, 0); failure != "" {
			return Error("ERR " + failure)
		}
	}
	if mode == "flush" {
		e.functions.current = incoming
		return Status("OK")
	}
	current := e.functions.current
	var replaced []*library
	revert := func() {
		for _, lib := range replaced {
			current.link(lib)
		}
	}
	for _, lib := range incoming.order {
		if old := current.libraries[lib.name]; old != nil {
			if mode != "replace" {
				revert()
				return Error("ERR Library " + lib.name + " already exists")
			}
			current.unlink(old)
			replaced = append(replaced, old)
		}
	}
	for name := range incoming.functions {
		if current.functions[name] != nil {
			revert()
			return Error("ERR Function " + name + " already exists")
		}
	}
	for _, lib := range incoming.order {
		current.link(lib)
	}
	return Status("OK")
}

func (e *Engine) acquireFunctionSession() *session {
	e.mu.Lock()
	if size := len(e.functions.free); size > 0 {
		s := e.functions.free[size-1]
		e.functions.free = e.functions.free[:size-1]
		e.mu.Unlock()
		return s
	}
	e.mu.Unlock()
	return newFunctionSession(e)
}

func (e *Engine) releaseFunctionSession(s *session) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.config.MemoryLimit <= 0 {
		e.functions.free = append(e.functions.free, s)
	}
}

func (s *session) callback(function *functionInfo) (lua.Value, bool) {
	if callbacks, loaded := s.libraries[function.library]; loaded {
		value, ok := callbacks[function.name]
		return value, ok
	}
	load, failure := s.runLibrary(function.library, 0)
	if failure != "" {
		return lua.Nil(), false
	}
	s.libraries[function.library] = load.callbacks
	value, ok := load.callbacks[function.name]
	return value, ok
}

func (e *Engine) forgetLibraries(s *session) {
	e.functions.mu.RLock()
	defer e.functions.mu.RUnlock()
	for lib := range s.libraries {
		if e.functions.current.libraries[lib.name] != lib {
			delete(s.libraries, lib)
		}
	}
}

func (e *Engine) FCall(name string, keys, args [][]byte, run Run) Reply {
	return e.fcall(name, keys, args, run, nil)
}

func (e *Engine) fcall(name string, keys, args [][]byte, run Run, caller [][]byte) Reply {
	e.functions.mu.RLock()
	function := e.functions.current.functions[name]
	e.functions.mu.RUnlock()
	if function == nil {
		return Error("ERR Function not found")
	}
	if run.ReadOnly && function.flags&NoWrites == 0 {
		return Error("ERR Can not execute a script with write flag using *_ro command.")
	}
	run.ReadOnly = run.ReadOnly || function.flags&NoWrites != 0
	s := e.acquireFunctionSession()
	defer e.releaseFunctionSession(s)
	e.forgetLibraries(s)
	callback, ok := s.callback(function)
	if !ok {
		return Error("ERR Function not found")
	}
	if caller == nil {
		verb := "FCALL"
		if run.ReadOnly && function.flags&NoWrites == 0 {
			verb = "FCALL_RO"
		}
		caller = append([][]byte{[]byte(verb), []byte(name), strconv.AppendInt(nil, int64(len(keys)), 10)}, append(cloneArguments(keys), cloneArguments(args)...)...)
	}
	info := RunInfo{Kind: Function, Name: name, Started: time.Now(), ReadOnly: run.ReadOnly, Replication: 3}
	return e.runSession(s, info, run, caller, callback, name, "@user_function", s.argumentTable(keys), s.argumentTable(args))
}

func stringMatch(pattern, text string, nocase bool) bool {
	skip := false
	return stringMatchNested(pattern, text, nocase, &skip, 0)
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

func stringMatchNested(pattern, text string, nocase bool, skip *bool, nesting int) bool {
	if nesting > 1000 {
		return false
	}
	at := func(index int) byte {
		if index < len(pattern) {
			return pattern[index]
		}
		return 0
	}
	for len(pattern) > 0 && len(text) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && at(1) == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 1 {
				return true
			}
			for len(text) > 0 {
				if stringMatchNested(pattern[1:], text, nocase, skip, nesting+1) {
					return true
				}
				if *skip {
					return false
				}
				text = text[1:]
			}
			*skip = true
			return false
		case '?':
			text = text[1:]
		case '[':
			pattern = pattern[1:]
			negate := at(0) == '^'
			if negate {
				pattern = pattern[1:]
			}
			matched := false
			for {
				switch {
				case at(0) == '\\' && len(pattern) >= 2:
					pattern = pattern[1:]
					if pattern[0] == text[0] {
						matched = true
					}
				case at(0) == ']' && len(pattern) > 0:
				case len(pattern) == 0:
				case len(pattern) >= 3 && pattern[1] == '-':
					start, end, c := pattern[0], pattern[2], text[0]
					if int8(start) > int8(end) {
						start, end = end, start
					}
					if nocase {
						start, end, c = lowerByte(start), lowerByte(end), lowerByte(c)
					}
					pattern = pattern[2:]
					if int8(c) >= int8(start) && int8(c) <= int8(end) {
						matched = true
					}
				case nocase && lowerByte(pattern[0]) == lowerByte(text[0]), !nocase && pattern[0] == text[0]:
					matched = true
				}
				if len(pattern) == 0 || pattern[0] == ']' {
					break
				}
				pattern = pattern[1:]
			}
			if negate {
				matched = !matched
			}
			if !matched {
				return false
			}
			text = text[1:]
		case '\\':
			if len(pattern) >= 2 {
				pattern = pattern[1:]
			}
			fallthrough
		default:
			if nocase && lowerByte(pattern[0]) != lowerByte(text[0]) || !nocase && pattern[0] != text[0] {
				return false
			}
			text = text[1:]
		}
		if len(pattern) > 0 {
			pattern = pattern[1:]
		}
		if len(text) == 0 {
			for at(0) == '*' && len(pattern) > 0 {
				pattern = pattern[1:]
			}
			break
		}
	}
	return len(pattern) == 0 && len(text) == 0
}
