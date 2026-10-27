package redis_test

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/acneism/lua/internal/redistest"
)

func pick(random *rand.Rand, items ...string) string { return items[random.IntN(len(items))] }

func scriptSetup(random *rand.Rand, key string) []string {
	switch random.IntN(7) {
	case 0:
		return []string{"SET", key, pick(random, "1", "abc", "10.5", "-7", "")}
	case 1:
		return []string{"RPUSH", key, "a", "b", "c"}
	case 2:
		return []string{"HSET", key, "f1", "1", "f2", "x"}
	case 3:
		return []string{"SADD", key, "a", "b", "1"}
	case 4:
		return []string{"ZADD", key, "1", "a", "2.5", "b"}
	case 5:
		return []string{"SADD", key, "3", "1", "2"}
	default:
		return []string{"SET", key, "9223372036854775806"}
	}
}

func scriptCommand(random *rand.Rand) string {
	k := func() string { return pick(random, "KEYS[1]", "KEYS[2]", "'k2'", "'k3'") }
	v := func() string {
		return pick(random, "'v'", "''", "'a b'", "'x\\0y'", "'10'", "'-3'", "'1.5'", "'abc'", "'0x10'", "' 7 '", "ARGV[1]", "ARGV[2]",
			"0", "1", "-1", "42", "-17", "1.5", "-2.25", "0.1", "1e20", "2^53", "2^63", "-2^63", "1/0", "-1/0", "0/0", "-0.0", "1e-7", "123456789012", "3.14159265358979")
	}
	n := func() string { return pick(random, "0", "1", "-1", "2", "5", "-3", "'2'", "1.5", "'x'", "100", "2^31") }
	f := func() string { return pick(random, "'f1'", "'f2'", "'f3'", "1") }
	m := func() string { return pick(random, "'a'", "'b'", "'c'", "1", "2", "'1'") }
	templates := []func() string{
		func() string { return "'SET', " + k() + ", " + v() },
		func() string { return "'SET', " + k() + ", " + v() + ", 'NX'" },
		func() string { return "'GET', " + k() },
		func() string { return "'INCR', " + k() },
		func() string { return "'DECR', " + k() },
		func() string { return "'INCRBY', " + k() + ", " + n() },
		func() string { return "'INCRBYFLOAT', " + k() + ", " + v() },
		func() string { return "'APPEND', " + k() + ", " + v() },
		func() string { return "'STRLEN', " + k() },
		func() string { return "'GETRANGE', " + k() + ", " + n() + ", " + n() },
		func() string {
			return "'SETRANGE', " + k() + ", " + pick(random, "0", "1", "3", "-1", "'x'") + ", " + v()
		},
		func() string { return "'MGET', " + k() + ", " + k() },
		func() string { return "'MSET', " + k() + ", " + v() + ", " + k() + ", " + v() },
		func() string { return "'DEL', " + k() },
		func() string { return "'EXISTS', " + k() + ", " + k() },
		func() string { return "'TYPE', " + k() },
		func() string { return "'OBJECT', 'ENCODING', " + k() },
		func() string { return "'LPUSH', " + k() + ", " + v() },
		func() string { return "'RPUSH', " + k() + ", " + v() + ", " + v() },
		func() string { return "'LRANGE', " + k() + ", " + n() + ", " + n() },
		func() string { return "'LPOP', " + k() },
		func() string { return "'RPOP', " + k() },
		func() string { return "'LLEN', " + k() },
		func() string { return "'LINDEX', " + k() + ", " + n() },
		func() string { return "'LSET', " + k() + ", " + n() + ", " + v() },
		func() string { return "'HSET', " + k() + ", " + f() + ", " + v() },
		func() string { return "'HGET', " + k() + ", " + f() },
		func() string { return "'HGETALL', " + k() },
		func() string { return "'HMGET', " + k() + ", " + f() + ", " + f() },
		func() string { return "'HINCRBY', " + k() + ", " + f() + ", " + n() },
		func() string { return "'HINCRBYFLOAT', " + k() + ", " + f() + ", " + v() },
		func() string { return "'HDEL', " + k() + ", " + f() },
		func() string { return "'HLEN', " + k() },
		func() string { return "'HEXISTS', " + k() + ", " + f() },
		func() string { return "'SADD', " + k() + ", " + m() },
		func() string { return "'SREM', " + k() + ", " + m() },
		func() string { return "'SISMEMBER', " + k() + ", " + m() },
		func() string { return "'SCARD', " + k() },
		func() string { return "'SMEMBERS', " + k() },
		func() string { return "'ZADD', " + k() + ", " + v() + ", " + m() },
		func() string { return "'ZSCORE', " + k() + ", " + m() },
		func() string { return "'ZINCRBY', " + k() + ", " + v() + ", " + m() },
		func() string { return "'ZRANGE', " + k() + ", 0, -1, 'WITHSCORES'" },
		func() string { return "'ZCARD', " + k() },
		func() string { return "'ZRANK', " + k() + ", " + m() },
		func() string { return "'PFADD', " + k() + ", " + m() },
		func() string { return "'PFCOUNT', " + k() },
		func() string { return "'ECHO', " + v() },
		func() string { return "'PING'" },
		func() string { return "'PING', " + v() },
		func() string { return "'DBSIZE'" },
		func() string { return "'NOPE'" },
		func() string { return "'GET'" },
		func() string { return "'MULTI'" },
		func() string { return "'SUBSCRIBE', 'c'" },
		func() string { return "'SET', " + k() + ", " + pick(random, "{}", "true", "nil") },
		func() string { return "" },
		func() string { return v() },
	}
	return templates[random.IntN(len(templates))]()
}

