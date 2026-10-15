package types

import (
	"fmt"
	"math"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestValueLayout(t *testing.T) {
	if unsafe.Sizeof(Value{}) != 16 || unsafe.Alignof(Value{}) != 8 {
		t.Fatalf("size=%d align=%d", unsafe.Sizeof(Value{}), unsafe.Alignof(Value{}))
	}
	typ := reflect.TypeFor[Value]()
	wantNames := []string{"Type", "Flags", "Extra", "DataSlot", "Scalar"}
	wantKinds := []reflect.Kind{reflect.Uint8, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64}
	wantOffsets := []uintptr{0, 1, 2, 4, 8}
	if typ.NumField() != len(wantNames) {
		t.Fatalf("fields=%d", typ.NumField())
	}
	for i, name := range wantNames {
		field := typ.Field(i)
		if field.Name != name || field.Type.Kind() != wantKinds[i] || field.Offset != wantOffsets[i] {
			t.Errorf("field %d: %+v", i, field)
		}
	}
	for i, tag := range []Type{TypeUndef, TypeNull, TypeFalse, TypeTrue, TypeLong, TypeDouble, TypeString, TypeArray, TypeObject, TypeResource, TypeReference} {
		if tag != Type(i) {
			t.Errorf("tag %d = %d", i, tag)
		}
	}
	for i, flag := range []Flags{FlagZoneRequest, FlagTainted, FlagOwnedMutable, FlagImmutable} {
		if flag != Flags(1<<i) {
			t.Errorf("flag %d = %d", i, flag)
		}
	}
}

func TestConstructorsAndPredicates(t *testing.T) {
	values := []Value{NewUndef(), NewNull(), NewBool(false), NewBool(true), NewLong(-42), NewDouble(math.Copysign(0, -1))}
	for i, v := range values {
		if v.Type != Type(i) || v.Flags != 0 || v.Extra != 0 || v.DataSlot != 0 {
			t.Errorf("constructor %d: %+v", i, v)
		}
		got := []bool{v.IsUndef(), v.IsNull(), v.IsBool(), v.IsLong(), v.IsDouble()}
		want := []bool{i == 0, i == 1, i == 2 || i == 3, i == 4, i == 5}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("predicates %d: %v, want %v", i, got, want)
		}
		if i < 4 && v.Scalar != 0 {
			t.Errorf("nonzero payload: %+v", v)
		}
	}
	if values[4].Scalar != math.MaxUint64-41 || values[5].Scalar != 1<<63 {
		t.Fatal("scalar bits were not preserved")
	}
	if (Value{}) != NewUndef() || Identical(NewUndef(), NewNull()) || !Identical(NewUndef(), NewUndef()) {
		t.Fatal("undefined tag must remain distinct from null")
	}
	if !Identical(Value{Type: TypeNull, Scalar: 1}, NewNull()) {
		t.Fatal("scalar payload must be ignored for null in Identical")
	}
	if !Identical(Value{Type: TypeTrue, Scalar: 7}, NewBool(true)) {
		t.Fatal("scalar payload must be ignored for bool in Identical")
	}
	v := NewLong(42)
	v.Flags, v.Extra, v.DataSlot = FlagTainted, 17, 23
	if !Identical(v, NewLong(42)) || CompareScalaires(v, NewLong(42)) != 0 {
		t.Fatal("metadata must not affect scalar comparisons")
	}
}

type scalarCase struct {
	v   Value
	php string
}

func scalarCases() []scalarCase {
	// Reading an undefined PHP variable yields null; ?? avoids its diagnostic.
	cases := []scalarCase{{NewUndef(), "($undefined ?? null)"}, {NewNull(), "null"}, {NewBool(false), "false"}, {NewBool(true), "true"}}
	for _, n := range []int64{math.MinInt64, math.MinInt64 + 1, -3037000500, -3037000499, -3, -2, -1, 0, 1, 2, 3, 3037000499, 3037000500, 1<<53 - 1, 1<<53 + 1, math.MaxInt64 - 1, math.MaxInt64} {
		expr := fmt.Sprintf("%d", n)
		if n == math.MinInt64 {
			expr = "PHP_INT_MIN"
		}
		cases = append(cases, scalarCase{NewLong(n), expr})
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 1, -1, 1.75, -1.75, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.Nextafter(0x1p63, 0), 0x1p63, math.Nextafter(0x1p63, math.Inf(1)), -0x1p63, math.Nextafter(-0x1p63, math.Inf(-1)), math.Nextafter(0x1p64, 0), 0x1p64, -0x1p64, 1e30, -1e30, math.MaxFloat64, -math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		cases = append(cases, scalarCase{NewDouble(f), fmt.Sprintf("unpack('E', hex2bin('%016x'))[1]", math.Float64bits(f))})
	}
	return cases
}

