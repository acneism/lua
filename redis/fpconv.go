package redis

import (
	"math"
	"strconv"
)

type diyFloat struct {
	frac uint64
	exp  int
}

const (
	fpconvHiddenBit uint64 = 1 << 52
	fpconvExpBias          = 1023 + 52
)

var fpconvPowers = [...]diyFloat{
	{18054884314459144840, -1220},
	{13451937075301367670, -1193},
	{10022474136428063862, -1166},
	{14934650266808366570, -1140},
	{11127181549972568877, -1113},
	{16580792590934885855, -1087},
	{12353653155963782858, -1060},
	{18408377700990114895, -1034},
	{13715310171984221708, -1007},
	{10218702384817765436, -980},
	{15227053142812498563, -954},
	{11345038669416679861, -927},
	{16905424996341287883, -901},
	{12595523146049147757, -874},
	{9384396036005875287, -847},
	{13983839803942852151, -821},
	{10418772551374772303, -794},
	{15525180923007089351, -768},
	{11567161174868858868, -741},
	{17236413322193710309, -715},
	{12842128665889583758, -688},
	{9568131466127621947, -661},
	{14257626930069360058, -635},
	{10622759856335341974, -608},
	{15829145694278690180, -582},
	{11793632577567316726, -555},
	{17573882009934360870, -529},
	{13093562431584567480, -502},
	{9755464219737475723, -475},
	{14536774485912137811, -449},
	{10830740992659433045, -422},
	{16139061738043178685, -396},
	{12024538023802026127, -369},
	{17917957937422433684, -343},
	{13349918974505688015, -316},
	{9946464728195732843, -289},
	{14821387422376473014, -263},
	{11042794154864902060, -236},
	{16455045573212060422, -210},
	{12259964326927110867, -183},
	{18268770466636286478, -157},
	{13611294676837538539, -130},
	{10141204801825835212, -103},
	{15111572745182864684, -77},
	{11258999068426240000, -50},
	{16777216000000000000, -24},
	{12500000000000000000, 3},
	{9313225746154785156, 30},
	{13877787807814456755, 56},
	{10339757656912845936, 83},
	{15407439555097886824, 109},
	{11479437019748901445, 136},
	{17105694144590052135, 162},
	{12744735289059618216, 189},
	{9495567745759798747, 216},
	{14149498560666738074, 242},
	{10542197943230523224, 269},
	{15709099088952724970, 295},
	{11704190886730495818, 322},
	{17440603504673385349, 348},
	{12994262207056124023, 375},
	{9681479787123295682, 402},
	{14426529090290212157, 428},
	{10748601772107342003, 455},
	{16016664761464807395, 481},
	{11933345169920330789, 508},
	{17782069995880619868, 534},
	{13248674568444952270, 561},
	{9871031767461413346, 588},
	{14708983551653345445, 614},
	{10959046745042015199, 641},
	{16330252207878254650, 667},
	{12166986024289022870, 694},
	{18130221999122236476, 720},
	{13508068024458167312, 747},
	{10064294952495520794, 774},
	{14996968138956309548, 800},
	{11173611982879273257, 827},
	{16649979327439178909, 853},
	{12405201291620119593, 880},
	{9242595204427927429, 907},
	{13772540099066387757, 933},
	{10261342003245940623, 960},
	{15290591125556738113, 986},
	{11392378155556871081, 1013},
	{16975966327722178521, 1039},
	{12648080533535911531, 1066},
}

var fpconvTens = [...]uint64{10000000000000000000, 1000000000000000000, 100000000000000000, 10000000000000000, 1000000000000000, 100000000000000, 10000000000000, 1000000000000, 100000000000, 10000000000, 1000000000, 100000000, 10000000, 1000000, 100000, 10000, 1000, 100, 10, 1}

func fpconvBuild(d float64) diyFloat {
	bits := math.Float64bits(d)
	fp := diyFloat{frac: bits & (fpconvHiddenBit - 1), exp: int(bits >> 52 & 0x7ff)}
	if fp.exp != 0 {
		fp.frac += fpconvHiddenBit
		fp.exp -= fpconvExpBias
	} else {
		fp.exp = -fpconvExpBias + 1
	}
	return fp
}

func fpconvNormalize(fp diyFloat) diyFloat {
	for fp.frac&fpconvHiddenBit == 0 {
		fp.frac <<= 1
		fp.exp--
	}
	fp.frac <<= 11
	fp.exp -= 11
	return fp
}

func fpconvBoundaries(fp diyFloat) (lower, upper diyFloat) {
	upper = diyFloat{frac: fp.frac<<1 + 1, exp: fp.exp - 1}
	for upper.frac&(fpconvHiddenBit<<1) == 0 {
		upper.frac <<= 1
		upper.exp--
	}
	upper.frac <<= 10
	upper.exp -= 10
	shift := 1
	if fp.frac == fpconvHiddenBit {
		shift = 2
	}
	lower = diyFloat{frac: fp.frac<<shift - 1, exp: fp.exp - shift}
	lower.frac <<= lower.exp - upper.exp
	lower.exp = upper.exp
	return lower, upper
}