const describeSource = "local function d(x) if type(x) == 'table' then local s = {} for k, v in pairs(x) do s[#s + 1] = tostring(k) .. '=' .. d(v) end return '{' .. table.concat(s, ',') .. '}' end return type(x) .. ':' .. tostring(x) end\n"

func scriptCase(random *rand.Rand) redistest.Case {
	item := redistest.Case{Keys: []string{"k0", "k1"}, Args: []string{fmt.Sprint(random.IntN(200) - 100), "a b"}}
	keys := random.Perm(4)
	for _, index := range keys[:random.IntN(4)] {
		item.Setup = append(item.Setup, scriptSetup(random, fmt.Sprintf("k%d", index)))
	}
	item.ReadOnly = random.IntN(10) == 0
	var source strings.Builder
	if random.IntN(12) == 0 {
		source.WriteString("#!lua flags=no-writes\n")
	}
	source.WriteString(describeSource)
	count := 1 + random.IntN(5)
	for i := 1; i <= count; i++ {
		call := scriptCommand(random)
		switch random.IntN(7) {
		case 0, 1:
			fmt.Fprintf(&source, "local r%d = redis.call(%s)\n", i, call)
		case 2, 3:
			fmt.Fprintf(&source, "local r%d = redis.pcall(%s)\n", i, call)
		case 4:
			fmt.Fprintf(&source, "local r%d = {pcall(redis.call, %s)}\n", i, call)
		case 5:
			fmt.Fprintf(&source, "local r%d = select('#', redis.pcall(%s))\n", i, call)
		default:
			fmt.Fprintf(&source, "local r%d = d(redis.pcall(%s))\n", i, call)
		}
	}
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("r%d", i+1)
	}
	last := names[count-1]
	switch random.IntN(12) {
	case 0, 1, 2:
		fmt.Fprintf(&source, "return {%s}", strings.Join(names, ", "))
	case 3:
		fmt.Fprintf(&source, "return %s", last)
	case 4:
		fmt.Fprintf(&source, "return d(%s)", last)
	case 5:
		fmt.Fprintf(&source, "error(type(%s) == 'table' and type(rawget(%s, 'err')) ~= 'string' and d(%s) or %s)", last, last, last, last)
	case 6:
		fmt.Fprintf(&source, "error(type(%s) == 'table' and type(rawget(%s, 'err')) ~= 'string' and d(%s) or %s, 0)", last, last, last, last)
	case 7:
		fmt.Fprintf(&source, "return redis.error_reply(tostring(type(%s) == 'table' and d(%s) or %s))", last, last, last)
	case 8:
		fmt.Fprintf(&source, "return redis.status_reply(type(%s) == 'table' and d(%s) or tostring(%s))", last, last, last)
	case 9:
		fmt.Fprintf(&source, "return {tonumber(%s) or 'nan-ish', type(%s)}", last, last)
	case 10:
		fmt.Fprintf(&source, "return {err = 'E ' .. d(%s)}", last)
	default:
		fmt.Fprintf(&source, "return {%s, {%s}, d(%s)}", last, last, last)
	}
	item.Source = source.String()
	return item
}

