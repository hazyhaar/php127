package types

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestProcessZoneFreeze(t *testing.T) {
	const marker = "PHP127: freeze panic checks reached\n"
	// A separate process preserves the irreversible freeze across -count runs.
	if os.Getenv("PHP127_PROCESS_ZONE_TEST") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestProcessZoneFreeze$")
		cmd.Env = append(os.Environ(), "PHP127_PROCESS_ZONE_TEST=1")
		if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), marker) {
			t.Fatalf("child: %v\n%s", err, output)
		}
		return
	}
	if IsProcessZoneFrozen() {
		t.Fatal("premature freeze")
	}
	texts := []string{"", "\x00", "x", "literal\x00with NUL", strings.Repeat("literal", 100)}
	values := make([]Value, len(texts))
	for i, text := range texts {
		v, err := InternProcessString(text)
		if err != nil {
			t.Fatal(err)
		}
		again, err := InternProcessString(text)
		if err != nil || again != v {
			t.Fatal("deduplication failed")
		}
		if v.Flags != FlagImmutable|FlagInterned {
			t.Fatal("process flags")
		}
		h := StringInfo(nil, v)
		if h.RefCount != math.MaxUint32 || h.Hash&(1<<63) == 0 {
			t.Fatal("process header")
		}
		values[i] = v
	}
	if values[0] != emptyString || values[1] != byteTable[0] || values[2] != byteTable['x'] {
		t.Fatal("prepopulation not deduplicated")
	}
	if _, err := InternProcessString(strings.Repeat("z", 256<<10)); !errors.Is(err, zone.ErrExhausted) {
		t.Fatalf("capacity: %v", err)
	}
	FreezeProcessZone()
	FreezeProcessZone()
	if !IsProcessZoneFrozen() {
		t.Fatal("freeze absent")
	}
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				z := zone.New(4096)
				for range 100 {
					for i, v := range values {
						h := StringInfo(z, v)
						if StringData(z, v) != texts[i] || StringHash(z, v) != h.Hash {
							t.Error("shared read changed")
						}
						copyValue := RetainString(z, v)
						if err := Append(z, &copyValue, byteTable['!']); err != nil {
							t.Error(err)
							return
						}
						if StringData(z, copyValue) != texts[i]+"!" || StringInfo(z, v) != h {
							t.Error("shared storage mutated")
						}
						ReleaseString(z, &copyValue)
					}
				}
			})
		}
		wg.Wait()
	})
	if _, err := os.Stdout.WriteString(marker); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"x", "new literal"} {
		func() {
			defer func() {
				if got := recover(); got != "types: process zone is frozen" {
					t.Errorf("intern after freeze panic = %v", got)
				}
			}()
			InternProcessString(text)
		}()
	}
}
