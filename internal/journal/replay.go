package journal

import (
	"context"
	"fmt"

	"github.com/wyw14/cry-96/internal/model"
)

// ReplayState contains the recovered state and the applied partition cursor.
type ReplayState struct {
	ChamberID string
	Snapshot  model.ChamberSnapshot
	Cursor    uint64
	Applied   []model.Event
}

// Replayer restores each chamber with an independent sequence cursor.
type Replayer struct {
	store Store
}

func NewReplayer(store Store) *Replayer { return &Replayer{store: store} }

func (r *Replayer) ReplayChamber(ctx context.Context, chamberID string) (ReplayState, error) {
	snapshot, ok, err := r.store.Snapshot(ctx, chamberID)
	if err != nil {
		return ReplayState{}, err
	}
	if !ok {
		snapshot = model.ChamberSnapshot{ChamberID: chamberID}
	}
	events, err := r.store.Events(ctx, chamberID, snapshot.Sequence)
	if err != nil {
		return ReplayState{}, err
	}
	cursor := snapshot.Sequence
	for _, event := range events {
		if event.ChamberID != chamberID {
			return ReplayState{}, fmt.Errorf("replay partition mismatch chamber=%s event=%s", chamberID, event.ChamberID)
		}
		if event.Sequence != cursor+1 {
			return ReplayState{}, fmt.Errorf("replay gap chamber=%s expected=%d got=%d", chamberID, cursor+1, event.Sequence)
		}
		cursor = event.Sequence
	}
	return ReplayState{
		ChamberID: chamberID, Snapshot: snapshot,
		Cursor: cursor, Applied: append([]model.Event(nil), events...),
	}, nil
}

func (r *Replayer) ReplayAll(ctx context.Context) (map[string]ReplayState, error) {
	chambers, err := r.store.Chambers(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]ReplayState, len(chambers))
	for _, chamberID := range chambers {
		state, err := r.ReplayChamber(ctx, chamberID)
		if err != nil {
			return nil, err
		}
		result[chamberID] = state
	}
	return result, nil
}
