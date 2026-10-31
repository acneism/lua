package lua

import (
	"testing"

	"github.com/acneism/lua/internal/scenario"
)

func BenchmarkScenario(b *testing.B) {
	for _, item := range scenario.All() {
		b.Run(item.Name, func(b *testing.B) { benchmarkScript(b, item.Source) })
	}
}
