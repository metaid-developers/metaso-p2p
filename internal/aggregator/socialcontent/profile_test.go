package socialcontent

import (
	"errors"
	"strings"
	"testing"
)

const aliceAddress = "17QEufa9n25HqWvwuqC7ucs7XTp4JnvMfo"
const aliceGlobalMetaId = "idq1gc67aqxy0x4lpp7ammsnvcxtyfl8kd3j49ldqe"

type fakeAuthorProfileLookup struct {
	byIdentity map[string]*AuthorProfileSnapshot
}

func (f *fakeAuthorProfileLookup) LookupLocalByIdentity(identity string) (*AuthorProfileSnapshot, error) {
	if f == nil || f.byIdentity == nil {
		return nil, nil
	}
	return f.byIdentity[strings.ToLower(strings.TrimSpace(identity))], nil
}

func TestFeedPostDetailReturnCanonicalAuthorWithName(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	agg.SetProfileLookup(&fakeAuthorProfileLookup{byIdentity: map[string]*AuthorProfileSnapshot{
		aliceGlobalMetaId: {Name: "Alice"},
	}})
	pin := testPin("buzz-alice:i0", PathSimpleBuzz, OperationCreate, "mvc", 200, []byte(`{"text":"hi Alice"}`))
	pin.GlobalMetaId = aliceAddress
	pin.MetaId = "meta-alice"
	pin.Address = aliceAddress
	pin.CreateMetaId = "meta-alice"
	pin.CreateAddress = aliceAddress
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("post: %v", err)
	}

	result, err := agg.List(FeedParams{Size: 10, ChainName: "mvc"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v", result.Items)
	}
	author := result.Items[0].Author
	if author.GlobalMetaId != aliceGlobalMetaId {
		t.Fatalf("author.globalMetaId = %q, want %q", author.GlobalMetaId, aliceGlobalMetaId)
	}
	if author.Name != "Alice" {
		t.Fatalf("author.name = %q, want Alice", author.Name)
	}
	if author.MetaId != "meta-alice" || author.Address != aliceAddress {
		t.Fatalf("metaId/address must be preserved: %+v", author)
	}

	detail, err := agg.FindPost("buzz-alice:i0", "mvc")
	if err != nil || detail == nil {
		t.Fatalf("FindPost: %v", err)
	}
	item := agg.postItemFromRecord(detail)
	if item.Author.GlobalMetaId != aliceGlobalMetaId || item.Author.Name != "Alice" {
		t.Fatalf("post detail author = %+v", item.Author)
	}
}

