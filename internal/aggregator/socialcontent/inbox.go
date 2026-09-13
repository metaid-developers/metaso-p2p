package socialcontent

// The socialcontent side of the interactions inbox (R3): owner-keyed indexes
// over the comments and like states that target simplebuzz posts published
// by a given identity. See internal/aggregator/qa/inbox.go for the qa half
// and docs/specs/2026-09-13-metaweb-surf-reads-api.md §3.
//
// Key families (socialcontent namespace):
//
//	cownb:<owner>:<invTs>:<chain>:<commentPinId>      comments on my posts
//	lownb:<owner>:<invTs>:<chain>:<target>:<actor>    like state on my posts (value: LikeEvent JSON)
//
// Unlike qa, this read model processes confirmed pins only (mempool events
// are parked until confirmation), so its inbox rows are always confirmed.

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/inbox"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

const (
	keyInboxCommentOwner = "cownb:"
	keyInboxLikeOwner    = "lownb:"
	keyInboxOwnerState   = "inbox_owner_index_state:v1"

	inboxExcerptRunes = 256
	inboxScanCap      = 2000
)

// errStopInboxScan ends a prefix scan early (below the since bound or over
// the per-family cap).
var errStopInboxScan = errors.New("stop inbox scan")

func commentOwnerKey(owner string, ts int64, chain, commentPinID string) []byte {
	return []byte(keyInboxCommentOwner + owner + ":" + invertedTimestamp(ts) + ":" + chain + ":" + commentPinID)
}

func likeOwnerKey(owner string, ts int64, chain, target, actor string) []byte {
	return []byte(keyInboxLikeOwner + owner + ":" + invertedTimestamp(ts) + ":" + chain + ":" + target + ":" + actor)
}

func inboxOwnerStateKey() []byte {
	return []byte(keyInboxOwnerState)
}

// postAuthorIdentities returns the owner identity candidates of a post.
func postAuthorIdentities(post *PostRecord) []string {
	return inbox.OwnerIdentities(post.AuthorGlobalMetaId, post.AuthorMetaId, post.AuthorAddress)
}

// maintainCommentOwnerIndex keys a comment under its (existing) target post's
// author identities. Comments on not-yet-indexed posts are keyed later by
// reconcilePendingInteractions.
func (a *Aggregator) maintainCommentOwnerIndex(chain string, comment *CommentRecord) error {
	if comment == nil || comment.TargetPinId == "" {
		return nil
	}
	post, err := a.loadPost(chain, comment.TargetPinId)
	if err != nil || post == nil {
		return err
	}
	ts := metawebdoc.NormalizeUnixSeconds(comment.Timestamp)
	for _, owner := range postAuthorIdentities(post) {
		if err := a.setStore(Namespace, commentOwnerKey(owner, ts, chain, comment.PinId), []byte(comment.PinId)); err != nil {
			return err
		}
	}
	return nil
}

// maintainLikeOwnerIndex keeps the like owner entries in step with the last
// state of one (chain, target, actor) pair: likes are keyed under the target
// post's author; un-likes leave no entry. previous carries the superseded
// state so its index keys can be cleared.
func (a *Aggregator) maintainLikeOwnerIndex(chain string, event *LikeEvent, actor string, previous *LikeEvent) error {
	if event == nil || actor == "" {
		return nil
	}
	post, err := a.loadPost(chain, event.TargetPinId)
	if err != nil || post == nil {
		return err
	}
	owners := postAuthorIdentities(post)
	newTs := metawebdoc.NormalizeUnixSeconds(event.Timestamp)
	if previous != nil {
		prevTs := metawebdoc.NormalizeUnixSeconds(previous.Timestamp)
		if prevTs != newTs {
			for _, owner := range owners {
				_ = a.deleteStore(Namespace, likeOwnerKey(owner, prevTs, chain, event.TargetPinId, actor))
			}
		}
	}
	raw, err := marshalRecord(event)
	if err != nil {
		return err
	}
	if event.IsLike {
		for _, owner := range owners {
			if err := a.setStore(Namespace, likeOwnerKey(owner, newTs, chain, event.TargetPinId, actor), raw); err != nil {
				return err
			}
		}
		return nil
	}
	// Un-like (or same-timestamp supersede of a like by an un-like).
	for _, owner := range owners {
		_ = a.deleteStore(Namespace, likeOwnerKey(owner, newTs, chain, event.TargetPinId, actor))
	}
	return nil
}

// ---------------------------------------------------------------------------
// one-time backfill
// ---------------------------------------------------------------------------

