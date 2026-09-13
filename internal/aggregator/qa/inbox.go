package qa

// The qa side of the interactions inbox (R3): owner-keyed indexes over the
// answers / comments / like states that target pins published by a given
// identity, so "what happened to my pins since T" is one scan per family
// instead of N+1 probing. Read paths filter revoked/hidden records, so stale
// index entries are harmless.
//
// Key families (qa namespace):
//
//	qown:<owner>:<invTs>:<chain>:<answerSrc>        resolved answers of my questions
//	cownq:<owner>:<invTs>:<chain>:<commentSrc>      comments on my questions/answers
//	lownq:<owner>:<invTs>:<chain>:<target>:<actor>  like state on my pins (value: likeState JSON)
//
// `owner` is lowercase; one key per identity form (globalMetaId / metaId /
// address) of the target publisher. Index keys order newest-first by inverted
// createdAt seconds, the same scheme as the other qa indexes.

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/inbox"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

const (
	keyInboxAnswerOwner  = "qown:"
	keyInboxCommentOwner = "cownq:"
	keyInboxLikeOwner    = "lownq:"
	keyInboxOwnerState   = "inbox_owner_index_state:v1"

	inboxExcerptRunes = 256
	inboxScanCap      = 2000
)

// errStopInboxScan ends a prefix scan early (below the since bound or over
// the per-family cap).
var errStopInboxScan = errors.New("stop inbox scan")

func answerOwnerKey(owner string, ts int64, chainName, answerSourcePinId string) []byte {
	return []byte(keyInboxAnswerOwner + owner + ":" + invertedTimestamp(ts) + ":" + chainName + ":" + answerSourcePinId)
}

func commentOwnerKey(owner string, ts int64, chainName, commentSourcePinId string) []byte {
	return []byte(keyInboxCommentOwner + owner + ":" + invertedTimestamp(ts) + ":" + chainName + ":" + commentSourcePinId)
}

func likeOwnerKey(owner string, ts int64, chainName, targetSourcePinId, actor string) []byte {
	return []byte(keyInboxLikeOwner + owner + ":" + invertedTimestamp(ts) + ":" + chainName + ":" + targetSourcePinId + ":" + actor)
}

func inboxOwnerStateKey() []byte {
	return []byte(keyInboxOwnerState)
}

// ---------------------------------------------------------------------------
// index maintenance (called from the process paths)
// ---------------------------------------------------------------------------

// maintainAnswerOwnerIndex keys a resolved answer under its question's
// publisher identities. Pending (unresolved) answers have no owner yet; they
// are keyed when the question arrives (attachPendingAnswers).
func (a *Aggregator) maintainAnswerOwnerIndex(rec *AnswerRecord) error {
	if rec == nil || rec.QuestionPinId == "" {
		return nil
	}
	question, err := a.loadQuestion(rec.QuestionChain, rec.QuestionPinId)
	if err != nil || question == nil {
		return err
	}
	for _, owner := range inbox.OwnerIdentities(question.Publisher.GlobalMetaId, question.Publisher.MetaId, question.Publisher.Address) {
		if err := a.store.Set(Namespace, answerOwnerKey(owner, rec.CreatedAt, rec.ChainName, rec.SourcePinId), []byte{}); err != nil {
			return err
		}
	}
	return nil
}

// targetPublisher resolves the publisher of a comment/like target locator.
func (a *Aggregator) targetPublisher(locator recordLocator) (Identity, bool) {
	switch locator.kind {
	case "q":
		rec, err := a.loadQuestion(locator.chainName, locator.sourcePinId)
		if err != nil || rec == nil {
			return Identity{}, false
		}
		return rec.Publisher, true
	case "a":
		rec, err := a.loadAnswer(locator.chainName, locator.sourcePinId)
		if err != nil || rec == nil {
			return Identity{}, false
		}
		return rec.Publisher, true
	}
	return Identity{}, false
}

