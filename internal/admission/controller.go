package admission

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/signal"
)

type Decision struct {
	Allowed  bool      `json:"allowed"`
	Reason   string    `json:"reason"`
	VesselID uuid.UUID `json:"vessel_id,omitempty"`
}

// Controller grants admission only from observed sealed gate positions.
type Controller struct {
	queue   *Queue
	gates   *gate.State
	signals *signal.Store
	now     func() time.Time
}

func NewController(queue *Queue, gates *gate.State, signals *signal.Store, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	return &Controller{queue: queue, gates: gates, signals: signals, now: now}
}

func (c *Controller) Evaluate(ctx context.Context, cycle model.TransitCycle) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	entry, ok := c.queue.Peek(cycle.ChamberID)
	if !ok {
		return Decision{Reason: "queue empty"}, nil
	}
	if entry.PlanID != cycle.PlanID {
		return Decision{VesselID: entry.Vessel.ID, Reason: "head vessel belongs to another plan"}, nil
	}
	if !c.gates.Sealed(cycle.ChamberID) {
		return Decision{VesselID: entry.Vessel.ID, Reason: "chamber gates are not sealed"}, nil
	}
	state := c.signals.Set(entry.Vessel.ID, cycle.ID, cycle.Generation, model.SignalProceed, c.now())
	if state.Aspect != model.SignalProceed || !c.queue.MarkReleased(cycle.ChamberID, entry.Vessel.ID) {
		return Decision{}, errors.New("failed to commit admission decision")
	}
	return Decision{Allowed: true, VesselID: entry.Vessel.ID, Reason: "sealed chamber ready"}, nil
}

func (c *Controller) OwnerAllowed(vesselID uuid.UUID, owner string, token uint64, lease model.Lease, now time.Time) bool {
	return lease.VesselID == vesselID && lease.Owner == owner &&
		lease.FencingToken == token && lease.Active(now)
}

func (c *Controller) Queue() *Queue { return c.queue }
