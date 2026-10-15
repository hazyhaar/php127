package types

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

func arrayValue(t testing.TB, z *zone.Zone) Value {
	t.Helper()
	v, err := NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The oracle emits typed, binary-safe keys and values, never JSON object keys
// (which would erase the distinction between integer and string keys).
const arrayPHPEncoder = `function enc($v) {
if(is_array($v)){echo "a:",count($v),"{";foreach($v as $k=>$x){enc($k);enc($x);}echo "}";}
elseif(is_string($v)){echo "s:",bin2hex($v),";";}
elseif(is_int($v)){echo "i:",$v,";";}
elseif(is_bool($v)){echo $v?"b:1;":"b:0;";}
elseif(is_null($v)){echo "n;";}
else{echo "d:",bin2hex(pack('E',$v)),";";}}
`

func encodeArrayValue(z *zone.Zone, v Value, out *strings.Builder) {
	switch v.Type {
	case TypeArray:
		fmt.Fprintf(out, "a:%d{", ArrayLen(z, v))
		ArrayForEach(z, v, func(k, x Value) bool { encodeArrayValue(z, k, out); encodeArrayValue(z, x, out); return true })
		out.WriteByte('}')
	case TypeString:
		fmt.Fprintf(out, "s:%x;", StringData(z, v))
	case TypeLong:
		fmt.Fprintf(out, "i:%d;", v.ToLong())
	case TypeTrue:
		out.WriteString("b:1;")
	case TypeFalse:
		out.WriteString("b:0;")
	case TypeNull:
		out.WriteString("n;")
	case TypeDouble:
		fmt.Fprintf(out, "d:%016x;", v.Scalar)
	default:
		panic("invalid oracle value")
	}
}

func checkArrayPHP(t *testing.T, program, want string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n")
	cmd.Stdin = strings.NewReader("<?php if(PHP_VERSION!=='8.3.6'||PHP_INT_SIZE!==8){fwrite(STDERR,PHP_VERSION);exit(99);}" + arrayPHPEncoder + program)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	got, err := cmd.Output()
	if err != nil || stderr.Len() != 0 || string(got) != want {
		t.Fatalf("PHP error=%v stderr=%q\ngot=%s\nwant=%s", err, stderr.String(), got, want)
	}
}

func TestArrayPHPKeysAndNextIndex(t *testing.T) {
	z := zone.New(1 << 20)
	cases := scalarCases()[1:]
	for _, text := range []string{"0", "42", "042", "01", "-7", "-0", "+42", " 42", "42 ", "42.0", "", "9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809", "0\x00", "00", "-01", "a\x00b"} {
		cases = append(cases, scalarCase{stringValue(t, z, text, 0), phpString(text)})
	}
	for _, tc := range cases {
		t.Run(tc.php, func(t *testing.T) {
			var program, want strings.Builder
			program.WriteString(`set_error_handler(function($n,$s){echo "W:",$n,":",$s,"\n";});$a=[];`)
			warn := func(w ArrayWarning) error {
				var buf [128]byte
				fmt.Fprintf(&want, "W:8192:%s\n", w.AppendMessage(buf[:0]))
				return nil
			}
			a := arrayValue(t, z)
			// Update, append, unset and append again exercise saturation and negatives.
			for _, op := range []string{"set", "set", "append", "unset", "append"} {
				var err error
				switch op {
				case "set":
					fmt.Fprintf(&program, "$a[%s]=1;", tc.php)
					err = ArraySet(z, &a, tc.v, NewLong(1), warn)
				case "unset":
					fmt.Fprintf(&program, "unset($a[%s]);", tc.php)
					err = ArrayUnset(z, &a, tc.v, warn)
				case "append":
					program.WriteString(`try{$a[]=2;}catch(Error $e){echo "E:",$e->getMessage(),"\n";}`)
					err = ArrayAppend(z, &a, NewLong(2))
				}
				if err != nil {
					if err != ErrArrayOccupied {
						t.Fatal(err)
					}
					fmt.Fprintf(&want, "E:%s\n", err)
				}
				program.WriteString("enc($a);echo \"\n\";")
				encodeArrayValue(z, a, &want)
				want.WriteByte('\n')
			}
			fmt.Fprintf(&program, "enc(array_key_exists(%s,$a));enc(isset($a[%s]));", tc.php, tc.php)
			exists, err := ArrayKeyExists(z, a, tc.v, warn)
			if err != nil {
				t.Fatal(err)
			}
			encodeArrayValue(z, NewBool(exists), &want)
			isset, err := ArrayIsset(z, a, tc.v, warn)
			if err != nil {
				t.Fatal(err)
			}
			encodeArrayValue(z, NewBool(isset), &want)
			checkArrayPHP(t, program.String(), want.String())
			ReleaseArray(z, &a)
		})
	}
}

func TestArrayPHPCollisionsAndCompaction(t *testing.T) {
	for _, stringsMode := range []bool{false, true} {
		z := zone.New(1 << 20)
		a := arrayValue(t, z)
		var program, want strings.Builder
		program.WriteString("$a=[];")
		keys := make([]scalarCase, 0, 40)
		for i := range int64(40) {
			if stringsMode {
				text := fmt.Sprintf("key-%d", i)
				keys = append(keys, scalarCase{stringValue(t, z, text, 0), phpString(text)})
			} else {
				n := i * 64
				keys = append(keys, scalarCase{NewLong(n), fmt.Sprint(n)})
			}
		}
		snapshot := func() {
			program.WriteString("enc($a);echo \"\n\";")
			encodeArrayValue(z, a, &want)
			want.WriteByte('\n')
		}
		for i, tc := range keys[:16] {
			fmt.Fprintf(&program, "$a[%s]=%d;", tc.php, i)
			arraySet(t, z, &a, tc.v, NewLong(int64(i)))
			snapshot()
		}
		before := ArrayInfo(z, a)
		for _, i := range []int{0, 15, 8, 1, 2, 3, 4, 5, 6, 7, 9, 10} {
			fmt.Fprintf(&program, "unset($a[%s]);", keys[i].php)
			if err := ArrayUnset(z, &a, keys[i].v, nil); err != nil {
				t.Fatal(err)
			}
			snapshot()
		}
		if ArrayInfo(z, a).Used != before.Used {
			t.Fatal("unset moved buckets")
		}
		for _, i := range []int{0, 15, 8, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32} {
			fmt.Fprintf(&program, "$a[%s]=%d;", keys[i].php, i+100)
			arraySet(t, z, &a, keys[i].v, NewLong(int64(i+100)))
			snapshot()
		}
		checkArrayPHP(t, program.String(), want.String())
		for _, tc := range keys {
			v, ok, err := ArrayGet(z, a, tc.v, nil)
			if err != nil {
				t.Fatal(err)
			}
			if ok && v.IsUndef() {
				t.Fatal("live tombstone")
			}
		}
	}
}

func TestArrayCoWNestedAndIterationPHP(t *testing.T) {
	z := zone.New(1 << 16)
	a := arrayValue(t, z)
	inner := arrayValue(t, z)
	arraySet(t, z, &inner, byteTable['y'], NewLong(0))
	arraySet(t, z, &a, byteTable['x'], inner)
	ReleaseArray(z, &inner)
	b := RetainArray(z, a)
	x, ok, err := ArrayGet(z, b, byteTable['x'], nil)
	if err != nil || !ok {
		t.Fatal(err)
	}
	x = RetainValue(z, x)
	arraySet(t, z, &x, byteTable['y'], NewLong(1))
	arraySet(t, z, &b, byteTable['x'], x)
	ReleaseValue(z, &x)
	var want strings.Builder
	encodeArrayValue(z, a, &want)
	encodeArrayValue(z, b, &want)
	if ArrayEqual(z, a, b) || ArrayIdentical(z, a, b) {
		t.Fatal("nested CoW not separated")
	}
	checkArrayPHP(t, `$a=['x'=>['y'=>0]];$b=$a;$b['x']['y']=1;enc($a);enc($b);`, want.String())
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
	a = arrayValue(t, z)
	for i := range int64(8) {
		arraySet(t, z, &a, NewLong(i), NewLong(i))
	}
	want.Reset()
	ArrayForEach(z, a, func(k, v Value) bool {
		encodeArrayValue(z, k, &want)
		encodeArrayValue(z, v, &want)
		if k.ToLong() == 0 {
			if err := ArrayUnset(z, &a, NewLong(1), nil); err != nil {
				t.Fatal(err)
			}
			for i := range int64(20) {
				if err := ArrayAppend(z, &a, NewLong(i+100)); err != nil {
					t.Fatal(err)
				}
			}
			arraySet(t, z, &a, NewLong(2), NewLong(99))
		}
		return true
	})
	encodeArrayValue(z, a, &want)
	checkArrayPHP(t, `$a=range(0,7);foreach($a as $k=>$v){enc($k);enc($v);if($k===0){unset($a[1]);for($i=0;$i<20;$i++)$a[]=$i+100;$a[2]=99;}}enc($a);`, want.String())
	if ArrayInfo(z, a).RefCount != 1 {
		t.Fatal("iterator leaked snapshot")
	}
	ArrayForEach(z, a, func(Value, Value) bool { return false })
	if ArrayInfo(z, a).RefCount != 1 {
		t.Fatal("early break leaked snapshot")
	}
	func() {
		defer func() {
			if recover() != "stop" {
				t.Fatal("visitor panic swallowed")
			}
		}()
		ArrayForEach(z, a, func(Value, Value) bool { panic("stop") })
	}()
	if ArrayInfo(z, a).RefCount != 1 {
		t.Fatal("panic leaked snapshot")
	}
	ReleaseArray(z, &a)
}

func TestArrayPHPComparisons(t *testing.T) {
	t.Run("integer versus string key", func(t *testing.T) {
		z := zone.New(4096)
		a, b := arrayValue(t, z), arrayValue(t, z)
		arraySet(t, z, &a, NewLong(1), byteTable['x'])
		arraySet(t, z, &b, byteTable['a'], byteTable['x'])
		if ArrayIdentical(z, a, b) {
			t.Fatal("key type ignored")
		}
		ReleaseArray(z, &a)
		ReleaseArray(z, &b)
	})
	z := zone.New(1 << 20)
	cases := []scalarCase{{NewNull(), "null"}, {NewBool(false), "false"}, {NewBool(true), "true"}, {NewLong(0), "0"}, {NewLong(1), "1"}, {NewLong(math.MaxInt64), "PHP_INT_MAX"}, {NewDouble(1.5), "1.5"}, {NewDouble(math.Inf(1)), "INF"}, {NewDouble(math.NaN()), "NAN"}, {NewDouble(1e20), "1e20"}, {NewDouble(1e-7), "1e-7"}}
	for _, text := range []string{"", "0", "00", "1", "01", "1.0", "1.5", "a", "INF", "NAN", "1.0E+20", "1.0E-7", "9223372036854775807"} {
		cases = append(cases, scalarCase{stringValue(t, z, text, 0), phpString(text)})
	}
	empty := arrayValue(t, z)
	nonempty := arrayValue(t, z)
	arraySet(t, z, &nonempty, NewLong(0), NewLong(1))
	cases = append(cases, scalarCase{empty, "[]"}, scalarCase{nonempty, "[1]"})
	var program, want strings.Builder
	for _, x := range cases {
		for _, y := range cases {
			a, b := arrayValue(t, z), arrayValue(t, z)
			arraySet(t, z, &a, byteTable['x'], x.v)
			arraySet(t, z, &a, byteTable['y'], NewNull())
			arraySet(t, z, &b, byteTable['y'], NewNull())
			arraySet(t, z, &b, byteTable['x'], y.v)
			fmt.Fprintf(&program, "$a=['x'=>%s,'y'=>null];$b=['y'=>null,'x'=>%s];enc($a==$b);enc($a===$b);$b=['x'=>%s,'y'=>null];enc($a==$b);enc($a===$b);", x.php, y.php, y.php)
			encodeArrayValue(z, NewBool(ArrayEqual(z, a, b)), &want)
			encodeArrayValue(z, NewBool(ArrayIdentical(z, a, b)), &want)
			ReleaseArray(z, &b)
			b = arrayValue(t, z)
			arraySet(t, z, &b, byteTable['x'], y.v)
			arraySet(t, z, &b, byteTable['y'], NewNull())
			encodeArrayValue(z, NewBool(ArrayEqual(z, a, b)), &want)
			encodeArrayValue(z, NewBool(ArrayIdentical(z, a, b)), &want)
			ReleaseArray(z, &a)
			ReleaseArray(z, &b)
		}
	}
	checkArrayPHP(t, program.String(), want.String())
	// PHP short-circuits equality of the same array even with NaN elements.
	a := arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), NewDouble(math.NaN()))
	if !ArrayEqual(z, a, a) || !ArrayIdentical(z, a, a) {
		t.Fatal("same array NaN")
	}
}

