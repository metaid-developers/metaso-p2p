package qa

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

func (a *Aggregator) processPin(pin *aggregator.PinInscription, isMempool bool) error {
	if pin == nil || strings.TrimSpace(pin.Id) == "" || strings.TrimSpace(pin.ChainName) == "" {
		return nil
	}
	chain := normaliseChain(pin.ChainName)
	switch protocolPathFromPinPath(pin.Path) {
	case PathSimpleQuestion:
		return a.processQuestion(pin, chain, isMempool)
	case PathSimpleAnswer:
		return a.processAnswer(pin, chain, isMempool)
	case PathPayLike:
		return a.processLike(pin, chain, isMempool)
	case PathPayComment:
		return a.processComment(pin, chain, isMempool)
	default:
		return nil
	}
}

func (a *Aggregator) processQuestion(pin *aggregator.PinInscription, chain string, isMempool bool) error {
	op := operationOf(pin)
	payload, err := parseQuestion(pin)
	if err != nil {
		return err
	}

	var previous *QuestionRecord
	if op == OperationCreate {
		// A question whose title is missing, empty, or does not end with a
		// question mark is skipped from the Q&A index (R7; it stays a valid
		// on-chain pin in the generic pipeline).
		if !titleEndsWithQuestionMark(payload.Title) {
			return nil
		}
		previous, err = a.loadQuestion(chain, pin.Id)
		if err != nil {
			return err
		}
		if previous != nil && !(!isMempool && previous.IsMempool) {
			return nil // already indexed (or newer state already held)
		}
	} else {
		target := targetPinID(pin)
		if target == "" {
			return nil
		}
		locator, ok := a.lookupLocator(target)
		if !ok || locator.kind != "q" {
			return nil
		}
		previous, err = a.loadQuestion(locator.chainName, locator.sourcePinId)
		if err != nil || previous == nil {
			return err
		}
	}

	rec := a.questionRecordFromPin(pin, chain, payload, previous, isMempool, op)
	mergeConfirmedReplayWithPendingQuestionCurrent(previous, rec, pin.Id)
	// A confirmed replay replaces the mempool record whose relay timestamp
	// keyed the time index; re-key so the feed never serves the question twice.
	if previous != nil && previous.CreatedAt != rec.CreatedAt {
		_ = a.store.Delete(Namespace, questionTimeKey(previous.CreatedAt, rec.ChainName, rec.SourcePinId))
	}
	if err := a.saveQuestion(rec); err != nil {
		return err
	}
	if err := a.mapPin(pin.Id, recordLocator{kind: "q", chainName: rec.ChainName, sourcePinId: rec.SourcePinId}); err != nil {
		return err
	}
	// Attach answers that referenced this question (by its source or newest
	// version pin id) before it was indexed, then refresh the aggregates.
	if err := a.attachPendingAnswers(rec); err != nil {
		return err
	}
	return a.refreshQuestion(rec.ChainName, rec.SourcePinId)
}

// attachPendingAnswers moves answers held in the pending index into the
// question's answer index once the question becomes resolvable. Both the
// source pin id and every newer version pin id are checked, so answers that
// referenced a modify version attach too.
func (a *Aggregator) attachPendingAnswers(rec *QuestionRecord) error {
	candidates := []string{rec.SourcePinId}
	if rec.CurrentPinId != "" && rec.CurrentPinId != rec.SourcePinId {
		candidates = append(candidates, rec.CurrentPinId)
	}
	for _, pinId := range candidates {
		prefix := pendingPrefix(pinId)
		var attached [][]byte
		if err := a.store.ScanPrefix(Namespace, prefix, func(key, _ []byte) error {
			attached = append(attached, append([]byte(nil), key...))
			return nil
		}); err != nil {
			return err
		}
		for _, key := range attached {
			parts := strings.SplitN(strings.TrimPrefix(string(key), string(prefix)), ":", 2)
			if len(parts) != 2 {
				continue
			}
			answer, err := a.loadAnswer(parts[0], parts[1])
			if err != nil {
				return err
			}
			if answer == nil {
				_ = a.store.Delete(Namespace, key)
				continue
			}
			answer.QuestionChain = rec.ChainName
			answer.QuestionPinId = rec.SourcePinId
			if err := a.saveAnswer(answer); err != nil {
				return err
			}
			if !answer.Hidden {
				if err := a.store.Set(Namespace, answerIndexKey(answer.QuestionChain, answer.QuestionPinId, answer.CreatedAt, answer.SourcePinId), []byte{}); err != nil {
					return err
				}
			}
			_ = a.store.Delete(Namespace, key)
		}
	}
	return nil
}

