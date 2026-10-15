package spl_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/spl"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func mustStr(t *testing.T, z *zone.Zone, s string) types.Value {
	t.Helper()
	v, err := types.NewString(z, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSplDoublyLinkedListStackQueue(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(spl.CleanupDispatcher(z))

	obj, dll, err := spl.NewSplDoublyLinkedList(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &obj)

	if resolved, err := spl.DLLFromObject(z, obj); err != nil || resolved != dll {
		t.Fatalf("DLLFromObject failed: %v", err)
	}
	if !dll.IsEmpty() || dll.Count() != 0 {
		t.Fatal("expected empty initial list")
	}

	// Exceptions on empty list.
	for _, fn := range []func() (types.Value, error){
		func() (types.Value, error) { return dll.Pop(z) },
		func() (types.Value, error) { return dll.Shift(z) },
		func() (types.Value, error) { return dll.Top() },
		func() (types.Value, error) { return dll.Bottom() },
	} {
		if _, err := fn(); err == nil {
			t.Fatal("expected RuntimeException on empty list")
		} else if se, ok := errors.AsType[spl.SPLException](err); !ok || se.Class != "RuntimeException" {
			t.Fatalf("expected RuntimeException, got %v", err)
		}
	}

	sA := mustStr(t, z, "a")
	defer types.ReleaseValue(z, &sA)
	sB := mustStr(t, z, "b")
	defer types.ReleaseValue(z, &sB)
	sC := mustStr(t, z, "c")
	defer types.ReleaseValue(z, &sC)
	sZero := mustStr(t, z, "zero")
	defer types.ReleaseValue(z, &sZero)

	dll.Push(z, sA)
	dll.Push(z, sC)
	if err := dll.Add(z, 1, sB); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	dll.Unshift(z, sZero)

	if dll.Count() != 4 || dll.IsEmpty() {
		t.Fatalf("Count = %d, want 4", dll.Count())
	}
	bot, err := dll.Bottom()
	if err != nil || types.StringData(z, bot) != "zero" {
		t.Fatalf("Bottom = %v, err=%v", bot, err)
	}
	top, err := dll.Top()
	if err != nil || types.StringData(z, top) != "c" {
		t.Fatalf("Top = %v, err=%v", top, err)
	}

	shifted, err := dll.Shift(z)
	if err != nil || types.StringData(z, shifted) != "zero" {
		t.Fatalf("Shift = %v, err=%v", shifted, err)
	}
	types.ReleaseValue(z, &shifted)

	// LIFO iteration keep.
	if _, err := dll.SetIteratorMode(spl.IT_MODE_LIFO | spl.IT_MODE_KEEP); err != nil {
		t.Fatal(err)
	}
	var lifoKeys []int
	var lifoVals []string
	for dll.Rewind(); dll.Valid(); dll.Next(z) {
		lifoKeys = append(lifoKeys, dll.Key())
		lifoVals = append(lifoVals, types.StringData(z, dll.Current()))
	}
	if !reflect.DeepEqual(lifoKeys, []int{2, 1, 0}) || !reflect.DeepEqual(lifoVals, []string{"c", "b", "a"}) {
		t.Fatalf("LIFO iteration = (%v, %v)", lifoKeys, lifoVals)
	}

	// Prev in FIFO mode.
	if _, err := dll.SetIteratorMode(spl.IT_MODE_FIFO | spl.IT_MODE_KEEP); err != nil {
		t.Fatal(err)
	}
	dll.Rewind()
	dll.Next(z)
	dll.Prev(z)
	if dll.Key() != 0 || types.StringData(z, dll.Current()) != "a" {
		t.Fatalf("Prev failed: key=%d cur=%s", dll.Key(), types.StringData(z, dll.Current()))
	}

	// SplStack and SplQueue frozen modes.
	stackObj, stack, err := spl.NewSplStack(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &stackObj)
	if stack.GetIteratorMode() != 6 {
		t.Fatalf("SplStack mode = %d, want 6", stack.GetIteratorMode())
	}
	if _, err := stack.SetIteratorMode(spl.IT_MODE_FIFO); err == nil {
		t.Fatal("expected RuntimeException when setting FIFO on SplStack")
	}

	qObj, queue, err := spl.NewSplQueue(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &qObj)
	if queue.GetIteratorMode() != 4 {
		t.Fatalf("SplQueue mode = %d, want 4", queue.GetIteratorMode())
	}
	if _, err := queue.SetIteratorMode(spl.IT_MODE_LIFO); err == nil {
		t.Fatal("expected RuntimeException when setting LIFO on SplQueue")
	}
	queue.Enqueue(z, sA)
	queue.Enqueue(z, sB)
	dq, err := queue.Dequeue(z)
	if err != nil || types.StringData(z, dq) != "a" {
		t.Fatalf("Dequeue = %v, err=%v", dq, err)
	}
	types.ReleaseValue(z, &dq)
}

func TestSplFixedArray(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(spl.CleanupDispatcher(z))

	if _, _, err := spl.NewSplFixedArray(z, -1); err == nil {
		t.Fatal("expected ValueError on negative size")
	} else if se, ok := errors.AsType[spl.SPLException](err); !ok || se.Class != "ValueError" {
		t.Fatalf("expected ValueError, got %v", err)
	}

	obj, fa, err := spl.NewSplFixedArray(z, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &obj)

	if fa.Count() != 3 || fa.GetSize() != 3 {
		t.Fatalf("size = %d, want 3", fa.Count())
	}
	if fa.OffsetExists(0) {
		t.Fatal("OffsetExists(0) on null slot must be false")
	}
	if err := fa.OffsetSet(z, 1, types.NewLong(42)); err != nil {
		t.Fatal(err)
	}
	if !fa.OffsetExists(1) {
		t.Fatal("OffsetExists(1) must be true after OffsetSet")
	}
	v, err := fa.OffsetGet(1)
	if err != nil || v.ToLong() != 42 {
		t.Fatalf("OffsetGet(1) = %v, %v", v, err)
	}
	if err := fa.OffsetUnset(z, 1); err != nil {
		t.Fatal(err)
	}
	if fa.OffsetExists(1) {
		t.Fatal("OffsetExists(1) must be false after OffsetUnset")
	}
	if _, err := fa.OffsetGet(3); err == nil {
		t.Fatal("expected RuntimeException on out-of-range OffsetGet")
	}

	if _, err := fa.SetSize(z, 5); err != nil || fa.GetSize() != 5 {
		t.Fatalf("SetSize(5) failed: %v", err)
	}
	if _, err := fa.SetSize(z, 2); err != nil || fa.GetSize() != 2 {
		t.Fatalf("SetSize(2) failed: %v", err)
	}

	// fromArray with preserveKeys = true and false.
	phpArr, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseArray(z, &phpArr)
	_ = types.ArraySet(z, &phpArr, types.NewLong(2), types.NewLong(99), nil)
	_ = types.ArraySet(z, &phpArr, types.NewLong(0), types.NewLong(11), nil)

	faObj2, fa2, err := spl.FixedArrayFromArray(z, phpArr, true)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &faObj2)
	if fa2.Count() != 3 {
		t.Fatalf("fromArray(preserveKeys=true) size = %d, want 3", fa2.Count())
	}
	v0, _ := fa2.OffsetGet(0)
	v1, _ := fa2.OffsetGet(1)
	v2, _ := fa2.OffsetGet(2)
	if v0.ToLong() != 11 || !v1.IsNull() || v2.ToLong() != 99 {
		t.Fatalf("unexpected fromArray contents: %v, %v, %v", v0, v1, v2)
	}
}

