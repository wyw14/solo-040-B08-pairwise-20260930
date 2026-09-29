package safety

import (
	"context"
	"errors"

	"github.com/wyw14/cry-96/internal/cycle"
	"github.com/wyw14/cry-96/internal/model"
)

// Service couples cycle terminal state with equipment quiescence.
type Service struct {
	coordinator *Coordinator
	cycles      *cycle.Coordinator
}

func NewService(coordinator *Coordinator, cycles *cycle.Coordinator) *Service {
	return &Service{coordinator: coordinator, cycles: cycles}
}

func (s *Service) EmergencyStop(ctx context.Context, chamberID string) (model.EmergencyRecord, error) {
	if chamberID == "" {
		return model.EmergencyRecord{}, errors.New("chamber is required")
	}
	if _, ok := s.cycles.Current(chamberID); ok {
		if _, err := s.cycles.Cancel(ctx, chamberID, "emergency"); err != nil {
			return model.EmergencyRecord{}, err
		}
	}
	return s.coordinator.Stop(ctx, chamberID)
}

func (s *Service) Status(chamberID string) model.EmergencyRecord {
	return s.coordinator.Status(chamberID)
}

func (s *Service) Barrier() *Barrier { return s.coordinator.barrier }