// maintainCommentOwnerIndex keys a comment under its target's publisher
// identities, clearing superseded entries when the pin is re-keyed (confirmed
// replay replaces the relay timestamp) or re-targeted (modify moves it).
func (a *Aggregator) maintainCommentOwnerIndex(rec *CommentRecord, previous *CommentRecord) error {
	if rec == nil || rec.TargetPinId == "" {
		return nil
	}
	locator := recordLocator{kind: rec.TargetKind, chainName: rec.TargetChain, sourcePinId: rec.TargetPinId}
	publisher, ok := a.targetPublisher(locator)
	if !ok {
		return nil
	}
	if previous != nil && (previous.CreatedAt != rec.CreatedAt || previous.TargetPinId != rec.TargetPinId || previous.TargetKind != rec.TargetKind) {
		oldLocator := recordLocator{kind: previous.TargetKind, chainName: previous.TargetChain, sourcePinId: previous.TargetPinId}
		if oldPublisher, ok := a.targetPublisher(oldLocator); ok {
			for _, owner := range inbox.OwnerIdentities(oldPublisher.GlobalMetaId, oldPublisher.MetaId, oldPublisher.Address) {
				_ = a.store.Delete(Namespace, commentOwnerKey(owner, previous.CreatedAt, previous.ChainName, previous.SourcePinId))
				_ = a.store.Delete(Namespace, commentOwnerKey(owner, rec.CreatedAt, previous.ChainName, previous.SourcePinId))
			}
		}
	}
	for _, owner := range inbox.OwnerIdentities(publisher.GlobalMetaId, publisher.MetaId, publisher.Address) {
		if err := a.store.Set(Namespace, commentOwnerKey(owner, rec.CreatedAt, rec.ChainName, rec.SourcePinId), []byte{}); err != nil {
			return err
		}
	}
	return nil
}

// maintainLikeOwnerIndex keeps the like owner entries in step with the last
// state of one (target, actor) pair: like/dislike states are keyed under the
// target owner; cancelled states (IsLike 0) leave no entry.
func (a *Aggregator) maintainLikeOwnerIndex(locator recordLocator, state likeState, previous *likeState) error {
	publisher, ok := a.targetPublisher(locator)
	if !ok {
		return nil
	}
	actor := state.ActorKey()
	if actor == "" {
		return nil
	}
	owners := inbox.OwnerIdentities(publisher.GlobalMetaId, publisher.MetaId, publisher.Address)
	if previous != nil && (previous.Timestamp != state.Timestamp || previous.ActorKey() != actor) {
		for _, owner := range owners {
			_ = a.store.Delete(Namespace, likeOwnerKey(owner, previous.Timestamp, locator.chainName, locator.sourcePinId, previous.ActorKey()))
		}
	}
	if state.IsLike == 0 {
		// Cancelled: the current timestamp carries no entry either.
		for _, owner := range owners {
			_ = a.store.Delete(Namespace, likeOwnerKey(owner, state.Timestamp, locator.chainName, locator.sourcePinId, actor))
		}
		return nil
	}
	raw := mustJSON(state)
	for _, owner := range owners {
		if err := a.store.Set(Namespace, likeOwnerKey(owner, state.Timestamp, locator.chainName, locator.sourcePinId, actor), raw); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// one-time backfill
// ---------------------------------------------------------------------------

// ensureInboxOwnerIndexes backfills the owner indexes from the existing qa
// record stores (answers, comments, like states). State-key gated and run
// once per store.
func (a *Aggregator) ensureInboxOwnerIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}
	if _, err := a.store.Get(Namespace, inboxOwnerStateKey()); err == nil {
		return nil
	} else if !errors.Is(err, pebble.ErrNotFound) {
		return err
	}

	if err := a.store.ScanPrefix(Namespace, []byte(keyAnswer), func(_, value []byte) error {
		var rec AnswerRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		return a.maintainAnswerOwnerIndex(&rec)
	}); err != nil {
		return err
	}

	if err := a.store.ScanPrefix(Namespace, []byte(keyCommentRec), func(_, value []byte) error {
		var rec CommentRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		return a.maintainCommentOwnerIndex(&rec, nil)
	}); err != nil {
		return err
	}

	if err := a.store.ScanPrefix(Namespace, []byte(keyLikeState), func(key, value []byte) error {
		rest := strings.TrimPrefix(string(key), keyLikeState)
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 || len(value) == 0 {
			return nil
		}
		targetSourcePinId := parts[0]
		locator, ok := a.lookupLocator(targetSourcePinId)
		if !ok || !locator.isQATarget() {
			return nil
		}
		var state likeState
		if err := json.Unmarshal(value, &state); err != nil {
			return nil
		}
		return a.maintainLikeOwnerIndex(locator, state, nil)
	}); err != nil {
		return err
	}

	return a.store.Set(Namespace, inboxOwnerStateKey(), []byte("done"))
}