// questionRecordFromPin builds the record for a create, or the updated record
// for a modify/revoke. A modify with an empty title keeps the previous title
// (the protocol requires one; removing it is treated as malformed).
func (a *Aggregator) questionRecordFromPin(pin *aggregator.PinInscription, chain string, payload *questionPayload, previous *QuestionRecord, isMempool bool, op string) *QuestionRecord {
	ts := metawebdoc.NormalizeUnixSeconds(pin.Timestamp)
	if op == OperationCreate {
		return &QuestionRecord{
			SourcePinId:  pin.Id,
			CurrentPinId: pin.Id,
			ChainName:    chain,
			Title:        payload.Title,
			Content:      payload.Content,
			Summary:      summaryOf(payload.Content),
			Tags:         payload.Tags,
			ContentType:  payload.ContentType,
			Attachments:  payload.Attachments,
			Publisher:    identityFromPin(pin),
			Operation:    op,
			IsMempool:    isMempool,
			CreatedAt:    ts,
			UpdatedAt:    ts,
		}
	}
	rec := *previous
	rec.CurrentPinId = pin.Id
	rec.IsMempool = isMempool
	rec.Operation = op
	rec.UpdatedAt = ts
	if payload.Title != "" {
		rec.Title = payload.Title
	}
	if payload.Content != "" {
		rec.Content = payload.Content
		rec.Summary = summaryOf(payload.Content)
	}
	if payload.Tags != nil {
		rec.Tags = payload.Tags
	}
	if payload.ContentType != "" {
		rec.ContentType = payload.ContentType
	}
	if payload.Attachments != nil {
		rec.Attachments = payload.Attachments
	}
	if op == OperationRevoke {
		rec.Hidden = true
	}
	return &rec
}

func (a *Aggregator) processAnswer(pin *aggregator.PinInscription, chain string, isMempool bool) error {
	op := operationOf(pin)
	payload, err := parseAnswer(pin)
	if err != nil {
		return err
	}

	var previous *AnswerRecord
	if op == OperationCreate {
		// A create without answerTo/content is not a legal answer; skip it
		// from the index rather than erroring the pipeline.
		if payload.AnswerTo == "" || payload.Content == "" {
			return nil
		}
		previous, err = a.loadAnswer(chain, pin.Id)
		if err != nil {
			return err
		}
		if previous != nil && !(!isMempool && previous.IsMempool) {
			return nil
		}
	} else {
		target := targetPinID(pin)
		if target == "" {
			return nil
		}
		locator, ok := a.lookupLocator(target)
		if !ok || locator.kind != "a" {
			return nil
		}
		previous, err = a.loadAnswer(locator.chainName, locator.sourcePinId)
		if err != nil || previous == nil {
			return err
		}
	}

	rec := a.answerRecordFromPin(pin, chain, payload, previous, isMempool, op)
	mergeConfirmedReplayWithPendingAnswerCurrent(previous, rec, pin.Id)

	// Resolve the question. An answer whose answerTo does not resolve stays
	// pending (no orphan answer lists); resolution prefers the pin map (any
	// version of the question, any chain — pin ids are chain-unique).
	locator, ok := a.lookupLocator(payload.AnswerTo)
	if ok && locator.kind == "q" {
		rec.QuestionChain = locator.chainName
		rec.QuestionPinId = locator.sourcePinId
	}

	// A confirmed replay replaces the mempool record whose relay timestamp
	// keyed the per-question and global time indexes; re-key both so no
	// surface ever serves the answer twice.
	if previous != nil && previous.CreatedAt != rec.CreatedAt {
		_ = a.store.Delete(Namespace, answerIndexKey(previous.QuestionChain, previous.QuestionPinId, previous.CreatedAt, rec.SourcePinId))
		_ = a.store.Delete(Namespace, answerTimeKey(previous.CreatedAt, rec.ChainName, rec.SourcePinId))
	}

	if err := a.saveAnswer(rec); err != nil {
		return err
	}
	if err := a.mapPin(pin.Id, recordLocator{kind: "a", chainName: rec.ChainName, sourcePinId: rec.SourcePinId}); err != nil {
		return err
	}

	if rec.QuestionPinId == "" {
		// Orphan answer: pending index only, excluded from every surface.
		_ = a.store.Delete(Namespace, answerTimeKey(rec.CreatedAt, rec.ChainName, rec.SourcePinId))
		return a.store.Set(Namespace, pendingKey(payload.AnswerTo, rec.ChainName, rec.SourcePinId), []byte{})
	}

	_ = a.store.Delete(Namespace, pendingKey(payload.AnswerTo, rec.ChainName, rec.SourcePinId))
	if err := a.store.Delete(Namespace, answerIndexKey(rec.QuestionChain, rec.QuestionPinId, rec.CreatedAt, rec.SourcePinId)); err != nil {
		return err
	}
	if !rec.Hidden {
		if err := a.store.Set(Namespace, answerIndexKey(rec.QuestionChain, rec.QuestionPinId, rec.CreatedAt, rec.SourcePinId), []byte{}); err != nil {
			return err
		}
	}
	if err := a.maintainAnswerTimeIndex(rec); err != nil {
		return err
	}
	return a.refreshQuestion(rec.QuestionChain, rec.QuestionPinId)
}

