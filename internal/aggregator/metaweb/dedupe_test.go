package metaweb

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

func dedupeDoc(pinId, publisher, title, body string, createdAt int64) scoredDoc {
	return scoredDoc{
		doc: metawebdoc.Document{
			ProtocolKey:           "simplebuzz",
			SourcePinId:           pinId,
			CurrentPinId:          pinId,
			ChainName:             "mvc",
			Title:                 title,
			ContentExcerpt:        body,
			PublisherGlobalMetaId: publisher,
			CreatedAt:             createdAt,
		},
	}
}

func dedupePinIds(groups []groupedDoc) []string {
	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.rep.doc.SourcePinId)
	}
	return ids
}

func TestNormalizeDedupeText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello, World!", "helloworld"},
		{"一天一个新Skill 第21期 · taste-skill 配图", "一天一个新skill第21期tasteskill配图"},
		{"  spaces\tand\nnewlines  ", "spacesandnewlines"},
		{"「quotes」（brackets）【all】dropped", "quotesbracketsalldropped"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeDedupeText(c.in); got != c.want {
			t.Errorf("normalizeDedupeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVersionMarkerExtractAndStrip(t *testing.T) {
	cases := []struct {
		title    string
		marker   string
		stripped string
	}{
		{"一天一个新Skill 第22期 · lieflat-charts 亲测介绍文案（v2）", "v2", "一天一个新Skill 第22期 · lieflat-charts 亲测介绍文案（ ）"},
		{"「向 Sunny 团队学习」终版调研清单 v1.2（勘误版）", "v1.2+勘误版+终版", "「向 Sunny 团队学习」 调研清单  （ ）"},
		{"plain title without marker", "", "plain title without marker"},
		{"rev2 is not a marker", "", "rev2 is not a marker"},
		{"V3 Uppercase", "v3", "  Uppercase"},
	}
	for _, c := range cases {
		if got := extractVersionMarker(c.title); got != c.marker {
			t.Errorf("extractVersionMarker(%q) = %q, want %q", c.title, got, c.marker)
		}
		if got := stripVersionMarkers(c.title); got != c.stripped {
			t.Errorf("stripVersionMarkers(%q) = %q, want %q", c.title, got, c.stripped)
		}
	}
	// Marker-stripped titles of a deliberate version pair normalize equal.
	a := normalizeDedupeText(stripVersionMarkers("「向 Sunny 团队学习」终版调研清单 v1.2（勘误版）"))
	b := normalizeDedupeText(stripVersionMarkers("「向 Sunny 团队学习」终版调研清单 v1.1"))
	if a != b {
		t.Errorf("stripped titles differ: %q vs %q", a, b)
	}
}

// Evidence set A: byte-identical body, two create pins, same publisher.
func TestDedupe_IdenticalBodyCollapses(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-a1:i0", "idq1pub", "视频制作 经验", "完全相同的七十二字正文", 1785527226),
		dedupeDoc("pin-a2:i0", "idq1pub", "另一条标题", "完全相同的七十二字正文", 1785524234),
		dedupeDoc("pin-other:i0", "idq1pub", "别的内容", "不一样的正文", 1785525000),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].rep.doc.SourcePinId != "pin-a1:i0" {
		t.Errorf("representative = %s, want pin-a1:i0 (first in sorted order)", groups[0].rep.doc.SourcePinId)
	}
	if groups[0].duplicateCount != 1 {
		t.Errorf("duplicateCount = %d, want 1", groups[0].duplicateCount)
	}
	if groups[0].versions != nil {
		t.Errorf("versions = %v, want nil for hard collapse", groups[0].versions)
	}
	if groups[1].duplicateCount != 0 {
		t.Errorf("singleton duplicateCount = %d, want 0", groups[1].duplicateCount)
	}
}

// Evidence set B: identical title, same publisher, same createdAt second,
// three pins — bodies may differ slightly.
func TestDedupe_IdenticalTitleCollapses(t *testing.T) {
	title := "一天一个新Skill 第21期 · taste-skill 配图 Prompt 与视频脚本（供 eleven）"
	matches := []scoredDoc{
		dedupeDoc("pin-b1:i0", "idq1k8rd", title, "body one", 1788321974),
		dedupeDoc("pin-b2:i0", "idq1k8rd", title, "body two", 1788321974),
		dedupeDoc("pin-b3:i0", "idq1k8rd", title, "body three", 1788321974),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].rep.doc.SourcePinId != "pin-b1:i0" {
		t.Errorf("representative = %s, want pin-b1:i0", groups[0].rep.doc.SourcePinId)
	}
	if groups[0].duplicateCount != 2 {
		t.Errorf("duplicateCount = %d, want 2", groups[0].duplicateCount)
	}
}

// Evidence set C: near-identical titles differing only in punctuation /
// whitespace must normalize equal and collapse.
func TestDedupe_NearIdenticalTitleNormalizes(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-c1:i0", "idq1l7fz", "游戏创作类目落地研究——Roblox 范式 × 普通人入口", "body a", 100),
		dedupeDoc("pin-c2:i0", "idq1l7fz", "游戏创作类目落地研究 —— Roblox范式×普通人入口", "body b", 90),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 1 || groups[0].duplicateCount != 1 {
		t.Fatalf("groups = %+v, want one group with duplicateCount 1", dedupePinIds(groups))
	}
}

// Cross-publisher identical bodies are quote/citation reposts: never deduped.
func TestDedupe_CrossPublisherKept(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-x:i0", "idq1alice", "same title", "same body", 100),
		dedupeDoc("pin-y:i0", "idq1bob", "same title", "same body", 100),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (cross-publisher kept)", len(groups))
	}
}