func luaNumber(random *rand.Rand) string {
	return pick(random, "0", "1", "-1", "2", "3", "7", "-7", "10", "255", "1.5", "-2.25", "0.1", "1/3", "2/3", "1e15", "1e16", "1e100", "-1e-5",
		"2^53", "2^63", "2^31", "3.14159265358979", "123456789012345", "0.5", "-0.0", "1/0", "-1/0", "0/0", "100", "17", "-3.5", "1e-300")
}

func luaString(random *rand.Rand) string {
	return pick(random, "''", "'a'", "'abc'", "'hello world'", "'10'", "' 10 '", "'0x1F'", "'0X1f'", "'1e2'", "'.5'", "'5.'", "'-.5e-3'", "'inf'", "'nan'",
		"'1e'", "'0x'", "'  0x10  '", "'1 2'", "'abc123'", "'AbC'", "'a,b,,c'", "'x\\0y'", "'(a(b)c)'", "'%d+'", "'key=val; k2=v2'", "'  spaced  out  '",
		"'0'", "'-0'", "'+5'", "'1e400'", "'0x7fffffff'", "'zz'", "'1010'", "'ff'", "'Z9'")
}

func luaOperand(random *rand.Rand) string {
	switch random.IntN(10) {
	case 0, 1, 2, 3, 4:
		return luaNumber(random)
	case 5, 6, 7, 8:
		return luaString(random)
	default:
		return pick(random, "nil", "true", "{}")
	}
}

func luaValue(random *rand.Rand) string {
	switch random.IntN(4) {
	case 0:
		return "I(" + luaNumber(random) + ")"
	case 1:
		return luaNumber(random)
	}
	return pick(random, luaString(random), luaString(random), "nil", "true")
}

func luaArith(random *rand.Rand, depth int) string {
	if depth == 0 || random.IntN(3) == 0 {
		return luaValue(random)
	}
	operator := pick(random, "+", "-", "*", "/", "%", "^", "..")
	if random.IntN(8) == 0 {
		return "(-" + luaArith(random, depth-1) + ")"
	}
	return "(" + luaArith(random, depth-1) + " " + operator + " " + luaArith(random, depth-1) + ")"
}

func luaFormat(random *rand.Rand) string {
	conversion := pick(random, "d", "i", "c", "o", "u", "x", "X", "e", "E", "f", "g", "G", "q", "s", "%")
	spec := "%"
	for range random.IntN(3) {
		spec += pick(random, "-", "+", " ", "#", "0")
	}
	if random.IntN(2) == 0 {
		spec += fmt.Sprint(random.IntN(21))
	}
	if random.IntN(2) == 0 {
		spec += "." + fmt.Sprint(random.IntN(18))
	}
	spec += conversion
	if random.IntN(15) == 0 {
		spec = pick(random, "%z", "%", "%5", "%.", "%123d", "%-+ #0-+ #0d")
	}
	argument := luaNumber(random)
	switch conversion {
	case "s", "q":
		argument = luaOperand(random)
	case "c":
		argument = pick(random, "65", "97", "0", "255", "10", "34", "92")
	case "%":
		argument = "nil"
	}
	if random.IntN(10) == 0 {
		argument = luaString(random)
	}
	format := strings.ReplaceAll(spec, "'", "\\'")
	if random.IntN(3) == 0 {
		format = "<" + format + ">" + pick(random, "", " %s", " %d")
		return fmt.Sprintf("string.format('%s', %s, %s)", format, argument, luaOperand(random))
	}
	return fmt.Sprintf("string.format('%s', %s)", format, argument)
}