func TestSplObjectStorageAndFunctions(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(spl.CleanupDispatcher(z))

	o1, err := types.NewObject(z, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &o1)
	o2, err := types.NewObject(z, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &o2)
	o3, err := types.NewObject(z, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &o3)

	id1, err := spl.SplObjectID(z, o1)
	if err != nil || id1.ToLong() <= 0 {
		t.Fatalf("SplObjectID = %v, %v", id1, err)
	}
	h1, err := spl.SplObjectHash(z, o1)
	if err != nil || len(types.StringData(z, h1)) != 32 {
		t.Fatalf("SplObjectHash = %v, %v", h1, err)
	}
	defer types.ReleaseValue(z, &h1)

	classesArr, err := spl.SplClasses(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &classesArr)
	if types.ArrayLen(z, classesArr) < 50 {
		t.Fatalf("expected >= 50 SPL classes, got %d", types.ArrayLen(z, classesArr))
	}

	sObj1, s1, err := spl.NewSplObjectStorage(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &sObj1)

	sObj2, s2, err := spl.NewSplObjectStorage(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &sObj2)

	if err := s1.Attach(z, o1, types.NewLong(100)); err != nil {
		t.Fatal(err)
	}
	if err := s1.Attach(z, o2, types.NewLong(200)); err != nil {
		t.Fatal(err)
	}
	if err := s2.Attach(z, o2, types.NewLong(250)); err != nil {
		t.Fatal(err)
	}
	if err := s2.Attach(z, o3, types.NewLong(300)); err != nil {
		t.Fatal(err)
	}

	if n := s1.AddAll(z, s2); n != 3 {
		t.Fatalf("AddAll count = %d, want 3", n)
	}
	info2, err := s1.OffsetGet(z, o2)
	if err != nil || info2.ToLong() != 250 {
		t.Fatalf("OffsetGet(o2) after AddAll = %v, %v", info2, err)
	}

	if n := s1.RemoveAllExcept(z, s2); n != 2 {
		t.Fatalf("RemoveAllExcept count = %d, want 2", n)
	}
	if has1, _ := s1.Contains(z, o1); has1 {
		t.Fatal("o1 should have been removed by RemoveAllExcept")
	}
	if n := s1.RemoveAll(z, s2); n != 0 {
		t.Fatalf("RemoveAll count = %d, want 0", n)
	}
	if _, err := s1.OffsetGet(z, o1); err == nil {
		t.Fatal("expected UnexpectedValueException on missing object")
	} else if se, ok := errors.AsType[spl.SPLException](err); !ok || se.Class != "UnexpectedValueException" {
		t.Fatalf("expected UnexpectedValueException, got %v", err)
	}
}

