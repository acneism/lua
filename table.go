package lua

import (
	"math"
	"math/bits"
	"reflect"
)

const maxTableBits = 26

var (
	numberBytes = int64(reflect.TypeFor[float64]().Size())
	valueBytes  = int64(reflect.TypeFor[Value]().Size())
	nodeBytes   = int64(reflect.TypeFor[tableNode]().Size())
)

type tableNode struct {
	key   Value
	value Value
	next  int
}

type Table struct {
	object    object
	version   uint64
	sequence  int
	numbers   []float64
	array     []Value
	generic   bool
	nodes     []tableNode
	lastFree  int
	metatable *Table
	readonly  bool
	memory    *Budget
}

func NewTable(arraySize, hashSize int) *Table {
	t := &Table{}
	t.object.kind, t.object.table = TypeTable, t
	t.resize(arraySize, hashSize)
	return t
}

func (t *Table) Value() Value              { return Value{object: &t.object} }
func (t *Table) ReadOnly() bool            { return t.readonly }
func (t *Table) SetReadOnly(readonly bool) { t.readonly = readonly }
func (t *Table) Metatable() *Table         { return t.metatable }

func (t *Table) SetMetatable(meta *Table) error {
	if t.readonly {
		return plainError("Attempt to modify a readonly table")
	}
	t.metatable = meta
	return nil
}

const holeBits = 0x7ff4dead0000beef

func (t *Table) arraySize() int {
	if t.generic {
		return len(t.array)
	}
	return len(t.numbers)
}

func (t *Table) arrayAt(index int) Value {
	if t.generic {
		return t.array[index]
	}
	if number := t.numbers[index]; math.Float64bits(number) != holeBits {
		return Number(number)
	}
	return Nil()
}

func (t *Table) makeGeneric() {
	t.memory.reserve(int64(len(t.numbers)) * valueBytes)
	t.array = make([]Value, len(t.numbers))
	for index := range t.numbers {
		t.array[index] = t.arrayAt(index)
	}
	t.numbers, t.generic = nil, true
}

func arrayIndex(key Value) int {
	if key.Type() == TypeNumber && key.number >= 1 && key.number <= 1<<maxTableBits && key.number == math.Trunc(key.number) {
		return int(key.number)
	}
	return -1
}

func stringHash(s string) uint32 {
	hash := uint32(len(s))
	step := (len(s) >> 5) + 1
	for i := len(s); i >= step; i -= step {
		hash ^= (hash << 5) + (hash >> 2) + uint32(s[i-1])
	}
	return hash
}

func (t *Table) mainPosition(key Value) int {
	if len(t.nodes) == 0 {
		return -1
	}
	switch key.Type() {
	case TypeString:
		return int(key.text.hash & uint32(len(t.nodes)-1))
	case TypeBoolean:
		return int(key.number) & (len(t.nodes) - 1)
	case TypeNumber:
		if key.number == 0 {
			return 0
		}
		word := math.Float64bits(key.number)
		hash := uint32(word) + uint32(word>>32)
		return int(uint64(hash) % uint64((len(t.nodes)-1)|1))
	default:
		if key.object == nil {
			return 0
		}
		return int(key.object.id % uint64((len(t.nodes)-1)|1))
	}
}

func (t *Table) find(key Value) int {
	for i := t.mainPosition(key); i >= 0; i = t.nodes[i].next {
		if t.nodes[i].key.Equal(key) {
			return i
		}
	}
	return -1
}

func (t *Table) getString(key *stringValue) Value {
	if len(t.nodes) == 0 {
		return Nil()
	}
	for i := int(key.hash & uint32(len(t.nodes)-1)); i >= 0; i = t.nodes[i].next {
		node := &t.nodes[i]
		if text := node.key.text; text == key || text != nil && text.hash == key.hash && text.text == key.text {
			return node.value
		}
	}
	return Nil()
}