func TestFeedAuthorOmitsNameWithoutProfile(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	agg.SetProfileLookup(&fakeAuthorProfileLookup{byIdentity: map[string]*AuthorProfileSnapshot{}})
	pin := testPin("buzz-anon:i0", PathSimpleBuzz, OperationCreate, "mvc", 300, []byte(`{"text":"anon"}`))
	pin.GlobalMetaId = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	pin.MetaId = "meta-anon"
	pin.Address = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	pin.CreateMetaId = "meta-anon"
	pin.CreateAddress = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("post: %v", err)
	}
	result, err := agg.List(FeedParams{Size: 10, ChainName: "mvc"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	author := result.Items[0].Author
	if !strings.HasPrefix(author.GlobalMetaId, "idq1") {
		t.Fatalf("globalMetaId = %q, want idq1 prefix", author.GlobalMetaId)
	}
	if author.Name != "" {
		t.Fatalf("author.name = %q, want empty when no profile", author.Name)
	}
}

// TestLegacyPostRecordCanonicalizedAtQueryTime simulates rows indexed before
// identity canonicalization (AuthorGlobalMetaId stored as the bare address)
// and verifies the response assembly normalizes them without a backfill.
func TestLegacyPostRecordCanonicalizedAtQueryTime(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	agg.SetProfileLookup(&fakeAuthorProfileLookup{byIdentity: map[string]*AuthorProfileSnapshot{
		aliceGlobalMetaId: {Name: "Alice"},
	}})
	legacy := &PostRecord{
		SourcePinId:        "legacy-post:i0",
		CurrentPinId:       "legacy-post:i0",
		ChainName:          "mvc",
		ProtocolPath:       PathSimpleBuzz,
		AuthorGlobalMetaId: aliceAddress, // legacy: address stored as globalMetaId
		AuthorMetaId:       "meta-alice",
		AuthorAddress:      aliceAddress,
		ContentType:        "application/json",
		PayloadText:        "legacy content",
		CreatedAt:          400,
		UpdatedAt:          400,
	}
	if err := agg.saveRecord(postRecordKey("mvc", legacy.SourcePinId), legacy); err != nil {
		t.Fatalf("save legacy post: %v", err)
	}
	if err := agg.writePostIndexes(legacy); err != nil {
		t.Fatalf("write legacy indexes: %v", err)
	}

	result, err := agg.List(FeedParams{Size: 10, ChainName: "mvc"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v", result.Items)
	}
	author := result.Items[0].Author
	if author.GlobalMetaId != aliceGlobalMetaId {
		t.Fatalf("legacy author.globalMetaId = %q, want %q", author.GlobalMetaId, aliceGlobalMetaId)
	}
	if author.Name != "Alice" {
		t.Fatalf("legacy author.name = %q, want Alice", author.Name)
	}
	if author.Address != aliceAddress || author.MetaId != "meta-alice" {
		t.Fatalf("legacy metaId/address must be preserved: %+v", author)
	}
}

func TestCommentsReturnAuthorNameAndCanonicalGlobalMetaId(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	agg.SetProfileLookup(&fakeAuthorProfileLookup{byIdentity: map[string]*AuthorProfileSnapshot{
		aliceGlobalMetaId: {Name: "Alice"},
	}})
	target := testPin("buzz-target:i0", PathSimpleBuzz, OperationCreate, "mvc", 500, []byte(`{"text":"target"}`))
	if _, err := agg.HandleBlockPin(target); err != nil {
		t.Fatalf("target post: %v", err)
	}

	// Insert a comment directly with a legacy authorGlobalMetaId (bare
	// address) to prove the query-time normalization path for comments.
	comment := &CommentRecord{
		PinId:              "comment-legacy:i0",
		ChainName:          "mvc",
		TargetPinId:        "buzz-target:i0",
		AuthorGlobalMetaId: aliceAddress,
		AuthorMetaId:       "meta-alice",
		AuthorAddress:      aliceAddress,
		Content:            "nice post",
		ContentType:        "text/plain",
		Timestamp:          600,
	}
	if err := agg.saveRecord(commentRecordKey("mvc", comment.PinId), comment); err != nil {
		t.Fatalf("save comment: %v", err)
	}
	if err := agg.setStore(Namespace, commentTargetKey("mvc", "buzz-target:i0", comment.Timestamp, comment.PinId), []byte(comment.PinId)); err != nil {
		t.Fatalf("index comment: %v", err)
	}

	result, err := agg.ListComments(CommentParams{PinId: "buzz-target:i0", ChainName: "mvc", Size: 10})
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("comments = %+v", result.Items)
	}
	got := result.Items[0]
	if got.AuthorGlobalMetaId != aliceGlobalMetaId {
		t.Fatalf("authorGlobalMetaId = %q, want %q", got.AuthorGlobalMetaId, aliceGlobalMetaId)
	}
	if got.AuthorName != "Alice" {
		t.Fatalf("authorName = %q, want Alice", got.AuthorName)
	}
	if got.AuthorMetaId != "meta-alice" || got.AuthorAddress != aliceAddress {
		t.Fatalf("metaId/address must be preserved: %+v", got)
	}
}

// TestEnrichAuthorDegradesOnLookupError verifies a profile lookup failure
// never fails the feed: the item keeps its canonical identity and no name.
func TestEnrichAuthorDegradesOnLookupError(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	agg.profileLookup = errProfileLookup{}
	pin := testPin("buzz-err:i0", PathSimpleBuzz, OperationCreate, "mvc", 700, []byte(`{"text":"err"}`))
	pin.GlobalMetaId = aliceAddress
	pin.MetaId = "meta-alice"
	pin.Address = aliceAddress
	pin.CreateMetaId = "meta-alice"
	pin.CreateAddress = aliceAddress
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("post: %v", err)
	}
	result, err := agg.List(FeedParams{Size: 10, ChainName: "mvc"})
	if err != nil {
		t.Fatalf("List must not fail on profile lookup error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v", result.Items)
	}
	if result.Items[0].Author.GlobalMetaId != aliceGlobalMetaId {
		t.Fatalf("globalMetaId = %q, want %q", result.Items[0].Author.GlobalMetaId, aliceGlobalMetaId)
	}
	if result.Items[0].Author.Name != "" {
		t.Fatalf("name = %q, want empty on lookup error", result.Items[0].Author.Name)
	}
}

type errProfileLookup struct{}

func (errProfileLookup) LookupLocalByIdentity(string) (*AuthorProfileSnapshot, error) {
	return nil, errors.New("lookup failed")
}