// maintainAnswerTimeIndex keeps the global visible-answer list (atime) in
// step with one answer record: the entry exists exactly when the answer is
// non-hidden, question-resolved, and its question is not hidden.
func (a *Aggregator) maintainAnswerTimeIndex(rec *AnswerRecord) error {
	key := answerTimeKey(rec.CreatedAt, rec.ChainName, rec.SourcePinId)
	if !a.answerGloballyVisible(rec) {
		return a.store.Delete(Namespace, key)
	}
	return a.store.Set(Namespace, key, []byte{})
}

// questionVisible reports whether a question record qualifies for the Q&A
// surfaces: not revoked, and its current title ends with a question mark
// (R7). A title without one is treated exactly like the empty-title rule.
// The predicate is derived, never stored, so a modify that restores the
// question mark restores visibility with it.
func questionVisible(rec *QuestionRecord) bool {
	return rec != nil && !rec.Hidden && titleEndsWithQuestionMark(rec.Title)
}

// answerGloballyVisible reports whether an answer may appear on the global
// answer surfaces (GET /api/qa/answers): resolved, non-hidden, and parented
// by a visible question.
func (a *Aggregator) answerGloballyVisible(rec *AnswerRecord) bool {
	if rec == nil || rec.Hidden || rec.QuestionPinId == "" {
		return false
	}
	question, err := a.loadQuestion(rec.QuestionChain, rec.QuestionPinId)
	return err == nil && questionVisible(question)
}

func (a *Aggregator) answerRecordFromPin(pin *aggregator.PinInscription, chain string, payload *answerPayload, previous *AnswerRecord, isMempool bool, op string) *AnswerRecord {
	ts := metawebdoc.NormalizeUnixSeconds(pin.Timestamp)
	if op == OperationCreate {
		return &AnswerRecord{
			SourcePinId:  pin.Id,
			CurrentPinId: pin.Id,
			ChainName:    chain,
			AnswerTo:     payload.AnswerTo,
			Content:      payload.Content,
			Summary:      summaryOf(payload.Content),
			Tags:         payload.Tags,
			ContentType:  payload.ContentType,
			Attachments:  payload.Attachments,
			Publisher:    identityFromPin(pin),
			Operation:    op,
			IsMempool:    isMempool,
			CreatedAt:    ts,
			UpdatedAt:    ts,
		}
	}
	rec := *previous
	rec.CurrentPinId = pin.Id
	rec.IsMempool = isMempool
	rec.Operation = op
	rec.UpdatedAt = ts
	if payload.AnswerTo != "" {
		rec.AnswerTo = payload.AnswerTo
	}
	if payload.Content != "" {
		rec.Content = payload.Content
		rec.Summary = summaryOf(payload.Content)
	}
	if payload.Tags != nil {
		rec.Tags = payload.Tags
	}
	if payload.ContentType != "" {
		rec.ContentType = payload.ContentType
	}
	if payload.Attachments != nil {
		rec.Attachments = payload.Attachments
	}
	if op == OperationRevoke {
		rec.Hidden = true
	}
	return &rec
}

