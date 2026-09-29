package cycle

import (
	"errors"

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

func applySignalTransition(current *model.TransitCycle) error {
	if current.Stage.Terminal() {
		return errors.New("terminal transit cannot accept signal handoff")
	}
	if current.Stage == model.StageReleasing {
		return current.Advance(model.StageCompleted, "field signal handoff confirmed", current.UpdatedAt.Add(1))
	}
	return errors.New("signal handoff requires releasing stage")
}

func terminalStage(reason string) model.TransitStage {
	if reason == "emergency" {
		return model.StageEmergency
	}
	return model.StageCancelled
}
