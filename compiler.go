package lua

import (
	"fmt"
	"strings"
)

type opcode uint8

const (
	opConstant opcode = iota
	opMove
	opGetGlobal
	opSetGlobal
	opGetUpvalue
	opSetUpvalue
	opGetTable
	opSetTable
	opNewTable
	opSetList
	opClosure
	opVararg
	opUnary
	opBinary
	opJump
	opJumpFalse
	opJumpTrue
	opCall
	opTailCall
	opReturn
	opClose
	opForPrep
	opForLoop
	opForCall
	opPoll
	opForAccumulate
	opCallDirect
	opCallDirectUpvalue
	opCallDirectConstant
	opCallDirectUpvalueConstant
	opLoadNil
	opGetTableK
	opSetTableRK
	opSelf
	opTestSet
	opConcat
	opForIterator
	opCompare
)

type nameInfo struct {
	kind     string
	name     string
	register int16
}

type instruction struct {
	op          opcode
	a, b, c, d  int
	line        int
	text        string
	left, right nameInfo
	method      bool
}

type upvalueDescriptor struct {
	local bool
	index int
	name  string
}

type Proto struct {
	canonical    *redisProto
	rawPC        []int
	code         []uint64
	sites        []uint32
	metadata     []instruction
	lines        []int32
	constants    []Value
	children     []*Proto
	upvalues     []upvalueDescriptor
	chunk        string
	parameters   int
	maxRegisters int
	vararg       bool
	usesVararg   bool
	argRegister  int
	line         int
	needsArg     bool
}

type compiler struct {
	code  []instruction
	proto *Proto
}

func Compile(source []byte, chunk string) (*Proto, error) {
	return compile(source, chunk, 0)
}

func compile(source []byte, chunk string, depth int) (proto *Proto, err error) {
	defer func() {
		if raised := recover(); raised != nil {
			if failure, ok := raised.(*Error); ok {
				err = failure
			} else {
				err = &Error{Message: fmt.Sprint(raised), Internal: true}
			}
			proto = nil
		}
	}()
	if chunk == "" {
		chunk = string(source)
	}
	chunk, _, _ = strings.Cut(chunk, "\x00")
	return translateRedis(parseRedis(source, chunk, depth)), nil
}