// ensureInboxOwnerIndexes backfills the owner indexes from the existing
// comment records and like states. State-key gated, run once per store.
func (a *Aggregator) ensureInboxOwnerIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}
	if _, err := a.store.Get(Namespace, inboxOwnerStateKey()); err == nil {
		return nil
	} else if !errors.Is(err, pebble.ErrNotFound) {
		return err
	}

	if err := a.store.ScanPrefix(Namespace, []byte(keyCommentRecord), func(_, value []byte) error {
		var comment CommentRecord
		if err := json.Unmarshal(value, &comment); err != nil {
			return nil
		}
		return a.maintainCommentOwnerIndex(comment.ChainName, &comment)
	}); err != nil {
		return err
	}

	if err := a.store.ScanPrefix(Namespace, []byte(keyLikeState), func(key, value []byte) error {
		rest := strings.TrimPrefix(string(key), keyLikeState)
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) != 3 || len(value) == 0 {
			return nil
		}
		chain, _, actor := parts[0], parts[1], parts[2]
		var event LikeEvent
		if err := json.Unmarshal(value, &event); err != nil {
			return nil
		}
		return a.maintainLikeOwnerIndex(chain, &event, actor, nil)
	}); err != nil {
		return err
	}

	return a.store.Set(Namespace, inboxOwnerStateKey(), []byte("done"))
}

// ---------------------------------------------------------------------------
// read path
// ---------------------------------------------------------------------------

// InboxHits returns the socialcontent interactions targeting posts owned by
// `owner`, filtered to the requested types, not older than sinceSec
// (inclusive) and strictly after the (afterTs, afterPinId) cursor in the
// contract order.
func (a *Aggregator) InboxHits(owner string, sinceSec int64, types map[string]bool, afterTs int64, afterPinId string, limit int) ([]inbox.Hit, error) {
	if limit <= 0 || limit > inboxScanCap {
		limit = inboxScanCap
	}
	hits := make([]inbox.Hit, 0, 64)
	for _, id := range inbox.OwnerIdentities(owner) {
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

func (a *Aggregator) scanInboxFamily(prefix string, sinceSec int64, afterTs int64, afterPinId string, limit int, hits *[]inbox.Hit, decode func(key, value []byte) (*inbox.Hit, bool)) error {
	if len(*hits) >= limit {
		return nil
	}
	scanErr := a.store.ScanPrefix(Namespace, []byte(prefix), func(key, value []byte) error {
		if len(*hits) >= limit {
			return errStopInboxScan
		}
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

func (a *Aggregator) commentHitFromKey(key, value []byte) (*inbox.Hit, bool) {
	rest := strings.TrimPrefix(string(key), keyInboxCommentOwner)
	// rest = <owner>:<invTs>:<chain>:<commentPinId>. Parsed from the left
	// with the pin id kept whole (pin ids are colon-free on chain, but test
	// conventions use colons, so nothing may split them).
	segments := strings.SplitN(rest, ":", 4)
	if len(segments) != 4 {
		return nil, false
	}
	chain, commentPinID := segments[2], segments[3]
	var comment CommentRecord
	if err := loadJSON(a.store, commentRecordKey(chain, commentPinID), &comment); err != nil || comment.PinId == "" {
		return nil, false
	}
	// Revoked target posts retire their comment rows.
	post, err := a.loadPost(chain, comment.TargetPinId)
	if err != nil || post == nil || post.Hidden {
		return nil, false
	}
	return &inbox.Hit{
		Type:              inbox.TypePayComment,
		PinId:             comment.PinId,
		ChainName:         comment.ChainName,
		TargetPinId:       comment.TargetPinId,
		ActorGlobalMetaId: comment.AuthorGlobalMetaId,
		ActorMetaId:       comment.AuthorMetaId,
		ActorAddress:      comment.AuthorAddress,
		CreatedAt:         metawebdoc.NormalizeUnixSeconds(comment.Timestamp),
		Excerpt:           metawebdoc.CapRunes(comment.Content, inboxExcerptRunes),
	}, true
}

func (a *Aggregator) likeHitFromEntry(key, value []byte) (*inbox.Hit, bool) {
	rest := strings.TrimPrefix(string(key), keyInboxLikeOwner)
	// rest = <owner>:<invTs>:<chain>:<target>:<actor>. Parsed from the RIGHT
	// (owner, ts, chain, actor fixed; the target keeps its colons whole).
	segments := strings.Split(rest, ":")
	if len(segments) < 5 || len(value) == 0 {
		return nil, false
	}
	chain, target := segments[2], strings.Join(segments[3:len(segments)-1], ":")
	var event LikeEvent
	if err := json.Unmarshal(value, &event); err != nil || !event.IsLike {
		return nil, false
	}
	post, err := a.loadPost(chain, target)
	if err != nil || post == nil || post.Hidden {
		return nil, false
	}
	return &inbox.Hit{
		Type:              inbox.TypePayLike,
		PinId:             event.PinId,
		ChainName:         event.ChainName,
		TargetPinId:       event.TargetPinId,
		ActorGlobalMetaId: event.ActorGlobalMetaId,
		ActorMetaId:       event.ActorMetaId,
		ActorAddress:      event.ActorAddress,
		CreatedAt:         metawebdoc.NormalizeUnixSeconds(event.Timestamp),
		Excerpt:           "like",
	}, true
}