func luaPatternItem(random *rand.Rand, captures *int) string {
	switch random.IntN(14) {
	case 0, 1:
		return pick(random, "a", "b", "c", "1", " ", "=", ";", "x", "%.", "%%")
	case 2:
		return "."
	case 3, 4:
		return pick(random, "%a", "%d", "%l", "%s", "%u", "%w", "%x", "%p", "%c", "%z", "%A", "%D", "%S", "%W")
	case 5:
		return pick(random, "[abc]", "[^%d]", "[a-f]", "[%a_]", "[^ ]", "[]]", "[%]x]", "[a-]", "[^%s=;]")
	case 6:
		return "%b()"
	case 7:
		return pick(random, "%f[%w]", "%f[%W]", "%f[%d]")
	case 8:
		if *captures < 3 {
			*captures++
			return "(" + luaPatternItem(random, captures) + luaPatternItem(random, captures) + ")"
		}
		return "a"
	case 9:
		if *captures < 3 {
			*captures++
			return "()"
		}
		return "b"
	case 10:
		if *captures > 0 {
			return "%" + fmt.Sprint(1+random.IntN(*captures))
		}
		return "c"
	default:
		item := pick(random, "a", ".", "%w", "%d", "[abc]", "%s", "x")
		return item + pick(random, "*", "+", "-", "?")
	}
}

func luaPattern(random *rand.Rand) string {
	if random.IntN(25) == 0 {
		return pick(random, "[a", "%", "(", "(()", "%b", "%f", "%g", "a)", "%1", "[%", "((((((((((((((((((((((((((((((((a))))))))))))))))))))))))))))))))")
	}
	captures := 0
	var pattern strings.Builder
	if random.IntN(5) == 0 {
		pattern.WriteString("^")
	}
	for range 1 + random.IntN(4) {
		pattern.WriteString(luaPatternItem(random, &captures))
	}
	if random.IntN(6) == 0 {
		pattern.WriteString("$")
	}
	return pattern.String()
}

func luaPatternCall(random *rand.Rand) string {
	subject := pick(random, "'hello world'", "'abc def ghi'", "'aaa bbb'", "'f(a(b)c)d'", "'key=val; k2=v2'", "'  x1 y22 z333  '", "'THE (quick) fox'", "''", "'a.b.c'", "'x\\0y'", "'100 + 200 = 300'")
	pattern := strings.ReplaceAll(luaPattern(random), "'", "\\'")
	switch random.IntN(7) {
	case 0:
		return fmt.Sprintf("string.find(%s, '%s', %s)", subject, pattern, pick(random, "1", "2", "-3", "0", "100", "nil"))
	case 1:
		return fmt.Sprintf("string.find(%s, '%s', 1, true)", subject, pattern)
	case 2:
		return fmt.Sprintf("string.match(%s, '%s', %s)", subject, pattern, pick(random, "1", "3", "-5", "nil"))
	case 3:
		return fmt.Sprintf("(function() local r = {} for a, b in string.gmatch(%s, '%s') do r[#r + 1] = tostring(a) .. '/' .. tostring(b) if #r > 20 then break end end return table.concat(r, ',') end)()", subject, pattern)
	case 4:
		replacement := pick(random, "'<%0>'", "'%1-'", "'%%'", "'x'", "''", "{a = 'A', b = true, c = 1}", "function(c) return c .. '!' end", "function() return nil end", "function() return false end", "'%2'", "'%9'")
		return fmt.Sprintf("string.gsub(%s, '%s', %s, %s)", subject, pattern, replacement, pick(random, "nil", "1", "2", "0", "-1"))
	case 5:
		return fmt.Sprintf("string.gsub(%s, '%s', '[%%0]')", subject, pattern)
	default:
		return fmt.Sprintf("string.gfind ~= nil and string.find(%s, '%s')", subject, pattern)
	}
}

