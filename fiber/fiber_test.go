package fiber_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
	"testing/synctest"

	"code.hazyhaar.fr/devhoros/php127/fiber"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestFiberLifecycleSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		z := zone.New(zone.DefaultCapacity)
		defer z.Reset(fiber.CleanupDispatcher(z))

		if cur := fiber.GetCurrent(z); cur != nil {
			t.Fatalf("expected nil current fiber before start, got %v", cur)
		}
		if curObj := fiber.GetCurrentObject(z); !curObj.IsNull() {
			t.Fatalf("expected null current object before start, got %v", curObj)
		}

		var fRef *fiber.Fiber
		obj, f, err := fiber.NewObject(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			if fiber.GetCurrent(z) != fRef {
				t.Errorf("GetCurrent mismatch inside fiber")
			}
			if !fRef.IsRunning() || !fRef.IsStarted() || fRef.IsSuspended() || fRef.IsTerminated() {
				t.Errorf("unexpected state flags inside running fiber")
			}
			s1, err := types.Concat(z, args[0], args[1])
			if err != nil {
				return types.NewNull(), err
			}
			resumed, err := fiber.Suspend(z, s1)
			if err != nil {
				return types.NewNull(), err
			}
			s2, err := types.Concat(z, s1, resumed)
			if err != nil {
				return types.NewNull(), err
			}
			return s2, nil
		})
		if err != nil {
			t.Fatalf("NewObject failed: %v", err)
		}
		defer types.ReleaseValue(z, &obj)
		fRef = f

		resolved, err := fiber.FromObject(z, obj)
		if err != nil || resolved != f {
			t.Fatalf("FromObject failed: %v", err)
		}

		if f.IsStarted() || f.IsSuspended() || f.IsRunning() || f.IsTerminated() {
			t.Fatalf("unexpected initial state: %v", f.State())
		}

		a0, err := types.NewString(z, "hello ", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer types.ReleaseValue(z, &a0)
		a1, err := types.NewString(z, "fiber", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer types.ReleaseValue(z, &a1)

		yielded, err := f.Start(z, a0, a1)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		synctest.Wait()

		if got := types.StringData(z, yielded); got != "hello fiber" {
			t.Fatalf("Start yielded %q, want %q", got, "hello fiber")
		}
		if !f.IsStarted() || !f.IsSuspended() || f.IsRunning() || f.IsTerminated() {
			t.Fatalf("expected suspended state after first yield, got %v", f.State())
		}

		// GetReturn while suspended must fail with FiberError.
		if _, err := f.GetReturn(z); err == nil {
			t.Fatalf("expected GetReturn error while suspended")
		} else if fe, ok := errors.AsType[fiber.FiberError](err); !ok {
			t.Fatalf("expected FiberError, got %T: %v", err, err)
		} else if fe.Message == "" {
			t.Fatalf("empty FiberError message")
		}

		resumeArg, err := types.NewString(z, " world", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer types.ReleaseValue(z, &resumeArg)

		finalYield, err := f.Resume(z, resumeArg)
		if err != nil {
			t.Fatalf("Resume failed: %v", err)
		}
		synctest.Wait()

		if !finalYield.IsNull() {
			t.Fatalf("Resume on termination must return null, got %v", finalYield)
		}
		if !f.IsTerminated() || f.IsSuspended() || f.IsRunning() {
			t.Fatalf("expected terminated state, got %v", f.State())
		}

		ret, err := f.GetReturn(z)
		if err != nil {
			t.Fatalf("GetReturn failed: %v", err)
		}
		if got := types.StringData(z, ret); got != "hello fiber world" {
			t.Fatalf("GetReturn = %q, want %q", got, "hello fiber world")
		}
	})
}

