package socialcontent

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	keyPostRecord    = "post:record:"
	keyPostPin       = "post:pin:"
	keyPostTime      = "post:time:"
	keyPostAuthor    = "post:author:"
	keyLikeEvent     = "like:event:"
	keyLikeState     = "like:state:"
	keyCommentRecord = "comment:record:"
	keyCommentTarget = "comment:target:"
	keyMempoolEvent  = "mempool:event:"
)

func postRecordKey(chain, source string) []byte {
	return []byte(keyPostRecord + strings.ToLower(chain) + ":" + source)
}

func postPinKey(chain, pinID string) []byte {
	return []byte(keyPostPin + strings.ToLower(chain) + ":" + pinID)
}

func postTimeKey(ts int64, chain, source string) []byte {
	return []byte(keyPostTime + invertedTimestamp(ts) + ":" + strings.ToLower(chain) + ":" + source)
}

func postTimePrefix() []byte { return []byte(keyPostTime) }

func parsePostTimeKey(key []byte) (chain, source string, ok bool) {
	rest := strings.TrimPrefix(string(key), keyPostTime)
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func postAuthorKey(identity string, ts int64, chain, source string) []byte {
	return []byte(keyPostAuthor + strings.ToLower(strings.TrimSpace(identity)) + ":" + invertedTimestamp(ts) + ":" + strings.ToLower(chain) + ":" + source)
}

func postAuthorPrefix(identity string) []byte {
	return []byte(keyPostAuthor + strings.ToLower(strings.TrimSpace(identity)) + ":")
}

func parsePostAuthorKey(key []byte) (chain, source string, ok bool) {
	rest := strings.TrimPrefix(string(key), keyPostAuthor)
	sep := strings.IndexByte(rest, ':')
	if sep <= 0 || sep+1 >= len(rest) {
		return "", "", false
	}
	parts := strings.SplitN(rest[sep+1:], ":", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func likeEventKey(chain, pinID string) []byte {
	return []byte(keyLikeEvent + strings.ToLower(chain) + ":" + pinID)
}

func likeStateKey(chain, target, actor string) []byte {
	return []byte(keyLikeState + strings.ToLower(chain) + ":" + target + ":" + strings.ToLower(actor))
}

func likeStatePrefix(chain, target string) []byte {
	return []byte(keyLikeState + strings.ToLower(chain) + ":" + target + ":")
}

func commentRecordKey(chain, pinID string) []byte {
	return []byte(keyCommentRecord + strings.ToLower(chain) + ":" + pinID)
}

func commentTargetKey(chain, target string, ts int64, pinID string) []byte {
	return []byte(keyCommentTarget + strings.ToLower(chain) + ":" + target + ":" + invertedTimestamp(ts) + ":" + pinID)
}

func commentTargetPrefix(chain, target string) []byte {
	return []byte(keyCommentTarget + strings.ToLower(chain) + ":" + target + ":")
}

func mempoolEventKey(chain, pinID string) []byte {
	return []byte(keyMempoolEvent + strings.ToLower(chain) + ":" + pinID)
}

func invertedTimestamp(ts int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^uint64(ts))
	return base64.RawURLEncoding.EncodeToString(buf)
}

func decodeInvertedTimestamp(encoded string) (int64, bool) {
	buf, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(buf) != 8 {
		return 0, false
	}
	return int64(^binary.BigEndian.Uint64(buf)), true
}

func marshalRecord(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal social record: %w", err)
	}
	return raw, nil
}

func loadJSON[T any](store *storage.PebbleStore, key []byte, out *T) error {
	raw, err := store.Get(Namespace, key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func encodeCursor(offset int) string {
	if offset <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("offset:%d", offset)))
}

func decodeCursor(cursor string) (int, error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil || !strings.HasPrefix(string(raw), "offset:") {
		return 0, ErrInvalidCursor
	}
	var offset int
	if _, err := fmt.Sscanf(string(raw), "offset:%d", &offset); err != nil || offset < 0 {
		return 0, ErrInvalidCursor
	}
	return offset, nil
}

func encodePostCursor(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	return "k:" + base64.RawURLEncoding.EncodeToString(key)
}

func decodePostCursor(cursor string) ([]byte, error) {
	if strings.TrimSpace(cursor) == "" {
		return nil, nil
	}
	raw := strings.TrimSpace(cursor)
	if !strings.HasPrefix(raw, "k:") {
		return nil, ErrInvalidCursor
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "k:"))
	if err != nil || len(key) == 0 {
		return nil, ErrInvalidCursor
	}
	return key, nil
}
