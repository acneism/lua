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
	String("__add"), String("__sub"), String("__mul"), String("__div"), String("__mod"), String("__pow"),
	String("__index"), String("__newindex"), String("__call"), String("__eq"), String("__lt"), String("__le"),
	String("__concat"), String("__unm"), String("__len"), String("__tostring"),
}

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
	cache := &s.methodCache[event]
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
