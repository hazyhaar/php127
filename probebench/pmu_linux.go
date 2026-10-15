//go:build linux && amd64

package probebench

/*
#define _GNU_SOURCE
#include <stdint.h>
#include <sched.h>
#include <x86intrin.h>

static inline uint64_t rdtscp_serialized(uint32_t *aux) {
    unsigned int a = 0;
    _mm_lfence();
    uint64_t tsc = __rdtscp(&a);
    _mm_lfence();
    *aux = (uint32_t)a;
    return tsc;
}

static inline int pin_to_core_c(int core_id) {
    cpu_set_t set;
    CPU_ZERO(&set);
    CPU_SET(core_id, &set);
    return sched_setaffinity(0, sizeof(cpu_set_t), &set);
}
*/
import "C"

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	// Constantes noyau Linux perf_event_open(2)
	perfTypeSoftware           = 1
	perfCountSWPageFaultsMin   = 2
	perfCountSWContextSwitches = 3
	perfCountSWCPUMigrations   = 4

	perfEventIOCEnable  = 0x2400
	perfEventIOCDisable = 0x2401
	perfEventIOCReset   = 0x2403
	perfFlagFDCloexec   = 0x08

	// Bits de struct perf_event_attr (offset 40, uint64 little-endian)
	perfBitDisabled      = 1 << 0
	perfBitInherit       = 1 << 1
	perfBitExcludeUser   = 1 << 4
	perfBitExcludeKernel = 1 << 5
	perfBitExcludeHv     = 1 << 6

	// Type PMU matériel "cpu_core" (P-cores Golden Cove 0..15 sur Intel Core i9-14900K).
	pmuTypeIntelCore = 4

	// Événements raw PMU Intel Golden Cove (/sys/bus/event_source/devices/cpu_core/events/*)
	rawEventCycles      = 0x003c // event=0x3c (CPU_CLK_UNHALTED.THREAD)
	rawEventRefCycles   = 0x013c // event=0x3c, umask=0x01
	rawEventInstr       = 0x00c0 // event=0xc0 (INST_RETIRED.ANY)
	rawEventCacheRefs   = 0x4f2e // event=0x2e, umask=0x4f (LONGEST_LAT_CACHE.REFERENCE)
	rawEventCacheMisses = 0x412e // event=0x2e, umask=0x41 (LONGEST_LAT_CACHE.MISS)
	rawEventBranches    = 0x00c4 // event=0xc4 (BR_INST_RETIRED.ALL_BRANCHES)
	rawEventBranchMiss  = 0x00c5 // event=0xc5 (BR_MISP_RETIRED.ALL_BRANCHES)
	rawEventL1DLoads    = 0x81d0 // MEM_INST_RETIRED.ALL_LOADS (event=0xd0, umask=0x81)
	rawEventL1DStores   = 0x82d0 // MEM_INST_RETIRED.ALL_STORES (event=0xd0, umask=0x82)

	raplSysfsEnergyPath = "/sys/class/powercap/intel-rapl:0/energy_uj"
	raplMaxCounterUJ    = 262143999999 // Débordement du compteur RAPL package
)

