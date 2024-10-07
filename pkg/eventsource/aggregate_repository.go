package eventsource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/anypb"
)

func NewAggregateRepository[T any, R aggregateRoot[T]](
	eventStore EventStore,
) *AggregateRepository[T, R] {
	return &AggregateRepository[T, R]{
		eventStore: eventStore,
	}
}

type AggregateRepository[T any, R aggregateRoot[T]] struct {
	eventStore EventStore
}

func (r *AggregateRepository[T, R]) Get(
	ctx context.Context, id string,
) (*Aggregate[T, R], error) {
	agg, err := r.Load(ctx, id)
	if err != nil {
		return nil, err
	}

	if agg.Version() == 0 {
		return nil, ErrAggregateDoesNotExist
	}

	return agg, nil
}

func (r *AggregateRepository[T, R]) Create(
	ctx context.Context, id string, cmd Command,
) (*Aggregate[T, R], error) {
	if id == "" {
		randomID, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("generate ID: %w", err)
		}
		id = randomID.String()
	}

	agg, err := r.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}

	if agg.Version() != 0 {
		return nil, ErrAggregateAlreadyExists
	}

	if err := agg.ChangeState(ctx, cmd); err != nil {
		return nil, fmt.Errorf("change state: %w", err)
	}

	if err := r.Save(ctx, agg); err != nil {
		if errors.Is(err, ErrConcurrentUpdate) {
			return nil, ErrAggregateAlreadyExists
		}
		return nil, fmt.Errorf("save: %w", err)
	}

	return agg, nil
}

func (r *AggregateRepository[T, R]) GetOrCreate(
	ctx context.Context, id string, cmd Command,
) (*Aggregate[T, R], error) {
	if id == "" {
		randomID, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("generate ID: %w", err)
		}
		id = randomID.String()
	}

	agg, err := r.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}

	if agg.Version() > 0 {
		return agg, nil
	}

	if err := agg.ChangeState(ctx, cmd); err != nil {
		return nil, fmt.Errorf("change state: %w", err)
	}

	if err := r.Save(ctx, agg); err != nil {
		if errors.Is(err, ErrConcurrentUpdate) {
			agg, err = r.Load(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("load: %w", err)
			}
			return agg, nil
		}
		return nil, fmt.Errorf("save: %w", err)
	}

	return agg, nil
}

func (r *AggregateRepository[T, R]) Update(
	ctx context.Context, id string, cmd Command,
) (*Aggregate[T, R], error) {
	agg, err := r.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}

	if agg.Version() == 0 {
		return nil, ErrAggregateDoesNotExist
	}

	if err := agg.ChangeState(ctx, cmd); err != nil {
		return nil, fmt.Errorf("change state: %w", err)
	}

	if err := r.Save(ctx, agg); err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}

	return agg, nil
}

func (r *AggregateRepository[T, R]) Load(
	ctx context.Context, id string,
) (*Aggregate[T, R], error) {
	events, err := r.eventStore.ListEvents(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	var root R = new(T)
	var version int
	causationIDs := make(map[string]struct{}, len(events))

	for _, event := range events {
		stateChange, err := event.Data.UnmarshalNew()
		if err != nil {
			return nil, fmt.Errorf("unmarshal state change: %w", err)
		}

		root.ApplyStateChange(stateChange)
		version = event.AggregateVersion

		if id, ok := event.Metadata[CausationID].(string); ok {
			causationIDs[id] = struct{}{}
		}
	}

	return &Aggregate[T, R]{
		id:           id,
		version:      version,
		root:         root,
		stateChanges: nil,
		causationIDs: causationIDs,
	}, nil
}

func (r *AggregateRepository[T, R]) Save(
	ctx context.Context, agg *Aggregate[T, R],
) error {
	if len(agg.stateChanges) == 0 {
		return nil
	}

	originalVersion := agg.Version() - len(agg.stateChanges)
	metadata := MetadataFromContext(ctx)
	events := make(Events, 0, len(agg.stateChanges))

	for i, stateChange := range agg.stateChanges {
		id, err := uuid.NewRandom()
		if err != nil {
			return fmt.Errorf("generate event ID: %w", err)
		}
		data, err := anypb.New(stateChange)
		if err != nil {
			return fmt.Errorf("marshal state change: %w", err)
		}
		events = append(events, &Event{
			ID:               id.String(),
			AggregateID:      agg.ID(),
			AggregateVersion: originalVersion + i + 1,
			Timestamp:        time.Now(),
			Metadata:         metadata,
			Data:             data,
		})
	}

	if err := r.eventStore.SaveEvents(
		ctx, agg.ID(), originalVersion, events,
	); err != nil {
		return fmt.Errorf("save events: %w", err)
	}

	agg.stateChanges = nil

	return nil
}
