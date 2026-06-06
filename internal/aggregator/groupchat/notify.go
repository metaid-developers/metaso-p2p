package groupchat

import (
	"log"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// sendNotifyEvent sends a notify event on the aggregator's channel if the channel is not full.
// Returns true if the event was delivered, false if the channel was full (event dropped).
func (a *Aggregator) sendNotifyEvent(event *aggregator.NotifyEvent) bool {
	if event == nil {
		return false
	}

	select {
	case a.notifyCh <- event:
		return true
	default:
		// Channel full, drop event (non-blocking)
		log.Printf("[groupchat] notify channel full, dropping event type=%s", event.Type)
		return false
	}
}

// notifyGroupChat sends a group chat notification to connected clients.
func (a *Aggregator) notifyGroupChat(groupId string, chatMsg interface{}) {
	a.sendNotifyEvent(&aggregator.NotifyEvent{
		Type:    "WS_SERVER_NOTIFY_GROUP_CHAT",
		GroupId: groupId,
		Payload: chatMsg,
	})
}

// notifyGroupRole sends a role change notification.
func (a *Aggregator) notifyGroupRole(metaId, groupId string, roleInfo interface{}) {
	a.sendNotifyEvent(&aggregator.NotifyEvent{
		Type:    "WS_SERVER_NOTIFY_GROUP_ROLE",
		MetaId:  metaId,
		GroupId: groupId,
		Payload: roleInfo,
	})
}
