package cycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/cycle"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/hydraulics"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/signal"
)

type handoffHarness struct {
	ctx         context.Context
	coordinator *cycle.Coordinator
	gates       *gate.Controller
	store       *signal.Store
	publisher   *signal.Publisher
	plan        model.ConvoyPlan
	cycle       model.TransitCycle
	now         time.Time
}

func newHandoffHarness(t *testing.T) *handoffHarness {
	t.Helper()
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	recorder := journal.NewRecorder(journal.NewMemoryStore(), clock)
	gateState := gate.NewState("chamber-1")
	gateCommands := gate.NewCommands()
	gateController := gate.NewController(gateState, gateCommands, recorder, clock)
	ramp := hydraulics.NewRamp(time.Millisecond, clock)
	hydraulicsController := hydraulics.NewController(gateCommands, recorder, ramp, clock)
	store := signal.NewStore()
	coordinator := cycle.NewCoordinator(gateController, hydraulicsController, store, recorder, clock)
	publisher := signal.NewPublisher(store, recorder, clock)

	vesselA, err := model.NewVessel("MV Alpha", "ALPHA-1", 120)
	if err != nil {
		t.Fatalf("vessel A: %v", err)
	}
	vesselB, err := model.NewVessel("MV Bravo", "BRAVO-2", 90)
	if err != nil {
		t.Fatalf("vessel B: %v", err)
	}
	plan := model.ConvoyPlan{
		ID: uuid.New(), ChamberID: "chamber-1",
		Direction: model.DirectionUpstream, Vessels: []model.Vessel{vesselA, vesselB},
		CreatedAt: now,
	}
	ctx := context.Background()
	if _, err := coordinator.Start(ctx, plan); err != nil {
		t.Fatalf("start cycle: %v", err)
	}

	for index := 0; index < 4; index++ {
		current, ok := coordinator.Current(plan.ChamberID)
		if !ok {
			t.Fatal("active transit disappeared")
		}
		command, err := gateController.Request(ctx, current, model.DirectionUpstream, false)
		if err != nil {
			t.Fatalf("request gate command %d: %v", index, err)
		}
		ack := model.DeviceAck{
			CommandID: command.ID, CycleID: command.CycleID, ChamberID: command.ChamberID,
			Generation: command.Generation, Epoch: command.Epoch, Device: command.Device,
			Settled: true, ObservedAt: now,
		}
		if err := coordinator.ApplyGateAck(ctx, ack); err != nil {
			t.Fatalf("gate ack %d: %v", index, err)
		}
	}
	releasing, ok := coordinator.Current(plan.ChamberID)
	if !ok || releasing.Stage != model.StageReleasing {
		t.Fatalf("expected releasing stage, got %#v ok=%v", releasing, ok)
	}
	return &handoffHarness{
		ctx: ctx, coordinator: coordinator, gates: gateController, store: store,
		publisher: publisher, plan: plan, cycle: releasing, now: now,
	}
}

func (h *handoffHarness) publishAll(t *testing.T) {
	if _, err := h.publisher.Publish(h.ctx, h.cycle, h.plan.Vessels, model.SignalProceed); err != nil {
		t.Fatalf("publish convoy signals: %v", err)
	}
}

func TestHandoffRejectsEmptyRelease(t *testing.T) {
	h := newHandoffHarness(t)
	// No signal has ever been published for the convoy.
	err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation)
	if err == nil {
		t.Fatal("handoff without any release signals must not complete the transit")
	}
	current, _ := h.coordinator.Current(h.plan.ChamberID)
	if current.Stage != model.StageReleasing {
		t.Fatalf("transit changed stage on rejected handoff: %s", current.Stage)
	}
}

func TestHandoffRejectsMissingConvoyMember(t *testing.T) {
	h := newHandoffHarness(t)
	// Only the lead vessel holds a current-generation proceed signal.
	h.store.Set(h.plan.Vessels[0].ID, h.cycle.ID, h.cycle.Generation, model.StageReleasing, model.SignalProceed, h.now)
	err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation)
	if err == nil {
		t.Fatal("handoff with an unconfirmed convoy member must not complete the transit")
	}
	current, _ := h.coordinator.Current(h.plan.ChamberID)
	if current.Stage != model.StageReleasing {
		t.Fatalf("transit changed stage on rejected handoff: %s", current.Stage)
	}
}