func (a *Aggregator) processLike(pin *aggregator.PinInscription, chain string, isMempool bool) error {
	payload, err := parseLike(pin)
	if err != nil {
		return err
	}
	locator, ok := a.lookupLocator(payload.LikeTo)
	if !ok || !locator.isQATarget() {
		// Target is not a Q&A pin (simplebuzz targets belong to socialcontent;
		// likes on comment pins are out of scope per the v2 contract).
		return nil
	}
	actor := identityFromPin(pin).actorKey()
	if actor == "" {
		return nil
	}
	targetKey := likeStateKey(locator.sourcePinId, actor)
	// PebbleStore.Get returns ErrNotFound for absent keys; treat any Get
	// miss as "no state yet" (same convention as the other aggregators).
	raw, _ := a.store.Get(Namespace, targetKey)
	if raw != nil {
		var state likeState
		if err := unmarshalStrict(raw, &state); err != nil {
			return err
		}
		ts := metawebdoc.NormalizeUnixSeconds(pin.Timestamp)
		// Last state per publisher wins; a confirmed replay of the same pin
		// (equal timestamp) replaces the mempool state, an older pin never
		// regresses a newer state.
		if state.Timestamp > ts {
			return nil
		}
	}
	if err := a.store.Set(Namespace, targetKey, mustJSON(likeState{
		PinId:     pin.Id,
		IsLike:    payload.IsLike,
		Timestamp: metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
		IsMempool: isMempool,
	})); err != nil {
		return err
	}
	return a.refreshEngagement(locator)
}

// processComment indexes PayComment pins whose commentTo resolves to a Q&A
// pin (question or answer, any version) as full comment records. Comments on
// pins outside the Q&A index stay with socialcontent; comments on comments
// are out of scope (flat lists only). modify updates the body in place,
// revoke hides the comment (it leaves comment lists and commentCount).
func (a *Aggregator) processComment(pin *aggregator.PinInscription, chain string, isMempool bool) error {
	op := operationOf(pin)
	payload, err := parseComment(pin)
	if err != nil {
		return err
	}

	var previous *CommentRecord
	var target recordLocator
	if op == OperationCreate {
		locator, ok := a.lookupLocator(payload.CommentTo)
		if !ok || !locator.isQATarget() {
			return nil
		}
		target = locator
		previous, err = a.loadComment(chain, pin.Id)
		if err != nil {
			return err
		}
		if previous != nil && !(!isMempool && previous.IsMempool) {
			return nil // already indexed (or newer state already held)
		}
	} else {
		versionTarget := targetPinID(pin)
		if versionTarget == "" {
			return nil
		}
		locator, ok := a.lookupLocator(versionTarget)
		if !ok || locator.kind != "c" {
			return nil
		}
		previous, err = a.loadComment(locator.chainName, locator.sourcePinId)
		if err != nil || previous == nil {
			return err
		}
		target = recordLocator{kind: previous.TargetKind, chainName: previous.TargetChain, sourcePinId: previous.TargetPinId}
		// A modify that moves the comment to another Q&A pin re-targets it;
		// an unresolvable new target keeps the current attachment.
		if payload.CommentTo != "" && payload.CommentTo != previous.CommentTo {
			if relocated, ok := a.lookupLocator(payload.CommentTo); ok && relocated.isQATarget() {
				target = relocated
			}
		}
	}

	rec := a.commentRecordFromPin(pin, chain, payload, previous, isMempool, op, target)
	mergeConfirmedReplayWithPendingCommentCurrent(previous, rec, pin.Id)
	if err := a.saveComment(rec); err != nil {
		return err
	}
	if err := a.mapPin(pin.Id, recordLocator{kind: "c", chainName: rec.ChainName, sourcePinId: rec.SourcePinId}); err != nil {
		return err
	}

	// Re-key the per-target list when the pin moves (target change) or the
	// confirmed block time replaces the relay time it was first keyed under.
	if previous != nil && previous.CreatedAt != rec.CreatedAt {
		_ = a.store.Delete(Namespace, commentIndexKey(previous.TargetPinId, previous.CreatedAt, previous.SourcePinId))
	}
	if previous != nil && previous.TargetPinId != "" {
		_ = a.store.Delete(Namespace, commentIndexKey(previous.TargetPinId, rec.CreatedAt, previous.SourcePinId))
	}
	if !rec.Hidden && rec.TargetPinId != "" {
		// The ct value carries the comment pin's own chain (a comment may
		// live on a different chain than its target).
		if err := a.store.Set(Namespace, commentIndexKey(rec.TargetPinId, rec.CreatedAt, rec.SourcePinId), []byte(rec.ChainName)); err != nil {
			return err
		}
	}

	if err := a.refreshEngagement(target); err != nil {
		return err
	}
	if previous != nil && (previous.TargetPinId != rec.TargetPinId || previous.TargetKind != rec.TargetKind) {
		return a.refreshEngagement(recordLocator{kind: previous.TargetKind, chainName: previous.TargetChain, sourcePinId: previous.TargetPinId})
	}
	return nil
}