// ProbeMetrics regroupe l'ensemble des sondes matérielles PMU, sondes noyau et métriques mémoire.
// Aucune grandeur énergétique synthétique (ni modèle linéaire CMOS ni électrons) n'y figure.
// La sonde RAPL package est dormante : elle n'est renseignée (RAPLAvailable = true) que lorsque
// le compteur matériel est lisible et que la charge système permet d'isoler la puissance de repos.
type ProbeMetrics struct {
	Label        string  `json:"label"`
	Iterations   int64   `json:"iterations"`
	WallNs       float64 `json:"wall_ns"`
	TSCCycles    uint64  `json:"tsc_cycles"`
	CoreStart    uint32  `json:"core_start"`
	CoreEnd      uint32  `json:"core_end"`
	CoreMigrated bool    `json:"core_migrated"`

	// Compteurs matériels PMU P-core (Golden Cove)
	CyclesUser      uint64 `json:"cycles_user"`
	CyclesUserIQR   uint64 `json:"cycles_user_iqr"`
	CyclesKernel    uint64 `json:"cycles_kernel"`
	Instructions    uint64 `json:"instructions"`
	InstructionsIQR uint64 `json:"instructions_iqr"`
	Branches        uint64 `json:"branches"`
	BranchMisses    uint64 `json:"branch_misses"`
	L1DLoads        uint64 `json:"l1d_loads"`
	L1DStores       uint64 `json:"l1d_stores"`
	CacheRefs       uint64 `json:"cache_refs"`
	CacheMisses     uint64 `json:"cache_misses"`

	// Colonne RAPL package dormante (microjoules nets après soustraction du repos ; absente si RAPLAvailable == false)
	RAPLAvailable bool    `json:"rapl_available,omitempty"`
	RAPLNetUJ     float64 `json:"rapl_net_uj,omitempty"`
	RAPLNetIQRUJ  float64 `json:"rapl_net_iqr_uj,omitempty"`

	// Deltas signés (pour DeltaPerOp sans plafonnement à zéro ni fréquence codée en dur)
	SignedWallNs       float64 `json:"signed_wall_ns,omitempty"`
	SignedCyclesUser   int64   `json:"signed_cycles_user,omitempty"`
	SignedCyclesKernel int64   `json:"signed_cycles_kernel,omitempty"`
	SignedInstructions int64   `json:"signed_instructions,omitempty"`
	SignedL1D          int64   `json:"signed_l1d,omitempty"`
	SignedBranches     int64   `json:"signed_branches,omitempty"`
	ValidDelta         bool    `json:"valid_delta,omitempty"`

	// Sondes logicielles du noyau Linux (perf_event_open PERF_TYPE_SOFTWARE + rusage)
	PageFaultsMin   uint64 `json:"page_faults_min"`
	PageFaultsMaj   uint64 `json:"page_faults_maj"`
	ContextSwitches uint64 `json:"context_switches"`
	CPUMigrations   uint64 `json:"cpu_migrations"`
	MaxRSSKiB       int64  `json:"max_rss_kib"`

	// Mémoire Go vs Arène Zone (pour les mesures In-Process)
	GoHeapBytes  uint64 `json:"go_heap_bytes"`
	GoHeapAllocs uint64 `json:"go_heap_allocs"`
	ZoneBytes    uint64 `json:"zone_bytes"`

	// Ratios microarchitecturaux dérivés des compteurs matériels
	IPC            float64 `json:"ipc"`
	BranchMissPct  float64 `json:"branch_miss_pct"`
	CacheMissPct   float64 `json:"cache_miss_pct"`
	DRAMTrafficKiB float64 `json:"dram_traffic_kib"`
	KernelCyclePct float64 `json:"kernel_cycle_pct"`
	Multiplexed    bool    `json:"multiplexed"`
}

