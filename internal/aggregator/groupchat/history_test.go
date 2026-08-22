package groupchat

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func seedHistoryGroupCreate(t *testing.T, agg *Aggregator, pinId, groupId, name, note, metaId, globalMetaId, address string, ts int64) {
	t.Helper()
	_, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id:            pinId,
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: address,
		CreateMetaId:  metaId,
		MetaId:        metaId,
		GlobalMetaId:  globalMetaId,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   groupId,
			GroupName: name,
			GroupNote: note,
		}),
	})
	if err != nil {
		t.Fatalf("seed group create %s: %v", groupId, err)
	}
}

func seedHistoryGroupJoin(t *testing.T, agg *Aggregator, pinId, groupId string, state float64, metaId, globalMetaId, address string, ts int64) {
	t.Helper()
	_, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id:            pinId,
		Path:          "/protocols/simplegroupjoin",
		Operation:     "create",
		CreateAddress: address,
		CreateMetaId:  metaId,
		MetaId:        metaId,
		GlobalMetaId:  globalMetaId,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody: mustMarshal(t, SimpleGroupJoin{
			GroupId: groupId,
			State:   state,
		}),
	})
	if err != nil {
		t.Fatalf("seed group join %s state=%v: %v", groupId, state, err)
	}
}

func TestGroupHistoryForIdentityChairAndMember(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	const (
		creatorMetaId  = "meta-creator"
		creatorGlobal  = "idq1-creator"
		creatorAddress = "M Creator Addr"
		memberMetaId   = "meta-member"
		memberGlobal   = "idq1-member"
		memberAddress  = "M Member Addr"
	)

	seedHistoryGroupCreate(t, agg, "create-g1:i0", "g1:i0", "合同审查协作", "审查合同条款", creatorMetaId, creatorGlobal, creatorAddress, 1000)
	seedHistoryGroupCreate(t, agg, "create-g2:i0", "g2:i0", "海报设计任务", "", creatorMetaId, creatorGlobal, creatorAddress, 2000)
	seedHistoryGroupJoin(t, agg, "join-g1:i0", "g1:i0", 1, memberMetaId, memberGlobal, memberAddress, 1500)
	seedHistoryGroupJoin(t, agg, "leave-g1:i0", "g1:i0", -1, memberMetaId, memberGlobal, memberAddress, 3000)
	seedHistoryGroupJoin(t, agg, "join-g2:i0", "g2:i0", 1, memberMetaId, memberGlobal, memberAddress, 2500)

	// Chair side: both created groups, newest first.
	chairHistory, err := agg.GroupHistoryForIdentity(creatorMetaId)
	if err != nil {
		t.Fatalf("GroupHistoryForIdentity(creator): %v", err)
	}
	if len(chairHistory) != 2 {
		t.Fatalf("chair history len = %d, want 2: %+v", len(chairHistory), chairHistory)
	}
	if chairHistory[0].GroupId != "g2:i0" || chairHistory[1].GroupId != "g1:i0" {
		t.Fatalf("chair history order = [%s %s], want [g2:i0 g1:i0]", chairHistory[0].GroupId, chairHistory[1].GroupId)
	}
	for _, item := range chairHistory {
		if item.JoinedAs != "chair" {
			t.Fatalf("group %s joinedAs = %q, want chair", item.GroupId, item.JoinedAs)
		}
		if !item.StillMember {
			t.Fatalf("group %s stillMember = false, want true", item.GroupId)
		}
	}
	if chairHistory[1].JoinPinId != "create-g1:i0" {
		t.Fatalf("chair joinPinId = %q, want create pin", chairHistory[1].JoinPinId)
	}
	if chairHistory[1].Title != "合同审查协作" || chairHistory[1].Goal != "审查合同条款" {
		t.Fatalf("g1 title/goal = %q/%q", chairHistory[1].Title, chairHistory[1].Goal)
	}

	// Member side via a different alias (globalMetaId): join history works from
	// any identity form, leave marks stillMember=false, and the join pin is the
	// anchor for members.
	memberHistory, err := agg.GroupHistoryForIdentity(memberGlobal)
	if err != nil {
		t.Fatalf("GroupHistoryForIdentity(member): %v", err)
	}
	if len(memberHistory) != 2 {
		t.Fatalf("member history len = %d, want 2: %+v", len(memberHistory), memberHistory)
	}
	if memberHistory[0].GroupId != "g2:i0" {
		t.Fatalf("member newest group = %q, want g2:i0 (joined at 2500)", memberHistory[0].GroupId)
	}
	if memberHistory[0].JoinedAs != "member" || memberHistory[0].JoinPinId != "join-g2:i0" || !memberHistory[0].StillMember {
		t.Fatalf("g2 member row = %+v", memberHistory[0])
	}
	left := memberHistory[1]
	if left.GroupId != "g1:i0" || left.JoinedAs != "member" || left.JoinPinId != "join-g1:i0" {
		t.Fatalf("g1 member row = %+v", left)
	}
	if left.StillMember {
		t.Fatalf("g1 stillMember = true after leave, want false")
	}
	if left.JoinedAt != 1500 {
		t.Fatalf("g1 joinedAt = %d, want join ts 1500", left.JoinedAt)
	}

	// Address alias resolves the same history.
	byAddress, err := agg.GroupHistoryForIdentity(memberAddress)
	if err != nil || len(byAddress) != 2 {
		t.Fatalf("history by address len = %d err=%v, want 2", len(byAddress), err)
	}

	// Unknown identity yields empty history, not an error.
	empty, err := agg.GroupHistoryForIdentity("idq1-nobody")
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown identity history = %+v err=%v, want empty", empty, err)
	}
}