func (t *Table) replaceString(key *stringValue, value Value) bool {
	if len(t.nodes) == 0 {
		return false
	}
	for i := int(key.hash & uint32(len(t.nodes)-1)); i >= 0; i = t.nodes[i].next {
		node := &t.nodes[i]
		if text := node.key.text; text == key || text != nil && text.hash == key.hash && text.text == key.text {
			t.version++
			node.value = value
			return true
		}
	}
	return false
}

func (t *Table) RawGet(key Value) Value {
	if index := arrayIndex(key); index > 0 && index <= t.arraySize() {
		return t.arrayAt(index - 1)
	}
	if key.IsNil() {
		return Nil()
	}
	if index := t.find(key); index >= 0 {
		return t.nodes[index].value
	}
	return Nil()
}

func (t *Table) RawSet(key, value Value) error {
	if t.readonly {
		return plainError("Attempt to modify a readonly table")
	}
	if key.IsNil() {
		return plainError("table index is nil")
	}
	if key.Type() == TypeNumber && math.IsNaN(key.number) {
		return plainError("table index is NaN")
	}
	t.set(key, value)
	return nil
}

func (t *Table) set(key, value Value) {
	if index := arrayIndex(key); index > 0 && index <= t.arraySize() {
		t.setArray(index-1, value)
		return
	}
	t.version++
	if index := t.find(key); index >= 0 {
		t.invalidateSequence(key, value)
		t.nodes[index].value = value
		return
	}
	main := t.mainPosition(key)
	if main < 0 || !t.nodes[main].value.IsNil() {
		free := -1
		for t.lastFree > 0 {
			t.lastFree--
			if t.nodes[t.lastFree].key.IsNil() {
				free = t.lastFree
				break
			}
		}
		if free < 0 {
			t.rehash(key)
			t.set(key, value)
			return
		}
		other := t.mainPosition(t.nodes[main].key)
		if other != main {
			for t.nodes[other].next != main {
				other = t.nodes[other].next
			}
			t.nodes[other].next = free
			t.nodes[free] = t.nodes[main]
			t.nodes[main].next = -1
			t.nodes[main].value = Nil()
		} else {
			t.nodes[free].next = t.nodes[main].next
			t.nodes[main].next = free
			main = free
		}
	}
	t.invalidateSequence(key, value)
	t.nodes[main].key = key
	t.nodes[main].value = value
}

func (t *Table) setArray(index int, value Value) {
	if t.sequence >= 0 {
		switch {
		case value.IsNil():
			if index == t.sequence-1 {
				t.sequence--
			} else if index < t.sequence {
				t.sequence = -1
			}
		case index == t.sequence:
			t.sequence++
		case index > t.sequence:
			t.sequence = -1
		}
	}
	if !t.generic {
		switch {
		case value.object == &numberMarker && math.Float64bits(value.number) != holeBits:
			t.numbers[index] = value.number
			return
		case value.IsNil():
			t.numbers[index] = math.Float64frombits(holeBits)
			return
		}
		t.makeGeneric()
	}
	t.array[index] = value
}

func (t *Table) invalidateSequence(key, value Value) {
	if t.sequence >= 0 && !value.IsNil() && key.object == &numberMarker && key.number >= 1 && key.number == math.Trunc(key.number) {
		t.sequence = -1
	}
}

func countArrayKey(key Value, counts *[maxTableBits + 1]int) int {
	if index := arrayIndex(key); index > 0 {
		counts[bits.Len(uint(index-1))]++
		return 1
	}
	return 0
}

func (t *Table) rehash(extra Value) {
	var counts [maxTableBits + 1]int
	arrayUse, totalUse := 0, 0
	for i := range t.arraySize() {
		if !t.arrayAt(i).IsNil() {
			counts[bits.Len(uint(i))]++
			arrayUse++
			totalUse++
		}
	}
	for _, node := range t.nodes {
		if !node.value.IsNil() {
			arrayUse += countArrayKey(node.key, &counts)
			totalUse++
		}
	}
	arrayUse += countArrayKey(extra, &counts)
	totalUse++
	arraySize, included, accumulated := 0, 0, 0
	for i, size := 0, 1; i <= maxTableBits && size/2 < arrayUse; i, size = i+1, size*2 {
		if counts[i] > 0 {
			accumulated += counts[i]
			if accumulated > size/2 {
				arraySize = size
				included = accumulated
			}
		}
		if accumulated == arrayUse {
			break
		}
	}
	t.resize(arraySize, totalUse-included)
}

