package lua

import "math"

func armExp(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 1) {
		return x + 1
	}
	if math.IsInf(x, -1) {
		return 0
	}
	return armExpTail(x, 0, 0)
}

func armExpTail(x, xtail float64, sign uint64) float64 {
	top := uint32(math.Float64bits(x)>>52) & 0x7ff
	if top < 0x3c9 {
		return math.Copysign(1+x, math.Float64frombits(sign<<45))
	}
	if top >= 0x409 {
		if x < 0 {
			return math.Float64frombits(sign << 45)
		}
		return math.Copysign(math.Inf(1), math.Float64frombits(sign<<45))
	}
	kd := math.FMA(0x1.71547652b82fep0*128, x, 0x1.8p52)
	ki := math.Float64bits(kd)
	kd -= 0x1.8p52
	r := math.FMA(kd, -0x1.62e42fefa0000p-8, x)
	r = math.FMA(kd, -0x1.cf79abc9e3b3ap-47, r)
	r += xtail
	index := int(2 * (ki % 128))
	tail := armExpTab(index)
	bits := math.Float64bits(armExpTab(index+1)) + ((ki + sign) << 45)
	r2 := float64(r * r)
	p := math.FMA(r2, math.FMA(r, armExpPoly(1), armExpPoly(0)), tail+r)
	p = math.FMA(float64(r2*r2), math.FMA(r, armExpPoly(3), armExpPoly(2)), p)
	if top >= 0x408 {
		return armExpSpecial(p, bits, ki)
	}
	scale := math.Float64frombits(bits)
	return math.FMA(scale, p, scale)
}

func armExpSpecial(tmp float64, bits, ki uint64) float64 {
	if ki&0x80000000 == 0 {
		bits -= uint64(1009) << 52
		scale := math.Float64frombits(bits)
		return 0x1p1009 * math.FMA(scale, tmp, scale)
	}
	bits += uint64(1022) << 52
	scale := math.Float64frombits(bits)
	product := float64(scale * tmp)
	y := scale + product
	if math.Abs(y) < 1 {
		one := math.Copysign(1, y)
		lo := scale - y + product
		hi := one + y
		lo = one - hi + y + lo
		y = float64(hi+lo) - one
		if y == 0 {
			y = math.Copysign(0, scale)
		}
	}
	return 0x1p-1022 * y
}

func armLog(x float64) float64 {
	ix := math.Float64bits(x)
	if ix-0x3fee000000000000 < 0x3ff1090000000000-0x3fee000000000000 {
		if x == 1 {
			return 0
		}
		r := x - 1
		r2 := float64(r * r)
		r3 := float64(r * r2)
		b := armLogPoly1
		p := math.FMA(r3, b(10), math.FMA(r2, b(9), math.FMA(r, b(8), b(7))))
		p = math.FMA(r3, p, math.FMA(r2, b(6), math.FMA(r, b(5), b(4))))
		p = math.FMA(r3, p, math.FMA(r2, b(3), math.FMA(r, b(2), b(1))))
		w := float64(r * 0x1p27)
		rhi := r + w - w
		rlo := r - rhi
		square := float64(rhi * rhi)
		hi := math.FMA(square, b(0), r)
		lo := math.FMA(square, b(0), r-hi)
		lo = math.FMA(float64(b(0)*rlo), rhi+r, lo)
		return math.FMA(r3, p, lo) + hi
	}
	top := uint32(ix >> 48)
	if top-0x10 >= 0x7ff0-0x10 {
		if ix*2 == 0 {
			return math.Inf(-1)
		}
		if math.IsInf(x, 1) {
			return x
		}
		if math.IsNaN(x) {
			return x + x
		}
		if x < 0 {
			return math.Float64frombits(0xfff8000000000000)
		}
		ix = math.Float64bits(x*0x1p52) - (uint64(52) << 52)
	}
	tmp := ix - 0x3fe6000000000000
	index := int((tmp >> 45) % 128)
	k := float64(int64(tmp) >> 52)
	z := math.Float64frombits(ix - (tmp & (uint64(0xfff) << 52)))
	r := math.FMA(z, armLogTab(index*2), -1)
	w := math.FMA(k, 0x1.62e42fefa3800p-1, armLogTab(index*2+1))
	hi := w + r
	lo := math.FMA(k, 0x1.ef35793c76730p-45, w-hi+r)
	r2 := float64(r * r)
	p := math.FMA(r2, math.FMA(r, armLogPoly(4), armLogPoly(3)), math.FMA(r, armLogPoly(2), armLogPoly(1)))
	y := math.FMA(float64(r*r2), p, math.FMA(r2, armLogPoly(0), lo))
	return y + hi
}

