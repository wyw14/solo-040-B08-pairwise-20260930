package model

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func testCycle(t *testing.T, stage TransitStage, generation uint64, vesselIDs []uuid.UUID) TransitCycle {
	t.Helper()
	cycleValue, err := NewTransitCycle("chamber-1", uuid.New(), DirectionUpstream, generation, vesselIDs, time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new transit cycle: %v", err)
	}
	for _, next := range []TransitStage{StageAdmitting, StageSealed, StageLeveling, StageReleasing, StageCompleted} {
		if err := cycleValue.Advance(next, "test", cycleValue.UpdatedAt.Add(time.Second)); err != nil {
			t.Fatalf("advance to %s: %v", next, err)
		}
		if next == stage {
			break
		}
	}
	return cycleValue
}

func proceedStates(cycleValue TransitCycle, ids []uuid.UUID, generation uint64) []SignalState {
	states := make([]SignalState, 0, len(ids))
	for _, id := range ids {
		states = append(states, SignalState{
			VesselID: id, CycleID: cycleValue.ID, Generation: generation,
			Stage: StageReleasing, Aspect: SignalProceed,
		})
	}
	return states
}

func TestHasReleaseSignals(t *testing.T) {
	vesselA, vesselB := uuid.New(), uuid.New()
	members := []uuid.UUID{vesselA, vesselB}
	cycleValue := testCycle(t, StageReleasing, 3, members)

	t.Run("all members current generation proceed", func(t *testing.T) {
		if !cycleValue.HasReleaseSignals(proceedStates(cycleValue, members, 3)) {
			t.Fatal("expected complete current-generation signals to satisfy the convoy")
		}
	})

	t.Run("empty release never satisfies", func(t *testing.T) {
		if cycleValue.HasReleaseSignals(nil) {
			t.Fatal("expected empty signal set to be rejected")
		}
	})

	t.Run("missing member is rejected", func(t *testing.T) {
		if cycleValue.HasReleaseSignals(proceedStates(cycleValue, members[:1], 3)) {
			t.Fatal("expected handoff with a missing convoy member to be rejected")
		}
	})

	t.Run("stale generation signal is rejected", func(t *testing.T) {
		if cycleValue.HasReleaseSignals(proceedStates(cycleValue, members, 2)) {
			t.Fatal("expected previous-generation signals to be rejected")
		}
	})

	t.Run("foreign cycle signal is rejected", func(t *testing.T) {
		states := proceedStates(cycleValue, members, 3)
		states[0].CycleID = uuid.New()
		if cycleValue.HasReleaseSignals(states) {
			t.Fatal("expected another cycle's signals to be rejected")
		}
	})

	t.Run("admission stage proceed is rejected", func(t *testing.T) {
		states := proceedStates(cycleValue, members, 3)
		states[0].Stage = StagePlanned
		if cycleValue.HasReleaseSignals(states) {
			t.Fatal("expected an admission-era proceed not to count as a release confirmation")
		}
	})

	t.Run("non proceed aspect is rejected", func(t *testing.T) {
		states := proceedStates(cycleValue, members, 3)
		states[1].Aspect = SignalHold
		if cycleValue.HasReleaseSignals(states) {
			t.Fatal("expected a held member signal to block completion")
		}
	})

	t.Run("convoy without members is rejected", func(t *testing.T) {
		empty, err := NewTransitCycle("chamber-1", uuid.New(), DirectionUpstream, 1, nil, time.Now())
		if err == nil {
			t.Fatal("expected cycle creation without vessels to fail")
		}
		if empty.HasReleaseSignals(nil) {
			t.Fatal("expected memberless convoy never to satisfy release")
		}
	})
}