func TestArrayObjectAndArrayIterator(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(spl.CleanupDispatcher(z))

	initArr, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseArray(z, &initArr)

	for k, v := range map[string]string{
		"a": "img12.png",
		"b": "img10.png",
		"c": "img2.png",
		"d": "img1.png",
		"e": "IMG2.png",
		"f": "img02.png",
	} {
		kv := mustStr(t, z, k)
		vv := mustStr(t, z, v)
		_ = types.ArraySet(z, &initArr, kv, vv, nil)
		types.ReleaseValue(z, &kv)
		types.ReleaseValue(z, &vv)
	}

	aoObj, ao, err := spl.NewArrayObject(z, initArr, spl.ARRAY_AS_PROPS, "ArrayIterator")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &aoObj)

	if ao.GetFlags() != spl.ARRAY_AS_PROPS {
		t.Fatalf("GetFlags = %d, want %d", ao.GetFlags(), spl.ARRAY_AS_PROPS)
	}
	ao.SetFlags(spl.STD_PROP_LIST)
	if ao.GetFlags() != spl.STD_PROP_LIST {
		t.Fatalf("GetFlags = %d, want %d", ao.GetFlags(), spl.STD_PROP_LIST)
	}

	if err := ao.Natsort(z); err != nil {
		t.Fatal(err)
	}
	itObj, it, err := ao.GetIterator(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &itObj)

	var natOrder []string
	for it.Rewind(z); it.Valid(z); it.Next(z) {
		natOrder = append(natOrder, types.StringData(z, it.Key(z))+":"+types.StringData(z, it.Current(z)))
	}
	wantNat := []string{"e:IMG2.png", "f:img02.png", "d:img1.png", "c:img2.png", "b:img10.png", "a:img12.png"}
	if !reflect.DeepEqual(natOrder, wantNat) {
		t.Fatalf("natsort = %v, want %v", natOrder, wantNat)
	}

	if err := it.Seek(z, 3); err != nil {
		t.Fatal(err)
	}
	if got := types.StringData(z, it.Current(z)); got != "img2.png" {
		t.Fatalf("Seek(3) Current = %q, want img2.png", got)
	}
	if err := it.Seek(z, 99); err == nil {
		t.Fatal("expected OutOfBoundsException on Seek(99)")
	} else if se, ok := errors.AsType[spl.SPLException](err); !ok || se.Class != "OutOfBoundsException" {
		t.Fatalf("expected OutOfBoundsException, got %v", err)
	}

	if err := ao.Uksort(z, func(z *zone.Zone, a, b types.Value) (int, error) {
		// Reverse key order.
		return -cmp.Compare(types.StringData(z, a), types.StringData(z, b)), nil
	}); err != nil {
		t.Fatal(err)
	}
	var revKeys []string
	for it.Rewind(z); it.Valid(z); it.Next(z) {
		revKeys = append(revKeys, types.StringData(z, it.Key(z)))
	}
	if !reflect.DeepEqual(revKeys, []string{"f", "e", "d", "c", "b", "a"}) {
		t.Fatalf("uksort reverse keys = %v", revKeys)
	}
}

