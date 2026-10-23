package lua

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func binaryProbe(value int) string {
	return fmt.Sprintf("local f,e=loadstring(\"return \\%d\",\"@probe\") local out=\"\" for i=1,#e do out=out..string.format(\"%%02x\",string.byte(e,i)) end return out", value)
}

func TestBinarySyntaxTokens(t *testing.T) {
	for _, value := range []int{128, 255} {
		want := ""
		for _, snippet := range readCorpus(t) {
			if snippet.Src == binaryProbe(value) {
				want = snippet.Want
				break
			}
		}
		if want == "" {
			t.Fatalf("missing binary reference %d", value)
		}
		s := NewState(Options{})
		fn := s.Load(compileTest(t, fmt.Sprintf("local f,e=loadstring(\"return \\%d\",\"@probe\") return e", value)), nil)
		values, err := s.Call(fn)
		if err != nil || len(values) != 1 || hex.EncodeToString([]byte(values[0].Text())) != want {
			t.Fatalf("binary token %d: %v %v; want hex %s", value, values, err, want)
		}
	}
}
