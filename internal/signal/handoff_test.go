package signal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

func testSignalCycle(stage model.TransitStage, vessels []model.Vessel) (model.TransitCycle, *Store, *Publisher) {
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	store := NewStore()
	recorder := journal.NewRecorder(journal.NewMemoryStore(), func() time.Time { return now })
	ids := make([]uuid.UUID, 0, len(vessels))
	for _, vessel := range vessels {
		ids = append(ids, vessel.ID)
	}
	cycleValue, err := model.NewTransitCycle("chamber-1", uuid.New(), model.DirectionUpstream, 1, ids, now)
	if err != nil {
		panic(err)
	}
	for _, next := range []model.TransitStage{
		model.StageAdmitting, model.StageSealed, model.StageLeveling, model.StageReleasing,
	} {
		if err := cycleValue.Advance(next, "test", now.Add(time.Second)); err != nil {
			panic(err)
		}
		if next == stage {
			break
		}
	}
	publisher := NewPublisher(store, recorder, func() time.Time { return now })
	return cycleValue, store, publisher
}

func TestPublisherRejectsEmptyConvoy(t *testing.T) {
	vessel, err := model.NewVessel("MV Alpha", "ALPHA-1", 120)
	if err != nil {
		t.Fatalf("vessel: %v", err)
	}
	cycleValue, store, publisher := testSignalCycle(model.StageReleasing, []model.Vessel{vessel})

	if _, err := publisher.Publish(context.Background(), cycleValue, nil, model.SignalProceed); err == nil {
		t.Fatal("publishing to an empty vessel list must fail instead of recording an empty release")
	}
	if len(store.List()) != 0 {
		t.Fatal("rejected publication must not leave any signal state behind")
	}
}

func TestPublisherRejectsTerminalTransit(t *testing.T) {
	vessel, _ := model.NewVessel("MV Alpha", "ALPHA-1", 120)
	cycleValue, _, publisher := testSignalCycle(model.StageReleasing, []model.Vessel{vessel})
	if err := cycleValue.Advance(model.StageCompleted, "done", time.Now()); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := publisher.Publish(context.Background(), cycleValue, []model.Vessel{vessel}, model.SignalProceed); err == nil {
		t.Fatal("publishing release signals onto a completed transit must fail")
	}
}

type fakeLookup struct {
	current   model.TransitCycle
	ok        bool
	applyErr  error
	applyHits int
}

func (f *fakeLookup) Current(string) (model.TransitCycle, bool) { return f.current, f.ok }

func (f *fakeLookup) ApplySignalHandoff(_ context.Context, _ uuid.UUID, _ uint64) error {
	f.applyHits++
	return f.applyErr
}

func releasingCycle(t *testing.T, generation uint64) model.TransitCycle {
	t.Helper()
	vessel, _ := model.NewVessel("MV Alpha", "ALPHA-1", 120)
	cycleValue, err := model.NewTransitCycle("chamber-1", uuid.New(), model.DirectionUpstream,
		generation, []uuid.UUID{vessel.ID}, time.Now())
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	for _, stage := range []model.TransitStage{
		model.StageAdmitting, model.StageSealed, model.StageLeveling, model.StageReleasing,
	} {
		if err := cycleValue.Advance(stage, "test", time.Now()); err != nil {
			t.Fatalf("advance %s: %v", stage, err)
		}
	}
	return cycleValue
}

func TestConfirmHandoffValidation(t *testing.T) {
	ctx := context.Background()
	cycleValue := releasingCycle(t, 2)

	t.Run("legal handoff is applied and recorded once", func(t *testing.T) {
		lookup := &fakeLookup{current: cycleValue, ok: true}
		service := NewService(NewStore())
		service.BindTransitLookup(lookup)
		handoff := Handoff{ChamberID: cycleValue.ChamberID, CycleID: cycleValue.ID, Generation: 2}
		if err := service.ConfirmHandoff(ctx, handoff); err != nil {
			t.Fatalf("legal handoff: %v", err)
		}
		if lookup.applyHits != 1 || len(service.History()) != 1 {
			t.Fatalf("expected one apply and one history entry, got %d/%d", lookup.applyHits, len(service.History()))
		}
	})

	t.Run("coordinator refusal is not recorded", func(t *testing.T) {
		lookup := &fakeLookup{current: cycleValue, ok: true, applyErr: errors.New("release signals missing")}
		service := NewService(NewStore())
		service.BindTransitLookup(lookup)
		handoff := Handoff{ChamberID: cycleValue.ChamberID, CycleID: cycleValue.ID, Generation: 2}
		if err := service.ConfirmHandoff(ctx, handoff); err == nil {
			t.Fatal("coordinator refusal must surface as a handoff error")
		}
		if len(service.History()) != 0 {
			t.Fatal("failed handoff must not be appended to history")
		}
	})

	t.Run("stale generation is rejected before apply", func(t *testing.T) {
		lookup := &fakeLookup{current: cycleValue, ok: true}
		service := NewService(NewStore())
		service.BindTransitLookup(lookup)
		handoff := Handoff{ChamberID: cycleValue.ChamberID, CycleID: cycleValue.ID, Generation: 1}
		if err := service.ConfirmHandoff(ctx, handoff); err == nil {
			t.Fatal("old-generation handoff must be rejected")
		}
		if lookup.applyHits != 0 {
			t.Fatal("stale handoff must never reach the coordinator")
		}
	})

	t.Run("non releasing stage is rejected", func(t *testing.T) {
		early := releasingCycle(t, 2)
		early.Stage = model.StageLeveling
		lookup := &fakeLookup{current: early, ok: true}
		service := NewService(NewStore())
		service.BindTransitLookup(lookup)
		handoff := Handoff{ChamberID: early.ChamberID, CycleID: early.ID, Generation: 2}
		if err := service.ConfirmHandoff(ctx, handoff); err == nil {
			t.Fatal("handoff before releasing must be rejected")
		}
		if lookup.applyHits != 0 {
			t.Fatal("early handoff must never reach the coordinator")
		}
	})

	t.Run("completed transit is rejected", func(t *testing.T) {
		done := releasingCycle(t, 2)
		if err := done.Advance(model.StageCompleted, "done", time.Now()); err != nil {
			t.Fatalf("complete: %v", err)
		}
		lookup := &fakeLookup{current: done, ok: true}
		service := NewService(NewStore())
		service.BindTransitLookup(lookup)
		handoff := Handoff{ChamberID: done.ChamberID, CycleID: done.ID, Generation: 2}
		if err := service.ConfirmHandoff(ctx, handoff); err == nil {
			t.Fatal("handoff against a completed transit must be rejected")
		}
		if lookup.applyHits != 0 {
			t.Fatal("terminal handoff must never reach the coordinator")
		}
	})
}
