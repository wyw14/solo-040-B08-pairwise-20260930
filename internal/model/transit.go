package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Direction identifies which river reach sends vessels into a chamber.
type Direction string

const (
	DirectionUpstream   Direction = "upstream"
	DirectionDownstream Direction = "downstream"
)

func (d Direction) Valid() bool {
	return d == DirectionUpstream || d == DirectionDownstream
}

func (d Direction) Opposite() Direction {
	if d == DirectionUpstream {
		return DirectionDownstream
	}
	return DirectionUpstream
}

// TransitStage is the externally visible phase of a lock transit.
type TransitStage string

const (
	StagePlanned   TransitStage = "planned"
	StageAdmitting TransitStage = "admitting"
	StageSealed    TransitStage = "sealed"
	StageLeveling  TransitStage = "leveling"
	StageReleasing TransitStage = "releasing"
	StageCompleted TransitStage = "completed"
	StageCancelled TransitStage = "cancelled"
	StageEmergency TransitStage = "emergency"
)

var stageOrder = map[TransitStage]int{
	StagePlanned: 0, StageAdmitting: 1, StageSealed: 2,
	StageLeveling: 3, StageReleasing: 4, StageCompleted: 5,
}

func (s TransitStage) Terminal() bool {
	return s == StageCompleted || s == StageCancelled || s == StageEmergency
}

func (s TransitStage) CanAdvance(next TransitStage) bool {
	if s.Terminal() {
		return false
	}
	if next == StageCancelled || next == StageEmergency {
		return true
	}
	current, currentOK := stageOrder[s]
	following, nextOK := stageOrder[next]
	return currentOK && nextOK && following == current+1
}

// TransitCycle owns a complete passage and its device-command generation.
type TransitCycle struct {
	ID         uuid.UUID    `json:"id"`
	ChamberID  string       `json:"chamber_id"`
	PlanID     uuid.UUID    `json:"plan_id"`
	Direction  Direction    `json:"direction"`
	Stage      TransitStage `json:"stage"`
	Generation uint64       `json:"generation"`
	VesselIDs  []uuid.UUID  `json:"vessel_ids"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	Reason     string       `json:"reason,omitempty"`
}

func NewTransitCycle(chamberID string, planID uuid.UUID, direction Direction, generation uint64, vesselIDs []uuid.UUID, now time.Time) (TransitCycle, error) {
	if chamberID == "" {
		return TransitCycle{}, errors.New("chamber id is required")
	}
	if planID == uuid.Nil {
		return TransitCycle{}, errors.New("plan id is required")
	}
	if !direction.Valid() {
		return TransitCycle{}, fmt.Errorf("invalid direction %q", direction)
	}
	if generation == 0 {
		return TransitCycle{}, errors.New("generation must be positive")
	}
	ids := make([]uuid.UUID, 0, len(vesselIDs))
	seen := make(map[uuid.UUID]struct{}, len(vesselIDs))
	for _, id := range vesselIDs {
		if id == uuid.Nil {
			return TransitCycle{}, errors.New("vessel id is required")
		}
		if _, duplicate := seen[id]; duplicate {
			return TransitCycle{}, errors.New("duplicate vessel in convoy")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return TransitCycle{}, errors.New("convoy requires at least one vessel")
	}
	return TransitCycle{
		ID: uuid.New(), ChamberID: chamberID, PlanID: planID,
		Direction: direction, Stage: StagePlanned, Generation: generation,
		VesselIDs: ids, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}, nil
}

func (c TransitCycle) Clone() TransitCycle {
	c.VesselIDs = append([]uuid.UUID(nil), c.VesselIDs...)
	return c
}

// HasReleaseSignals reports whether every convoy member currently holds a
// proceed signal published during the releasing stage of this exact transit
// generation. It closes the release-signal -> member-confirmation ->
// completion loop: an empty convoy, a missing member, an admission-stage
// signal or a signal left on an older generation all fail.
func (c TransitCycle) HasReleaseSignals(states []SignalState) bool {
	if len(c.VesselIDs) == 0 {
		return false
	}
	confirmed := make(map[uuid.UUID]struct{}, len(states))
	for _, state := range states {
		if state.CycleID == c.ID && state.Generation == c.Generation &&
			state.Stage == StageReleasing && state.Aspect == SignalProceed {
			confirmed[state.VesselID] = struct{}{}
		}
	}
	if len(confirmed) != len(c.VesselIDs) {
		return false
	}
	for _, vesselID := range c.VesselIDs {
		if _, ok := confirmed[vesselID]; !ok {
			return false
		}
	}
	return true
}

func (c *TransitCycle) Advance(next TransitStage, reason string, now time.Time) error {
	if !c.Stage.CanAdvance(next) {
		return fmt.Errorf("cannot advance transit %s from %s to %s", c.ID, c.Stage, next)
	}
	c.Stage = next
	c.Reason = reason
	c.UpdatedAt = now.UTC()
	return nil
}

func (c TransitCycle) IdentityMatches(id uuid.UUID, generation uint64) bool {
	return c.ID == id && c.Generation == generation
}
