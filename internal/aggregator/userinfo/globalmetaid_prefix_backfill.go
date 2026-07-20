package userinfo

import (
	"context"
	"errors"
	"fmt"
)

type GlobalMetaIDPrefixBackfillOptions struct {
	Context  context.Context
	Client   *BackfillClient
	PageSize int
}

type GlobalMetaIDPrefixBackfillSummary struct {
	Status                string `json:"status"`
	IndexedCount          int64  `json:"indexedCount"`
	DuplicateCount        int64  `json:"duplicateCount"`
	ReplacedCount         int64  `json:"replacedCount"`
	InvalidCount          int64  `json:"invalidCount"`
	MissingTimestampCount int64  `json:"missingTimestampCount"`
}

func (a *Aggregator) BackfillGlobalMetaIDPrefix(opts GlobalMetaIDPrefixBackfillOptions) (GlobalMetaIDPrefixBackfillSummary, error) {
	if a == nil || a.store == nil {
		return GlobalMetaIDPrefixBackfillSummary{}, errors.New("userinfo aggregator is required")
	}
	if opts.Client == nil {
		return GlobalMetaIDPrefixBackfillSummary{}, errors.New("userinfo backfill client is required")
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultBackfillPageSize
	}

	state, err := a.loadGlobalMetaIDPrefixIndexState()
	if err != nil {
		return GlobalMetaIDPrefixBackfillSummary{}, err
	}
	if state == nil {
		state = &globalMetaIDPrefixIndexState{Status: globalMetaIDPrefixStateBuilding}
		if err := a.saveGlobalMetaIDPrefixIndexState(*state); err != nil {
			return GlobalMetaIDPrefixBackfillSummary{}, err
		}
	}
	if state.Status == globalMetaIDPrefixStateReady {
		return globalMetaIDPrefixBackfillSummary(*state), nil
	}
	state.Status = globalMetaIDPrefixStateBuilding

	cursor := state.Cursor
	seenCursors := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return globalMetaIDPrefixBackfillSummary(*state), err
		}
		if _, seen := seenCursors[cursor]; seen {
			return globalMetaIDPrefixBackfillSummary(*state), fmt.Errorf("repeated MANAPI cursor %q for path /", cursor)
		}
		seenCursors[cursor] = struct{}{}

		page, err := opts.Client.ListPath(ctx, "/", cursor, pageSize)
		if err != nil {
			return globalMetaIDPrefixBackfillSummary(*state), err
		}

		pageState := *state
		records := make([]globalMetaIDCreationRecord, 0, len(page.Pins))
		for _, sourcePin := range page.Pins {
			record, reason := globalMetaIDCreationRecordFromPin(nil, sourcePin.toAggregatorPin())
			switch reason {
			case "":
				records = append(records, record)
			case "missing_timestamp":
				pageState.MissingTimeCount++
			default:
				pageState.InvalidCount++
			}
		}
		nextCursor := page.NextCursor
		if nextCursor == "" {
			pageState.Status = globalMetaIDPrefixStateReady
			pageState.Cursor = ""
		} else {
			pageState.Cursor = nextCursor
		}
		if _, err := a.upsertGlobalMetaIDCreationRecordsWithState(records, &pageState); err != nil {
			return globalMetaIDPrefixBackfillSummary(*state), err
		}
		state = &pageState
		if state.Status == globalMetaIDPrefixStateReady {
			return globalMetaIDPrefixBackfillSummary(*state), nil
		}
		cursor = nextCursor
	}
}

func globalMetaIDPrefixBackfillSummary(state globalMetaIDPrefixIndexState) GlobalMetaIDPrefixBackfillSummary {
	return GlobalMetaIDPrefixBackfillSummary{
		Status:                state.Status,
		IndexedCount:          state.IndexedCount,
		DuplicateCount:        state.DuplicateCount,
		ReplacedCount:         state.ReplacedCount,
		InvalidCount:          state.InvalidCount,
		MissingTimestampCount: state.MissingTimeCount,
	}
}