func TestFiberNestedAndRaceFreeZoneAccessSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		z := zone.New(zone.DefaultCapacity)
		defer z.Reset(fiber.CleanupDispatcher(z))

		sharedArr, err := types.NewArray(z)
		if err != nil {
			t.Fatal(err)
		}
		defer types.ReleaseValue(z, &sharedArr)

		var outer, inner *fiber.Fiber

		inner, err = fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			if fiber.GetCurrent(z) != inner {
				t.Errorf("expected inner fiber as current")
			}
			for i := range int64(10) {
				if err := types.ArrayAppend(z, &sharedArr, types.NewLong(100+i)); err != nil {
					return types.NewNull(), err
				}
				v, err := fiber.Suspend(z, types.NewLong(i*2))
				if err != nil {
					return types.NewNull(), err
				}
				if err := types.ArrayAppend(z, &sharedArr, v); err != nil {
					return types.NewNull(), err
				}
			}
			return types.NewLong(999), nil
		})
		if err != nil {
			t.Fatal(err)
		}

		outer, err = fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			if fiber.GetCurrent(z) != outer {
				t.Errorf("expected outer fiber as current")
			}
			v, err := inner.Start(z)
			if err != nil {
				return types.NewNull(), err
			}
			if fiber.GetCurrent(z) != outer {
				t.Errorf("expected outer fiber restored after inner suspend")
			}
			for range int64(10) {
				fromMain, err := fiber.Suspend(z, v)
				if err != nil {
					return types.NewNull(), err
				}
				if fiber.GetCurrent(z) != outer {
					t.Errorf("expected outer fiber after outer resume")
				}
				v, err = inner.Resume(z, fromMain)
				if err != nil {
					return types.NewNull(), err
				}
			}
			return inner.GetReturn(z)
		})
		if err != nil {
			t.Fatal(err)
		}

		y, err := outer.Start(z)
		if err != nil {
			t.Fatal(err)
		}
		for step := range int64(10) {
			if y.ToLong() != step*2 {
				t.Fatalf("step %d: got yield %d, want %d", step, y.ToLong(), step*2)
			}
			if err := types.ArrayAppend(z, &sharedArr, types.NewLong(200+step)); err != nil {
				t.Fatal(err)
			}
			y, err = outer.Resume(z, types.NewLong(300+step))
			if err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()

		if !y.IsNull() {
			t.Fatalf("expected null on outer completion, got %v", y)
		}
		ret, err := outer.GetReturn(z)
		if err != nil {
			t.Fatal(err)
		}
		if ret.ToLong() != 999 {
			t.Fatalf("outer GetReturn = %d, want 999", ret.ToLong())
		}
		if gotLen := types.ArrayLen(z, sharedArr); gotLen != 30 {
			t.Fatalf("sharedArr length = %d, want 30", gotLen)
		}
	})
}

func TestFiberThrowAndErrorsSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		z := zone.New(zone.DefaultCapacity)
		defer z.Reset(fiber.CleanupDispatcher(z))

		// 1. Suspend outside of a fiber.
		if _, err := fiber.Suspend(z, types.NewNull()); err == nil {
			t.Fatal("expected error when suspending outside fiber")
		} else if fe, ok := errors.AsType[fiber.FiberError](err); !ok || fe.Message != "Cannot suspend outside of a fiber" {
			t.Fatalf("unexpected error: %v", err)
		}

		// 2. Throw caught inside fiber, then normal return.
		fCaught, err := fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			_, err := fiber.Suspend(z, types.NewLong(1))
			if err != nil {
				return types.NewLong(42), nil
			}
			return types.NewLong(0), nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// GetReturn and Resume before start.
		if _, err := fCaught.GetReturn(z); err == nil {
			t.Fatal("expected error on GetReturn before start")
		}
		if _, err := fCaught.Resume(z, types.NewNull()); err == nil {
			t.Fatal("expected error on Resume before start")
		}
		if _, err := fCaught.Throw(z, errors.New("boom")); err == nil {
			t.Fatal("expected error on Throw before start")
		}

		if _, err := fCaught.Start(z); err != nil {
			t.Fatal(err)
		}
		// Start a second time must fail.
		if _, err := fCaught.Start(z); err == nil {
			t.Fatal("expected error on second Start")
		}

		excStr, err := types.NewString(z, "injected fault", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer types.ReleaseValue(z, &excStr)

		res, err := fCaught.ThrowValue(z, excStr)
		if err != nil {
			t.Fatalf("ThrowValue caught inside fiber should succeed, got: %v", err)
		}
		if !res.IsNull() {
			t.Fatalf("expected null from ThrowValue when fiber returns, got %v", res)
		}
		ret, err := fCaught.GetReturn(z)
		if err != nil || ret.ToLong() != 42 {
			t.Fatalf("GetReturn after caught throw = (%v, %v), want 42", ret, err)
		}

		// 3. Throw uncaught inside fiber.
		fUncaught, err := fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			return fiber.Suspend(z, types.NewLong(7))
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fUncaught.Start(z); err != nil {
			t.Fatal(err)
		}
		boom := errors.New("uncaught exception")
		if _, err := fUncaught.Throw(z, boom); !errors.Is(err, boom) {
			t.Fatalf("expected boom error from Throw, got %v", err)
		}
		if !fUncaught.IsTerminated() {
			t.Fatal("expected terminated state after uncaught throw")
		}
		if _, err := fUncaught.GetReturn(z); err == nil {
			t.Fatal("expected FiberError on GetReturn after uncaught exception")
		} else if fe, ok := errors.AsType[fiber.FiberError](err); !ok || fe.Message != "Cannot get fiber return value: The fiber threw an uncaught exception" {
			t.Fatalf("unexpected GetReturn error: %v", err)
		}
	})
}

func TestFiberZoneResetCleanupSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		z := zone.New(zone.DefaultCapacity)
		var deferRan bool
		var suspendInDeferErr error

		f, err := fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			defer func() {
				deferRan = true
				_, suspendInDeferErr = fiber.Suspend(z, types.NewNull())
			}()
			return fiber.Suspend(z, types.NewLong(123))
		})
		if err != nil {
			t.Fatal(err)
		}

		v, err := f.Start(z)
		if err != nil || v.ToLong() != 123 {
			t.Fatalf("Start = (%v, %v), want 123", v, err)
		}
		if !f.IsSuspended() {
			t.Fatal("expected fiber to be suspended before Reset")
		}

		// Reset must abort the suspended fiber synchronously without leaking its goroutine.
		z.Reset(fiber.CleanupDispatcher(z))
		synctest.Wait()

		if !deferRan {
			t.Fatal("expected fiber defer to run during Zone.Reset")
		}
		if fe, ok := errors.AsType[fiber.FiberError](suspendInDeferErr); !ok || fe.Message != "Cannot suspend in a force-closed fiber" {
			t.Fatalf("expected force-closed FiberError in defer, got %v", suspendInDeferErr)
		}
		if !f.IsTerminated() {
			t.Fatal("expected fiber to be terminated after Zone.Reset")
		}
	})
}

func TestFiberDifferentialVsPHP(t *testing.T) {
	phpScript := `
$out = [];
$f = new Fiber(function(int $a, int $b): int {
    $out_inner = Fiber::getCurrent() !== null;
    $r1 = Fiber::suspend($a + $b);
    $r2 = Fiber::suspend($r1 * 2);
    return $r2 + 100;
});
$out["before_started"] = $f->isStarted();
$out["y1"] = $f->start(10, 20);
$out["mid_suspended"] = $f->isSuspended();
$out["y2"] = $f->resume(7);
$out["y3"] = $f->resume(50);
$out["after_terminated"] = $f->isTerminated();
$out["ret"] = $f->getReturn();
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

	synctest.Test(t, func(t *testing.T) {
		z := zone.New(zone.DefaultCapacity)
		defer z.Reset(fiber.CleanupDispatcher(z))

		f, err := fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
			if fiber.GetCurrent(z) == nil {
				return types.NewNull(), errors.New("missing current fiber")
			}
			r1, err := fiber.Suspend(z, types.Add(args[0], args[1]))
			if err != nil {
				return types.NewNull(), err
			}
			r2, err := fiber.Suspend(z, types.Mul(r1, types.NewLong(2)))
			if err != nil {
				return types.NewNull(), err
			}
			return types.Add(r2, types.NewLong(100)), nil
		})
		if err != nil {
			t.Fatal(err)
		}

		if f.IsStarted() != want["before_started"].(bool) {
			t.Fatalf("before_started mismatch")
		}
		y1, err := f.Start(z, types.NewLong(10), types.NewLong(20))
		if err != nil {
			t.Fatal(err)
		}
		if float64(y1.ToLong()) != want["y1"].(float64) {
			t.Fatalf("y1 = %d, want %v", y1.ToLong(), want["y1"])
		}
		if f.IsSuspended() != want["mid_suspended"].(bool) {
			t.Fatalf("mid_suspended mismatch")
		}
		y2, err := f.Resume(z, types.NewLong(7))
		if err != nil {
			t.Fatal(err)
		}
		if float64(y2.ToLong()) != want["y2"].(float64) {
			t.Fatalf("y2 = %d, want %v", y2.ToLong(), want["y2"])
		}
		y3, err := f.Resume(z, types.NewLong(50))
		if err != nil {
			t.Fatal(err)
		}
		if !y3.IsNull() || want["y3"] != nil {
			t.Fatalf("y3 = %v, want nil", y3)
		}
		if f.IsTerminated() != want["after_terminated"].(bool) {
			t.Fatalf("after_terminated mismatch")
		}
		ret, err := f.GetReturn(z)
		if err != nil {
			t.Fatal(err)
		}
		if float64(ret.ToLong()) != want["ret"].(float64) {
			t.Fatalf("ret = %d, want %v", ret.ToLong(), want["ret"])
		}
	})
}

func BenchmarkFiberSuspendResume(b *testing.B) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(fiber.CleanupDispatcher(z))

	f, err := fiber.New(z, func(z *zone.Zone, args []types.Value) (types.Value, error) {
		v := args[0]
		for {
			var err error
			v, err = fiber.Suspend(z, v)
			if err != nil {
				return types.NewNull(), err
			}
		}
	})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Start(z, types.NewLong(1)); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := f.Resume(z, types.NewLong(1)); err != nil {
			b.Fatal(err)
		}
	}
}