func fpconvMultiply(a, b diyFloat) diyFloat {
	const low = 0xffffffff
	ahbl := (a.frac >> 32) * (b.frac & low)
	albh := (a.frac & low) * (b.frac >> 32)
	albl := (a.frac & low) * (b.frac & low)
	ahbh := (a.frac >> 32) * (b.frac >> 32)
	carry := ahbl&low + albh&low + albl>>32 + 1<<31
	return diyFloat{frac: ahbh + ahbl>>32 + albh>>32 + carry>>32, exp: a.exp + b.exp + 64}
}

func fpconvCachedPower(exp int) (diyFloat, int) {
	const steps, first, count = 8, -348, 87
	approx := int(float64(-(exp + count)) * 0.30102999566398114)
	index := (approx - first) / steps
	for {
		current := exp + fpconvPowers[index].exp + 64
		switch {
		case current < -60:
			index++
		case current > -32:
			index--
		default:
			return fpconvPowers[index], first + index*steps
		}
	}
}

func fpconvRound(digits []byte, delta, rem, kappa, frac uint64) {
	for rem < frac && delta-rem >= kappa && (rem+kappa < frac || frac-rem > rem+kappa-frac) {
		digits[len(digits)-1]--
		rem += kappa
	}
}

func fpconvDigits(fp, upper, lower diyFloat, buffer []byte, k int) ([]byte, int) {
	wfrac := upper.frac - fp.frac
	delta := upper.frac - lower.frac
	shift := uint(-upper.exp)
	one := uint64(1) << shift
	part1 := upper.frac >> shift
	part2 := upper.frac & (one - 1)
	digits := buffer[:0]
	kappa := 10
	for index := 10; kappa > 0; index++ {
		div := fpconvTens[index]
		digit := part1 / div
		if digit != 0 || len(digits) > 0 {
			digits = append(digits, byte(digit)+'0')
		}
		part1 -= digit * div
		kappa--
		if rest := part1<<shift + part2; rest <= delta {
			fpconvRound(digits, delta, rest, div<<shift, wfrac)
			return digits, k + kappa
		}
	}
	for unit := 18; ; unit-- {
		part2 *= 10
		delta *= 10
		kappa--
		digit := part2 >> shift
		if digit != 0 || len(digits) > 0 {
			digits = append(digits, byte(digit)+'0')
		}
		part2 &= one - 1
		if part2 < delta {
			fpconvRound(digits, delta, part2, one, wfrac*fpconvTens[unit])
			return digits, k + kappa
		}
	}
}

func fpconvEmit(dst, digits []byte, k int, negative bool) []byte {
	count := len(digits)
	exp := k + count - 1
	if exp < 0 {
		exp = -exp
	}
	if k >= 0 && exp < count+7 {
		dst = append(dst, digits...)
		for range k {
			dst = append(dst, '0')
		}
		return dst
	}
	if k < 0 && (k > -7 || exp < 4) {
		offset := count + k
		if offset <= 0 {
			dst = append(dst, '0', '.')
			for range -offset {
				dst = append(dst, '0')
			}
			return append(dst, digits...)
		}
		dst = append(dst, digits[:offset]...)
		dst = append(dst, '.')
		return append(dst, digits[offset:]...)
	}
	limit := 18
	if negative {
		limit = 17
	}
	count = min(count, limit)
	dst = append(dst, digits[0])
	if count > 1 {
		dst = append(dst, '.')
		dst = append(dst, digits[1:count]...)
	}
	dst = append(dst, 'e')
	if k+count-1 < 0 {
		dst = append(dst, '-')
	} else {
		dst = append(dst, '+')
	}
	return strconv.AppendInt(dst, int64(exp), 10)
}

func fpconvDtoa(d float64) string {
	bits := math.Float64bits(d)
	negative := bits>>63 != 0
	dst := make([]byte, 0, 24)
	if negative {
		dst = append(dst, '-')
	}
	switch {
	case d == 0:
		return string(append(dst, '0'))
	case math.IsNaN(d):
		return string(append(dst, "nan"...))
	case math.IsInf(d, 0):
		return string(append(dst, "inf"...))
	}
	w := fpconvBuild(d)
	lower, upper := fpconvBoundaries(w)
	power, k := fpconvCachedPower(upper.exp)
	w = fpconvMultiply(fpconvNormalize(w), power)
	upper = fpconvMultiply(upper, power)
	lower = fpconvMultiply(lower, power)
	lower.frac++
	upper.frac--
	var buffer [24]byte
	digits, exponent := fpconvDigits(w, upper, lower, buffer[:], -k)
	return string(fpconvEmit(dst, digits, exponent, negative))
}

func double2ll(d float64) (int64, bool) {
	if d < -0x1p62 || d > 0x1p62 || d != d {
		return 0, false
	}
	value := int64(d)
	return value, float64(value) == d
}

func d2string(d float64) string {
	switch {
	case math.IsNaN(d):
		return "nan"
	case math.IsInf(d, 1):
		return "inf"
	case math.IsInf(d, -1):
		return "-inf"
	case d == 0:
		if math.Signbit(d) {
			return "-0"
		}
		return "0"
	}
	return commandNumber(d)
}

func commandNumber(d float64) string {
	if value, ok := double2ll(d); ok {
		return strconv.FormatInt(value, 10)
	}
	return fpconvDtoa(d)
}
