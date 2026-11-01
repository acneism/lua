package lua

type metaEvent uint8

const (
	eventAdd metaEvent = iota
	eventSubtract
	eventMultiply
	eventDivide
	eventModulo
	eventPower
	eventIndex
	eventNewIndex
	eventCall
	eventEqual
	eventLess
	eventLessEqual
	eventConcat
	eventNegate
	eventLength
	eventToString
	eventCount
)

var eventNames = [eventCount]Value{
	sharedString("__add"), sharedString("__sub"), sharedString("__mul"), sharedString("__div"), sharedString("__mod"), sharedString("__pow"),
	sharedString("__index"), sharedString("__newindex"), sharedString("__call"), sharedString("__eq"), sharedString("__lt"), sharedString("__le"),
	sharedString("__concat"), sharedString("__unm"), sharedString("__len"), sharedString("__tostring"),
}

const methodWays = 4

type methodCache struct {
	table   *Table
	version uint64
	method  Value
}

func eventFor(name string) (metaEvent, bool) {
	for event, value := range eventNames {
		if value.Text() == name {
			return metaEvent(event), true
		}
	}
	return 0, false
}

func (s *State) eventMethod(table *Table, event metaEvent) Value {
	cache := &s.methodCache[event][table.object.id%methodWays]
	if cache.table == table && cache.version == table.version {
		return cache.method
	}
	method := table.RawGet(eventNames[event])
	*cache = methodCache{table: table, version: table.version, method: method}
	return method
}

func (s *State) metatableOf(value Value) *Table {
	switch value.Type() {
	case TypeTable:
		return value.object.table.metatable
	case TypeString:
		return s.stringMeta
	}
	return nil
}

func (s *State) binaryTableMethod(value Value, operation int) Value {
	if value.Type() != TypeTable || value.object.table.metatable == nil {
		return Nil()
	}
	return s.eventMethod(value.object.table.metatable, metaEvent(operation))
}

func (s *State) metaFor(value Value, event metaEvent) Value {
	table := s.metatableOf(value)
	if table == nil {
		return Nil()
	}
	return s.eventMethod(table, event)
}

func (s *State) cached(event metaEvent, table *Table) (Value, bool) {
	if table == nil {
		return Nil(), false
	}
	cache := &s.methodCache[event][table.object.id%methodWays]
	return cache.method, cache.table == table && cache.version == table.version
}

func (v Value) luaFunction() bool {
	return v.object != nil && v.object.kind == TypeFunction && v.object.function.proto != nil
}

func (s *State) compareMethod(left, right *Value, mode opcode) (Value, bool) {
	if left.object == nil || right.object == nil || left.object.kind != TypeTable || right.object.kind != TypeTable {
		return Nil(), false
	}
	meta := left.object.table.metatable
	if meta == nil || meta != right.object.table.metatable {
		return Nil(), false
	}
	event := eventLessEqual
	switch comparisonKind(mode) {
	case compareEqual:
		if left.object == right.object {
			return Nil(), false
		}
		event = eventEqual
	case compareLess:
		event = eventLess
	}
	method, ok := s.cached(event, meta)
	return method, ok && method.luaFunction()
}

func (s *State) inheritedString(table *Table, key *stringValue) Value {
	for range 4 {
		method, ok := s.cached(eventIndex, table.metatable)
		if !ok || method.object == nil || method.object.kind != TypeTable {
			return Nil()
		}
		table = method.object.table
		found, known := table.lookup(key)
		if !known {
			found = table.getString(key)
		}
		if !found.IsNil() {
			return found
		}
	}
	return Nil()
}