func luaStringCall(random *rand.Rand) string {
	s := luaString(random)
	i := pick(random, "1", "2", "-1", "-2", "0", "3", "10", "-10", "nil", "'2'", "1.9")
	j := pick(random, "1", "2", "-1", "-2", "0", "3", "10", "-10", "nil", "'3'")
	switch random.IntN(9) {
	case 0:
		return fmt.Sprintf("string.sub(%s, %s, %s)", s, i, j)
	case 1:
		return fmt.Sprintf("string.rep(%s, %s)", s, pick(random, "0", "1", "3", "-1", "2.7", "'2'"))
	case 2:
		return fmt.Sprintf("string.byte(%s, %s, %s)", s, i, j)
	case 3:
		return fmt.Sprintf("string.char(%s, %s)", pick(random, "65", "0", "255", "97", "256", "-1", "'66'"), pick(random, "98", "99", "10"))
	case 4:
		return fmt.Sprintf("string.upper(%s) .. string.lower(%s)", s, s)
	case 5:
		return fmt.Sprintf("string.len(%s) + #%s", s, s)
	case 6:
		return fmt.Sprintf("string.reverse(%s)", s)
	case 7:
		return fmt.Sprintf("(%s):rep(2, nil)", s)
	default:
		return fmt.Sprintf("tostring(%s) .. tostring(%s)", s, luaNumber(random))
	}
}

func luaConvert(random *rand.Rand) string {
	switch random.IntN(5) {
	case 0:
		return fmt.Sprintf("tonumber(%s)", luaString(random))
	case 1:
		return fmt.Sprintf("tonumber(%s, %d)", luaString(random), 2+random.IntN(35))
	case 2:
		return fmt.Sprintf("tostring(%s)", luaNumber(random))
	case 3:
		return fmt.Sprintf("tonumber(%s) == %s", luaString(random), luaNumber(random))
	default:
		return fmt.Sprintf("%s .. ''", luaNumber(random))
	}
}

func luaCompare(random *rand.Rand) string {
	operator := pick(random, "<", "<=", ">", ">=", "==", "~=")
	return luaValue(random) + " " + operator + " " + luaValue(random)
}

func luaArray(random *rand.Rand) string {
	values := make([]string, 1+random.IntN(8))
	for i := range values {
		values[i] = pick(random, "3", "1", "-2", "7.5", "0", "10", "1", "100", "-0.5")
	}
	if random.IntN(4) == 0 {
		for i := range values {
			values[i] = pick(random, "'b'", "'a'", "'ab'", "'B'", "''", "'10'", "'9'")
		}
	}
	return "{" + strings.Join(values, ", ") + "}"
}

func luaTableCall(random *rand.Rand) string {
	t := luaArray(random)
	position := pick(random, "1", "2", "0", "-1", "5", "100")
	switch random.IntN(9) {
	case 0:
		return fmt.Sprintf("(function() local t = %s table.sort(t) return table.concat(t, ',') end)()", t)
	case 1:
		return fmt.Sprintf("(function() local t = %s table.sort(t, function(a, b) return a > b end) return table.concat(t, ',') end)()", t)
	case 2:
		return fmt.Sprintf("table.concat(%s, %s, %s, %s)", t, pick(random, "','", "''", "nil", "1"), pick(random, "1", "2", "nil", "0"), pick(random, "nil", "3", "-1", "10"))
	case 3:
		return fmt.Sprintf("(function() local t = %s table.insert(t, %s, 'n') return table.concat(t, ',') end)()", t, position)
	case 4:
		return fmt.Sprintf("(function() local t = %s local r = table.remove(t, %s) return tostring(r) .. ':' .. table.concat(t, ',') end)()", t, position)
	case 5:
		return fmt.Sprintf("select('#', unpack(%s, %s, %s))", t, pick(random, "1", "2", "-1", "nil"), pick(random, "nil", "3", "0", "10"))
	case 6:
		return fmt.Sprintf("(function() local t = %s t[%s] = nil t[%s] = 'x' return #t end)()", t, pick(random, "1", "2", "3", "#t"), pick(random, "10", "5", "#t + 1", "#t + 2"))
	case 7:
		var b strings.Builder
		b.WriteString("(function() local t = {} ")
		for range 3 + random.IntN(10) {
			key := pick(random, "'a'", "'b'", "'key'", "'x1'", "'x2'", "'long_key_name'", "1", "2", "3", "10", "1.5", "-1", "true", "'1'")
			if random.IntN(5) == 0 {
				fmt.Fprintf(&b, "t[%s] = nil ", key)
			} else {
				fmt.Fprintf(&b, "t[%s] = %d ", key, random.IntN(100))
			}
		}
		b.WriteString("local r = {} for k, v in pairs(t) do r[#r + 1] = tostring(k) .. '=' .. tostring(v) end return table.concat(r, ',') .. '#' .. #t end)()")
		return b.String()
	default:
		return fmt.Sprintf("table.maxn(%s) + table.getn(%s)", t, t)
	}
}