func TestSPLDifferentialVsPHP(t *testing.T) {
	phpScript := `
$out = [];

// 1. SplDoublyLinkedList & SplStack & SplQueue
$dll = new SplDoublyLinkedList();
$dll->push(10);
$dll->push(30);
$dll->add(1, 20);
$dll->unshift(5);
$out["dll_shift"] = $dll->shift();
$out["dll_top"] = $dll->top();
$out["dll_bottom"] = $dll->bottom();
$dll->setIteratorMode(SplDoublyLinkedList::IT_MODE_LIFO | SplDoublyLinkedList::IT_MODE_DELETE);
$lifoDel = [];
for ($dll->rewind(); $dll->valid(); $dll->next()) {
    $lifoDel[] = [$dll->key(), $dll->current()];
}
$out["dll_lifo_del"] = $lifoDel;
$out["dll_after_del_count"] = $dll->count();

// 2. SplFixedArray
$fa = SplFixedArray::fromArray([2 => "two", 0 => "zero"], true);
$fa->setSize(4);
$fa[3] = "three";
$out["fa_array"] = $fa->toArray();

// 3. ArrayObject sorting
$ao = new ArrayObject(["k2" => "img10", "k1" => "img2", "k3" => "IMG1", "k4" => "img02"]);
$ao->natcasesort();
$natKeys = [];
foreach ($ao->getIterator() as $k => $v) {
    $natKeys[] = "$k=$v";
}
$out["ao_natcase"] = $natKeys;

// 4. spl_classes count
$out["spl_classes_count"] = count(spl_classes());

echo json_encode($out);
`
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", phpScript)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("/usr/bin/php failed: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &want); err != nil {
		t.Fatalf("unmarshal php json: %v", err)
	}

	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(spl.CleanupDispatcher(z))

	// 1. Verify SplDoublyLinkedList against PHP oracle.
	dllObj, dll, err := spl.NewSplDoublyLinkedList(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &dllObj)
	dll.Push(z, types.NewLong(10))
	dll.Push(z, types.NewLong(30))
	if err := dll.Add(z, 1, types.NewLong(20)); err != nil {
		t.Fatal(err)
	}
	dll.Unshift(z, types.NewLong(5))
	sh, err := dll.Shift(z)
	if err != nil || float64(sh.ToLong()) != want["dll_shift"].(float64) {
		t.Fatalf("dll_shift = %v, want %v", sh, want["dll_shift"])
	}
	top, _ := dll.Top()
	bot, _ := dll.Bottom()
	if float64(top.ToLong()) != want["dll_top"].(float64) || float64(bot.ToLong()) != want["dll_bottom"].(float64) {
		t.Fatalf("top/bottom mismatch")
	}
	_, _ = dll.SetIteratorMode(spl.IT_MODE_LIFO | spl.IT_MODE_DELETE)
	var gotLifoDel [][]int64
	for dll.Rewind(); dll.Valid(); dll.Next(z) {
		gotLifoDel = append(gotLifoDel, []int64{int64(dll.Key()), dll.Current().ToLong()})
	}
	wantLifoDel := want["dll_lifo_del"].([]any)
	if len(gotLifoDel) != len(wantLifoDel) {
		t.Fatalf("dll_lifo_del len = %d, want %d", len(gotLifoDel), len(wantLifoDel))
	}
	for i, pair := range wantLifoDel {
		p := pair.([]any)
		if gotLifoDel[i][0] != int64(p[0].(float64)) || gotLifoDel[i][1] != int64(p[1].(float64)) {
			t.Fatalf("dll_lifo_del[%d] = %v, want %v", i, gotLifoDel[i], p)
		}
	}
	if float64(dll.Count()) != want["dll_after_del_count"].(float64) {
		t.Fatalf("dll_after_del_count = %d, want %v", dll.Count(), want["dll_after_del_count"])
	}

	// 2. Verify SplFixedArray against PHP oracle.
	srcArr, _ := types.NewArray(z)
	defer types.ReleaseArray(z, &srcArr)
	sTwo := mustStr(t, z, "two")
	defer types.ReleaseValue(z, &sTwo)
	sZero := mustStr(t, z, "zero")
	defer types.ReleaseValue(z, &sZero)
	sThree := mustStr(t, z, "three")
	defer types.ReleaseValue(z, &sThree)
	_ = types.ArraySet(z, &srcArr, types.NewLong(2), sTwo, nil)
	_ = types.ArraySet(z, &srcArr, types.NewLong(0), sZero, nil)

	faObj, fa, err := spl.FixedArrayFromArray(z, srcArr, true)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &faObj)
	_, _ = fa.SetSize(z, 4)
	_ = fa.OffsetSet(z, 3, sThree)
	wantFA := want["fa_array"].([]any)
	if fa.Count() != len(wantFA) {
		t.Fatalf("fa count = %d, want %d", fa.Count(), len(wantFA))
	}

	// 3. Verify ArrayObject natcasesort against PHP oracle.
	aoInput, _ := types.NewArray(z)
	defer types.ReleaseArray(z, &aoInput)
	for _, kv := range [][2]string{{"k2", "img10"}, {"k1", "img2"}, {"k3", "IMG1"}, {"k4", "img02"}} {
		k := mustStr(t, z, kv[0])
		v := mustStr(t, z, kv[1])
		_ = types.ArraySet(z, &aoInput, k, v, nil)
		types.ReleaseValue(z, &k)
		types.ReleaseValue(z, &v)
	}
	aoObj, ao, err := spl.NewArrayObject(z, aoInput, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &aoObj)
	if err := ao.Natcasesort(z); err != nil {
		t.Fatal(err)
	}
	itObj, it, err := ao.GetIterator(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &itObj)
	var gotNatCase []string
	for it.Rewind(z); it.Valid(z); it.Next(z) {
		gotNatCase = append(gotNatCase, types.StringData(z, it.Key(z))+"="+types.StringData(z, it.Current(z)))
	}
	wantNatCase := want["ao_natcase"].([]any)
	for i, w := range wantNatCase {
		if gotNatCase[i] != w.(string) {
			t.Fatalf("ao_natcase[%d] = %q, want %q", i, gotNatCase[i], w.(string))
		}
	}

	// 4. Verify spl_classes count against PHP oracle.
	clsArr, err := spl.SplClasses(z)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &clsArr)
	if float64(types.ArrayLen(z, clsArr)) != want["spl_classes_count"].(float64) {
		t.Fatalf("spl_classes count = %d, want %v", types.ArrayLen(z, clsArr), want["spl_classes_count"])
	}
}