func TestArrayReferencesAndRecycle(t *testing.T) {
	z := zone.New(8192)
	guard := stringValue(t, z, "guard", 0)
	for iteration := range 10000 {
		a := arrayValue(t, z)
		key := stringValue(t, z, "key", 0)
		value := stringValue(t, z, "value", 0)
		arraySet(t, z, &a, key, value)
		if StringInfo(z, key).RefCount != 2 || StringInfo(z, value).RefCount != 2 {
			t.Fatal("insertion ownership")
		}
		alias := RetainArray(z, a)
		arraySet(t, z, &a, NewLong(100), NewLong(1))
		if StringInfo(z, key).RefCount != 3 || StringInfo(z, value).RefCount != 3 {
			t.Fatal("CoW child retain")
		}
		if err := ArrayUnset(z, &a, key, nil); err != nil {
			t.Fatal(err)
		}
		if StringInfo(z, key).RefCount != 2 || StringInfo(z, value).RefCount != 2 {
			t.Fatal("unset child release")
		}
		ReleaseString(z, &key)
		ReleaseString(z, &value)
		ReleaseArray(z, &alias)
		ReleaseArray(z, &a)
		if iteration%100 == 0 {
			assertString(t, z, guard, "guard")
		}
	}
}

func TestArrayLargeRecycle(t *testing.T) {
	z := zone.New(64 << 20)
	for range 8 {
		a := arrayValue(t, z)
		for i := range int64(70000) {
			arraySet(t, z, &a, NewLong(i*2), NewLong(i))
		}
		guard := stringValue(t, z, "guard", 0)
		if ArrayInfo(z, a).Cap != 131072 {
			t.Fatal("growth")
		}
		ReleaseArray(z, &a)
		assertString(t, z, guard, "guard")
		ReleaseString(z, &guard)
	}
}

