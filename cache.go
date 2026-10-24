package lua

type binaryCache struct {
	table   *Table
	version uint64
	method  Value
}

func (s *State) binaryTableMethod(value Value, operation int) Value {
	if value.Type() != TypeTable || value.object.table.metatable == nil {
		return Nil()
	}
	table := value.object.table.metatable
	cache := &s.binaryCache[operation]
	if cache.table == table && cache.version == table.version {
		return cache.method
	}
	var name string
	switch operation {
	case 0:
		name = "__add"
	case 1:
		name = "__sub"
	case 2:
		name = "__mul"
	case 3:
		name = "__div"
	case 4:
		name = "__mod"
	case 5:
		name = "__pow"
	}
	method := table.RawGet(String(name))
	*cache = binaryCache{table: table, version: table.version, method: method}
	return method
}