// readRAPLPackageUJ lit le compteur matériel RAPL package (/sys/class/powercap/intel-rapl:0/energy_uj)
// uniquement si les permissions noyau l'autorisent et que la machine est au repos (loadavg 1 min < 1.0).
func readRAPLPackageUJ() (uint64, bool) {
	loadBytes, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(loadBytes))
	if len(fields) == 0 {
		return 0, false
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || load1 >= 1.0 {
		// Machine non quiète : le compteur RAPL package intégrerait le bruit de fond des autres cœurs.
		return 0, false
	}
	raw, err := os.ReadFile(raplSysfsEnergyPath)
	if err != nil {
		return 0, false
	}
	val, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func raplDeltaUJ(before, after uint64) uint64 {
	if after >= before {
		return after - before
	}
	return (raplMaxCounterUJ - before) + after
}

// PerOp divise toutes les grandeurs extensives par nOp pour obtenir le coût unitaire par opération/mutation.
func (m ProbeMetrics) PerOp(nOp int64) ProbeMetrics {
	if nOp <= 1 {
		return m
	}
	f := float64(nOp)
	u := uint64(nOp)
	out := m
	out.Iterations = 1
	out.WallNs = m.WallNs / f
	out.TSCCycles = m.TSCCycles / u
	out.CyclesUser = m.CyclesUser / u
	out.CyclesUserIQR = m.CyclesUserIQR / u
	out.CyclesKernel = m.CyclesKernel / u
	out.Instructions = m.Instructions / u
	out.InstructionsIQR = m.InstructionsIQR / u
	out.Branches = m.Branches / u
	out.BranchMisses = m.BranchMisses / u
	out.L1DLoads = m.L1DLoads / u
	out.L1DStores = m.L1DStores / u
	out.CacheRefs = m.CacheRefs / u
	out.CacheMisses = m.CacheMisses / u
	out.GoHeapBytes = m.GoHeapBytes / u
	out.GoHeapAllocs = m.GoHeapAllocs / u
	out.ZoneBytes = m.ZoneBytes / u
	out.DRAMTrafficKiB = m.DRAMTrafficKiB / f
	if m.RAPLAvailable {
		out.RAPLNetUJ = m.RAPLNetUJ / f
		out.RAPLNetIQRUJ = m.RAPLNetIQRUJ / f
	}
	return out
}

// DeltaPerOp calcule le coût marginal signé d'une mutation : (High - Low) / (nHigh - nLow),
// sans plafonnement à zéro ni repli sur une fréquence codée en dur.
// Un delta dont la marge (High - Low) est inférieure ou égale à la somme des écarts interquartiles
// ou négative sur CyclesUser est marqué ValidDelta = false.
func DeltaPerOp(label string, low, high ProbeMetrics, deltaOps int64) ProbeMetrics {
	if deltaOps <= 0 {
		deltaOps = 1
	}
	f := float64(deltaOps)
	sWall := (high.WallNs - low.WallNs) / f
	sCycU := (int64(high.CyclesUser) - int64(low.CyclesUser)) / deltaOps
	sCycK := (int64(high.CyclesKernel) - int64(low.CyclesKernel)) / deltaOps
	sInsn := (int64(high.Instructions) - int64(low.Instructions)) / deltaOps
	sBr := (int64(high.Branches) - int64(low.Branches)) / deltaOps
	sBrM := (int64(high.BranchMisses) - int64(low.BranchMisses)) / deltaOps
	sL1L := (int64(high.L1DLoads) - int64(low.L1DLoads)) / deltaOps
	sL1S := (int64(high.L1DStores) - int64(low.L1DStores)) / deltaOps
	sCRef := (int64(high.CacheRefs) - int64(low.CacheRefs)) / deltaOps
	sCMiss := (int64(high.CacheMisses) - int64(low.CacheMisses)) / deltaOps

	rawDiffCyc := int64(high.CyclesUser) - int64(low.CyclesUser)
	combinedIQR := int64(high.CyclesUserIQR + low.CyclesUserIQR)
	valid := rawDiffCyc > 0 && sInsn > 0 && (combinedIQR == 0 || rawDiffCyc > combinedIQR)

	d := ProbeMetrics{
		Label:              label,
		Iterations:         deltaOps,
		WallNs:             sWall,
		CyclesUserIQR:      uint64(combinedIQR) / uint64(deltaOps),
		SignedWallNs:       sWall,
		SignedCyclesUser:   sCycU,
		SignedCyclesKernel: sCycK,
		SignedInstructions: sInsn,
		SignedL1D:          sL1L + sL1S,
		SignedBranches:     sBr,
		ValidDelta:         valid,
		MaxRSSKiB:          high.MaxRSSKiB,
	}
	if sCycU > 0 {
		d.CyclesUser = uint64(sCycU)
	}
	if sCycK > 0 {
		d.CyclesKernel = uint64(sCycK)
	}
	if sInsn > 0 {
		d.Instructions = uint64(sInsn)
	}
	if sBr > 0 {
		d.Branches = uint64(sBr)
	}
	if sBrM > 0 {
		d.BranchMisses = uint64(sBrM)
	}
	if sL1L > 0 {
		d.L1DLoads = uint64(sL1L)
	}
	if sL1S > 0 {
		d.L1DStores = uint64(sL1S)
	}
	if sCRef > 0 {
		d.CacheRefs = uint64(sCRef)
	}
	if sCMiss > 0 {
		d.CacheMisses = uint64(sCMiss)
	}
	d.computeDerived()
	return d
}

func (m *ProbeMetrics) computeDerived() {
	if m.CyclesUser > 0 {
		m.IPC = float64(m.Instructions) / float64(m.CyclesUser)
	}
	if m.Branches > 0 {
		m.BranchMissPct = (float64(m.BranchMisses) * 100.0) / float64(m.Branches)
	}
	if m.CacheRefs > 0 {
		m.CacheMissPct = (float64(m.CacheMisses) * 100.0) / float64(m.CacheRefs)
	}
	if totalCyc := m.CyclesUser + m.CyclesKernel; totalCyc > 0 {
		m.KernelCyclePct = (float64(m.CyclesKernel) * 100.0) / float64(totalCyc)
	}
	m.DRAMTrafficKiB = float64(m.CacheMisses*64) / 1024.0
}

func readRDTSCP() (uint64, uint32) {
	var aux C.uint32_t
	tsc := C.rdtscp_serialized(&aux)
	return uint64(tsc), uint32(aux & 0xfff)
}

// PinToPCore épingle le thread OS courant sur le P-core Golden Cove spécifié (ex. CPU 0).
func PinToPCore(coreID int) error {
	if rc := C.pin_to_core_c(C.int(coreID)); rc != 0 {
		return fmt.Errorf("sched_setaffinity(%d): rc=%d", coreID, int(rc))
	}
	return nil
}

type counterSpec struct {
	typ           uint32
	config        uint64
	excludeUser   bool
	excludeKernel bool
	inherit       bool
}

type pmuBank struct {
	fds []int
}

const (
	perfFormatTotalTimeEnabled = 1 << 0
	perfFormatTotalTimeRunning = 1 << 1
)

func openPerfCounter(spec counterSpec) (int, error) {
	var attr [136]byte
	binary.LittleEndian.PutUint32(attr[0:4], spec.typ)
	binary.LittleEndian.PutUint32(attr[4:8], 136)
	binary.LittleEndian.PutUint64(attr[8:16], spec.config)
	binary.LittleEndian.PutUint64(attr[32:40], uint64(perfFormatTotalTimeEnabled|perfFormatTotalTimeRunning))

	bits := uint64(perfBitDisabled | perfBitExcludeHv)
	if spec.excludeUser {
		bits |= perfBitExcludeUser
	}
	if spec.excludeKernel {
		bits |= perfBitExcludeKernel
	}
	if spec.inherit {
		bits |= perfBitInherit
	}
	binary.LittleEndian.PutUint64(attr[40:48], bits)

	r1, _, errno := syscall.Syscall6(
		syscall.SYS_PERF_EVENT_OPEN,
		uintptr(unsafe.Pointer(&attr[0])),
		0,
		^uintptr(0), // cpu = -1 (tout cœur où tourne le thread/enfant épinglé)
		^uintptr(0), // group_fd = -1
		perfFlagFDCloexec,
		0,
	)
	if errno != 0 {
		return -1, fmt.Errorf("perf_event_open(type=%d, config=0x%x): %w", spec.typ, spec.config, errno)
	}
	return int(r1), nil
}

func readPerfCounter(fd int) (uint64, bool, error) {
	var buf [24]byte
	n, err := syscall.Read(fd, buf[:])
	if err != nil || n != 24 {
		return 0, false, fmt.Errorf("read perf fd %d: n=%d err=%w", fd, n, err)
	}
	val := binary.LittleEndian.Uint64(buf[0:8])
	timeEnabled := binary.LittleEndian.Uint64(buf[8:16])
	timeRunning := binary.LittleEndian.Uint64(buf[16:24])
	multiplexed := false
	if timeEnabled != timeRunning && timeRunning > 0 {
		multiplexed = true
		val = uint64((float64(val) * float64(timeEnabled)) / float64(timeRunning))
	}
	return val, multiplexed, nil
}

func openPMUBank(specs []counterSpec) (pmuBank, error) {
	b := pmuBank{fds: make([]int, len(specs))}
	for i := range b.fds {
		b.fds[i] = -1
	}
	for i, s := range specs {
		fd, err := openPerfCounter(s)
		if err != nil {
			b.close()
			return pmuBank{}, err
		}
		b.fds[i] = fd
	}
	return b, nil
}

func (b *pmuBank) resetAndEnable() error {
	for _, fd := range b.fds {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), perfEventIOCReset, 0); errno != 0 {
			return fmt.Errorf("ioctl(PERF_EVENT_IOC_RESET): %w", errno)
		}
	}
	for _, fd := range b.fds {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), perfEventIOCEnable, 0); errno != 0 {
			return fmt.Errorf("ioctl(PERF_EVENT_IOC_ENABLE): %w", errno)
		}
	}
	return nil
}