// commentRecordFromPin builds the record for a create, or the updated record
// for a modify/revoke, resolved against its Q&A target.
func (a *Aggregator) commentRecordFromPin(pin *aggregator.PinInscription, chain string, payload *commentPayload, previous *CommentRecord, isMempool bool, op string, target recordLocator) *CommentRecord {
	ts := metawebdoc.NormalizeUnixSeconds(pin.Timestamp)
	if op == OperationCreate {
		return &CommentRecord{
			SourcePinId:  pin.Id,
			CurrentPinId: pin.Id,
			ChainName:    chain,
			TargetKind:   target.kind,
			TargetChain:  target.chainName,
			TargetPinId:  target.sourcePinId,
			CommentTo:    payload.CommentTo,
			Content:      payload.Content,
			ContentType:  payload.ContentType,
			Publisher:    identityFromPin(pin),
			Operation:    op,
			IsMempool:    isMempool,
			CreatedAt:    ts,
			UpdatedAt:    ts,
		}
	}
	rec := *previous
	rec.CurrentPinId = pin.Id
	rec.IsMempool = isMempool
	rec.Operation = op
	rec.UpdatedAt = ts
	rec.TargetKind = target.kind
	rec.TargetChain = target.chainName
	rec.TargetPinId = target.sourcePinId
	if payload.CommentTo != "" {
		rec.CommentTo = payload.CommentTo
	}
	if payload.Content != "" {
		rec.Content = payload.Content
	}
	if payload.ContentType != "" {
		rec.ContentType = payload.ContentType
	}
	if op == OperationRevoke {
		rec.Hidden = true
	}
	return &rec
}

// refreshEngagement recomputes like/dislike/comment counts of the located
// record from the engagement indexes, then cascades into the question
// aggregate refresh (top answer / hot points may change).
func (a *Aggregator) refreshEngagement(locator recordLocator) error {
	likes, dislikes := 0, 0
	err := a.store.ScanPrefix(Namespace, likeStatePrefix(locator.sourcePinId), func(_, value []byte) error {
		var state likeState
		if err := unmarshalStrict(value, &state); err != nil {
			return nil
		}
		switch state.IsLike {
		case 1:
			likes++
		case -1:
			dislikes++
		}
		return nil
	})
	if err != nil {
		return err
	}
	comments := 0
	// commentCount is the number of non-hidden comment records targeting the
	// pin: the per-target list index carries exactly those.
	if err := a.store.ScanPrefix(Namespace, commentIndexPrefix(locator.sourcePinId), func(_, _ []byte) error {
		comments++
		return nil
	}); err != nil {
		return err
	}

	switch locator.kind {
	case "q":
		rec, err := a.loadQuestion(locator.chainName, locator.sourcePinId)
		if err != nil || rec == nil {
			return err
		}
		rec.LikeCount, rec.DislikeCount, rec.CommentCount = likes, dislikes, comments
		if err := a.saveQuestion(rec); err != nil {
			return err
		}
		return a.refreshQuestionAggregate(rec)
	case "a":
		rec, err := a.loadAnswer(locator.chainName, locator.sourcePinId)
		if err != nil || rec == nil {
			return err
		}
		rec.LikeCount, rec.DislikeCount, rec.CommentCount = likes, dislikes, comments
		if err := a.saveAnswer(rec); err != nil {
			return err
		}
		if rec.QuestionPinId != "" {
			return a.refreshQuestion(rec.QuestionChain, rec.QuestionPinId)
		}
		return nil
	default:
		return nil
	}
}

