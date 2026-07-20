package userinfo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBackfillGlobalMetaIDPrefixResumesAndKeepsEarliestCreate(t *testing.T) {
	const (
		globalA = "idq1pgalt7f25cy4nfql2vy2mql7k4pz4nwjs8ggnv"
		globalB = "idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm"
	)

	var mu sync.Mutex
	var cursors []string
	failSecondPage := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Fatalf("path = %q, want /pin/path/list", r.URL.Path)
		}
		if r.URL.Query().Get("path") != "/" {
			t.Fatalf("query path = %q, want /", r.URL.Query().Get("path"))
		}
		cursor := r.URL.Query().Get("cursor")
		mu.Lock()
		cursors = append(cursors, cursor)
		shouldFail := cursor == "cursor-2" && failSecondPage
		if shouldFail {
			failSecondPage = false
		}
		mu.Unlock()
		if shouldFail {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch cursor {
		case "":
			_, _ = fmt.Fprintf(w, `{
                    "code":1,
                    "data":{
                      "list":[
                        {"id":"later:i0","operation":"create","path":"/","metaId":"meta-a","globalMetaId":"%s","timestamp":2000,"genesisHeight":-1,"chainName":"mvc"},
                        {"id":"modify:i0","operation":"modify","path":"/","metaId":"meta-b","globalMetaId":"%s","timestamp":1500,"genesisHeight":-1,"chainName":"mvc"},
                        {"id":"missing-time:i0","operation":"create","path":"/","metaId":"meta-b","globalMetaId":"%s","timestamp":0,"genesisHeight":-1,"chainName":"mvc"}
                      ],
                      "nextCursor":"cursor-2"
                    }
                  }`, globalA, globalB, globalB)
		case "cursor-2":
			_, _ = fmt.Fprintf(w, `{
                    "code":1,
                    "data":{
                      "list":[
                        {"id":"earlier:i0","operation":"create","path":"/","metaId":"meta-a","globalMetaId":"%s","timestamp":1000,"genesisHeight":-1,"chainName":"mvc"},
                        {"id":"root-b:i0","operation":"init","path":"/","metaId":"meta-b","globalMetaId":"%s","timestamp":1500,"genesisHeight":-1,"chainName":"mvc"}
                      ],
                      "nextCursor":""
                    }
                  }`, globalA, globalB)
		default:
			t.Fatalf("unexpected cursor %q", cursor)
		}
	}))
	defer server.Close()

	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	opts := GlobalMetaIDPrefixBackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		PageSize: 100,
	}

	summary, err := agg.BackfillGlobalMetaIDPrefix(opts)
	if err == nil {
		t.Fatal("first backfill returned nil error, want temporary MANAPI failure")
	}
	if summary.Status != globalMetaIDPrefixStateBuilding || summary.IndexedCount != 1 {
		t.Fatalf("first summary = %+v, want building with one indexed identity", summary)
	}
	state, err := agg.loadGlobalMetaIDPrefixIndexState()
	if err != nil {
		t.Fatalf("load state after failure: %v", err)
	}
	if state == nil || state.Cursor != "cursor-2" || state.Status != globalMetaIDPrefixStateBuilding {
		t.Fatalf("state after failure = %+v", state)
	}

	summary, err = agg.BackfillGlobalMetaIDPrefix(opts)
	if err != nil {
		t.Fatalf("resumed backfill: %v", err)
	}
	if summary.Status != globalMetaIDPrefixStateReady ||
		summary.IndexedCount != 2 ||
		summary.ReplacedCount != 1 ||
		summary.InvalidCount != 1 ||
		summary.MissingTimestampCount != 1 {
		t.Fatalf("completed summary = %+v", summary)
	}
	record, err := loadGlobalMetaIDCreationRecord(store.GetDB(namespace), globalMetaIDCreationKey(globalA))
	if err != nil {
		t.Fatalf("load global A creation record: %v", err)
	}
	if record == nil || record.PinID != "earlier:i0" || record.CreatedAt != 1000*1000 {
		t.Fatalf("global A record = %+v, want earlier root", record)
	}
	got, err := agg.lookupGlobalMetaIDPrefix(globalA[:8])
	if err != nil || got != globalA {
		t.Fatalf("lookup prefix = %q, %v; want %q", got, err, globalA)
	}

	mu.Lock()
	callsBeforeReadyRerun := len(cursors)
	mu.Unlock()
	summary, err = agg.BackfillGlobalMetaIDPrefix(opts)
	if err != nil || summary.Status != globalMetaIDPrefixStateReady {
		t.Fatalf("ready rerun = %+v, %v", summary, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cursors) != callsBeforeReadyRerun {
		t.Fatalf("ready rerun made another MANAPI request: cursors=%v", cursors)
	}
	if strings.Join(cursors, ",") != ",cursor-2,cursor-2" {
		t.Fatalf("cursor sequence = %v, want initial then failed/resumed cursor-2", cursors)
	}
}

func TestBackfillGlobalMetaIDPrefixRejectsRepeatedCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"data":{"list":[],"nextCursor":"same"}}`))
	}))
	defer server.Close()

	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	_, err := agg.BackfillGlobalMetaIDPrefix(GlobalMetaIDPrefixBackfillOptions{
		Client: NewBackfillClient(server.URL, server.Client()),
	})
	if err == nil || !strings.Contains(err.Error(), "repeated MANAPI cursor") {
		t.Fatalf("error = %v, want repeated cursor", err)
	}
}