// ---------------------------------------------------------------------------
// read path
// ---------------------------------------------------------------------------

// InboxHits returns the qa interactions targeting pins owned by `owner`
// (any identity form), filtered to the requested types, not older than
// sinceSec (inclusive) and strictly after the (afterTs, afterPinId) cursor
// in the contract order. The merged sort happens in the metaweb aggregator.
func (a *Aggregator) InboxHits(owner string, sinceSec int64, types map[string]bool, afterTs int64, afterPinId string, limit int) ([]inbox.Hit, error) {
	if limit <= 0 || limit > inboxScanCap {
		limit = inboxScanCap
	}
	hits := make([]inbox.Hit, 0, 64)
	for _, id := range inbox.OwnerIdentities(owner) {
		if types[inbox.TypeSimpleAnswer] {
			if err := a.scanInboxFamily(keyInboxAnswerOwner+id+":", sinceSec, afterTs, afterPinId, limit, &hits, a.answerHitFromKey); err != nil {
				return nil, err
			}
		}
		if types[inbox.TypePayComment] {
			if err := a.scanInboxFamily(keyInboxCommentOwner+id+":", sinceSec, afterTs, afterPinId, limit, &hits, a.commentHitFromKey); err != nil {
				return nil, err
			}
		}
		if types[inbox.TypePayLike] {
			if err := a.scanInboxFamily(keyInboxLikeOwner+id+":", sinceSec, afterTs, afterPinId, limit, &hits, a.likeHitFromEntry); err != nil {
				return nil, err
			}
		}
	}
	return hits, nil
}

// scanInboxFamily walks one family prefix (newest first by index key),
// applying the since and cursor bounds to each decoded hit.
func (a *Aggregator) scanInboxFamily(prefix string, sinceSec int64, afterTs int64, afterPinId string, limit int, hits *[]inbox.Hit, decode func(key, value []byte) (*inbox.Hit, bool)) error {
	if len(*hits) >= limit {
		return nil
	}
	scanErr := a.store.ScanPrefix(Namespace, []byte(prefix), func(key, value []byte) error {
		if len(*hits) >= limit {
			return errStopInboxScan
		}
		// ts comes from the key for the cheap since early-stop; the decoded
		// hit re-checks it (defensive) and applies the cursor filter, since
		// the key order inside one timestamp is not the contract order.
		head := strings.SplitN(strings.TrimPrefix(string(key), prefix), ":", 2)[0]
		ts, ok := decodeInvertedTimestamp(head)
		if !ok {
			return nil
		}
		if sinceSec > 0 && ts < sinceSec {
			return errStopInboxScan
		}
		hit, ok := decode(key, value)
		if !ok || hit == nil {
			return nil
		}
		if sinceSec > 0 && hit.CreatedAt < sinceSec {
			return nil
		}
		if afterTs > 0 && !hit.After(afterTs, afterPinId) {
			return nil
		}
		*hits = append(*hits, *hit)
		return nil
	})
	// The sentinel only ends this family's scan early; it is not an error.
	if scanErr != nil && scanErr != errStopInboxScan {
		return scanErr
	}
	return nil
}

func (a *Aggregator) answerHitFromKey(key, value []byte) (*inbox.Hit, bool) {
	rest := strings.TrimPrefix(string(key), keyInboxAnswerOwner)
	// rest = <owner>:<invTs>:<chain>:<answerSrc>
	segments := strings.SplitN(rest, ":", 4)
	if len(segments) != 4 {
		return nil, false
	}
	chainName, answerSourcePinId := segments[2], segments[3]
	rec, err := a.loadAnswer(chainName, answerSourcePinId)
	if err != nil || rec == nil || rec.Hidden || rec.QuestionPinId == "" {
		return nil, false
	}
	// The parent question must still exist (revoked questions retire their
	// answers' inbox rows; the R7 title rule does not apply here — the
	// answer happened).
	question, err := a.loadQuestion(rec.QuestionChain, rec.QuestionPinId)
	if err != nil || question == nil || question.Hidden {
		return nil, false
	}
	return &inbox.Hit{
		Type:              inbox.TypeSimpleAnswer,
		PinId:             rec.SourcePinId,
		ChainName:         rec.ChainName,
		TargetPinId:       rec.QuestionPinId,
		ActorGlobalMetaId: rec.Publisher.GlobalMetaId,
		ActorMetaId:       rec.Publisher.MetaId,
		ActorAddress:      rec.Publisher.Address,
		CreatedAt:         rec.CreatedAt,
		Excerpt:           metawebdoc.CapRunes(rec.Summary, inboxExcerptRunes),
		IsMempool:         rec.IsMempool,
	}, true
}