func encodeScalar(v Value) string {
	switch v.Type {
	case TypeLong:
		return fmt.Sprintf("i:%d", int64(v.Scalar))
	case TypeDouble:
		if math.IsNaN(math.Float64frombits(v.Scalar)) {
			return "d:nan"
		}
		return fmt.Sprintf("d:%016x", v.Scalar)
	case TypeTrue:
		return "b:1"
	case TypeFalse:
		return "b:0"
	default:
		panic("unexpected oracle result")
	}
}

func TestPHPScalarDifferential(t *testing.T) {
	version, err := exec.CommandContext(t.Context(), "/usr/bin/php", "-r", "echo PHP_VERSION, ':', PHP_INT_SIZE;").CombinedOutput()
	if err != nil || string(version) != "8.3.6:8" {
		t.Fatalf("PHP oracle: %q, error=%v", version, err)
	}
	var script strings.Builder
	script.WriteString(`function enc($v) {
if (is_int($v)) return 'i:'.$v;
if (is_float($v)) return is_nan($v) ? 'd:nan' : 'd:'.bin2hex(pack('E', $v));
if (is_bool($v)) return $v ? 'b:1' : 'b:0';
throw new Exception('unexpected oracle result');
}
$v = [`)
	cases := scalarCases()
	for _, c := range cases {
		script.WriteString(c.php + ",")
	}
	script.WriteString("];\n")
	var want strings.Builder
	for i, a := range cases {
		for _, conv := range []struct {
			name   string
			result Value
		}{
			{"bool", NewBool(a.v.ToBool())}, {"int", NewLong(a.v.ToLong())}, {"float", NewDouble(a.v.ToDouble())},
		} {
			label := fmt.Sprintf("%s(%d)", conv.name, i)
			fmt.Fprintf(&script, "echo '%s=',enc((%s)$v[%d]),\"\\n\";\n", label, conv.name, i)
			fmt.Fprintf(&want, "%s=%s\n", label, encodeScalar(conv.result))
		}
		for j, b := range cases {
			fmt.Fprintf(&script, "echo 'cmp(%d,%d)=',enc($v[%d]<=>$v[%d]),\"\\n\";\n", i, j, i, j)
			fmt.Fprintf(&want, "cmp(%d,%d)=%s\n", i, j, encodeScalar(NewLong(int64(CompareScalaires(a.v, b.v)))))
			// Undef is an internal tag, not a distinct PHP expression value.
			if i != 0 && j != 0 {
				fmt.Fprintf(&script, "echo 'same(%d,%d)=',enc($v[%d]===$v[%d]),\"\\n\";\n", i, j, i, j)
				fmt.Fprintf(&want, "same(%d,%d)=%s\n", i, j, encodeScalar(NewBool(Identical(a.v, b.v))))
			}
			if a.v.Type < TypeLong || b.v.Type < TypeLong {
				continue
			}
			for _, op := range []struct {
				name string
				fn   func(Value, Value) Value
			}{{"+", Add}, {"-", Sub}, {"*", Mul}} {
				fmt.Fprintf(&script, "echo '%s(%d,%d)=',enc($v[%d]%s$v[%d]),\"\\n\";\n", op.name, i, j, i, op.name, j)
				fmt.Fprintf(&want, "%s(%d,%d)=%s\n", op.name, i, j, encodeScalar(op.fn(a.v, b.v)))
			}
			fmt.Fprintf(&script, "try { echo '/(%d,%d)=',enc($v[%d]/$v[%d]),\"\\n\"; } catch (DivisionByZeroError $e) { echo get_class($e),':',$e->getMessage(),\"\\n\"; }\n", i, j, i, j)
			result, divErr := Div(a.v, b.v)
			if divErr != nil {
				if _, ok := divErr.(DivisionByZeroError); !ok || result != NewUndef() {
					t.Fatalf("division result=%+v error=%T", result, divErr)
				}
				fmt.Fprintf(&want, "/(%d,%d)=DivisionByZeroError:%s\n", i, j, divErr)
			} else {
				fmt.Fprintf(&want, "/(%d,%d)=%s\n", i, j, encodeScalar(result))
			}
		}
	}
	// Supply the program on stdin to avoid the operating system's argument limit.
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php")
	cmd.Stdin = strings.NewReader("<?php\n" + script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PHP failed: %v\n%s", err, output)
	}
	actualLines, expectedLines := strings.Split(string(output), "\n"), strings.Split(want.String(), "\n")
	if len(actualLines) != len(expectedLines) {
		t.Fatalf("PHP produced %d lines, want %d\n%s", len(actualLines), len(expectedLines), output)
	}
	for i, expected := range expectedLines {
		if actualLines[i] != expected {
			t.Errorf("oracle line %d: PHP %q; Go %q", i+1, actualLines[i], expected)
		}
	}
	t.Logf("PHP %s: %d scalar cases, %d differential results", version, len(cases), len(expectedLines)-1)
}

