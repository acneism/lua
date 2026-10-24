package redis

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ReadReply(reader *bufio.Reader) (Reply, error) { return readReply(reader, 0) }

func ParseRESP(data []byte) (Reply, int, error) {
	source := bytes.NewReader(data)
	reader := bufio.NewReader(source)
	reply, err := ReadReply(reader)
	return reply, len(data) - source.Len() - reader.Buffered(), err
}

func readLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > 1<<20 {
			return "", fmt.Errorf("RESP line exceeds limit")
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(line) < 2 || line[len(line)-2] != '\r' {
			return "", fmt.Errorf("invalid RESP line ending")
		}
		return string(line[:len(line)-2]), nil
	}
}

func readReply(reader *bufio.Reader, depth int) (Reply, error) {
	if depth > 128 {
		return Reply{}, fmt.Errorf("RESP nesting exceeds limit")
	}
	line, err := readLine(reader)
	if err != nil {
		return Reply{}, err
	}
	if len(line) == 0 {
		return Reply{}, fmt.Errorf("missing RESP type")
	}
	prefix, text := line[0], line[1:]
	switch prefix {
	case '+':
		return Status(text), nil
	case '-':
		return Error(text), nil
	case ':':
		value, err := strconv.ParseInt(text, 10, 64)
		return Integer(value), err
	case ',':
		value, err := strconv.ParseFloat(text, 64)
		return Double(value), err
	case '#':
		if text != "t" && text != "f" {
			return Reply{}, fmt.Errorf("invalid RESP boolean")
		}
		return Boolean(text == "t"), nil
	case '_':
		if text != "" {
			return Reply{}, fmt.Errorf("invalid RESP null")
		}
		return Null(), nil
	case '(':
		if strings.TrimLeft(text, "-0123456789") != "" || text == "" || text == "-" || strings.Contains(text[1:], "-") {
			return Reply{}, fmt.Errorf("invalid RESP big number")
		}
		return BigNumber(text), nil
	case '$', '=', '!':
		length, err := strconv.ParseInt(text, 10, 64)
		if err != nil || length < -1 || length > 512<<20 || length == -1 && prefix != '$' {
			return Reply{}, fmt.Errorf("invalid RESP bulk length")
		}
		if length == -1 {
			return Null(), nil
		}
		var body bytes.Buffer
		if _, err = io.CopyN(&body, reader, length); err != nil {
			return Reply{}, err
		}
		var end [2]byte
		if _, err = io.ReadFull(reader, end[:]); err != nil || end != [2]byte{'\r', '\n'} {
			return Reply{}, fmt.Errorf("invalid RESP bulk ending")
		}
		data := body.String()
		if prefix == '!' {
			return Error(data), nil
		}
		if prefix == '=' {
			if len(data) < 4 || data[3] != ':' {
				return Reply{}, fmt.Errorf("invalid RESP verbatim string")
			}
			return Verbatim(data[:3], data[4:]), nil
		}
		return Bulk(data), nil
	case '*', '%', '~':
		length, err := strconv.ParseInt(text, 10, 64)
		if err != nil || length < -1 || length > 1<<20 || length == -1 && prefix != '*' {
			return Reply{}, fmt.Errorf("invalid RESP aggregate length")
		}
		if length == -1 {
			return NullArray(), nil
		}
		if prefix == '%' {
			result := Map()
			for range length {
				key, err := readReply(reader, depth+1)
				if err != nil {
					return Reply{}, err
				}
				value, err := readReply(reader, depth+1)
				if err != nil {
					return Reply{}, err
				}
				result.Pairs = append(result.Pairs, Pair{key, value})
			}
			return result, nil
		}
		result := Array()
		if prefix == '~' {
			result.Type = SetReply
		}
		for range length {
			value, err := readReply(reader, depth+1)
			if err != nil {
				return Reply{}, err
			}
			result.Elements = append(result.Elements, value)
		}
		return result, nil
	}
	return Reply{}, fmt.Errorf("unsupported RESP type %q", prefix)
}
