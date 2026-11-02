package redis

import "testing"

func TestCRC64MatchesRedis(t *testing.T) {
	if got := crc64Jones([]byte("123456789")); got != 0xe9c6d914c4b8d9ca {
		t.Fatalf("crc64 = %#x", got)
	}
}

func TestLZFRejectsCorruptInput(t *testing.T) {
	if value, ok := lzfDecompress([]byte{2, 'a', 'b', 'c', 0xe0, 4, 0}, 16); !ok || string(value) != "abccccccccccccc"+"c" {
		t.Fatalf("decompress: %q %v", value, ok)
	}
	for _, input := range [][]byte{{}, {5, 'a'}, {0x20}, {0x20, 9}, {0xe0}} {
		if _, ok := lzfDecompress(input, 8); ok {
			t.Fatalf("accepted %v", input)
		}
	}
}

func TestStringMatch(t *testing.T) {
	for _, item := range []struct {
		pattern, text string
		want          bool
	}{{"lib*", "LIB1", true}, {"lib?", "lib", false}, {"[a-c]x", "bx", true}, {"[^a-c]x", "bx", false}, {"\\*", "*", true}, {"*a*b", "xaxb", true}, {"", "", true}, {"*", "", false}, {"a*", "a", true}, {"[", "a", false}} {
		if got := stringMatch(item.pattern, item.text, true); got != item.want {
			t.Errorf("%q %q: got %v", item.pattern, item.text, got)
		}
	}
}
