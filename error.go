package lua

import (
	"fmt"
	"strings"
)

type Error struct {
	Value    Value
	Message  string
	Line     int
	Abort    error
	Internal bool
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Abort != nil {
		return e.Abort.Error()
	}
	return e.Value.String()
}

func (e *Error) Unwrap() error { return e.Abort }

func plainError(message string) *Error {
	return &Error{Value: String(message), Message: message}
}

func chunkID(chunk string) string {
	if strings.HasPrefix(chunk, "=") {
		chunk = chunk[1:]
		if len(chunk) > 59 {
			chunk = chunk[:59]
		}
		return chunk
	}
	if strings.HasPrefix(chunk, "@") {
		chunk = chunk[1:]
		if len(chunk) > 59 {
			chunk = "..." + chunk[len(chunk)-56:]
		}
		return chunk
	}
	first, _, multiline := strings.Cut(chunk, "\n")
	if len(first) > 43 {
		first = first[:43]
		multiline = true
	}
	if multiline {
		first += "..."
	}
	return `[string "` + first + `"]`
}

func locatedError(chunk string, line int, message string) *Error {
	err := plainError(fmt.Sprintf("%s:%d: %s", chunkID(chunk), line, message))
	err.Line = line
	return err
}