func armPowLog(ix uint64) (float64, float64) {
	tmp := ix - 0x3fe6955500000000
	index := int((tmp>>45)%128) * 4
	k := float64(int64(tmp) >> 52)
	z := math.Float64frombits(ix - (tmp & (uint64(0xfff) << 52)))
	r := math.FMA(z, armPowTab(index), -1)
	t1 := math.FMA(k, 0x1.62e42fefa3800p-1, armPowTab(index+2))
	t2 := t1 + r
	lo1 := math.FMA(k, 0x1.ef35793c76730p-45, armPowTab(index+3))
	lo2 := t1 - t2 + r
	ar := float64(armPowPoly(0) * r)
	ar2 := float64(r * ar)
	ar3 := float64(r * ar2)
	hi := t2 + ar2
	lo3 := math.FMA(ar, r, -ar2)
	lo4 := t2 - hi + ar2
	p := math.FMA(ar2, math.FMA(r, armPowPoly(6), armPowPoly(5)), math.FMA(r, armPowPoly(4), armPowPoly(3)))
	p = math.FMA(ar2, p, math.FMA(r, armPowPoly(2), armPowPoly(1)))
	lo := math.FMA(ar3, p, lo1+lo2+lo3+lo4)
	y := hi + lo
	return y, hi - y + lo
}

func armPowInteger(bits uint64) int {
	e := int((bits >> 52) & 0x7ff)
	if e < 0x3ff {
		return 0
	}
	if e > 0x3ff+52 {
		return 2
	}
	shift := uint(0x3ff + 52 - e)
	if bits&((uint64(1)<<shift)-1) != 0 {
		return 0
	}
	if bits&(uint64(1)<<shift) != 0 {
		return 1
	}
	return 2
}

func armPow(x, y float64) float64 {
	ix, iy := math.Float64bits(x), math.Float64bits(y)
	topx, topy := uint32(ix>>52), uint32(iy>>52)
	sign := uint64(0)
	if topx-1 >= 0x7fe || (topy&0x7ff)-0x3be >= 0x43e-0x3be {
		if 2*iy-1 >= 2*uint64(0x7ff0000000000000)-1 {
			if y == 0 || x == 1 {
				return 1
			}
			if math.IsNaN(x) || math.IsNaN(y) {
				return luaNaN(x+y, x, y)
			}
			if math.Abs(x) == 1 {
				return 1
			}
			if (math.Abs(x) < 1) == !math.Signbit(y) {
				return 0
			}
			return y * y
		}
		if 2*ix-1 >= 2*uint64(0x7ff0000000000000)-1 {
			x2 := float64(x * x)
			if math.Signbit(x) && armPowInteger(iy) == 1 {
				x2 = -x2
			}
			if math.Signbit(y) {
				return 1 / x2
			}
			return x2
		}
		if ix>>63 != 0 {
			kind := armPowInteger(iy)
			if kind == 0 {
				return math.Float64frombits(0xfff8000000000000)
			}
			if kind == 1 {
				sign = uint64(0x800) << 7
			}
			ix &= 0x7fffffffffffffff
			topx &= 0x7ff
		}
		if (topy&0x7ff)-0x3be >= 0x43e-0x3be {
			if ix == 0x3ff0000000000000 {
				return 1
			}
			if topy&0x7ff < 0x3be {
				if ix > 0x3ff0000000000000 {
					return 1 + y
				}
				return 1 - y
			}
			if (ix > 0x3ff0000000000000) == (topy < 0x800) {
				return math.Inf(1)
			}
			return 0
		}
		if topx == 0 {
			ix = (math.Float64bits(x*0x1p52) & 0x7fffffffffffffff) - (uint64(52) << 52)
		}
	}
	hi, lo := armPowLog(ix)
	ehi := float64(y * hi)
	elo := math.FMA(y, lo, math.FMA(y, hi, -ehi))
	return armExpTail(ehi, elo, sign)
}

func fdLog10(x float64) float64 {
	if math.IsNaN(x) {
		return x + x
	}
	bits := math.Float64bits(x)
	high := int32(bits >> 32)
	k := 0
	if high < 0x100000 {
		if x == 0 {
			return math.Inf(-1)
		}
		if math.Signbit(x) {
			return math.Float64frombits(0x7ff8000000000000)
		}
		k -= 54
		x *= 0x1p54
		bits = math.Float64bits(x)
		high = int32(bits >> 32)
	}
	if high >= 0x7ff00000 {
		return x + x
	}
	k += int(high>>20) - 1023
	i := int(uint32(k) >> 31)
	high = (high & 0xfffff) | int32((0x3ff-i)<<20)
	x = math.Float64frombits((uint64(uint32(high)) << 32) | (bits & 0xffffffff))
	y := float64(k + i)
	z := float64(y*3.69423907715893078616e-13) + float64(0.434294481903251816668*armLog(x))
	return z + float64(y*0.301029995663611771306)
}

