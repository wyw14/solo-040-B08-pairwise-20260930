package monitor

import (
	"sort"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

type DeviceHealth struct {
	ChamberID string           `json:"chamber_id"`
	Device    model.DeviceKind `json:"device"`
	Online    bool             `json:"online"`
	Message   string           `json:"message"`
	CheckedAt time.Time        `json:"checked_at"`
}

// HealthRegistry provides current operational health, not historical reports.
type HealthRegistry struct {
	mu      sync.RWMutex
	records map[string]DeviceHealth
}

func NewHealthRegistry() *HealthRegistry {
	return &HealthRegistry{records: make(map[string]DeviceHealth)}
}

func healthKey(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

func (r *HealthRegistry) Observe(chamberID string, device model.DeviceKind, online bool, message string, now time.Time) DeviceHealth {
	record := DeviceHealth{
		ChamberID: chamberID, Device: device, Online: online,
		Message: message, CheckedAt: now.UTC(),
	}
	r.mu.Lock()
	r.records[healthKey(chamberID, device)] = record
	r.mu.Unlock()
	return record
}

func (r *HealthRegistry) List() []DeviceHealth {
	r.mu.RLock()
	result := make([]DeviceHealth, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, record)
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].ChamberID == result[j].ChamberID {
			return result[i].Device < result[j].Device
		}
		return result[i].ChamberID < result[j].ChamberID
	})
	return result
}

func (r *HealthRegistry) AllOnline(chamberID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	found := false
	for _, record := range r.records {
		if record.ChamberID == chamberID {
			found = true
			if !record.Online {
				return false
			}
		}
	}
	return found
}
