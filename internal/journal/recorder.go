package journal

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

// Recorder turns domain transitions into consistently stamped events.
type Recorder struct {
	store Store
	now   func() time.Time
}

func NewRecorder(store Store, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	return &Recorder{store: store, now: now}
}

func (r *Recorder) Record(ctx context.Context, chamberID string, kind model.EventType, cycleID uuid.UUID, generation uint64, payload any) (model.Event, error) {
	event, err := model.NewEvent(chamberID, kind, cycleID, generation, payload, r.now())
	if err != nil {
		return model.Event{}, err
	}
	return r.store.Append(ctx, event)
}

func (r *Recorder) Save(ctx context.Context, snapshot model.ChamberSnapshot) error {
	snapshot.SavedAt = r.now().UTC()
	return r.store.SaveSnapshot(ctx, snapshot)
}

func NewRuntimeStore(dataDirectory string) (Store, error) {
	if dataDirectory == "" {
		return NewMemoryStore(), nil
	}
	return NewFileStore(dataDirectory)
}
