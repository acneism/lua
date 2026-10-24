package luatest

import (
	"github.com/acneism/lua"
	"testing"
)

func TestEnvironmentParserDepth(t *testing.T) {
	RunCorpus(t, []string{"testdata/environment.json"}, lua.Options{})
}
