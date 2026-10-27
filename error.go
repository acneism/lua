package lua

import (
	"fmt"
	"strings"
)

type Error struct {
	Value        Value
	Message      string
	Line         int
	Abort        error
	Internal     bool
	cDepth       int
	handlerOwner *handlerOwner
	handlerID    uint64
	memory       bool
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
	return sizedChunkID(chunk, 60)
}

func sizedChunkID(chunk string, size int) string {
	if zero := strings.IndexByte(chunk, 0); zero >= 0 {
		chunk = chunk[:zero]
	}
	if strings.HasPrefix(chunk, "=") {
		chunk = chunk[1:]
		if len(chunk) >= size {
			chunk = chunk[:size-1]
		}
		return chunk
	}
	if strings.HasPrefix(chunk, "@") {
		chunk = chunk[1:]
		limit := size - len(" '...' ") - 1
		if len(chunk) > limit {
			chunk = "..." + chunk[len(chunk)-limit:]
		}
		return chunk
	}
	end := strings.IndexAny(chunk, "\r\n")
	if end < 0 {
		end = len(chunk)
	}
	first, multiline := chunk[:end], end != len(chunk)
	limit := size - len(` [string "..."] `) - 1
	if len(first) > limit {
		first = first[:limit]
		multiline = true
	}
	if multiline {
		first += "..."
	}
	return `[string "` + first + `"]`
}

func syntaxError(chunk string, line int, message string) *Error {
	err := plainError(fmt.Sprintf("%s:%d: %s", sizedChunkID(chunk, 80), line, message))
	err.Line = line
	return err
}

func locatedError(chunk string, line int, message string) *Error {
	err := plainError(fmt.Sprintf("%s:%d: %s", chunkID(chunk), line, message))
	err.Line = line
	return err
}