func TestUnsupportedTypesPanic(t *testing.T) {
	for tag := TypeString; tag <= TypeReference+1; tag++ {
		v := Value{Type: tag}
		for name, fn := range map[string]func(){
			"bool": func() { v.ToBool() }, "long": func() { v.ToLong() }, "double": func() { v.ToDouble() },
			"add-left": func() { Add(v, NewLong(1)) }, "add-right": func() { Add(NewLong(1), v) },
			"sub-left": func() { Sub(v, NewLong(1)) }, "sub-right": func() { Sub(NewLong(1), v) },
			"mul-left": func() { Mul(v, NewLong(1)) }, "mul-right": func() { Mul(NewLong(1), v) },
			"div-left": func() { Div(v, NewLong(1)) }, "div-right": func() { Div(NewLong(1), v) },
			"same-left": func() { Identical(v, NewLong(1)) }, "same-right": func() { Identical(NewLong(1), v) },
			"cmp-left": func() { CompareScalaires(v, NewLong(1)) }, "cmp-right": func() { CompareScalaires(NewLong(1), v) },
		} {
			t.Run(fmt.Sprintf("%d/%s", tag, name), func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Fatal("unsupported type accepted")
					}
				}()
				fn()
			})
		}
	}
	for _, v := range []Value{NewUndef(), NewNull(), NewBool(false), NewBool(true)} {
		for _, fn := range []func(Value, Value) Value{Add, Sub, Mul, func(a, b Value) Value { result, _ := Div(a, b); return result }} {
			for _, reverse := range []bool{false, true} {
				func() {
					defer func() {
						if recover() == nil {
							t.Errorf("arithmetic accepted tag %d", v.Type)
						}
					}()
					if reverse {
						fn(NewLong(1), v)
					} else {
						fn(v, NewLong(1))
					}
				}()
			}
		}
	}
}

var valueSink Value
var boolSink bool
var longSink int64
var doubleSink float64
var compareSink int
var errorSink error

func scalarOperations(a, b Value) {
	valueSink = NewUndef()
	valueSink = NewNull()
	valueSink = NewBool(a.ToBool())
	valueSink = NewLong(a.ToLong())
	valueSink = NewDouble(a.ToDouble())
	boolSink = a.IsUndef()
	boolSink = a.IsNull()
	boolSink = a.IsBool()
	boolSink = a.IsLong()
	boolSink = a.IsDouble()
	boolSink = a.ToBool()
	longSink = a.ToLong()
	doubleSink = a.ToDouble()
	valueSink = Add(a, b)
	valueSink = Sub(a, b)
	valueSink = Mul(a, b)
	valueSink, errorSink = Div(a, b)
	boolSink = Identical(a, b)
	compareSink = CompareScalaires(a, b)
}

func TestScalarAllocations(t *testing.T) {
	for _, a := range scalarCases() {
		for _, b := range scalarCases() {
			if a.v.Type < TypeLong || b.v.Type < TypeLong {
				continue
			}
			if allocs := testing.AllocsPerRun(10, func() { scalarOperations(a.v, b.v) }); allocs != 0 {
				t.Fatalf("%s, %s: %g allocations", a.php, b.php, allocs)
			}
		}
	}
}

func BenchmarkScalarOperations(b *testing.B) {
	for _, c := range []struct {
		name string
		a, b Value
	}{
		{"integer", NewLong(7), NewLong(2)}, {"overflow", NewLong(math.MinInt64), NewLong(-1)},
		{"double", NewDouble(1.75), NewDouble(-2.5)}, {"mixed", NewLong(7), NewDouble(2.5)},
		{"conversion-modulo", NewDouble(1e30), NewDouble(3)}, {"nan", NewDouble(math.NaN()), NewDouble(1)},
		{"division-zero", NewLong(1), NewLong(0)},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				scalarOperations(c.a, c.b)
			}
		})
	}
}