func fdExpm1(x float64) float64 {
	hx := uint32(math.Float64bits(x)>>32) & 0x7fffffff
	negative := math.Signbit(x)
	if hx >= 0x4043687a {
		if math.IsNaN(x) {
			return x + x
		}
		if math.IsInf(x, 1) {
			return x
		}
		if x > 709.782712893383973096 {
			return math.Inf(1)
		}
		if negative {
			return -1
		}
	}
	k := 0
	c := 0.0
	if hx > 0x3fd62e42 {
		var hi, lo float64
		if hx < 0x3ff0a2b2 {
			if negative {
				hi = x + 0.693147180369123816490
				lo = -1.90821492927058770002e-10
				k = -1
			} else {
				hi = x - 0.693147180369123816490
				lo = 1.90821492927058770002e-10
				k = 1
			}
		} else {
			half := 0.5
			if negative {
				half = -half
			}
			k = int(float64(1.44269504088896338700*x) + half)
			t := float64(k)
			hi = math.FMA(-t, 0.693147180369123816490, x)
			lo = float64(t * 1.90821492927058770002e-10)
		}
		x = hi - lo
		c = hi - x - lo
	} else if hx < 0x3c900000 {
		return x
	}
	hfx := float64(0.5 * x)
	hxs := float64(x * hfx)
	h2 := float64(hxs * hxs)
	h4 := float64(h2 * h2)
	p := math.FMA(hxs, -7.93650757867487942473e-5, 1.58730158725481460165e-3)
	q := math.FMA(hxs, -2.01099218183624371326e-7, 4.00821782732936239552e-6)
	r1 := math.FMA(h2, p, math.FMA(hxs, -3.33333333333331316428e-2, 1))
	r1 = math.FMA(h4, q, r1)
	t := math.FMA(-r1, hfx, 3)
	e := float64(hxs * ((r1 - t) / math.FMA(-x, t, 6)))
	if k == 0 {
		return x - math.FMA(x, e, -hxs)
	}
	e = math.FMA(x, e-c, -c)
	e -= hxs
	if k == -1 {
		return math.FMA(0.5, x-e, -0.5)
	}
	if k == 1 {
		if x < -0.25 {
			return -2 * (e - (x + 0.5))
		}
		return math.FMA(2, x-e, 1)
	}
	if k <= -2 || k > 56 {
		y := 1 - (e - x)
		return math.Ldexp(y, k) - 1
	}
	if k < 20 {
		t = math.Float64frombits(uint64(0x3ff00000-(0x200000>>uint(k))) << 32)
		return math.Ldexp(t-(e-x), k)
	}
	t = math.Float64frombits(uint64((0x3ff-k)<<20) << 32)
	y := x - (e + t)
	return math.Ldexp(y+1, k)
}

func fdSinh(x float64) float64 {
	bits := math.Float64bits(x) & 0x7fffffffffffffff
	high := uint32(bits >> 32)
	if high >= 0x7ff00000 {
		return x + x
	}
	h := math.Copysign(0.5, x)
	if high < 0x40360000 {
		if high < 0x3e300000 {
			return x
		}
		t := fdExpm1(math.Abs(x))
		if high < 0x3ff00000 {
			return h * (float64(2*t) - float64(t*t)/(t+1))
		}
		return h * (t + t/(t+1))
	}
	if high < 0x40862e42 {
		return h * armExp(math.Abs(x))
	}
	if bits <= 0x408633ce8fb9f87d {
		w := armExp(0.5 * math.Abs(x))
		return float64(h*w) * w
	}
	return x * 1e307
}

func fdCosh(x float64) float64 {
	bits := math.Float64bits(x) & 0x7fffffffffffffff
	high := uint32(bits >> 32)
	if high >= 0x7ff00000 {
		return x * x
	}
	if high < 0x3fd62e43 {
		t := fdExpm1(math.Abs(x))
		w := 1 + t
		if high < 0x3c800000 {
			return w
		}
		return 1 + float64(t*t)/(w+w)
	}
	if high < 0x40360000 {
		t := armExp(math.Abs(x))
		return math.FMA(0.5, t, 0.5/t)
	}
	if high < 0x40862e42 {
		return 0.5 * armExp(math.Abs(x))
	}
	if bits <= 0x408633ce8fb9f87d {
		w := armExp(0.5 * math.Abs(x))
		return float64(0.5*w) * w
	}
	return math.Inf(1)
}

func fdTanh(x float64) float64 {
	high := uint32(math.Float64bits(x)>>32) & 0x7fffffff
	if high >= 0x7ff00000 {
		if math.Signbit(x) {
			return 1/x - 1
		}
		return 1/x + 1
	}
	if high < 0x3c800000 {
		return x * float64(1+x)
	}
	z := 1.0
	if high < 0x40360000 {
		if high >= 0x3ff00000 {
			t := fdExpm1(2 * math.Abs(x))
			z = 1 - 2/(t+2)
		} else {
			t := fdExpm1(-2 * math.Abs(x))
			z = -t / (t + 2)
		}
	}
	return math.Copysign(z, x)
}