func TestHandoffRejectsStaleGenerationSignals(t *testing.T) {
	h := newHandoffHarness(t)
	for _, vessel := range h.plan.Vessels {
		h.store.Set(vessel.ID, h.cycle.ID, h.cycle.Generation-1, model.StageReleasing, model.SignalProceed, h.now)
	}
	err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation)
	if err == nil {
		t.Fatal("handoff confirmed against previous-generation signals must not complete")
	}
	current, _ := h.coordinator.Current(h.plan.ChamberID)
	if current.Stage != model.StageReleasing {
		t.Fatalf("transit changed stage on rejected handoff: %s", current.Stage)
	}
}

func TestHandoffRejectsOldGenerationIdentity(t *testing.T) {
	h := newHandoffHarness(t)
	h.publishAll(t)
	if err := h.coordinator.ApplySignalHandoff(h.ctx, uuid.New(), h.cycle.Generation); err == nil {
		t.Fatal("handoff for an unknown transit id must be rejected")
	}
	if err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation+1); err == nil {
		t.Fatal("handoff claiming a newer generation must be rejected")
	}
	if err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation-1); err == nil {
		t.Fatal("handoff claiming an older generation must be rejected")
	}
	current, _ := h.coordinator.Current(h.plan.ChamberID)
	if current.Stage != model.StageReleasing {
		t.Fatalf("transit changed stage on rejected handoff: %s", current.Stage)
	}
}

func TestLegalHandoffCompletesExactlyOnce(t *testing.T) {
	h := newHandoffHarness(t)
	h.publishAll(t)
	if err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation); err != nil {
		t.Fatalf("legal handoff should complete the transit: %v", err)
	}
	completed, _ := h.coordinator.Current(h.plan.ChamberID)
	if completed.Stage != model.StageCompleted {
		t.Fatalf("expected completed stage, got %s", completed.Stage)
	}
	if err := h.coordinator.ApplySignalHandoff(h.ctx, h.cycle.ID, h.cycle.Generation); err == nil {
		t.Fatal("a second handoff must not complete the transit again")
	}
	again, _ := h.coordinator.Current(h.plan.ChamberID)
	if again.Stage != model.StageCompleted {
		t.Fatalf("repeated handoff disturbed the terminal stage: %s", again.Stage)
	}
}

func TestHandoffBeforeReleasingIsRejected(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	recorder := journal.NewRecorder(journal.NewMemoryStore(), clock)
	gateController := gate.NewController(gate.NewState("chamber-9"), gate.NewCommands(), recorder, clock)
	hydraulicsController := hydraulics.NewController(gateController.Commands(), recorder, hydraulics.NewRamp(time.Millisecond, clock), clock)
	store := signal.NewStore()
	coordinator := cycle.NewCoordinator(gateController, hydraulicsController, store, recorder, clock)
	vessel, _ := model.NewVessel("MV Charlie", "CHARLIE-3", 70)
	plan := model.ConvoyPlan{
		ID: uuid.New(), ChamberID: "chamber-9", Direction: model.DirectionDownstream,
		Vessels: []model.Vessel{vessel}, CreatedAt: now,
	}
	started, err := coordinator.Start(context.Background(), plan)
	if err != nil {
		t.Fatalf("start cycle: %v", err)
	}
	for _, member := range started.VesselIDs {
		store.Set(member, started.ID, started.Generation, started.Stage, model.SignalProceed, now)
	}
	if err := coordinator.ApplySignalHandoff(context.Background(), started.ID, started.Generation); err == nil {
		t.Fatal("handoff must require the releasing stage even when signals are present")
	}
	current, _ := coordinator.Current(plan.ChamberID)
	if current.Stage != model.StagePlanned {
		t.Fatalf("stage changed after early handoff: %s", current.Stage)
	}
}