// Evidence set D: explicit version markers form a versions group —
// representative is the newest, every member listed, nothing hard-collapsed.
func TestDedupe_VersionMarkersGroup(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-d-v2:i0", "idq1pub", "一天一个新Skill 第22期 · lieflat-charts 亲测介绍文案（v2）", "body v2", 200),
		dedupeDoc("pin-d-v1:i0", "idq1pub", "一天一个新Skill 第22期 · lieflat-charts 亲测介绍文案（v1）", "body v1", 100),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	g := groups[0]
	if g.rep.doc.SourcePinId != "pin-d-v2:i0" {
		t.Errorf("representative = %s, want pin-d-v2:i0 (newest)", g.rep.doc.SourcePinId)
	}
	if g.duplicateCount != 1 {
		t.Errorf("duplicateCount = %d, want 1", g.duplicateCount)
	}
	if len(g.versions) != 2 {
		t.Fatalf("versions = %d entries, want 2", len(g.versions))
	}
	if g.versions[0].PinId != "pin-d-v2:i0" || g.versions[0].Version != "v2" {
		t.Errorf("versions[0] = %+v, want pin-d-v2:i0 / v2", g.versions[0])
	}
	if g.versions[1].PinId != "pin-d-v1:i0" || g.versions[1].Version != "v1" {
		t.Errorf("versions[1] = %+v, want pin-d-v1:i0 / v1", g.versions[1])
	}
}

// Evidence set D second pair: v1.2（勘误版） vs v1.1 — the stripped base
// titles must still group despite the extra Chinese marker.
func TestDedupe_ChineseVersionMarkerGroups(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-d-old:i0", "idq1pub", "「向 Sunny 团队学习」终版调研清单 v1.1", "body old", 100),
		dedupeDoc("pin-d-new:i0", "idq1pub", "「向 Sunny 团队学习」终版调研清单 v1.2（勘误版）", "body new", 200),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	g := groups[0]
	if g.rep.doc.SourcePinId != "pin-d-new:i0" {
		t.Errorf("representative = %s, want pin-d-new:i0 (newest)", g.rep.doc.SourcePinId)
	}
	if len(g.versions) != 2 || g.versions[0].Version != "v1.2+勘误版+终版" || g.versions[1].Version != "v1.1+终版" {
		t.Errorf("versions = %+v, want [v1.2+勘误版+终版, v1.1+终版]", g.versions)
	}
}

// Transitive merge: X shares a body with Y, Y shares a title with Z — all
// three land in one group.
func TestDedupe_TransitiveMerge(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-x:i0", "idq1pub", "title one", "shared body", 300),
		dedupeDoc("pin-y:i0", "idq1pub", "title two", "shared body", 200),
		dedupeDoc("pin-z:i0", "idq1pub", "title two", "other body", 100),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 1 || groups[0].duplicateCount != 2 {
		t.Fatalf("groups = %+v, want one group with duplicateCount 2", dedupePinIds(groups))
	}
}

// A versions group sits at its earliest member's sorted position even when
// the newest member sorts later (e.g. a lower score under sort=relevance).
func TestDedupe_VersionsGroupKeepsSortedPosition(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-first:i0", "idq1a", "unrelated top hit", "unique a", 50),
		dedupeDoc("pin-v1:i0", "idq1pub", "some doc（v1）", "body one", 100),
		dedupeDoc("pin-mid:i0", "idq1b", "unrelated mid hit", "unique b", 60),
		// Newest member of the version pair, but sorts last by score.
		dedupeDoc("pin-v2:i0", "idq1pub", "some doc（v2）", "body two", 200),
	}
	groups := dedupeMatches(matches)
	want := []string{"pin-first:i0", "pin-v2:i0", "pin-mid:i0"}
	got := dedupePinIds(groups)
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v (versions group at earliest member position, newest as rep)", got, want)
		}
	}
}

// Documents without publisher identity never group with each other.
func TestDedupe_NoPublisherNeverGroups(t *testing.T) {
	matches := []scoredDoc{
		dedupeDoc("pin-n1:i0", "", "same title", "same body", 100),
		dedupeDoc("pin-n2:i0", "", "same title", "same body", 100),
	}
	groups := dedupeMatches(matches)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (publisherless docs never group)", len(groups))
	}
}
