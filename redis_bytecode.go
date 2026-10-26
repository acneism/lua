package lua

type redisOpcode uint32

const (
	rMove redisOpcode = iota
	rLoadK
	rLoadBool
	rLoadNil
	rGetUpval
	rGetGlobal
	rGetTable
	rSetGlobal
	rSetUpval
	rSetTable
	rNewTable
	rSelf
	rAdd
	rSub
	rMul
	rDiv
	rMod
	rPow
	rUnm
	rNot
	rLen
	rConcat
	rJmp
	rEq
	rLt
	rLe
	rTest
	rTestSet
	rCall
	rTailCall
	rReturn
	rForLoop
	rForPrep
	rTForLoop
	rSetList
	rClose
	rClosure
	rVararg
)

const redisNoRegister = 255
const redisMaxJump = 131071
const redisRK = 256

type redisInstruction uint32

func (i redisInstruction) op() redisOpcode { return redisOpcode(i & 63) }
func (i redisInstruction) a() int          { return int(i >> 6 & 255) }
func (i redisInstruction) b() int          { return int(i >> 23) }
func (i redisInstruction) c() int          { return int(i >> 14 & 511) }
func (i redisInstruction) bx() int         { return int(i >> 14) }
func (i redisInstruction) sbx() int        { return i.bx() - redisMaxJump }
func (i *redisInstruction) setA(a int)     { *i = *i&^(255<<6) | redisInstruction(a)<<6 }
func (i *redisInstruction) setB(b int)     { *i = *i&^(511<<23) | redisInstruction(b)<<23 }
func (i *redisInstruction) setC(c int)     { *i = *i&^(511<<14) | redisInstruction(c)<<14 }
func (i *redisInstruction) setSBx(b int)   { *i = *i&((1<<14)-1) | redisInstruction(b+redisMaxJump)<<14 }
func redisABC(op redisOpcode, a, b, c int) redisInstruction {
	return redisInstruction(op) | redisInstruction(a)<<6 | redisInstruction(b)<<23 | redisInstruction(c)<<14
}
func redisABx(op redisOpcode, a, b int) redisInstruction {
	return redisInstruction(op) | redisInstruction(a)<<6 | redisInstruction(b)<<14
}
func redisTestMode(op redisOpcode) bool { return op >= rEq && op <= rTestSet }

type redisLocal struct {
	name       string
	start, end int
}

type redisProto struct {
	code                         []redisInstruction
	lines                        []int32
	constants                    []Value
	children                     []*redisProto
	locals                       []redisLocal
	upvalues                     []string
	source                       string
	line, lastLine               int
	parameters, vararg, maxStack int
}

type redisExpKind uint8

const (
	rVoid redisExpKind = iota
	rNil
	rFalse
	rTrue
	rK
	rNumber
	rLocal
	rUpvalue
	rGlobal
	rIndexed
	rJump
	rReloc
	rRegister
	rCallExp
	rVarargExp
)

type redisExp struct {
	k         redisExpKind
	info, aux int
	number    float64
	t, f      int
}

func redisExpression(k redisExpKind, info int) redisExp {
	return redisExp{k: k, info: info, t: -1, f: -1}
}
func (e *redisExp) jumps() bool        { return e.t != e.f }
func (e *redisExp) numeral() bool      { return e.k == rNumber && e.t == -1 && e.f == -1 }
func redisMultret(k redisExpKind) bool { return k == rCallExp || k == rVarargExp }

type redisBlock struct {
	previous      *redisBlock
	breaks        int
	locals        int
	upvalue, loop bool
}

type redisUpvalue struct {
	k     redisExpKind
	index int
}

type redisFunc struct {
	p                                 *redisProto
	parent                            *redisFunc
	ls                                *redisParser
	block                             *redisBlock
	lastTarget, pending, free, active int
	actvar                            [200]int
	upvalues                          []redisUpvalue
	constants                         map[string]int
	numbers                           map[float64]int
	nilConstant                       int
	jumpTails                         map[int]int
}

func (f *redisFunc) pc() int { return len(f.p.code) }