func (b *pmuBank) disableAndRead() ([]uint64, bool, error) {
	for _, fd := range b.fds {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), perfEventIOCDisable, 0); errno != 0 {
			return nil, false, fmt.Errorf("ioctl(PERF_EVENT_IOC_DISABLE): %w", errno)
		}
	}
	out := make([]uint64, len(b.fds))
	anyMux := false
	for i, fd := range b.fds {
		val, mux, err := readPerfCounter(fd)
		if err != nil {
			return nil, false, err
		}
		out[i] = val
		if mux {
			anyMux = true
		}
	}
	return out, anyMux, nil
}

func (b *pmuBank) close() {
	for i, fd := range b.fds {
		if fd >= 0 {
			_ = syscall.Close(fd)
			b.fds[i] = -1
		}
	}
}

// MeasureInProcess exécute fn() sur le P-core 0 avec verrouillage de thread OS,
// en deux passes matérielles sans multiplexage PMU.
// Toute erreur d'ouverture de compteur PMU, d'affinité CPU ou de migration de cœur est retournée explicitement.
func MeasureInProcess(label string, iterations int64, warmups int, zoneUsedFn func() uint64, fn func()) (ProbeMetrics, error) {
	return MeasureInProcessRuns(label, iterations, warmups, 1, zoneUsedFn, fn)
}