func luaMath(random *rand.Rand) string {
	x := luaNumber(random)
	switch random.IntN(9) {
	case 0:
		return fmt.Sprintf("math.floor(%s), math.ceil(%s)", x, x)
	case 1:
		return fmt.Sprintf("math.abs(%s)", x)
	case 2:
		return fmt.Sprintf("math.max(%s, %s, %s), math.min(%s, %s)", x, luaNumber(random), luaNumber(random), x, luaNumber(random))
	case 3:
		return fmt.Sprintf("math.fmod(%s, %s)", x, luaNumber(random))
	case 4:
		return fmt.Sprintf("math.modf(%s)", x)
	case 5:
		return fmt.Sprintf("math.sqrt(%s)", x)
	case 6:
		return fmt.Sprintf("(function() math.randomseed(%d) return math.random(), math.random(%d), math.random(-5, %d) end)()", random.IntN(100000)-50000, 1+random.IntN(1000), random.IntN(20)-5)
	case 7:
		return fmt.Sprintf("math.mod(%s, %s), math.huge, -math.huge", x, luaNumber(random))
	default:
		return fmt.Sprintf("math.floor(%s / %s)", x, luaNumber(random))
	}
}

func luaMisc(random *rand.Rand) string {
	switch random.IntN(8) {
	case 0:
		return fmt.Sprintf("type(%s)", luaOperand(random))
	case 1:
		return fmt.Sprintf("select('#', %s, nil, %s)", luaOperand(random), luaOperand(random))
	case 2:
		return fmt.Sprintf("select(%s, 'a', 'b', 'c')", pick(random, "1", "2", "-1", "-3", "0", "4", "'#'"))
	case 3:
		return fmt.Sprintf("assert(%s, %s)", luaOperand(random), luaString(random))
	case 4:
		return fmt.Sprintf("error(%s, %s)", luaOperand(random), pick(random, "0", "1", "2", "nil"))
	case 5:
		return fmt.Sprintf("rawequal(%s, %s)", luaOperand(random), luaOperand(random))
	case 6:
		return fmt.Sprintf("(function(...) return select('#', ...), ... end)(%s, %s)", luaOperand(random), luaOperand(random))
	default:
		return fmt.Sprintf("next({}, %s)", pick(random, "nil", "1", "'x'"))
	}
}

const probeSource = "local function I(x) return x end local out = {} local function probe(f) local function pack(...) return {n = select('#', ...), ...} end local r = pack(pcall(f)) local s = {} for i = 1, r.n do local v = r[i] local t = type(v) if t == 'table' or t == 'function' or t == 'thread' or t == 'userdata' then s[i] = t else s[i] = tostring(v) end end out[#out + 1] = table.concat(s, ',') end\n"

func luaCase(random *rand.Rand) string {
	var source strings.Builder
	source.WriteString(probeSource)
	for range 2 + random.IntN(7) {
		var expression string
		switch random.IntN(11) {
		case 0, 1:
			expression = luaArith(random, 3)
		case 2:
			expression = luaFormat(random)
		case 3, 4:
			expression = luaPatternCall(random)
		case 5:
			expression = luaStringCall(random)
		case 6:
			expression = luaConvert(random)
		case 7:
			expression = luaCompare(random)
		case 8:
			expression = luaTableCall(random)
		case 9:
			expression = luaMath(random)
		default:
			expression = luaMisc(random)
		}
		fmt.Fprintf(&source, "probe(function() return %s end)\n", expression)
	}
	source.WriteString("return table.concat(out, ' | ')")
	return source.String()
}