// refreshQuestion loads the question, recomputes engagement plus answer
// aggregates, saves, and refreshes the time index and search snapshot.
func (a *Aggregator) refreshQuestion(chainName, sourcePinId string) error {
	rec, err := a.loadQuestion(chainName, sourcePinId)
	if err != nil || rec == nil {
		return err
	}
	if err := a.refreshQuestionEngagementOnly(rec); err != nil {
		return err
	}
	return a.refreshQuestionAggregate(rec)
}

func (a *Aggregator) refreshQuestionEngagementOnly(rec *QuestionRecord) error {
	likes, dislikes := 0, 0
	err := a.store.ScanPrefix(Namespace, likeStatePrefix(rec.SourcePinId), func(_, value []byte) error {
		var state likeState
		if err := unmarshalStrict(value, &state); err != nil {
			return nil
		}
		switch state.IsLike {
		case 1:
			likes++
		case -1:
			dislikes++
		}
		return nil
	})
	if err != nil {
		return err
	}
	comments := 0
	if err := a.store.ScanPrefix(Namespace, commentIndexPrefix(rec.SourcePinId), func(_, _ []byte) error {
		comments++
		return nil
	}); err != nil {
		return err
	}
	rec.LikeCount, rec.DislikeCount, rec.CommentCount = likes, dislikes, comments
	return nil
}

// refreshQuestionAggregate recomputes answerCount / answersText / topAnswer /
// hotPoints from the answer index, writes the record, maintains the reverse
// time index, and folds the question into the warm search snapshot.
func (a *Aggregator) refreshQuestionAggregate(rec *QuestionRecord) error {
	answers := make([]*AnswerRecord, 0, rec.AnswerCount)
	prefix := answerIndexPrefix(rec.ChainName, rec.SourcePinId)
	if err := a.store.ScanPrefix(Namespace, prefix, func(key, _ []byte) error {
		parts := strings.SplitN(strings.TrimPrefix(string(key), string(prefix)), ":", 2)
		if len(parts) != 2 {
			return nil
		}
		answer, err := a.loadAnswer(rec.ChainName, parts[1])
		if err != nil {
			return err
		}
		if answer != nil && !answer.Hidden {
			answers = append(answers, answer)
		}
		return nil
	}); err != nil {
		return err
	}

	rec.AnswerCount = len(answers)
	rec.AnswersText = answersExcerpt(answers)
	rec.TopAnswer = topAnswerOf(answers)
	hotPoints := 2*len(answers) + rec.LikeCount + rec.CommentCount
	for _, answer := range answers {
		hotPoints += answer.LikeCount + answer.CommentCount
	}
	rec.HotPoints = hotPoints

	if err := a.saveQuestion(rec); err != nil {
		return err
	}
	if !questionVisible(rec) {
		_ = a.store.Delete(Namespace, questionTimeKey(rec.CreatedAt, rec.ChainName, rec.SourcePinId))
	} else if err := a.store.Set(Namespace, questionTimeKey(rec.CreatedAt, rec.ChainName, rec.SourcePinId), []byte{}); err != nil {
		return err
	}
	// Question visibility gates the global answer list too: a revoked or
	// de-indexed (R7 title without a question mark) question removes all its
	// answers from atime (and a visible one restores them).
	for _, answer := range answers {
		if err := a.maintainAnswerTimeIndexForQuestion(answer, !questionVisible(rec)); err != nil {
			return err
		}
	}
	a.refreshQuestionDoc(rec)
	return nil
}

// maintainAnswerTimeIndexForQuestion re-keys one answer's global-list entry
// under the parent question's visibility (answers scanned here are the
// non-hidden ones of the question's answer index).
func (a *Aggregator) maintainAnswerTimeIndexForQuestion(answer *AnswerRecord, questionExcluded bool) error {
	key := answerTimeKey(answer.CreatedAt, answer.ChainName, answer.SourcePinId)
	if questionExcluded {
		return a.store.Delete(Namespace, key)
	}
	return a.store.Set(Namespace, key, []byte{})
}

// answersExcerpt concatenates answer summaries (index order = newest first)
// into the search-only answers field, capped at 1024 runes.
func answersExcerpt(answers []*AnswerRecord) string {
	const capRunes = 1024
	var sb strings.Builder
	for _, answer := range answers {
		if answer.Summary == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(answer.Summary)
		if len([]rune(sb.String())) >= capRunes {
			break
		}
	}
	return metawebdoc.CapRunes(sb.String(), capRunes)
}