func (t *Table) resize(arraySize, hashSize int) {
	if arraySize < 0 || arraySize > 1<<maxTableBits || hashSize < 0 || hashSize > 1<<maxTableBits {
		panic(plainError("table overflow"))
	}
	old := *t
	oldSize, oldNodes := t.arraySize(), t.nodes
	if arraySize < t.sequence {
		t.sequence = -1
	}
	switch {
	case arraySize > oldSize && t.generic:
		t.memory.reserve(int64(arraySize-oldSize) * valueBytes)
		t.array = make([]Value, arraySize)
		copy(t.array, old.array)
	case arraySize > oldSize:
		t.memory.reserve(int64(arraySize-oldSize) * numberBytes)
		t.numbers = make([]float64, arraySize)
		copy(t.numbers, old.numbers)
		for index := oldSize; index < arraySize; index++ {
			t.numbers[index] = math.Float64frombits(holeBits)
		}
	case t.generic:
		t.array = t.array[:arraySize]
	default:
		t.numbers = t.numbers[:arraySize]
	}
	if hashSize > 0 {
		hashSize = 1 << bits.Len(uint(hashSize-1))
	}
	t.memory.reserve(int64(hashSize) * nodeBytes)
	t.nodes = make([]tableNode, hashSize)
	for i := range t.nodes {
		t.nodes[i].next = -1
	}
	t.lastFree = hashSize
	for i := arraySize; i < oldSize; i++ {
		if value := old.arrayAt(i); !value.IsNil() {
			t.set(Number(float64(i+1)), value)
		}
	}
	for i := len(oldNodes) - 1; i >= 0; i-- {
		if !oldNodes[i].value.IsNil() {
			t.set(oldNodes[i].key, oldNodes[i].value)
		}
	}
}

func (t *Table) Next(key Value) (Value, Value, error) {
	index := -1
	if !key.IsNil() {
		if array := arrayIndex(key); array > 0 && array <= t.arraySize() {
			index = array - 1
		} else {
			hash := t.find(key)
			if hash < 0 {
				return Nil(), Nil(), plainError("invalid key to 'next'")
			}
			index = t.arraySize() + hash
		}
	}
	index++
	for ; index < t.arraySize(); index++ {
		if value := t.arrayAt(index); !value.IsNil() {
			return Number(float64(index + 1)), value, nil
		}
	}
	for index -= t.arraySize(); index < len(t.nodes); index++ {
		if !t.nodes[index].value.IsNil() {
			return t.nodes[index].key, t.nodes[index].value, nil
		}
	}
	return Nil(), Nil(), nil
}

func (t *Table) Len() int {
	if t.sequence >= 0 {
		return t.sequence
	}
	upper := t.arraySize()
	if upper > 0 && t.arrayAt(upper-1).IsNil() {
		lower := 0
		for upper-lower > 1 {
			middle := (lower + upper) / 2
			if t.arrayAt(middle - 1).IsNil() {
				upper = middle
			} else {
				lower = middle
			}
		}
		return lower
	}
	if len(t.nodes) == 0 {
		return upper
	}
	lower := upper
	upper++
	for !t.RawGet(Number(float64(upper))).IsNil() {
		lower = upper
		upper *= 2
		if upper > math.MaxInt32 {
			index := 1
			for !t.RawGet(Number(float64(index))).IsNil() {
				index++
			}
			return index - 1
		}
	}
	for upper-lower > 1 {
		middle := (lower + upper) / 2
		if t.RawGet(Number(float64(middle))).IsNil() {
			upper = middle
		} else {
			lower = middle
		}
	}
	return lower
}
