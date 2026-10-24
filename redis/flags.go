package redis

import (
	"bytes"
	"strconv"
	"strings"
)

type Flags uint8

const (
	NoWrites Flags = 1 << iota
	AllowOOM
	AllowStale
	NoCluster
	AllowCrossSlotKeys
)

func parseKeyCount(text string) (int64, bool) {
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != text {
		return 0, false
	}
	return n, true
}

func parseFlags(script []byte) (Flags, int, *Reply) {
	if !bytes.HasPrefix(script, []byte("#!")) {
		return 0, 0, nil
	}
	end := bytes.IndexByte(script, '\n')
	if end < 0 || bytes.IndexByte(script[:end], 0) >= 0 {
		failure := Error("ERR Invalid script shebang")
		return 0, 0, &failure
	}
	words, ok := splitShebang(string(script[:end]))
	if !ok || len(words) == 0 {
		failure := Error("ERR Invalid engine in script shebang")
		return 0, 0, &failure
	}
	if words[0] != "#!lua" {
		failure := Error("ERR Unexpected engine in script shebang: " + words[0])
		return 0, 0, &failure
	}
	var flags Flags
	for _, word := range words[1:] {
		if !strings.HasPrefix(word, "flags=") {
			failure := Error("ERR Unknown lua shebang option: " + word)
			return 0, 0, &failure
		}
		if word == "flags=" {
			continue
		}
		for _, name := range strings.Split(word[6:], ",") {
			switch name {
			case "no-writes":
				flags |= NoWrites
			case "allow-oom":
				flags |= AllowOOM
			case "allow-stale":
				flags |= AllowStale
			case "no-cluster":
				flags |= NoCluster
			case "allow-cross-slot-keys":
				flags |= AllowCrossSlotKeys
			default:
				failure := Error("ERR Unexpected flag in script shebang: " + name)
				return 0, 0, &failure
			}
		}
	}
	return flags, end, nil
}

func splitShebang(text string) ([]string, bool) {
	var words []string
	for i := 0; i < len(text); {
		for i < len(text) && strings.ContainsRune(" \t\r\n\v\f", rune(text[i])) {
			i++
		}
		if i == len(text) {
			break
		}
		var word strings.Builder
		quote := byte(0)
		for i < len(text) {
			c := text[i]
			if quote == 0 && strings.ContainsRune(" \t\r\n\v\f", rune(c)) {
				break
			}
			if quote == 0 && (c == '\'' || c == '"') {
				quote = c
				i++
				continue
			}
			if quote != 0 && c == quote {
				quote = 0
				i++
				if i < len(text) && !strings.ContainsRune(" \t\r\n\v\f", rune(text[i])) {
					return nil, false
				}
				break
			}
			if c == '\\' && i+1 < len(text) && (quote == '"' || quote == '\'' && text[i+1] == '\'') {
				i++
				c = text[i]
				if quote == '"' {
					switch c {
					case 'n':
						c = '\n'
					case 'r':
						c = '\r'
					case 't':
						c = '\t'
					case 'b':
						c = '\b'
					case 'a':
						c = '\a'
					case 'x':
						if i+2 < len(text) {
							if value, err := strconv.ParseUint(text[i+1:i+3], 16, 8); err == nil {
								c = byte(value)
								i += 2
							}
						}
					}
				}
			}
			word.WriteByte(c)
			i++
		}
		if quote != 0 {
			return nil, false
		}
		words = append(words, word.String())
	}
	return words, true
}
