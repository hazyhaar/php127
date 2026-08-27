// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/security: Télémétrie souveraine, corrélation temporelle c2blueteam et diffusion d'événements

package security

import (
	"sync"
	"time"

	"github.com/hazyhaar/c2pkg/c2blueteam"
)

type SecurityEventType uint8

const (
	EventSQLiBlocked              SecurityEventType = 1
	EventSSRFBlocked              SecurityEventType = 2
	EventCommandInjectionBlocked  SecurityEventType = 3
	EventPathTraversalBlocked     SecurityEventType = 4
	EventCodeInjectionBlocked     SecurityEventType = 5
	EventHighEntropyEgressBlocked SecurityEventType = 6
	EventForeignFallbackExecuted  SecurityEventType = 7
	EventTaintSinkViolation       SecurityEventType = 8
)

func (t SecurityEventType) String() string {
	switch t {
	case EventSQLiBlocked:
		return "SQL_INJECTION_VETO"
	case EventSSRFBlocked:
		return "SSRF_VETO"
	case EventCommandInjectionBlocked:
		return "COMMAND_INJECTION_VETO"
	case EventPathTraversalBlocked:
		return "PATH_TRAVERSAL_VETO"
	case EventCodeInjectionBlocked:
		return "CODE_INJECTION_VETO"
	case EventHighEntropyEgressBlocked:
		return "HIGH_ENTROPY_EGRESS_BLOCKED"
	case EventForeignFallbackExecuted:
		return "FOREIGN_FALLBACK_EXECUTED"
	case EventTaintSinkViolation:
		return "TAINT_SINK_VIOLATION"
	default:
		return "UNKNOWN_SECURITY_EVENT"
	}
}

type SecurityEvent struct {
	TimestampMicro int64             `json:"timestamp_us"`
	Type           SecurityEventType `json:"event_type"`
	TypeStr        string            `json:"type_str"`
	Source         string            `json:"source"`
	Details        string            `json:"details"`
	EntropyQ8      uint32            `json:"entropy_q8"`
	TaintFlags     uint32            `json:"taint_flags"`
}

type TelemetryHub struct {
	mu          sync.RWMutex
	chanProc    c2blueteam.Probe_channel_t
	tracker     c2blueteam.C2bt_tracker_table_t
	subscribers map[chan SecurityEvent]struct{}
	totalEvents uint64
	totalVetoes uint64
}

var (
	GlobalHub     *TelemetryHub
	globalHubOnce sync.Once
)

func GetTelemetryHub() *TelemetryHub {
	globalHubOnce.Do(func() {
		GlobalHub = NewTelemetryHub()
	})
	return GlobalHub
}

func NewTelemetryHub() *TelemetryHub {
	th := &TelemetryHub{
		subscribers: make(map[chan SecurityEvent]struct{}),
	}
	c2blueteam.C2bt_channel_init(&th.chanProc)
	c2blueteam.C2bt_tracker_init(&th.tracker)
	return th
}

// RecordEvent enregistre un incident dans le ring buffer lock-free et notifie les observateurs LiveView
func (th *TelemetryHub) RecordEvent(evType SecurityEventType, source, details string, entropyQ8 uint32, taintFlags uint32) {
	nowUs := time.Now().UnixMicro()
	ev := SecurityEvent{
		TimestampMicro: nowUs,
		Type:           evType,
		TypeStr:        evType.String(),
		Source:         source,
		Details:        details,
		EntropyQ8:      entropyQ8,
		TaintFlags:     taintFlags,
	}

	th.mu.Lock()
	th.totalEvents++
	if evType != EventForeignFallbackExecuted {
		th.totalVetoes++
	}

	// Enregistrement dans la structure c2blueteam
	rawEv := c2blueteam.Probe_event_t{
		Ts_ns:     uint64(nowUs * 1000),
		Pid:       uint32(evType),
		Subsystem: uint16(evType),
		Action:    uint16(entropyQ8),
		Flags:     taintFlags,
	}
	c2blueteam.C2bt_channel_write(&th.chanProc, &rawEv)
	flags := taintFlags
	c2blueteam.C2bt_correlate_event(&th.tracker, &rawEv, &flags, 60000000000)

	// Diffusion aux souscripteurs (live55)
	for ch := range th.subscribers {
		select {
		case ch <- ev:
		default:
			// Canal plein : skip sans bloquer la VM
		}
	}
	th.mu.Unlock()
}

func (th *TelemetryHub) Subscribe() chan SecurityEvent {
	th.mu.Lock()
	defer th.mu.Unlock()
	ch := make(chan SecurityEvent, 128)
	th.subscribers[ch] = struct{}{}
	return ch
}

func (th *TelemetryHub) Unsubscribe(ch chan SecurityEvent) {
	th.mu.Lock()
	defer th.mu.Unlock()
	delete(th.subscribers, ch)
	close(ch)
}

func (th *TelemetryHub) Stats() (uint64, uint64) {
	th.mu.RLock()
	defer th.mu.RUnlock()
	return th.totalEvents, th.totalVetoes
}
