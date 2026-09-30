package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

type handoffAPIFixture struct {
	t         *testing.T
	handler   http.Handler
	runtime   *Runtime
	planID    string
	cycleID   string
	vesselIDs []string
}

func setupHandoffAPI(t *testing.T) *handoffAPIFixture {
	t.Helper()
	runtime, err := NewRuntime(context.Background(), "")
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	fixture := &handoffAPIFixture{
		t: t, handler: Router(runtime, t.TempDir()), runtime: runtime,
	}

	body := `{"chamber_id":"chamber-1","direction":"upstream","vessels":[` +
		`{"name":"MV Alpha","call_sign":"ALPHA-1","length_m":110},` +
		`{"name":"MV Bravo","call_sign":"BRAVO-2","length_m":85}]}`
	recorder := fixture.post("/api/plans", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create plan status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Plan  model.ConvoyPlan   `json:"plan"`
		Cycle model.TransitCycle `json:"cycle"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	fixture.planID = created.Plan.ID.String()
	fixture.cycleID = created.Cycle.ID.String()
	for _, vessel := range created.Plan.Vessels {
		fixture.vesselIDs = append(fixture.vesselIDs, vessel.ID.String())
	}

	// Drive the four gate settlements planned -> admitting -> sealed ->
	// leveling -> releasing directly against the domain wiring.
	chamberID := "chamber-1"
	for index := 0; index < 4; index++ {
		cycleValue, ok := runtime.Cycles.Current(chamberID)
		if !ok {
			t.Fatal("active transit disappeared")
		}
		command, err := runtime.Gates.Request(context.Background(), cycleValue, model.DirectionUpstream, false)
		if err != nil {
			t.Fatalf("gate request %d: %v", index, err)
		}
		ack := model.DeviceAck{
			CommandID: command.ID, CycleID: command.CycleID, ChamberID: command.ChamberID,
			Generation: command.Generation, Epoch: command.Epoch, Device: command.Device,
			Settled: true, ObservedAt: time.Now(),
		}
		if err := runtime.Cycles.ApplyGateAck(context.Background(), ack); err != nil {
			t.Fatalf("gate ack %d: %v", index, err)
		}
	}
	releasing, ok := runtime.Cycles.Current(chamberID)
	if !ok || releasing.Stage != model.StageReleasing {
		t.Fatalf("expected releasing stage, got %#v ok=%v", releasing, ok)
	}
	return fixture
}

func (f *handoffAPIFixture) post(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *handoffAPIFixture) handoff() *httptest.ResponseRecorder {
	f.t.Helper()
	cycleValue, _ := f.runtime.Cycles.Current("chamber-1")
	body := `{"cycle_id":"` + cycleValue.ID.String() + `","generation":` +
		jsonNumber(cycleValue.Generation) + `}`
	return f.post("/api/chambers/chamber-1/signals/handoff", body)
}

func jsonNumber(value uint64) string {
	return string(mustJSON(value))
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func (f *handoffAPIFixture) stage() model.TransitStage {
	cycleValue, _ := f.runtime.Cycles.Current("chamber-1")
	return cycleValue.Stage
}

func TestAPIHandoffRequiresReleasePublications(t *testing.T) {
	fixture := setupHandoffAPI(t)

	// The admission flow already records a planned-stage proceed for the lead
	// vessel; it must not count as a release confirmation.
	recorder := fixture.handoff()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("handoff before any release publication status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.stage() != model.StageReleasing {
		t.Fatalf("empty handoff advanced the transit to %s", fixture.stage())
	}

	// Publish the release signals to every convoy member.
	recorder = fixture.post("/api/plans/"+fixture.planID+"/signals", "")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("publish signals status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Drop one member's signal: a missing vessel still blocks completion.
	leadVessel := fixture.vesselIDs[0]
	id, err := parseUUID(leadVessel)
	if err != nil {
		t.Fatalf("parse vessel id: %v", err)
	}
	fixture.runtime.SignalStore.Remove(id)
	recorder = fixture.handoff()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("handoff with a missing member status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.stage() != model.StageReleasing {
		t.Fatalf("incomplete handoff advanced the transit to %s", fixture.stage())
	}

	// Restore the member's signal; a legal handoff completes exactly once.
	cycleValue, _ := fixture.runtime.Cycles.Current("chamber-1")
	fixture.runtime.SignalStore.Set(id, cycleValue.ID, cycleValue.Generation, model.StageReleasing, model.SignalProceed, time.Now())
	recorder = fixture.handoff()
	if recorder.Code != http.StatusOK {
		t.Fatalf("legal handoff status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.stage() != model.StageCompleted {
		t.Fatalf("legal handoff left transit at %s", fixture.stage())
	}
	recorder = fixture.handoff()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate handoff status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.stage() != model.StageCompleted {
		t.Fatalf("duplicate handoff disturbed terminal stage %s", fixture.stage())
	}
}

func TestAPIHandoffRejectsStaleGeneration(t *testing.T) {
	fixture := setupHandoffAPI(t)
	if recorder := fixture.post("/api/plans/"+fixture.planID+"/signals", ""); recorder.Code != http.StatusAccepted {
		t.Fatalf("publish signals status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	cycleValue, _ := fixture.runtime.Cycles.Current("chamber-1")
	staleBody := `{"cycle_id":"` + cycleValue.ID.String() + `","generation":` +
		string(mustJSON(cycleValue.Generation-1)) + `}`
	recorder := fixture.post("/api/chambers/chamber-1/signals/handoff", staleBody)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale-generation handoff status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.stage() != model.StageReleasing {
		t.Fatalf("stale handoff advanced the transit to %s", fixture.stage())
	}
}
