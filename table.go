package lua

import (
	"math"
	"math/bits"
)

const maxTableBits = 26

type tableNode struct {
	key   Value
	value Value
	next  int
}

type Table struct {
	object    object
	version   uint64
	array     []Value
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
		return int(stringHash(key.Text()) & uint32(len(t.nodes)-1))
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

func (t *Table) RawGet(key Value) Value {
	if index := arrayIndex(key); index > 0 && index <= len(t.array) {
		return t.array[index-1]
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
	if index := arrayIndex(key); index > 0 && index <= len(t.array) {
		t.array[index-1] = value
		return
	}
	t.version++
	if index := t.find(key); index >= 0 {
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
	t.nodes[main].key = key
	t.nodes[main].value = value
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
	for i, value := range t.array {
		if !value.IsNil() {
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
	oldArray, oldNodes := t.array, t.nodes
	if arraySize > len(oldArray) {
		t.memory.reserve(int64(arraySize-len(oldArray)) * 40)
		t.array = make([]Value, arraySize)
		copy(t.array, oldArray)
	} else {
		t.array = t.array[:arraySize]
	}
	if hashSize > 0 {
		hashSize = 1 << bits.Len(uint(hashSize-1))
	}
	t.memory.reserve(int64(hashSize) * 88)
	t.nodes = make([]tableNode, hashSize)
	for i := range t.nodes {
		t.nodes[i].next = -1
	}
	t.lastFree = hashSize
	for i := arraySize; i < len(oldArray); i++ {
		if !oldArray[i].IsNil() {
			t.set(Number(float64(i+1)), oldArray[i])
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
		if array := arrayIndex(key); array > 0 && array <= len(t.array) {
			index = array - 1
		} else {
			hash := t.find(key)
			if hash < 0 {
				return Nil(), Nil(), plainError("invalid key to 'next'")
			}
			index = len(t.array) + hash
		}
	}
	index++
	for ; index < len(t.array); index++ {
		if !t.array[index].IsNil() {
			return Number(float64(index + 1)), t.array[index], nil
		}
	}
	for index -= len(t.array); index < len(t.nodes); index++ {
		if !t.nodes[index].value.IsNil() {
			return t.nodes[index].key, t.nodes[index].value, nil
		}
	}
	return Nil(), Nil(), nil
}

func (t *Table) Len() int {
	upper := len(t.array)
	if upper > 0 && t.array[upper-1].IsNil() {
		lower := 0
		for upper-lower > 1 {
			middle := (lower + upper) / 2
			if t.array[middle-1].IsNil() {
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
