package lua

import "math"

func (s *State) openMath() {
	s.randomState = 0x1234abcd330e
	trig := Math{Sin: math.Sin, Cos: math.Cos, Tan: math.Tan, Asin: math.Asin, Acos: defaultAcos, Atan: math.Atan, Atan2: math.Atan2}
	if custom := s.options.Math; custom != nil {
		for _, pair := range []struct {
			target *func(float64) float64
			source func(float64) float64
		}{
			{&trig.Sin, custom.Sin}, {&trig.Cos, custom.Cos}, {&trig.Tan, custom.Tan},
			{&trig.Asin, custom.Asin}, {&trig.Acos, custom.Acos}, {&trig.Atan, custom.Atan},
		} {
			if pair.source != nil {
				*pair.target = pair.source
			}
		}
		if custom.Atan2 != nil {
			trig.Atan2 = custom.Atan2
		}
	}
	functions := make(map[string]GoFunction)
	for name, operation := range map[string]func(float64) float64{
		"abs": math.Abs, "acos": trig.Acos, "asin": trig.Asin, "atan": trig.Atan,
		"ceil": math.Ceil, "cos": trig.Cos, "cosh": fdCosh, "exp": armExp,
		"floor": math.Floor, "log": armLog, "log10": fdLog10,
		"sin": trig.Sin, "sinh": fdSinh, "sqrt": math.Sqrt, "tan": trig.Tan, "tanh": fdTanh,
		"deg": func(x float64) float64 { return x / (math.Pi / 180) },
		"rad": func(x float64) float64 { return x * (math.Pi / 180) },
	} {
		functions[name] = func(s *State, args []Value) ([]Value, error) {
			input := s.checkNumber(args, 0)
			value := operation(input)
			if name != "log10" && math.IsNaN(value) && !math.IsNaN(input) {
				sign := -1.0
				if name == "acos" || name == "asin" {
					sign = 1
				}
				value = math.Copysign(value, sign)
			}
			return []Value{Number(value)}, nil
		}
	}
	functions["atan2"] = func(s *State, args []Value) ([]Value, error) {
		right := s.checkNumber(args, 1)
		return []Value{Number(trig.Atan2(s.checkNumber(args, 0), right))}, nil
	}
	functions["fmod"] = func(s *State, args []Value) ([]Value, error) {
		b := s.checkNumber(args, 1)
		a := s.checkNumber(args, 0)
		value := math.Mod(a, b)
		if math.IsNaN(a) {
			value = a
		} else if math.IsNaN(b) {
			value = b
		}
		if math.IsNaN(value) && !math.IsNaN(a) && !math.IsNaN(b) {
			value = math.Copysign(value, -1)
		}
		return []Value{Number(value)}, nil
	}
	functions["pow"] = func(s *State, args []Value) ([]Value, error) {
		right := s.checkNumber(args, 1)
		return []Value{Number(luaPower(s.checkNumber(args, 0), right))}, nil
	}
	functions["modf"] = func(s *State, args []Value) ([]Value, error) {
		input := s.checkNumber(args, 0)
		whole, fraction := math.Modf(input)
		if math.IsInf(input, 0) {
			fraction = math.Copysign(0, input)
		}
		return []Value{Number(whole), Number(fraction)}, nil
	}
	functions["frexp"] = func(s *State, args []Value) ([]Value, error) {
		fraction, exponent := math.Frexp(s.checkNumber(args, 0))
		return []Value{Number(fraction), Number(float64(exponent))}, nil
	}
	functions["ldexp"] = func(s *State, args []Value) ([]Value, error) {
		exponent := s.checkInt(args, 1)
		return []Value{Number(math.Ldexp(s.checkNumber(args, 0), exponent))}, nil
	}
	for _, name := range []string{"min", "max"} {
		functions[name] = func(s *State, args []Value) ([]Value, error) {
			result := s.checkNumber(args, 0)
			for index := 1; index < len(args); index++ {
				value := s.checkNumber(args, index)
				if name == "min" && value < result || name == "max" && value > result {
					result = value
				}
			}
			return []Value{Number(result)}, nil
		}
	}
	functions["randomseed"] = func(s *State, args []Value) ([]Value, error) {
		s.randomState = uint64(uint32(s.checkInt(args, 0)))<<16 | 0x330e
		return nil, nil
	}
	functions["random"] = func(s *State, args []Value) ([]Value, error) {
		s.randomState = (s.randomState*0x5deece66d + 0xb) & ((1 << 48) - 1)
		r := float64((s.randomState>>17)%2147483647) / 2147483647.0
		switch len(args) {
		case 0:
			return []Value{Number(r)}, nil
		case 1:
			upper := s.checkInt(args, 0)
			if upper < 1 {
				s.argError(1, "interval is empty")
			}
			return []Value{Number(math.Floor(r*float64(upper)) + 1)}, nil
		case 2:
			lower, upper := s.checkInt(args, 0), s.checkInt(args, 1)
			if lower > upper {
				s.argError(2, "interval is empty")
			}
			width := int32(upper) - int32(lower) + 1
			return []Value{Number(math.Floor(r*float64(width)) + float64(lower))}, nil
		default:
			s.raise("wrong number of arguments", 1)
		}
		return nil, nil
	}
	table := s.library("math", []string{"abs", "acos", "asin", "atan2", "atan", "ceil", "cosh", "cos", "deg", "exp", "floor", "fmod", "frexp", "ldexp", "log10", "log", "max", "min", "modf", "pow", "rad", "random", "randomseed", "sinh", "sin", "sqrt", "tanh", "tan"}, functions)
	table.set(String("pi"), Number(math.Pi))
	table.set(String("huge"), Number(math.Inf(1)))
	table.set(String("mod"), table.RawGet(String("fmod")))
}

func defaultAcos(x float64) float64 {
	if x > 0.5 && x <= 1 {
		return 2 * math.Asin(math.Sqrt(float64((1-x)*0.5)))
	}
	if x < -0.5 && x >= -1 {
		return math.Pi - 2*math.Asin(math.Sqrt(float64((1+x)*0.5)))
	}
	return math.Acos(x)
}