// MeasureInProcessRuns exécute `runs` répétitions en processus et retourne la médiane ainsi que l'écart interquartile (IQR).
func MeasureInProcessRuns(label string, iterations int64, warmups int, runs int, zoneUsedFn func() uint64, fn func()) (ProbeMetrics, error) {
	if runs <= 0 {
		runs = 1
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := PinToPCore(0); err != nil {
		return ProbeMetrics{}, err
	}

	for range warmups {
		fn()
	}

	bank1, err := openPMUBank([]counterSpec{
		{pmuTypeIntelCore, rawEventCycles, false, true, false},
		{pmuTypeIntelCore, rawEventCycles, true, false, false},
		{pmuTypeIntelCore, rawEventInstr, false, true, false},
		{pmuTypeIntelCore, rawEventBranches, false, true, false},
		{pmuTypeIntelCore, rawEventBranchMiss, false, true, false},
		{perfTypeSoftware, perfCountSWPageFaultsMin, false, false, false},
		{perfTypeSoftware, perfCountSWContextSwitches, false, false, false},
		{perfTypeSoftware, perfCountSWCPUMigrations, false, false, false},
	})
	if err != nil {
		return ProbeMetrics{}, err
	}
	defer bank1.close()

	bank2, err := openPMUBank([]counterSpec{
		{pmuTypeIntelCore, rawEventL1DLoads, false, true, false},
		{pmuTypeIntelCore, rawEventL1DStores, false, true, false},
		{pmuTypeIntelCore, rawEventCacheRefs, false, true, false},
		{pmuTypeIntelCore, rawEventCacheMisses, false, true, false},
	})
	if err != nil {
		return ProbeMetrics{}, err
	}
	defer bank2.close()

	samples := make([]ProbeMetrics, 0, runs)
	for range runs {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)

		tsc0, core0 := readRDTSCP()
		t0 := time.Now()
		if err := bank1.resetAndEnable(); err != nil {
			return ProbeMetrics{}, err
		}
		fn()
		vals1, mux1, err := bank1.disableAndRead()
		if err != nil {
			return ProbeMetrics{}, err
		}
		wall := time.Since(t0)
		tsc1, core1 := readRDTSCP()

		runtime.ReadMemStats(&m1)
		var zBytes uint64
		if zoneUsedFn != nil {
			zBytes = zoneUsedFn()
		}

		if err := bank2.resetAndEnable(); err != nil {
			return ProbeMetrics{}, err
		}
		fn()
		vals2, mux2, err := bank2.disableAndRead()
		if err != nil {
			return ProbeMetrics{}, err
		}

		var ru syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
			return ProbeMetrics{}, err
		}

		var tscDelta uint64
		if tsc1 > tsc0 {
			tscDelta = tsc1 - tsc0
		}

		migrated := core0 != core1 || vals1[7] > 0
		pm := ProbeMetrics{
			Label:           label,
			Iterations:      iterations,
			WallNs:          float64(wall.Nanoseconds()),
			TSCCycles:       tscDelta,
			CoreStart:       core0,
			CoreEnd:         core1,
			CoreMigrated:    migrated,
			CyclesUser:      vals1[0],
			CyclesKernel:    vals1[1],
			Instructions:    vals1[2],
			Branches:        vals1[3],
			BranchMisses:    vals1[4],
			PageFaultsMin:   vals1[5],
			ContextSwitches: vals1[6],
			CPUMigrations:   vals1[7],
			L1DLoads:        vals2[0],
			L1DStores:       vals2[1],
			CacheRefs:       vals2[2],
			CacheMisses:     vals2[3],
			MaxRSSKiB:       ru.Maxrss,
			GoHeapBytes:     m1.TotalAlloc - m0.TotalAlloc,
			GoHeapAllocs:    m1.Mallocs - m0.Mallocs,
			ZoneBytes:       zBytes,
			Multiplexed:     mux1 || mux2,
		}
		pm.computeDerived()
		if migrated {
			return pm, fmt.Errorf("core migration detected on %s (core %d -> %d, sw_migrations=%d)", label, core0, core1, vals1[7])
		}
		samples = append(samples, pm)
	}

	return aggregateMedian(samples), nil
}

