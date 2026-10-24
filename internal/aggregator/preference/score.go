package preference

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// scoreKey identifies one video aggregate: chain-scoped, never merged
// across chains (decision D9).
type scoreKey struct {
	chainName   string
	videoSha256 string
}

// latestComparison is one latest-per-(chain, signer, unordered pair)
// comparison contributing to the open scores.
type latestComparison struct {
	chainName   string
	signer      string
	videoA      string // ordered so videoA <= videoB
	videoB      string
	winner      string // videoSha256 of the picked side, "" for tie
	tie         bool
	confirmedAt int64
	order       comparisonOrder
}

// comparisonOrder is the confirmed chain order used for latest-wins.
type comparisonOrder struct {
	height    int64
	timestamp int64
	pinId     string
	itemIndex int
}

func (o comparisonOrder) after(other comparisonOrder) bool {
	if o.height != other.height {
		return o.height > other.height
	}
	if o.timestamp != other.timestamp {
		return o.timestamp > other.timestamp
	}
	if o.pinId != other.pinId {
		return o.pinId > other.pinId
	}
	return o.itemIndex > other.itemIndex
}

// pairKey is the unordered video-pair identity of one comparison.
type pairKey struct {
	chainName string
	signer    string
	low       string
	high      string
}

// rebuildOpenScores recomputes the warm open-preference snapshot from the
// stored confirmed feedback records. Deterministic: the same confirmed chain
// input always yields the same summaries (acceptance G4 / §8 vector 10).
func (a *Aggregator) rebuildOpenScores() {
	if a == nil || a.store == nil {
		return
	}
	summaries := map[scoreKey]OpenSummary{}

	latest := map[pairKey]latestComparison{}
	err := a.store.ScanPrefix(Namespace, []byte(keyOpenFeedback), func(_, value []byte) error {
		var rec FeedbackRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		if rec.IsMempool || rec.Status != StatusValid || !rec.Supported() || rec.IsTest {
			return nil
		}
		order := comparisonOrder{height: rec.ConfirmedHeight, timestamp: rec.ConfirmedAt, pinId: rec.FeedbackPinId}
		for i := range rec.Items {
			item := &rec.Items[i]
			if item.Excluded {
				continue
			}
			candidate := latestComparisonFromItem(&rec, item, order, i)
			key := pairKeyOf(candidate)
			if existing, ok := latest[key]; !ok || candidate.order.after(existing.order) {
				latest[key] = candidate
			}
		}
		return nil
	})
	if err != nil {
		// Keep the previous snapshot on a scan failure; the next confirmed
		// write or restart retries the rebuild.
		return
	}

	type aggregate struct {
		comparisons  int
		wins         int
		ties         int
		judges       map[string]bool
		lastCompared int64
	}
	aggregates := map[scoreKey]*aggregate{}
	addVideo := func(chainName, videoSha256 string, won, tied bool, signer string, confirmedAt int64) {
		key := scoreKey{chainName: chainName, videoSha256: videoSha256}
		agg := aggregates[key]
		if agg == nil {
			agg = &aggregate{judges: map[string]bool{}}
			aggregates[key] = agg
		}
		agg.comparisons++
		if tied {
			agg.ties++
		} else if won {
			agg.wins++
		}
		agg.judges[signer] = true
		if confirmedAt > agg.lastCompared {
			agg.lastCompared = confirmedAt
		}
	}
	for _, comparison := range latest {
		aWon := comparison.winner == comparison.videoA
		bWon := comparison.winner == comparison.videoB
		addVideo(comparison.chainName, comparison.videoA, aWon, comparison.tie, comparison.signer, comparison.confirmedAt)
		addVideo(comparison.chainName, comparison.videoB, bWon, comparison.tie, comparison.signer, comparison.confirmedAt)
	}

	for key, agg := range aggregates {
		score := (float64(agg.wins) + 0.5*float64(agg.ties) + 1) / float64(agg.comparisons+2)
		state := StateEmerging
		if len(agg.judges) >= establishedJudgeThreshold {
			state = StateEstablished
		}
		last := agg.lastCompared
		summaries[key] = OpenSummary{
			Score:          &score,
			ScoreVersion:   ScoreVersion,
			State:          state,
			Comparisons:    agg.comparisons,
			Judges:         len(agg.judges),
			LastComparedAt: &last,
		}
	}

	snapshot := make(map[string]OpenSummary, len(summaries))
	for key, summary := range summaries {
		snapshot[openScoreSnapshotKey(key.chainName, key.videoSha256)] = summary
	}
	a.openScores.Store(snapshot)
}

