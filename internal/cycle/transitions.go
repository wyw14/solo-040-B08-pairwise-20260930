package cycle

import (
	"errors"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

func nextAfterGate(stage model.TransitStage) (model.TransitStage, error) {
	switch stage {
	case model.StagePlanned:
		return model.StageAdmitting, nil
	case model.StageAdmitting:
		return model.StageSealed, nil
	case model.StageSealed:
		return model.StageLeveling, nil
	case model.StageLeveling:
		return model.StageReleasing, nil
	default:
		return "", errors.New("gate acknowledgement is not valid in current stage")
	}
}

// applySignalTransition only completes a releasing transit once every convoy
// member holds a current-generation proceed signal. Empty releases, missing
// members and stale signals are rejected, and a terminal transit can never be
// completed a second time.
func applySignalTransition(current *model.TransitCycle, states []model.SignalState, now time.Time) error {
	if current.Stage.Terminal() {
		return errors.New("terminal transit cannot accept signal handoff")
	}
	if current.Stage != model.StageReleasing {
		return errors.New("signal handoff requires releasing stage")
	}
	if len(current.VesselIDs) == 0 {
		return errors.New("convoy has no members to confirm release")
	}
	if !current.HasReleaseSignals(states) {
		return errors.New("release signals missing for one or more convoy members")
	}
	return current.Advance(model.StageCompleted, "field signal handoff confirmed", now)
}

func terminalStage(reason string) model.TransitStage {
	if reason == "emergency" {
		return model.StageEmergency
	}
	return model.StageCancelled
}