func (a *Aggregator) commentHitFromKey(key, value []byte) (*inbox.Hit, bool) {
	rest := strings.TrimPrefix(string(key), keyInboxCommentOwner)
	// rest = <owner>:<invTs>:<chain>:<commentSrc>
	segments := strings.SplitN(rest, ":", 4)
	if len(segments) != 4 {
		return nil, false
	}
	chainName, commentSourcePinId := segments[2], segments[3]
	rec, err := a.loadComment(chainName, commentSourcePinId)
	if err != nil || rec == nil || rec.Hidden || rec.TargetPinId == "" {
		return nil, false
	}
	switch rec.TargetKind {
	case "q":
		question, err := a.loadQuestion(rec.TargetChain, rec.TargetPinId)
		if err != nil || question == nil || question.Hidden {
			return nil, false
		}
	case "a":
		answer, err := a.loadAnswer(rec.TargetChain, rec.TargetPinId)
		if err != nil || answer == nil || answer.Hidden {
			return nil, false
		}
	default:
		return nil, false
	}
	return &inbox.Hit{
		Type:              inbox.TypePayComment,
		PinId:             rec.SourcePinId,
		ChainName:         rec.ChainName,
		TargetPinId:       rec.TargetPinId,
		ActorGlobalMetaId: rec.Publisher.GlobalMetaId,
		ActorMetaId:       rec.Publisher.MetaId,
		ActorAddress:      rec.Publisher.Address,
		CreatedAt:         rec.CreatedAt,
		Excerpt:           metawebdoc.CapRunes(rec.Content, inboxExcerptRunes),
		IsMempool:         rec.IsMempool,
	}, true
}

func (a *Aggregator) likeHitFromEntry(key, value []byte) (*inbox.Hit, bool) {
	rest := strings.TrimPrefix(string(key), keyInboxLikeOwner)
	// rest = <owner>:<invTs>:<chain>:<target>:<actor>
	segments := strings.SplitN(rest, ":", 5)
	if len(segments) != 5 || len(value) == 0 {
		return nil, false
	}
	chainName, targetSourcePinId := segments[2], segments[3]
	var state likeState
	if err := json.Unmarshal(value, &state); err != nil || state.IsLike == 0 {
		return nil, false
	}
	if q, err := a.loadQuestion(chainName, targetSourcePinId); err == nil && q != nil {
		if q.Hidden {
			return nil, false
		}
	} else if answer, err := a.loadAnswer(chainName, targetSourcePinId); err == nil && answer != nil {
		if answer.Hidden {
			return nil, false
		}
	} else {
		return nil, false
	}
	actor := actorIdentityFromKey(state, segments[4])
	return &inbox.Hit{
		Type:              inbox.TypePayLike,
		PinId:             state.PinId,
		ChainName:         chainName,
		TargetPinId:       targetSourcePinId,
		ActorGlobalMetaId: actor.GlobalMetaId,
		ActorMetaId:       actor.MetaId,
		ActorAddress:      actor.Address,
		CreatedAt:         state.Timestamp,
		Excerpt:           likeExcerpt(state.IsLike),
		IsMempool:         state.IsMempool,
		Dislike:           state.IsLike < 0,
	}, true
}

func likeExcerpt(isLike int) string {
	if isLike < 0 {
		return "dislike"
	}
	return "like"
}

// actorIdentityFromKey prefers the actor identity persisted on the state;
// legacy states only carry the canonical actor key, classified best-effort.
func actorIdentityFromKey(state likeState, actorKey string) Identity {
	if state.ActorGlobalMetaId != "" || state.ActorMetaId != "" || state.ActorAddress != "" {
		return Identity{GlobalMetaId: state.ActorGlobalMetaId, MetaId: state.ActorMetaId, Address: state.ActorAddress}
	}
	if strings.HasPrefix(actorKey, "idq1") {
		return Identity{GlobalMetaId: actorKey}
	}
	return Identity{Address: actorKey}
}