func TestArrayWarningsErrorsAndBorrowing(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	stop := errors.New("handler exception")
	warn := func(ArrayWarning) error { return stop }
	before := ArrayInfo(z, a)
	if err := ArraySet(z, &a, NewDouble(1.5), NewLong(1), warn); err != stop || ArrayInfo(z, a) != before {
		t.Fatal("warning abort")
	}
	obj := objectValue(t, z, 0)
	RegisterObjectClassName(1, "stdClass")
	defer ReleaseObject(z, &obj)
	for _, key := range []Value{NewUndef(), a, obj} {
		if err := ArraySet(z, &a, key, NewNull(), nil); !errors.Is(err, ErrArrayKey) {
			t.Fatal(err)
		}
	}
	if err := ArrayAppend(z, &a, NewUndef()); err != ErrArrayValue {
		t.Fatal(err)
	}
	arraySet(t, z, &a, NewNull(), NewNull())
	if ok, err := ArrayKeyExists(z, a, emptyString, nil); err != nil || !ok {
		t.Fatal("null key")
	}
	if ok, err := ArrayIsset(z, a, emptyString, nil); err != nil || ok {
		t.Fatal("null isset")
	}
	// A borrowed child can be passed straight back to Set: retain precedes release.
	s := stringValue(t, z, "child", 0)
	arraySet(t, z, &a, NewLong(0), s)
	ReleaseString(z, &s)
	v, _, err := ArrayGet(z, a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	arraySet(t, z, &a, NewLong(0), v)
	v, _, err = ArrayGet(z, a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertString(t, z, v, "child")
	// Self assignment must capture the pre-mutation value, not form a cycle.
	arraySet(t, z, &a, byteTable['s'], a)
	v, _, err = ArrayGet(z, a, byteTable['s'], nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := ArrayKeyExists(z, v, byteTable['s'], nil); err != nil || ok {
		t.Fatal("self assignment formed a cycle")
	}
	ReleaseArray(z, &a)
}

func arrayExercise(z *zone.Zone) {
	a, err := NewArray(z)
	if err != nil {
		panic(err)
	}
	for i := range int64(20) {
		if err := ArrayAppend(z, &a, NewLong(i)); err != nil {
			panic(err)
		}
	}
	b := RetainArray(z, a)
	if err := ArraySet(z, &b, byteTable['x'], byteTable['v'], nil); err != nil {
		panic(err)
	}
	for i := range int64(18) {
		if err := ArrayUnset(z, &b, NewLong(i), nil); err != nil {
			panic(err)
		}
	}
	for i := int64(20); i < 60; i++ {
		if err := ArraySet(z, &b, NewLong(i), NewLong(i), nil); err != nil {
			panic(err)
		}
	}
	if _, _, err := ArrayGet(z, b, byteTable['x'], nil); err != nil {
		panic(err)
	}
	if _, err := ArrayIsset(z, b, byteTable['x'], nil); err != nil {
		panic(err)
	}
	if _, err := ArrayKeyExists(z, b, byteTable['x'], nil); err != nil {
		panic(err)
	}
	ArrayEqual(z, a, b)
	ArrayIdentical(z, a, b)
	IdenticalZ(z, a, b)
	ArrayForEach(z, b, func(Value, Value) bool { return true })
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
}

func TestArrayZeroHeap(t *testing.T) {
	z := zone.New(1 << 16)
	if n := testing.AllocsPerRun(1000, func() { arrayExercise(z) }); n != 0 {
		t.Fatalf("allocs=%g", n)
	}
	var message [128]byte
	for _, f := range []float64{1.75, 1000000.5, math.SmallestNonzeroFloat64, math.MaxFloat64, math.NaN()} {
		if n := testing.AllocsPerRun(100, func() {
			_, err := NormalizeArrayKey(z, NewDouble(f), func(w ArrayWarning) error { w.AppendMessage(message[:0]); return nil })
			if err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatalf("diagnostic allocations=%g", n)
		}
	}
}

func TestArrayRepresentationNeutralComparison(t *testing.T) {
	z := zone.New(8192)
	a, b := arrayValue(t, z), arrayValue(t, z)
	arraySet(t, z, &b, byteTable['x'], NewNull())
	if err := ArrayUnset(z, &b, byteTable['x'], nil); err != nil {
		t.Fatal(err)
	}
	for i := range int64(4) {
		arraySet(t, z, &a, NewLong(i), NewLong(i))
		arraySet(t, z, &b, NewLong(i), NewLong(i))
	}
	if ArrayInfo(z, a).Kind != ArrayPacked || ArrayInfo(z, b).Kind != ArrayHash || !ArrayEqual(z, a, b) || !ArrayIdentical(z, a, b) {
		t.Fatal("representation affects comparison")
	}
	arraySet(t, z, &b, NewLong(2), NewDouble(2))
	if !ArrayEqual(z, a, b) || ArrayIdentical(z, a, b) {
		t.Fatal("type-insensitive identity")
	}
	if err := ArrayUnset(z, &a, NewLong(0), nil); err != nil {
		t.Fatal(err)
	}
	if ArrayEqual(z, a, b) || ArrayIdentical(z, a, b) {
		t.Fatal("length ignored")
	}
}

func BenchmarkArray(b *testing.B) {
	z := zone.New(1 << 16)
	b.ReportAllocs()
	for b.Loop() {
		arrayExercise(z)
	}
}

func TestArrayPHPTransitions(t *testing.T) {
	for seed := uint64(1); seed <= 8; seed++ {
		z := zone.New(1 << 20)
		keys := []scalarCase{{NewNull(), "null"}, {NewBool(false), "false"}, {NewBool(true), "true"}, {NewLong(-7), "-7"}}
		for i := range int64(24) {
			keys = append(keys, scalarCase{NewLong(i), fmt.Sprint(i)})
		}
		for _, s := range []string{"0", "01", "x", "y", "42", "042", "", "a\x00b"} {
			keys = append(keys, scalarCase{stringValue(t, z, s, 0), phpString(s)})
		}
		a := arrayValue(t, z)
		var program, want strings.Builder
		program.WriteString("$a=[];")
		rng := rand.New(rand.NewPCG(seed, 13))
		for step := range 300 {
			key := keys[rng.IntN(len(keys))]
			val := NewLong(int64(step))
			var err error
			switch rng.IntN(5) {
			case 0:
				fmt.Fprintf(&program, "unset($a[%s]);", key.php)
				err = ArrayUnset(z, &a, key.v, nil)
			case 1:
				fmt.Fprintf(&program, "$a[]=%d;", step)
				err = ArrayAppend(z, &a, val)
			case 2:
				alias := RetainArray(z, a)
				fmt.Fprintf(&program, "$b=$a;$a[%s]=%d;enc($b);unset($b);", key.php, step)
				err = ArraySet(z, &a, key.v, val, nil)
				encodeArrayValue(z, alias, &want)
				ReleaseArray(z, &alias)
			default:
				fmt.Fprintf(&program, "$a[%s]=%d;", key.php, step)
				err = ArraySet(z, &a, key.v, val, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			program.WriteString("enc($a);echo \"\n\";")
			encodeArrayValue(z, a, &want)
			want.WriteByte('\n')
			fmt.Fprintf(&program, "enc(array_key_exists(%s,$a));enc($a[%s]??null);", key.php, key.php)
			v, ok, err := ArrayGet(z, a, key.v, nil)
			if err != nil {
				t.Fatal(err)
			}
			encodeArrayValue(z, NewBool(ok), &want)
			if !ok {
				v = NewNull()
			}
			encodeArrayValue(z, v, &want)
			assertArrayIndex(t, z, a)
		}
		checkArrayPHP(t, program.String(), want.String())
	}
}

func assertArrayIndex(t testing.TB, z *zone.Zone, a Value) {
	t.Helper()
	h := arrayHeader(z, a)
	b, slots := arrayData(z, h)
	seen := make([]bool, h.Cap)
	for slot, head := range slots {
		for i := head; i != EmptySlot; i = b[i].Next {
			if i >= h.Used || seen[i] {
				t.Fatal("invalid or cyclic collision chain")
			}
			if b[i].Val.IsUndef() || b[i].Hash&uint64(h.Cap-1) != uint64(slot) || b[i].Hash>>63 != 1 {
				t.Fatal("wrong hash slot")
			}
			seen[i] = true
		}
	}
	live := uint32(0)
	for i := uint32(0); i < h.Used; i++ {
		if !b[i].Val.IsUndef() {
			live++
			if h.Kind == ArrayHash && !seen[i] {
				t.Fatal("unreachable bucket")
			}
		}
	}
	if live != h.Len {
		t.Fatal("incorrect live count")
	}
}

func TestArrayCompactionWithoutAllocation(t *testing.T) {
	z := zone.New(544)
	a := arrayValue(t, z)
	// Packed and hash capacity eight both occupy the allocator's 512-byte class;
	// promotion still needs a separate allocation and must be done before filling.
	ReleaseArray(z, &a)
	var err error
	a, err = allocArray(z, ArrayHash, 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := range int64(8) {
		arraySet(t, z, &a, NewLong(i*8), NewLong(i))
	}
	for i := range int64(6) {
		if err := ArrayUnset(z, &a, NewLong(i*8), nil); err != nil {
			t.Fatal(err)
		}
	}
	h := ArrayInfo(z, a)
	arraySet(t, z, &a, NewLong(80), NewLong(80))
	if got := ArrayInfo(z, a); got.DataOffset != h.DataOffset || got.Cap != h.Cap || got.Used != 3 || got.Len != 3 {
		t.Fatalf("compaction=%+v", got)
	}
	assertArrayIndex(t, z, a)
}

func TestArrayCoWFailureBytesAndRecycling(t *testing.T) {
	for _, capacity := range []uint32{544, 576, 1088} {
		for _, mode := range []string{"append", "promote", "unset", "replace"} {
			z := zone.New(capacity)
			a := arrayValue(t, z)
			for i := range int64(8) {
				arraySet(t, z, &a, NewLong(i), NewLong(i))
			}
			b := RetainArray(z, a)
			h := ArrayInfo(z, a)
			raw, err := z.Bytes(a.DataSlot, 32)
			if err != nil {
				t.Fatal(err)
			}
			header := bytes.Clone(raw)
			raw, err = z.Bytes(h.DataOffset, h.Cap*bucketSize)
			if err != nil {
				t.Fatal(err)
			}
			data := bytes.Clone(raw)
			for range 3 {
				switch mode {
				case "append":
					err = ArrayAppend(z, &b, NewLong(8))
				case "promote":
					err = ArraySet(z, &b, byteTable['x'], NewLong(1), nil)
				case "unset":
					err = ArrayUnset(z, &b, NewLong(0), nil)
				case "replace":
					err = ArraySet(z, &b, NewLong(0), NewLong(99), nil)
				}
				// 1088 bytes allows CoW at unchanged capacity, but not growth.
				if capacity == 1088 && (mode == "unset" || mode == "replace") {
					if err != nil || a.DataSlot == b.DataSlot || ArrayInfo(z, a).RefCount != 1 {
						t.Fatal("CoW success", mode, err)
					}
					ReleaseArray(z, &b)
					break
				}
				if err != zone.ErrExhausted || a != b {
					t.Fatal("failed CoW", capacity, mode, err)
				}
				current, err := z.Bytes(a.DataSlot, 32)
				if err != nil || !bytes.Equal(current, header) {
					t.Fatal("header modified")
				}
				current, err = z.Bytes(h.DataOffset, h.Cap*bucketSize)
				if err != nil || !bytes.Equal(current, data) {
					t.Fatal("buckets modified")
				}
			}
		}
	}
	for _, capacity := range []uint32{0, 32, 512} {
		z := zone.New(capacity)
		for range 4 {
			if v, err := NewArray(z); err != zone.ErrExhausted || !v.IsUndef() {
				t.Fatal("constructor failure", v, err)
			}
		}
		if capacity >= 32 {
			if off, err := z.Alloc(32); err != nil || off != 0 {
				t.Fatal("constructor leaked header", off, err)
			}
		}
	}
}

func TestArrayFailureCountedChildren(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			z := zone.New(4096)
			a := arrayValue(t, z)
			for i := range int64(8) {
				arraySet(t, z, &a, NewLong(i), NewLong(i))
			}
			key := stringValue(t, z, "counted-key", 0)
			var val Value
			if nested {
				val = arrayValue(t, z)
			} else {
				val = stringValue(t, z, "counted-value", 0)
			}
			var alias Value
			if shared {
				alias = RetainArray(z, a)
			}
			for {
				if _, err := z.Alloc(8); err == zone.ErrExhausted {
					break
				} else if err != nil {
					t.Fatal(err)
				}
			}
			before := ArrayInfo(z, a)
			if err := ArraySet(z, &a, key, val, nil); err != zone.ErrExhausted {
				t.Fatalf("error=%v", err)
			}
			if ArrayInfo(z, a) != before || StringInfo(z, key).RefCount != 1 {
				t.Fatal("failed mutation changed owners")
			}
			if nested {
				if ArrayInfo(z, val).RefCount != 1 {
					t.Fatal("array retain leaked")
				}
			} else if StringInfo(z, val).RefCount != 1 {
				t.Fatal("string retain leaked")
			}
			ReleaseValue(z, &val)
			ReleaseString(z, &key)
			ReleaseValue(z, &alias)
			ReleaseArray(z, &a)
		}
	}
}

func TestArraySharedCompaction(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	for i := range int64(8) {
		arraySet(t, z, &a, NewLong(i*2), NewLong(i))
	}
	for i := range int64(6) {
		if err := ArrayUnset(z, &a, NewLong(i*2), nil); err != nil {
			t.Fatal(err)
		}
	}
	b := RetainArray(z, a)
	arraySet(t, z, &b, NewLong(100), NewLong(99))
	if h := ArrayInfo(z, b); h.Used != h.Len || h.Len != 3 {
		t.Fatalf("uncompacted copy: %+v", h)
	}
	if h := ArrayInfo(z, a); h.Used != 8 || h.Len != 2 {
		t.Fatalf("source changed: %+v", h)
	}
	assertArrayIndex(t, z, b)
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
}

func TestArrayReleasedHeaderZeroLink(t *testing.T) {
	z := zone.New(4096)
	a, b, guard := arrayValue(t, z), arrayValue(t, z), arrayValue(t, z)
	if a.DataSlot != 0 {
		t.Fatal("first header must be at zero")
	}
	stale := b
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
	defer ReleaseArray(z, &guard)
	defer func() {
		if recover() != "types: invalid array header" {
			t.Fatal("released header with zero link accepted")
		}
	}()
	ArrayInfo(z, stale)
}

func TestArrayIllegalKeyPHP(t *testing.T) {
	z := zone.New(4096)
	a := arrayValue(t, z)
	for _, op := range []string{"set", "get", "isset", "unset"} {
		var err error
		var program string
		switch op {
		case "set":
			err = ArraySet(z, &a, a, NewNull(), nil)
			program = "$a[[]]=1;"
		case "get":
			_, _, err = ArrayGet(z, a, a, nil)
			program = "$x=$a[[]];"
		case "isset":
			_, err = ArrayIsset(z, a, a, nil)
			program = "isset($a[[]]);"
		case "unset":
			err = ArrayUnset(z, &a, a, nil)
			program = "unset($a[[]]);"
		}
		if !errors.Is(err, ErrArrayKey) {
			t.Fatalf("%s: %v", op, err)
		}
		checkArrayPHP(t, "$a=[];try{"+program+"}catch(Error $e){echo $e->getMessage();}", err.Error())
	}
	ReleaseArray(z, &a)
}

func TestArrayObjectKeyPHP(t *testing.T) {
	RegisterObjectClassName(301, "stdClass")
	RegisterObjectClassName(302, "NamedKey")
	z := zone.New(8192)
	a := arrayValue(t, z)
	defer ReleaseArray(z, &a)
	for _, tc := range []struct {
		id   uint32
		name string
	}{{301, "stdClass"}, {302, "NamedKey"}} {
		v, err := NewObject(z, tc.id, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range []string{"set", "get", "isset", "unset"} {
			var statement string
			switch op {
			case "set":
				err = ArraySet(z, &a, v, NewNull(), nil)
				statement = "$a[$k]=1;"
			case "get":
				_, _, err = ArrayGet(z, a, v, nil)
				statement = "$v=$a[$k];"
			case "isset":
				_, err = ArrayIsset(z, a, v, nil)
				statement = "isset($a[$k]);"
			case "unset":
				err = ArrayUnset(z, &a, v, nil)
				statement = "unset($a[$k]);"
			}
			if !errors.Is(err, ErrArrayKey) {
				t.Fatalf("%s: %v", op, err)
			}
			checkArrayPHP(t, "class NamedKey{};$a=[];$k=new "+tc.name+";try{"+statement+"}catch(Error $e){echo $e->getMessage();}", err.Error())
		}
		ReleaseObject(z, &v)
	}
}

func TestArrayReleasedHeaderAndOverflow(t *testing.T) {
	z := zone.New(2048)
	a := arrayValue(t, z)
	guard := arrayValue(t, z)
	stale := a
	ReleaseArray(z, &a)
	func() {
		defer func() {
			if recover() != "types: invalid array header" {
				t.Fatal("dead header accepted")
			}
		}()
		RetainArray(z, stale)
	}()
	h := arrayHeader(z, guard)
	h.RefCount = math.MaxUint32
	func() {
		defer func() {
			if recover() != "types: array reference count overflow" || h.RefCount != math.MaxUint32 {
				t.Fatal("reference overflow")
			}
		}()
		RetainArray(z, guard)
	}()
	h.RefCount = 1
	if _, err := arrayDataSize(ArrayHash, 1<<31); err != ErrArrayTooLarge {
		t.Fatal("size overflow")
	}
	ReleaseArray(z, &guard)
}

func BenchmarkArrayOperations(b *testing.B) {
	for _, op := range []string{"PackedGet", "HashGet", "HashSet", "AppendCoW", "UnsetCoW", "Equal", "Identical", "Cursor", "Normalize", "Failure"} {
		b.Run(op, func(b *testing.B) {
			z := zone.New(1 << 16)
			a := arrayValue(b, z)
			for i := range int64(16) {
				arraySet(b, z, &a, NewLong(i), NewLong(i))
			}
			other := arrayValue(b, z)
			for i := range int64(16) {
				arraySet(b, z, &other, NewLong(i), NewLong(i))
			}
			if op != "PackedGet" && op != "Equal" && op != "Identical" {
				arraySet(b, z, &a, byteTable['x'], NewLong(1))
			}
			key := stringValue(b, z, "9223372036854775808", 0)
			full := zone.New(544)
			failed := arrayValue(b, full)
			RetainArray(full, failed)
			b.ReportAllocs()
			for b.Loop() {
				var err error
				switch op {
				case "PackedGet":
					valueSink, boolSink, err = ArrayGet(z, a, NewLong(7), nil)
				case "HashGet":
					valueSink, boolSink, err = ArrayGet(z, a, byteTable['x'], nil)
				case "HashSet":
					err = ArraySet(z, &a, byteTable['x'], NewLong(2), nil)
				case "AppendCoW":
					c := RetainArray(z, a)
					err = ArrayAppend(z, &c, NewLong(17))
					ReleaseArray(z, &c)
				case "UnsetCoW":
					c := RetainArray(z, a)
					err = ArrayUnset(z, &c, NewLong(7), nil)
					ReleaseArray(z, &c)
				case "Equal":
					boolSink = ArrayEqual(z, a, other)
				case "Identical":
					boolSink = ArrayIdentical(z, a, other)
				case "Cursor":
					ArrayForEach(z, a, func(k, v Value) bool { valueSink = v; return true })
				case "Normalize":
					valueSink, err = NormalizeArrayKey(z, key, nil)
				case "Failure":
					errorSink = ArrayAppend(full, &failed, NewLong(1))
					if errorSink != zone.ErrExhausted {
						b.Fatal(errorSink)
					}
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func arraySet(t testing.TB, z *zone.Zone, a *Value, k, v Value) {
	t.Helper()
	if err := ArraySet(z, a, k, v, nil); err != nil {
		t.Fatal(err)
	}
}

func TestArrayLayoutAndOrder(t *testing.T) {
	if unsafe.Sizeof(ArrayHeader{}) != 32 || unsafe.Offsetof(ArrayHeader{}.RefCount) != 0 || unsafe.Sizeof(Bucket{}) != 48 {
		t.Fatal("layout")
	}
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		if typ.Kind() == reflect.Struct {
			for f := range typ.Fields() {
				check(f.Type)
			}
			return
		}
		if typ.Kind() < reflect.Int || typ.Kind() > reflect.Uint64 {
			t.Fatalf("non-scalar field %v", typ)
		}
	}
	check(reflect.TypeFor[ArrayHeader]())
	check(reflect.TypeFor[Bucket]())
	z := zone.New(8192)
	a := arrayValue(t, z)
	if ArrayInfo(z, a).NextIdx != math.MinInt64 {
		t.Fatal("initial NextIdx")
	}
	for i := range int64(8) {
		arraySet(t, z, &a, NewLong(i), NewLong(i+10))
	}
	if ArrayInfo(z, a).Kind != ArrayPacked {
		t.Fatal("not packed")
	}
	if err := ArrayUnset(z, &a, NewLong(2), nil); err != nil {
		t.Fatal(err)
	}
	arraySet(t, z, &a, NewLong(2), NewLong(99))
	if ArrayInfo(z, a).Kind != ArrayHash {
		t.Fatal("reinsertion must preserve order")
	}
	want := []int64{0, 1, 3, 4, 5, 6, 7, 2}
	i := 0
	ArrayForEach(z, a, func(k, v Value) bool {
		if k.ToLong() != want[i] {
			t.Fatalf("index %d key %d", i, k.ToLong())
		}
		i++
		return true
	})
	if i != 8 || ArrayLen(z, a) != 8 {
		t.Fatal("count")
	}
	ReleaseArray(z, &a)
	if !a.IsUndef() {
		t.Fatal("release")
	}
}

func TestArrayFailureAtomicity(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, op := range []string{"set", "append", "unset", "promote"} {
			t.Run(op+map[bool]string{false: "/unique", true: "/shared"}[shared], func(t *testing.T) {
				z := zone.New(544)
				a := arrayValue(t, z)
				for i := range int64(8) {
					arraySet(t, z, &a, NewLong(i), NewLong(i))
				}
				if shared {
					RetainArray(z, a)
				}
				before, h := a, ArrayInfo(z, a)
				var err error
				switch op {
				case "set":
					err = ArraySet(z, &a, NewLong(8), NewLong(9), nil)
				case "append":
					err = ArrayAppend(z, &a, NewLong(9))
				case "unset":
					if !shared {
						return
					}
					err = ArrayUnset(z, &a, NewLong(0), nil)
				case "promote":
					err = ArraySet(z, &a, byteTable['x'], NewLong(9), nil)
				}
				if !errors.Is(err, zone.ErrExhausted) || a != before || ArrayInfo(z, a) != h {
					t.Fatalf("atomicity: %v %+v %+v", err, ArrayInfo(z, a), h)
				}
				for i := range int64(8) {
					v, ok, err := ArrayGet(z, a, NewLong(i), nil)
					if err != nil || !ok || v.ToLong() != i {
						t.Fatal("contents changed")
					}
				}
			})
		}
	}
}

func TestNormalizeArrayKeyAndArrayKeySameFastPaths(t *testing.T) {
	z := zone.New(65536)
	canonical := []struct {
		text string
		want int64
	}{
		{"0", 0},
		{"-5", -5},
		{"42", 42},
		{"9223372036854775807", math.MaxInt64},
		{"-9223372036854775808", math.MinInt64},
	}
	for _, tc := range canonical {
		s := stringValue(t, z, tc.text, 0)
		got, err := NormalizeArrayKey(z, s, nil)
		if err != nil || got.Type != TypeLong || got.ToLong() != tc.want {
			t.Fatalf("NormalizeArrayKey(%q) = %+v, %v; want Long(%d)", tc.text, got, err, tc.want)
		}
		ReleaseString(z, &s)
	}

	nonCanonical := []string{"", "-", "-0", "01", "00", "+5", "k_15", "user_id", "name", "9223372036854775808", "-9223372036854775809"}
	for _, text := range nonCanonical {
		s := stringValue(t, z, text, 0)
		got, err := NormalizeArrayKey(z, s, nil)
		if err != nil || got != s {
			t.Fatalf("NormalizeArrayKey(%q) = %+v, %v; want unchanged string %+v", text, got, err, s)
		}
		ReleaseString(z, &s)
	}

	k1 := stringValue(t, z, "k_15", 0)
	k2 := stringValue(t, z, "k_15", 0)
	k3 := stringValue(t, z, "k_16", 0)
	kShort := stringValue(t, z, "k_1", 0)
	if !arrayKeySame(z, k1, k1) {
		t.Fatal("same DataSlot must compare equal")
	}
	if !arrayKeySame(z, byteTable['a'], byteTable['a']) || arrayKeySame(z, byteTable['a'], byteTable['b']) {
		t.Fatal("processZone byteTable comparison failed")
	}
	if !arrayKeySame(z, k1, k2) {
		t.Fatal("distinct DataSlots with equal bytes must compare equal")
	}
	if arrayKeySame(z, k1, k3) {
		t.Fatal("same length with different bytes must compare unequal")
	}
	if arrayKeySame(z, k1, kShort) {
		t.Fatal("different lengths must short-circuit as unequal")
	}
	if arrayKeySame(z, k1, NewLong(4)) {
		t.Fatal("different types must compare unequal")
	}

	// Repeated updates and lookups with freshly allocated keys must cache the key hash on update.
	a := arrayValue(t, z)
	for i := range int64(128) {
		k, err := ConcatStringLong(z, kShort, i&15)
		if err != nil {
			t.Fatal(err)
		}
		if err := ArraySet(z, &a, k, NewLong(i), nil); err != nil {
			t.Fatal(err)
		}
		if StringInfo(z, k).Hash == 0 {
			t.Fatalf("iteration %d: ArraySet did not cache hash on updated key", i)
		}
		got, ok, err := ArrayGet(z, a, k, nil)
		if err != nil || !ok || got.ToLong() != i {
			t.Fatalf("iteration %d: ArrayGet = %+v, %v, %v", i, got, ok, err)
		}
		ReleaseString(z, &k)
	}
	ReleaseArray(z, &a)
	ReleaseString(z, &kShort)
	ReleaseString(z, &k3)
	ReleaseString(z, &k2)
	ReleaseString(z, &k1)
}

func TestArrayResourceElementsAndSeparateEmpty(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	res := Value{Type: TypeResource, Flags: FlagZoneRequest, Extra: 1, DataSlot: 42, Scalar: 42}
	if err := ArrayAppend(z, &a, res); err != nil {
		t.Fatalf("ArrayAppend(TypeResource): %v", err)
	}
	if err := ArraySet(z, &a, byteTable['s'], res, nil); err != nil {
		t.Fatalf("ArraySet(TypeResource): %v", err)
	}
	got0, ok, err := ArrayGet(z, a, NewLong(0), nil)
	if err != nil || !ok || got0 != res {
		t.Fatalf("ArrayGet(0) = %+v, %v, %v", got0, ok, err)
	}
	gotS, ok, err := ArrayGet(z, a, byteTable['s'], nil)
	if err != nil || !ok || gotS != res {
		t.Fatalf("ArrayGet('s') = %+v, %v, %v", gotS, ok, err)
	}
	if !ToBoolZ(z, res) {
		t.Fatal("ToBoolZ(TypeResource) = false; want true")
	}
	ReleaseArray(z, &a)

	emptyA := arrayValue(t, z)
	emptyB := RetainArray(z, emptyA)
	if err := SeparateArray(z, &emptyB); err != nil {
		t.Fatalf("SeparateArray(empty): %v", err)
	}
	if emptyA.DataSlot == emptyB.DataSlot || ArrayInfo(z, emptyA).RefCount != 1 || ArrayInfo(z, emptyB).RefCount != 1 {
		t.Fatalf("empty shared array not separated: a=%+v b=%+v", ArrayInfo(z, emptyA), ArrayInfo(z, emptyB))
	}
	ReleaseArray(z, &emptyA)
	ReleaseArray(z, &emptyB)
}


