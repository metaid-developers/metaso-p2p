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

func TestPrivateChatDeliveryFansOutToEveryRecipientSocket(t *testing.T) {
	srv, router := newTestRouter(t)
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	firstSession := connectPollingSocket(t, client, httpServer.URL, "shared_recipient")
	secondSession := connectPollingSocket(t, client, httpServer.URL, "shared_recipient")
	if got := srv.manager.CountByType("shared_recipient", ConnTypeApp); got != 2 {
		t.Fatalf("tracked app connections = %d, want 2", got)
	}

	srv.routeNotifyEvent(&aggregator.NotifyEvent{
		Type:      "WS_SERVER_NOTIFY_PRIVATE_CHAT",
		PinId:     "fanout-pin-i0",
		TargetIds: []string{"shared_recipient"},
		Payload: map[string]string{
			"pinId": "fanout-pin-i0",
		},
	})

	for i, sessionURL := range []string{firstSession, secondSession} {
		wire := pollSocketMessage(t, client, sessionURL)
		if !strings.Contains(wire, `42["message"`) || !strings.Contains(wire, `"pinId":"fanout-pin-i0"`) {
			t.Fatalf("socket %d did not receive private push: %q", i+1, wire)
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
		t.Fatal("socket shutdown deadlocked with shared recipient sockets")
	}
}

func connectPollingSocket(t *testing.T, client *http.Client, serverURL, metaID string) string {
	t.Helper()
	base := serverURL + "/socket/socket.io/?EIO=4&transport=polling&metaid=" + url.QueryEscape(metaID) + "&type=app"
	resp, err := client.Get(base)
	if err != nil {
		t.Fatalf("handshake for %s: %v", metaID, err)
	}
	handshake, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(handshake) < 2 || handshake[0] != '0' {
		t.Fatalf("unexpected handshake for %s: %q", metaID, handshake)
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
		t.Fatalf("connect packet for %s: %v", metaID, err)
	}
	io.Copy(io.Discard, postResp.Body)
	postResp.Body.Close()

	connectResp, err := client.Get(sessionURL)
	if err != nil {
		t.Fatalf("connect ack for %s: %v", metaID, err)
	}
	connectAck, _ := io.ReadAll(connectResp.Body)
	connectResp.Body.Close()
	if !strings.Contains(string(connectAck), "40") {
		t.Fatalf("unexpected connect ack for %s: %q", metaID, connectAck)
	}
	return sessionURL
}

func pollSocketMessage(t *testing.T, client *http.Client, sessionURL string) string {
	t.Helper()
	resp, err := client.Get(sessionURL)
	if err != nil {
		t.Fatalf("message poll: %v", err)
	}
	wire, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(wire)
}