func aggregateMedian(samples []ProbeMetrics) ProbeMetrics {
	runs := len(samples)
	if runs == 0 {
		return ProbeMetrics{}
	}
	slices.SortFunc(samples, func(a, b ProbeMetrics) int {
		return cmp.Compare(a.CyclesUser, b.CyclesUser)
	})
	med := samples[runs/2]
	medUint64 := func(fn func(ProbeMetrics) uint64) (uint64, uint64) {
		vals := make([]uint64, runs)
		for i, s := range samples {
			vals[i] = fn(s)
		}
		slices.Sort(vals)
		var iqr uint64
		if runs >= 4 && vals[(3*runs)/4] >= vals[runs/4] {
			iqr = vals[(3*runs)/4] - vals[runs/4]
		}
		return vals[runs/2], iqr
	}
	medFloat64 := func(fn func(ProbeMetrics) float64) float64 {
		vals := make([]float64, runs)
		for i, s := range samples {
			vals[i] = fn(s)
		}
		slices.Sort(vals)
		return vals[runs/2]
	}
	med.WallNs = medFloat64(func(m ProbeMetrics) float64 { return m.WallNs })
	med.TSCCycles, _ = medUint64(func(m ProbeMetrics) uint64 { return m.TSCCycles })
	med.CyclesUser, med.CyclesUserIQR = medUint64(func(m ProbeMetrics) uint64 { return m.CyclesUser })
	med.CyclesKernel, _ = medUint64(func(m ProbeMetrics) uint64 { return m.CyclesKernel })
	med.Instructions, med.InstructionsIQR = medUint64(func(m ProbeMetrics) uint64 { return m.Instructions })
	med.Branches, _ = medUint64(func(m ProbeMetrics) uint64 { return m.Branches })
	med.BranchMisses, _ = medUint64(func(m ProbeMetrics) uint64 { return m.BranchMisses })
	med.PageFaultsMin, _ = medUint64(func(m ProbeMetrics) uint64 { return m.PageFaultsMin })
	med.ContextSwitches, _ = medUint64(func(m ProbeMetrics) uint64 { return m.ContextSwitches })
	med.CPUMigrations, _ = medUint64(func(m ProbeMetrics) uint64 { return m.CPUMigrations })
	med.L1DLoads, _ = medUint64(func(m ProbeMetrics) uint64 { return m.L1DLoads })
	med.L1DStores, _ = medUint64(func(m ProbeMetrics) uint64 { return m.L1DStores })
	med.CacheRefs, _ = medUint64(func(m ProbeMetrics) uint64 { return m.CacheRefs })
	med.CacheMisses, _ = medUint64(func(m ProbeMetrics) uint64 { return m.CacheMisses })
	for _, s := range samples {
		if s.Multiplexed {
			med.Multiplexed = true
			break
		}
	}
	med.computeDerived()
	return med
}

