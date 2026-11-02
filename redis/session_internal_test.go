package redis

import (
	"testing"

	"github.com/acneism/lua"
)

func TestGlobalNamesAreInterned(t *testing.T) {
	s := newSession(NewEngine(Config{}))
	for _, table := range []*lua.Table{s.globals, s.globals.RawGet(lua.String("redis")).Table()} {
		key, _, err := table.Next(lua.Nil())
		for ; err == nil && !key.IsNil(); key, _, err = table.Next(key) {
			if key.Text() != "__redis__err__handler" && key != lua.Intern(key.Text()) {
				t.Fatalf("global name %q is not interned", key.Text())
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
