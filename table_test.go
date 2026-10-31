package lua

import (
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"testing"
)

func TestTableModel(t *testing.T) {
	seed := uint64(1)
	if text := os.Getenv("LUA_DIFF_SEED"); text != "" {
		var err error
		seed, err = strconv.ParseUint(text, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	s := NewState(Options{})
	table := s.NewTable(0, 0)
	model := make(map[Value]Value)
	keys := []Value{Bool(false), Bool(true), Number(-7), Number(0), Number(1.5), Number(1e300)}
	for i := 1; i <= 64; i++ {
		keys = append(keys, Number(float64(i)), String("key"+strconv.Itoa(i)))
	}
	for i := 0; i < 8; i++ {
		keys = append(keys, s.NewTable(0, 0).Value())
	}
	for step := 0; step < 5000; step++ {
		key := keys[random.IntN(len(keys))]
		value := Number(float64(random.IntN(1000)))
		if random.IntN(3) == 0 {
			value = Nil()
		}
		if err := table.RawSet(key, value); err != nil {
			t.Fatalf("seed %d step %d: %v", seed, step, err)
		}
		if value.IsNil() {
			delete(model, key)
		} else {
			model[key] = value
		}
		if !table.RawGet(key).Equal(value) {
			t.Fatalf("seed %d step %d: lookup differs", seed, step)
		}
		if step%31 != 0 {
			continue
		}
		seen := make(map[Value]bool)
		for key, value, err := table.Next(Nil()); !key.IsNil() || err != nil; key, value, err = table.Next(key) {
			if err != nil || seen[key] || !model[key].Equal(value) {
				t.Fatalf("seed %d step %d: traversal differs at %v: %v", seed, step, key, err)
			}
			seen[key] = true
			if len(seen) > len(model) {
				t.Fatalf("seed %d step %d: traversal does not terminate", seed, step)
			}
		}
		if len(seen) != len(model) {
			t.Fatalf("seed %d step %d: got %d keys, want %d", seed, step, len(seen), len(model))
		}
		length := table.Len()
		if length > 0 && table.RawGet(Number(float64(length))).IsNil() || !table.RawGet(Number(float64(length+1))).IsNil() {
			t.Fatalf("seed %d step %d: invalid length boundary %d", seed, step, length)
		}
	}
}

func TestTableReadOnlyAndInvalidKeys(t *testing.T) {
	table := NewTable(0, 0)
	for _, key := range []Value{Nil(), Number(math.NaN())} {
		if err := table.RawSet(key, Number(1)); err == nil {
			t.Fatalf("accepted %v", key)
		}
	}
	meta := NewTable(0, 0)
	if err := table.SetMetatable(meta); err != nil || table.Metatable() != meta {
		t.Fatal("metatable API failed")
	}
	table.SetReadOnly(true)
	if !table.ReadOnly() || table.RawSet(String("key"), Number(1)) == nil || table.SetMetatable(nil) == nil {
		t.Fatal("readonly table changed")
	}
	table.SetReadOnly(false)
	if err := table.RawSet(Number(math.Copysign(0, -1)), Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSet(Number(0), Number(2)); err != nil {
		t.Fatal(err)
	}
	key, value, err := table.Next(Nil())
	if err != nil || !math.Signbit(key.Float()) || value.Float() != 2 {
		t.Fatalf("lost the original zero key: %v %v %v", key, value, err)
	}
}

func TestTableNumericArray(t *testing.T) {
	s := NewState(Options{})
	table := s.NewTable(4, 0)
	nan := math.Float64frombits(0x7ff8000000000123)
	for key, value := range map[float64]Value{1: Number(1), 3: Number(nan), 4: Number(4)} {
		if err := table.RawSet(Number(key), value); err != nil {
			t.Fatal(err)
		}
	}
	if table.generic || table.Len() != 4 || !table.RawGet(Number(2)).IsNil() || math.Float64bits(table.RawGet(Number(3)).number) != 0x7ff8000000000123 {
		t.Fatalf("numeric array: generic=%v len=%d", table.generic, table.Len())
	}
	var keys []float64
	for key, _, err := table.Next(Nil()); !key.IsNil(); key, _, err = table.Next(key) {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.number)
	}
	if len(keys) != 3 || keys[0] != 1 || keys[1] != 3 || keys[2] != 4 {
		t.Fatalf("numeric array keys: %v", keys)
	}
	if err := table.RawSet(Number(2), Number(math.Float64frombits(holeBits))); err != nil {
		t.Fatal(err)
	}
	if !table.generic || math.Float64bits(table.RawGet(Number(2)).number) != holeBits || table.RawGet(Number(1)).number != 1 || math.Float64bits(table.RawGet(Number(3)).number) != 0x7ff8000000000123 {
		t.Fatalf("hole bits did not switch the array to values")
	}
	mixed := s.NewTable(3, 0)
	for key := 1; key <= 3; key++ {
		if err := mixed.RawSet(Number(float64(key)), Number(float64(key*10))); err != nil {
			t.Fatal(err)
		}
	}
	if err := mixed.RawSet(Number(2), String("x")); err != nil {
		t.Fatal(err)
	}
	if !mixed.generic || mixed.RawGet(Number(1)).number != 10 || mixed.RawGet(Number(2)).Text() != "x" || mixed.RawGet(Number(3)).number != 30 || mixed.Len() != 3 {
		t.Fatalf("string value did not switch the array to values")
	}
}

func TestTableReserveMatchesAllocation(t *testing.T) {
	for _, item := range []struct {
		array, hash int
		generic     bool
	}{{1024, 0, false}, {2048, 0, true}, {0, 1024, false}} {
		budget := NewBudget(0)
		table := &Table{memory: budget, generic: item.generic}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		table.resize(item.array, item.hash)
		runtime.ReadMemStats(&after)
		if allocated := int64(after.TotalAlloc - before.TotalAlloc); allocated < budget.Used() || allocated > budget.Used()+budget.Used()/8+64 {
			t.Fatalf("array %d hash %d generic %v: reserved %d, allocated %d", item.array, item.hash, item.generic, budget.Used(), allocated)
		}
	}
}
