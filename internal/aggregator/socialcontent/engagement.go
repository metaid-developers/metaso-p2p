package socialcontent

import (
	"encoding/json"
	"strings"
	"time"
)

func (a *Aggregator) canonicalTarget(chain, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ErrInvalidParameter
	}
	post, err := a.FindPost(target, chain)
	if err != nil {
		return "", err
	}
	if post != nil && post.SourcePinId != "" {
		return post.SourcePinId, nil
	}
	return target, nil
}

func (a *Aggregator) recomputeCounters(chain, target string) error {
	source, err := a.canonicalTarget(chain, target)
	if err != nil {
		return err
	}
	post, err := a.loadPost(chain, source)
	if err != nil {
		return err
	}
	if post == nil {
		return nil
	}

	likeCount := 0
	if err := a.store.ScanPrefix(Namespace, likeStatePrefix(chain, source), func(_, value []byte) error {
		var event LikeEvent
		if err := json.Unmarshal(value, &event); err != nil {
			return err
		}
		if event.IsLike && !event.IsMempool {
			likeCount++
		}
		return nil
	}); err != nil {
		return err
	}

	commentCount := 0
	if err := a.store.ScanPrefix(Namespace, commentTargetPrefix(chain, source), func(_, value []byte) error {
		commentID := string(value)
		if commentID == "" {
			return nil
		}
		var comment CommentRecord
		if err := loadJSON(a.store, commentRecordKey(chain, commentID), &comment); err != nil {
			return err
		}
		if comment.PinId != "" && !comment.IsMempool {
			commentCount++
		}
		return nil
	}); err != nil {
		return err
	}

	post.LikeCount = likeCount
	post.CommentCount = commentCount
	post.HotScore = hotScore(post, time.Now().Unix())
	return saveJSON(a.store, postRecordKey(chain, source), post)
}

func (a *Aggregator) reconcilePendingInteractions(post *PostRecord) error {
	if post == nil || post.SourcePinId == "" || post.Hidden {
		return nil
	}
	chain := post.ChainName
	if err := a.store.ScanPrefix(Namespace, []byte(keyLikeEvent+chain+":"), func(key, value []byte) error {
		var event LikeEvent
		if err := json.Unmarshal(value, &event); err != nil {
			return err
		}
		if event.TargetPinId != post.SourcePinId && event.TargetPinId != post.CurrentPinId {
			return nil
		}
		oldTarget := event.TargetPinId
		event.TargetPinId = post.SourcePinId
		if err := saveJSON(a.store, key, &event); err != nil {
			return err
		}
		actor := firstIdentity(event.ActorGlobalMetaId, event.ActorMetaId, event.ActorAddress)
		if actor != "" {
			if oldTarget != event.TargetPinId {
				_ = a.store.Delete(Namespace, likeStateKey(chain, oldTarget, actor))
			}
			if err := saveJSON(a.store, likeStateKey(chain, event.TargetPinId, actor), &event); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := a.store.ScanPrefix(Namespace, []byte(keyCommentRecord+chain+":"), func(key, value []byte) error {
		var comment CommentRecord
		if err := json.Unmarshal(value, &comment); err != nil {
			return err
		}
		if comment.TargetPinId != post.SourcePinId && comment.TargetPinId != post.CurrentPinId {
			return nil
		}
		oldTarget := comment.TargetPinId
		comment.TargetPinId = post.SourcePinId
		if oldTarget != comment.TargetPinId {
			_ = a.store.Delete(Namespace, commentTargetKey(chain, oldTarget, comment.Timestamp, comment.PinId))
		}
		if err := saveJSON(a.store, key, &comment); err != nil {
			return err
		}
		return a.store.Set(Namespace, commentTargetKey(chain, comment.TargetPinId, comment.Timestamp, comment.PinId), []byte(comment.PinId))
	}); err != nil {
		return err
	}

	return a.recomputeCounters(chain, post.SourcePinId)
}
