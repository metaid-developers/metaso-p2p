package qa

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Pebble key families of the qa namespace:
//
//	q:<chain>:<questionSourcePinId>                  question record (JSON)
//	a:<chain>:<answerSourcePinId>                    answer record (JSON)
//	pin:<pinId>                                      any-version pin → record locator
//	qa:<chain>:<questionSrc>:<invTs>:<answerSrc>     answers of a question, newest first
//	pending:<answerTo>:<chain>:<answerSrc>           answers awaiting their question
//	likest:<targetSrcPinId>:<actor>                  last like state per (target, actor)
//	cmt:<targetSrcPinId>:<commentPinId>              comment markers per target
//	qtime:<invCreatedAt>:<chain>:<questionSrc>       visible questions, newest first
const (
	keyQuestion  = "q:"
	keyAnswer    = "a:"
	keyPinMap    = "pin:"
	keyAnswerIdx = "qa:"
	keyPending   = "pending:"
	keyLikeState = "likest:"
	keyComment   = "cmt:"
	keyTime      = "qtime:"
)

func questionKey(chainName, sourcePinId string) []byte {
	return []byte(keyQuestion + chainName + ":" + sourcePinId)
}

func answerKey(chainName, sourcePinId string) []byte {
	return []byte(keyAnswer + chainName + ":" + sourcePinId)
}

func pinMapKey(pinId string) []byte {
	return []byte(keyPinMap + pinId)
}

func answerIndexKey(chainName, questionSourcePinId string, createdAt int64, answerSourcePinId string) []byte {
	return []byte(keyAnswerIdx + chainName + ":" + questionSourcePinId + ":" + invertedTimestamp(createdAt) + ":" + answerSourcePinId)
}

func answerIndexPrefix(chainName, questionSourcePinId string) []byte {
	return []byte(keyAnswerIdx + chainName + ":" + questionSourcePinId + ":")
}

func pendingKey(answerTo, chainName, answerSourcePinId string) []byte {
	return []byte(keyPending + answerTo + ":" + chainName + ":" + answerSourcePinId)
}

func pendingPrefix(answerTo string) []byte {
	return []byte(keyPending + answerTo + ":")
}

func likeStateKey(targetSourcePinId, actor string) []byte {
	return []byte(keyLikeState + targetSourcePinId + ":" + actor)
}

func likeStatePrefix(targetSourcePinId string) []byte {
	return []byte(keyLikeState + targetSourcePinId + ":")
}

func commentKey(targetSourcePinId, commentPinId string) []byte {
	return []byte(keyComment + targetSourcePinId + ":" + commentPinId)
}

func commentPrefix(targetSourcePinId string) []byte {
	return []byte(keyComment + targetSourcePinId + ":")
}

func questionTimeKey(createdAt int64, chainName, sourcePinId string) []byte {
	return []byte(keyTime + invertedTimestamp(createdAt) + ":" + chainName + ":" + sourcePinId)
}

func questionTimePrefix() []byte {
	return []byte(keyTime)
}

// parseQuestionTimeKey extracts chainName and sourcePinId from a qtime key.
func parseQuestionTimeKey(key []byte) (chainName, sourcePinId string, ok bool) {
	rest := strings.TrimPrefix(string(key), keyTime)
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// invertedTimestamp renders ts as a big-endian hex complement so ascending key
// order is descending timestamp order (same scheme as publishedcontent).
func invertedTimestamp(ts int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^uint64(ts))
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
}

func (a *Aggregator) loadQuestion(chainName, sourcePinId string) (*QuestionRecord, error) {
	if chainName == "" || sourcePinId == "" {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, questionKey(chainName, sourcePinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec QuestionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("loadQuestion: corrupt record %s/%s: %w", chainName, sourcePinId, err)
	}
	return &rec, nil
}

func (a *Aggregator) loadAnswer(chainName, sourcePinId string) (*AnswerRecord, error) {
	if chainName == "" || sourcePinId == "" {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, answerKey(chainName, sourcePinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec AnswerRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("loadAnswer: corrupt record %s/%s: %w", chainName, sourcePinId, err)
	}
	return &rec, nil
}

// recordLocator is the value of the pin map: a record family plus its key.
type recordLocator struct {
	kind        string // "q" or "a"
	chainName   string
	sourcePinId string
}

func (l recordLocator) String() string {
	return l.kind + ":" + l.chainName + ":" + l.sourcePinId
}

func parseRecordLocator(raw []byte) (recordLocator, bool) {
	parts := strings.SplitN(strings.TrimSpace(string(raw)), ":", 3)
	if len(parts) != 3 || (parts[0] != "q" && parts[0] != "a") || parts[1] == "" || parts[2] == "" {
		return recordLocator{}, false
	}
	return recordLocator{kind: parts[0], chainName: parts[1], sourcePinId: parts[2]}, true
}

func (a *Aggregator) mapPin(pinId string, locator recordLocator) error {
	if pinId == "" || locator.sourcePinId == "" {
		return nil
	}
	return a.store.Set(Namespace, pinMapKey(pinId), []byte(locator.String()))
}

// lookupLocator resolves any pin id (source or any modify/revoke version) to
// its record locator via the global pin map, with a direct-record fallback.
func (a *Aggregator) lookupLocator(pinId string) (recordLocator, bool) {
	pinId = strings.TrimSpace(pinId)
	if pinId == "" {
		return recordLocator{}, false
	}
	if raw, err := a.store.Get(Namespace, pinMapKey(pinId)); err == nil && raw != nil {
		if locator, ok := parseRecordLocator(raw); ok {
			return locator, true
		}
	}
	for _, chainName := range lookupChains {
		if rec, err := a.loadQuestion(chainName, pinId); err == nil && rec != nil {
			return recordLocator{kind: "q", chainName: chainName, sourcePinId: pinId}, true
		}
		if rec, err := a.loadAnswer(chainName, pinId); err == nil && rec != nil {
			return recordLocator{kind: "a", chainName: chainName, sourcePinId: pinId}, true
		}
	}
	return recordLocator{}, false
}

func (a *Aggregator) saveQuestion(rec *QuestionRecord) error {
	if rec == nil {
		return errors.New("saveQuestion: nil record")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return a.store.Set(Namespace, questionKey(rec.ChainName, rec.SourcePinId), raw)
}

func (a *Aggregator) saveAnswer(rec *AnswerRecord) error {
	if rec == nil {
		return errors.New("saveAnswer: nil record")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return a.store.Set(Namespace, answerKey(rec.ChainName, rec.SourcePinId), raw)
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func unmarshalStrict(raw []byte, target any) error {
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("unmarshal %T: %w", target, err)
	}
	return nil
}
