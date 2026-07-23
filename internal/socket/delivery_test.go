package socket

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func TestPrivateChatPollingDeliveryThroughTrackedConnection(t *testing.T) {
	srv, router := newTestRouter(t)
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	base := httpServer.URL + "/socket/socket.io/?EIO=4&transport=polling&metaid=recipient_global&type=app"
	resp, err := client.Get(base)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	handshake, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(handshake) < 2 || handshake[0] != '0' {
		t.Fatalf("unexpected handshake: %q", handshake)
	}
	var open struct {
		SID string `json:"sid"`
	}
	if err := json.Unmarshal(handshake[1:], &open); err != nil || open.SID == "" {
		t.Fatalf("decode handshake %q: sid=%q err=%v", handshake, open.SID, err)
	}

	sessionURL := base + "&sid=" + url.QueryEscape(open.SID)
	postResp, err := client.Post(sessionURL, "text/plain;charset=UTF-8", strings.NewReader("40"))
	if err != nil {
		t.Fatalf("connect packet: %v", err)
	}
	io.Copy(io.Discard, postResp.Body)
	postResp.Body.Close()

	connectResp, err := client.Get(sessionURL)
	if err != nil {
		t.Fatalf("connect ack: %v", err)
	}
	connectAck, _ := io.ReadAll(connectResp.Body)
	connectResp.Body.Close()
	if !strings.Contains(string(connectAck), "40") {
		t.Fatalf("unexpected connect ack: %q", connectAck)
	}
	if got := srv.manager.CountByType("recipient_global", ConnTypeApp); got != 1 {
		t.Fatalf("tracked app connections = %d, want 1", got)
	}

	srv.routeNotifyEvent(&aggregator.NotifyEvent{
		Type:      "WS_SERVER_NOTIFY_PRIVATE_CHAT",
		PinId:     "delivery-pin-i0",
		TargetIds: []string{"recipient_global", "recipient_meta", "recipient_address"},
		Payload: map[string]string{
			"fromGlobalMetaId": "sender_global",
			"toGlobalMetaId":   "recipient_global",
			"pinId":            "delivery-pin-i0",
		},
	})

	messageResp, err := client.Get(sessionURL)
	if err != nil {
		t.Fatalf("message poll: %v", err)
	}
	wire, _ := io.ReadAll(messageResp.Body)
	messageResp.Body.Close()
	for _, fragment := range []string{
		`42["message"`,
		`"M":"WS_SERVER_NOTIFY_PRIVATE_CHAT"`,
		`"fromGlobalMetaId":"sender_global"`,
		`"toGlobalMetaId":"recipient_global"`,
		`"pinId":"delivery-pin-i0"`,
	} {
		if !strings.Contains(string(wire), fragment) {
			t.Fatalf("wire payload %q does not contain %q", wire, fragment)
		}
	}

	shutdownDone := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("socket shutdown deadlocked with an active tracked connection")
	}
}