func latestComparisonFromItem(rec *FeedbackRecord, item *FeedbackComparison, order comparisonOrder, itemIndex int) latestComparison {
	low, high := item.A.VideoSha256, item.B.VideoSha256
	if low > high {
		low, high = high, low
	}
	comparison := latestComparison{
		chainName:   rec.ChainName,
		signer:      rec.SignerGlobalMetaId,
		videoA:      low,
		videoB:      high,
		tie:         item.Pick == "tie",
		confirmedAt: rec.ConfirmedAt,
		order:       order,
	}
	// order.itemIndex makes duplicate pairs inside one batch deterministic.
	comparison.order.itemIndex = itemIndex
	if !comparison.tie {
		switch item.Pick {
		case "a":
			comparison.winner = item.A.VideoSha256
		case "b":
			comparison.winner = item.B.VideoSha256
		}
	}
	return comparison
}

func pairKeyOf(comparison latestComparison) pairKey {
	return pairKey{chainName: comparison.chainName, signer: comparison.signer, low: comparison.videoA, high: comparison.videoB}
}

func openScoreSnapshotKey(chainName, videoSha256 string) string {
	return strings.ToLower(strings.TrimSpace(chainName)) + ":" + strings.TrimSpace(videoSha256)
}

// openScoreSnapshot returns the warm summary map (never nil after Init).
func (a *Aggregator) openScoreSnapshot() map[string]OpenSummary {
	if snapshot, ok := a.openScores.Load().(map[string]OpenSummary); ok {
		return snapshot
	}
	return nil
}

// OpenSummaryFor returns the open summary of one (chainName, videoSha256)
// identity; unjudged identities get the exact zero-evidence summary.
func (a *Aggregator) OpenSummaryFor(chainName, videoSha256 string) OpenSummary {
	if snapshot := a.openScoreSnapshot(); snapshot != nil {
		if summary, ok := snapshot[openScoreSnapshotKey(chainName, videoSha256)]; ok {
			return summary
		}
	}
	return UnjudgedOpenSummary()
}

// listFeedbackRecords returns every stored feedback record, newest first by
// confirmed order then relay time (mempool records sort by observed time).
// Used by the comparisons evidence API.
func (a *Aggregator) listFeedbackRecords() ([]FeedbackRecord, error) {
	records := make([]FeedbackRecord, 0)
	err := a.store.ScanPrefix(Namespace, []byte(keyOpenFeedback), func(_, value []byte) error {
		var rec FeedbackRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		records = append(records, rec)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list feedback records: %w", err)
	}
	sort.SliceStable(records, func(i, j int) bool {
		left := evidenceSortTime(records[i])
		right := evidenceSortTime(records[j])
		if left != right {
			return left > right
		}
		return records[i].FeedbackPinId < records[j].FeedbackPinId
	})
	return records, nil
}

func evidenceSortTime(rec FeedbackRecord) int64 {
	if rec.IsMempool {
		return rec.ObservedAt
	}
	return rec.ConfirmedAt
}

// Supported reports whether the batch belongs to the v1 Motion Video
// projection.
func (r *FeedbackRecord) Supported() bool {
	return hasMotionVideoCategory(r.Categories)
}