// topAnswerOf picks the highest-scored answer; ties break to the newer
// answer, then the smaller pin id — the R4 ordering rule.
func topAnswerOf(answers []*AnswerRecord) *TopAnswerInfo {
	var best *AnswerRecord
	for _, answer := range answers {
		if best == nil {
			best = answer
			continue
		}
		if answer.Score() > best.Score() {
			best = answer
			continue
		}
		if answer.Score() == best.Score() && answer.CreatedAt > best.CreatedAt {
			best = answer
			continue
		}
		if answer.Score() == best.Score() && answer.CreatedAt == best.CreatedAt && answer.SourcePinId < best.SourcePinId {
			best = answer
		}
	}
	if best == nil {
		return nil
	}
	return &TopAnswerInfo{
		PinId:        best.SourcePinId,
		Summary:      best.Summary,
		Publisher:    best.Publisher,
		CreatedAt:    best.CreatedAt,
		LikeCount:    best.LikeCount,
		DislikeCount: best.DislikeCount,
	}
}

// mergeConfirmedReplayWithPendingQuestionCurrent keeps a newer pending
// mempool version when an older confirmed pin replays (same scheme as
// publishedcontent.mergeConfirmedReplayWithPendingCurrent).
func mergeConfirmedReplayWithPendingQuestionCurrent(previous, candidate *QuestionRecord, confirmedPinID string) {
	if previous == nil || candidate == nil || candidate.IsMempool || !previous.IsMempool {
		return
	}
	if previous.CurrentPinId == "" || previous.CurrentPinId == previous.SourcePinId || previous.CurrentPinId == confirmedPinID {
		return
	}
	candidate.CurrentPinId = previous.CurrentPinId
	candidate.Title = previous.Title
	candidate.Content = previous.Content
	candidate.Summary = previous.Summary
	candidate.Tags = previous.Tags
	candidate.ContentType = previous.ContentType
	candidate.Attachments = previous.Attachments
	candidate.Operation = previous.Operation
	candidate.Hidden = previous.Hidden
	candidate.IsMempool = true
	candidate.UpdatedAt = previous.UpdatedAt
}

func mergeConfirmedReplayWithPendingAnswerCurrent(previous, candidate *AnswerRecord, confirmedPinID string) {
	if previous == nil || candidate == nil || candidate.IsMempool || !previous.IsMempool {
		return
	}
	if previous.CurrentPinId == "" || previous.CurrentPinId == previous.SourcePinId || previous.CurrentPinId == confirmedPinID {
		return
	}
	candidate.CurrentPinId = previous.CurrentPinId
	candidate.Content = previous.Content
	candidate.Summary = previous.Summary
	candidate.Tags = previous.Tags
	candidate.ContentType = previous.ContentType
	candidate.Attachments = previous.Attachments
	candidate.Operation = previous.Operation
	candidate.Hidden = previous.Hidden
	candidate.IsMempool = true
	candidate.UpdatedAt = previous.UpdatedAt
}

// mergeConfirmedReplayWithPendingCommentCurrent keeps a newer pending
// mempool version when an older confirmed pin replays (same scheme as the
// question/answer merges).
func mergeConfirmedReplayWithPendingCommentCurrent(previous, candidate *CommentRecord, confirmedPinID string) {
	if previous == nil || candidate == nil || candidate.IsMempool || !previous.IsMempool {
		return
	}
	if previous.CurrentPinId == "" || previous.CurrentPinId == previous.SourcePinId || previous.CurrentPinId == confirmedPinID {
		return
	}
	candidate.CurrentPinId = previous.CurrentPinId
	candidate.Content = previous.Content
	candidate.ContentType = previous.ContentType
	candidate.Operation = previous.Operation
	candidate.Hidden = previous.Hidden
	candidate.IsMempool = true
	candidate.UpdatedAt = previous.UpdatedAt
}

// summaryOf derives the wire summary: first ~200 runes of the
// markdown-stripped body.
func summaryOf(content string) string {
	return metawebdoc.CapRunes(metawebdoc.StripMarkdown(content), metawebdoc.SummaryMaxRunes)
}
