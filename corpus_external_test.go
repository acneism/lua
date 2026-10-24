package lua_test

import (
	"testing"

	"github.com/acneism/lua"
	"github.com/acneism/lua/luatest"
)

func TestCorpus(t *testing.T) {
	luatest.RunCorpus(t, []string{"testdata/snippets.json"}, lua.Options{})
}
