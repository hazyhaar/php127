package stdlib

import (
	"io"
	"math"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func nativeAbs(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("abs", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asNumber(z, args[0], "abs", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	if n.IsLong() {
		v := n.ToLong()
		if v == math.MinInt64 {
			return types.NewDouble(9223372036854775808), nil
		}
		if v < 0 {
			v = -v
		}
		return types.NewLong(v), nil
	}
	return types.NewDouble(math.Abs(n.ToDouble())), nil
}

func nativeMin(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return minMax(z, args, "min", true)
}

func nativeMax(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return minMax(z, args, "max", false)
}

func minMax(z *zone.Zone, args []types.Value, name string, wantMin bool) (types.Value, error) {
	if err := arity(name, len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	var items []types.Value
	if len(args) == 1 && deref(z, args[0]).Type == types.TypeArray {
		_, vals := arrayItems(z, args[0])
		if len(vals) == 0 {
			return types.Value{}, ValueError{name + "(): Argument #1 ($value) must contain at least one element"}
		}
		items = vals
	} else {
		items = args
	}
	best := deref(z, items[0])
	for i := 1; i < len(items); i++ {
		cur := deref(z, items[i])
		c := phpCmp(z, cur, best)
		if wantMin && c < 0 || !wantMin && c > 0 {
			best = cur
		}
	}
	return best, nil
}

func nativeIntdiv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("intdiv", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asIntParam(z, args[0], "intdiv", "1", "num1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asIntParam(z, args[1], "intdiv", "2", "num2")
	if err != nil {
		return types.Value{}, err
	}
	if b == 0 {
		return types.Value{}, types.DivisionByZeroError{}
	}
	if a == math.MinInt64 && b == -1 {
		return types.Value{}, ArithmeticError{"Division of PHP_INT_MIN by -1 is not an integer"}
	}
	return types.NewLong(a / b), nil
}

func nativeFdiv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fdiv", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asFloatParam(z, args[0], "fdiv", "1", "num1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asFloatParam(z, args[1], "fdiv", "2", "num2")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(a / b), nil
}

func nativeFmod(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fmod", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asFloatParam(z, args[0], "fmod", "1", "num1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asFloatParam(z, args[1], "fmod", "2", "num2")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(math.Mod(a, b)), nil
}

func nativeFloor(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "floor", math.Floor)
}

func nativeCeil(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "ceil", math.Ceil)
}

func nativeSqrt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "sqrt", math.Sqrt)
}

func unaryFloat(z *zone.Zone, args []types.Value, name string, fn func(float64) float64) (types.Value, error) {
	if err := arity(name, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asNumber(z, args[0], name, "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(fn(n.ToDouble())), nil
}

const (
	phpRoundHalfUp   = 1
	phpRoundHalfDown = 2
	phpRoundHalfEven = 3
	phpRoundHalfOdd  = 4
)

func roundToInt(val float64, mode int64) float64 {
	if val == 0.0 {
		return val
	}
	absVal := math.Abs(val)
	floorVal := math.Floor(absVal)
	diff := absVal - floorVal
	var rounded float64
	if diff > 0.5+1e-14 {
		rounded = floorVal + 1.0
	} else if diff < 0.5-1e-14 {
		rounded = floorVal
	} else {
		switch mode {
		case phpRoundHalfDown:
			rounded = floorVal
		case phpRoundHalfEven:
			if math.Mod(floorVal, 2.0) != 0.0 {
				rounded = floorVal + 1.0
			} else {
				rounded = floorVal
			}
		case phpRoundHalfOdd:
			if math.Mod(floorVal, 2.0) == 0.0 {
				rounded = floorVal + 1.0
			} else {
				rounded = floorVal
			}
		default: // phpRoundHalfUp
			rounded = floorVal + 1.0
		}
	}
	return math.Copysign(rounded, val)
}

func nativeRound(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("round", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	n, err := asNumber(z, args[0], "round", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	prec := int64(0)
	if len(args) >= 2 {
		prec, err = asIntParam(z, args[1], "round", "2", "precision")
		if err != nil {
			return types.Value{}, err
		}
	}
	mode := int64(phpRoundHalfUp)
	if len(args) == 3 {
		mode, err = asIntParam(z, args[2], "round", "3", "mode")
		if err != nil {
			return types.Value{}, err
		}
		if mode < phpRoundHalfUp || mode > phpRoundHalfOdd {
			return types.Value{}, ValueError{"round(): Argument #3 ($mode) must be a valid rounding mode (PHP_ROUND_*)"}
		}
	}
	f := n.ToDouble()
	if math.IsNaN(f) || math.IsInf(f, 0) || f == 0.0 {
		return types.NewDouble(f), nil
	}
	if prec > 308 {
		prec = 308
	}
	if prec < -308 {
		prec = -308
	}
	if prec == 0 {
		return types.NewDouble(roundToInt(f, mode)), nil
	}
	if prec > 0 {
		p := math.Pow(10, float64(prec))
		if math.IsInf(p, 0) {
			return types.NewDouble(f), nil
		}
		return types.NewDouble(roundToInt(f*p, mode) / p), nil
	}
	p := math.Pow(10, float64(-prec))
	if math.IsInf(p, 0) {
		return types.NewDouble(math.Copysign(0, f)), nil
	}
	return types.NewDouble(roundToInt(f/p, mode) * p), nil
}

func nativePow(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("pow", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	base, err := asNumber(z, args[0], "pow", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	exp, err := asNumber(z, args[1], "pow", "2", "exponent")
	if err != nil {
		return types.Value{}, err
	}
	if base.IsLong() && exp.IsLong() {
		e := exp.ToLong()
		if e >= 0 {
			if v, ok := powInt(base.ToLong(), e); ok {
				return types.NewLong(v), nil
			}
		}
	}
	return types.NewDouble(math.Pow(base.ToDouble(), exp.ToDouble())), nil
}

func powInt(base, exp int64) (int64, bool) {
	if exp == 0 {
		return 1, true
	}
	result := int64(1)
	b := base
	for exp > 0 {
		if exp&1 == 1 {
			if b != 0 && (result > math.MaxInt64/abs64(b) || result < math.MinInt64/abs64(b)) {
				return 0, false
			}
			next := result * b
			if b < 0 && result < 0 && next > 0 || b > 0 && result < 0 && next > 0 && result != math.MinInt64 {
				return 0, false
			}
			result = next
		}
		exp >>= 1
		if exp == 0 {
			break
		}
		if b != 0 && (b > math.MaxInt64/abs64(b) || b < math.MinInt64/abs64(b)) {
			return 0, false
		}
		b *= b
	}
	return result, true
}

func abs64(v int64) int64 {
	if v < 0 {
		if v == math.MinInt64 {
			return math.MaxInt64
		}
		return -v
	}
	return v
}

func nativeIsNan(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return floatPred(z, args, "is_nan", math.IsNaN)
}

func nativeIsInfinite(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return floatPred(z, args, "is_infinite", func(f float64) bool { return math.IsInf(f, 0) })
}

func nativeIsFinite(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return floatPred(z, args, "is_finite", func(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) })
}

func floatPred(z *zone.Zone, args []types.Value, name string, pred func(float64) bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	f, err := asFloatParam(z, args[0], name, "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(pred(f)), nil
}