// MeasureSubprocess exécute un binaire externe (php, phpr, php127) épinglé sur P-core 0
// avec héritage noyau des compteurs PMU (PerfBitInherit = 1) et capture de Rusage de l'enfant.
// Prend la médiane et calcule l'écart interquartile (IQR = Q3 - Q1) sur `runs` passages.
func MeasureSubprocess(label string, bin string, args []string, iterations int64, runs int) (ProbeMetrics, string, error) {
	if runs <= 0 {
		runs = 1
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := PinToPCore(0); err != nil {
		return ProbeMetrics{}, "", err
	}

	warmCmd := exec.Command(bin, args...)
	outBytes, err := warmCmd.CombinedOutput()
	if err != nil {
		return ProbeMetrics{}, "", fmt.Errorf("%s %v: %w (%s)", bin, args, err, string(outBytes))
	}
	stdoutStr := string(outBytes)

	bank1, err := openPMUBank([]counterSpec{
		{pmuTypeIntelCore, rawEventCycles, false, true, true},
		{pmuTypeIntelCore, rawEventCycles, true, false, true},
		{pmuTypeIntelCore, rawEventInstr, false, true, true},
		{pmuTypeIntelCore, rawEventBranches, false, true, true},
		{pmuTypeIntelCore, rawEventBranchMiss, false, true, true},
		{perfTypeSoftware, perfCountSWPageFaultsMin, false, false, true},
		{perfTypeSoftware, perfCountSWContextSwitches, false, false, true},
		{perfTypeSoftware, perfCountSWCPUMigrations, false, false, true},
	})
	if err != nil {
		return ProbeMetrics{}, "", err
	}
	defer bank1.close()

	bank2, err := openPMUBank([]counterSpec{
		{pmuTypeIntelCore, rawEventL1DLoads, false, true, true},
		{pmuTypeIntelCore, rawEventL1DStores, false, true, true},
		{pmuTypeIntelCore, rawEventCacheRefs, false, true, true},
		{pmuTypeIntelCore, rawEventCacheMisses, false, true, true},
	})
	if err != nil {
		return ProbeMetrics{}, "", err
	}
	defer bank2.close()

	// Si RAPL est accessible et la machine au repos, mesure la puissance de repos sur 50 ms avant la série
	var idlePowerUPerNs float64
	_, raplOK := readRAPLPackageUJ()
	if raplOK {
		e0, ok0 := readRAPLPackageUJ()
		tIdle0 := time.Now()
		time.Sleep(50 * time.Millisecond)
		e1, ok1 := readRAPLPackageUJ()
		idleDur := time.Since(tIdle0).Nanoseconds()
		if ok0 && ok1 && idleDur > 0 {
			idlePowerUPerNs = float64(raplDeltaUJ(e0, e1)) / float64(idleDur)
		} else {
			raplOK = false
		}
	}

	samples := make([]ProbeMetrics, 0, runs)
	for range runs {
		if err := PinToPCore(0); err != nil {
			return ProbeMetrics{}, "", err
		}
		cmd1 := exec.Command(bin, args...)
		var rapl0 uint64
		if raplOK {
			rapl0, raplOK = readRAPLPackageUJ()
		}
		tsc0, core0 := readRDTSCP()
		t0 := time.Now()
		if err := bank1.resetAndEnable(); err != nil {
			return ProbeMetrics{}, "", err
		}
		err := cmd1.Run()
		vals1, mux1, rerr := bank1.disableAndRead()
		wall := time.Since(t0)
		tsc1, core1 := readRDTSCP()
		var raplNetUJ float64
		if raplOK {
			if rapl1, ok := readRAPLPackageUJ(); ok {
				rawUJ := float64(raplDeltaUJ(rapl0, rapl1))
				idleUJ := idlePowerUPerNs * float64(wall.Nanoseconds())
				if rawUJ > idleUJ {
					raplNetUJ = rawUJ - idleUJ
				}
			} else {
				raplOK = false
			}
		}
		if err != nil {
			return ProbeMetrics{}, "", err
		}
		if rerr != nil {
			return ProbeMetrics{}, "", rerr
		}
		if core0 != core1 {
			return ProbeMetrics{}, "", fmt.Errorf("parent core migration detected on %s (%d -> %d)", label, core0, core1)
		}

		var maxRSS int64
		var minFlt uint64
		if cmd1.ProcessState != nil {
			if ru, ok := cmd1.ProcessState.SysUsage().(*syscall.Rusage); ok && ru != nil {
				maxRSS = ru.Maxrss
				if ru.Minflt > 0 {
					minFlt = uint64(ru.Minflt)
				}
			}
		}

		cmd2 := exec.Command(bin, args...)
		if err := bank2.resetAndEnable(); err != nil {
			return ProbeMetrics{}, "", err
		}
		if err := cmd2.Run(); err != nil {
			return ProbeMetrics{}, "", err
		}
		vals2, mux2, err := bank2.disableAndRead()
		if err != nil {
			return ProbeMetrics{}, "", err
		}

		if minFlt == 0 {
			minFlt = vals1[5]
		}
		var tscDelta uint64
		if tsc1 > tsc0 {
			tscDelta = tsc1 - tsc0
		}
		pm := ProbeMetrics{
			Label:           label,
			Iterations:      iterations,
			WallNs:          float64(wall.Nanoseconds()),
			TSCCycles:       tscDelta,
			CoreStart:       core0,
			CoreEnd:         core1,
			CoreMigrated:    core0 != core1,
			CyclesUser:      vals1[0],
			CyclesKernel:    vals1[1],
			Instructions:    vals1[2],
			Branches:        vals1[3],
			BranchMisses:    vals1[4],
			PageFaultsMin:   minFlt,
			ContextSwitches: vals1[6],
			CPUMigrations:   vals1[7],
			L1DLoads:        vals2[0],
			L1DStores:       vals2[1],
			CacheRefs:       vals2[2],
			CacheMisses:     vals2[3],
			MaxRSSKiB:       maxRSS,
			RAPLAvailable:   raplOK,
			RAPLNetUJ:       raplNetUJ,
			Multiplexed:     mux1 || mux2,
		}
		pm.computeDerived()
		samples = append(samples, pm)
	}

	return aggregateMedian(samples), stdoutStr, nil
}
