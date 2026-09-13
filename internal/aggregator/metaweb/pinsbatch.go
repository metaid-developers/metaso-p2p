package metaweb

// POST /api/metaweb/pins:batch — the deep-read batch endpoint (R2): up to 50
// pin ids in one call; per-pin failures become {"error": …} entries and never
// fail the batch. Entries share the single pin-read data shape so existing
// consumers parse both identically. See
// docs/specs/2026-09-13-metaweb-surf-reads-api.md §2.

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

const (
	batchMaxPins            = 50
	batchResolveConcurrency = 8
)

type pinBatchRequest struct {
	PinIds []string `json:"pinIds"`
}

type pinBatchData struct {
	Pins map[string]any `json:"pins"`
}

func (a *Aggregator) handlePinBatch(c *gin.Context) {
	var body pinBatchRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		api.RespErr(c, codeInvalidParam, "invalid body")
		return
	}
	if len(body.PinIds) == 0 {
		api.RespErr(c, codeInvalidParam, "pinIds required")
		return
	}
	if len(body.PinIds) > batchMaxPins {
		api.RespErr(c, codeInvalidParam, "pinIds limited to 50 per call")
		return
	}

	// Dedupe preserving first-seen order; entries are keyed by pin id, so
	// duplicates would otherwise race on the result map.
	seen := make(map[string]struct{}, len(body.PinIds))
	ids := make([]string, 0, len(body.PinIds))
	for _, raw := range body.PinIds {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		api.RespErr(c, codeInvalidParam, "pinIds required")
		return
	}

	results := make([]any, len(ids))
	sem := make(chan struct{}, batchResolveConcurrency)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(idx int, pinId string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if !pinIDPattern.MatchString(pinId) {
				results[idx] = map[string]string{"error": "malformed pinId"}
				return
			}
			data, err := a.resolvePinData(pinId)
			if err != nil {
				switch {
				case errors.Is(err, ErrRemotePinNotFound):
					results[idx] = map[string]string{"error": "pin not found"}
				case errors.Is(err, ErrInvalidPinId):
					results[idx] = map[string]string{"error": "malformed pinId"}
				default:
					results[idx] = map[string]string{"error": "pin unavailable"}
				}
				return
			}
			results[idx] = data
		}(i, id)
	}
	wg.Wait()

	pins := make(map[string]any, len(ids))
	for i, id := range ids {
		pins[id] = results[i]
	}
	api.RespSuccess(c, pinBatchData{Pins: pins})
}
