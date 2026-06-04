package socket

import (
	"log"
	"sync"
	"time"

	sio "github.com/zishang520/socket.io/v2/socket"
)

// ConnType represents the type of client connection.
type ConnType string

const (
	ConnTypePC  ConnType = "pc"
	ConnTypeApp ConnType = "app"
)

// TrackedConnection wraps a Socket.IO socket with metadata for connection management.
type TrackedConnection struct {
	MetaId      string
	ConnType    ConnType
	Socket      *sio.Socket
	ConnectedAt time.Time
	LastPing    time.Time
}

// ConnectionManager tracks all active connections and enforces device limits.
type ConnectionManager struct {
	mu          sync.RWMutex
	connections map[string][]*TrackedConnection // keyed by metaid
	maxPC       int
	maxApp      int
}

// NewConnectionManager creates a new ConnectionManager.
func NewConnectionManager(maxPC, maxApp int) *ConnectionManager {
	return &ConnectionManager{
		connections: make(map[string][]*TrackedConnection),
		maxPC:       maxPC,
		maxApp:      maxApp,
	}
}

// Add registers a new connection. If adding would exceed the per-user device limit
// for the connection type, the oldest connection of that type is evicted: removed
// from the manager map and disconnected. The actual Socket.Disconnect call is made
// after releasing the manager lock, because the underlying socket.io library invokes
// the "disconnect" handler synchronously, which would otherwise re-enter Remove and
// deadlock on m.mu.
func (m *ConnectionManager) Add(metaId string, connType ConnType, sock *sio.Socket) *TrackedConnection {
	m.mu.Lock()

	tc := &TrackedConnection{
		MetaId:      metaId,
		ConnType:    connType,
		Socket:      sock,
		ConnectedAt: time.Now(),
		LastPing:    time.Now(),
	}

	conns := m.connections[metaId]

	limit := m.maxPC
	if connType == ConnTypeApp {
		limit = m.maxApp
	}

	var sameType []*TrackedConnection
	for _, c := range conns {
		if c.ConnType == connType {
			sameType = append(sameType, c)
		}
	}

	var toDisconnect *TrackedConnection
	if len(sameType) >= limit {
		oldest := sameType[0]
		for _, c := range sameType {
			if c.ConnectedAt.Before(oldest.ConnectedAt) {
				oldest = c
			}
		}
		// Remove from the map while still holding the lock so subsequent
		// state observers (stats / list / FindBySocket) cannot see the evicted
		// connection. The Disconnect side-effect runs after Unlock.
		m.removeLocked(metaId, oldest)
		toDisconnect = oldest
	}

	m.connections[metaId] = append(m.connections[metaId], tc)
	log.Printf("[socket] connection added: metaid=%s type=%s socket=%s total=%d",
		metaId, connType, sock.Id(), len(m.connections[metaId]))
	m.mu.Unlock()

	if toDisconnect != nil {
		log.Printf("[socket] device limit reached: metaid=%s type=%s, disconnecting oldest %s",
			metaId, connType, toDisconnect.Socket.Id())
		toDisconnect.Socket.Disconnect(true)
	}
	return tc
}

// Remove unregisters a connection.
func (m *ConnectionManager) Remove(metaId string, tc *TrackedConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(metaId, tc)
}

// removeLocked removes a connection without acquiring the lock.
func (m *ConnectionManager) removeLocked(metaId string, tc *TrackedConnection) {
	conns := m.connections[metaId]
	for i, c := range conns {
		if c == tc {
			m.connections[metaId] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(m.connections[metaId]) == 0 {
		delete(m.connections, metaId)
	}
	log.Printf("[socket] connection removed: metaid=%s type=%s socket=%s remaining=%d",
		metaId, tc.ConnType, tc.Socket.Id(), len(m.connections[metaId]))
}

// CountByType returns the number of connections of a given type for a metaid.
func (m *ConnectionManager) CountByType(metaId string, connType ConnType) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, c := range m.connections[metaId] {
		if c.ConnType == connType {
			count++
		}
	}
	return count
}

// FindBySocket finds a TrackedConnection by its underlying Socket.IO socket.
func (m *ConnectionManager) FindBySocket(sock *sio.Socket) *TrackedConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conns := range m.connections {
		for _, c := range conns {
			if c.Socket == sock {
				return c
			}
		}
	}
	return nil
}

// UpdatePing updates the last ping time for a connection.
func (m *ConnectionManager) UpdatePing(tc *TrackedConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tc.LastPing = time.Now()
}

// TotalConnections returns the total number of active connections.
func (m *ConnectionManager) TotalConnections() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, conns := range m.connections {
		total += len(conns)
	}
	return total
}

// OnlineList returns a paginated list of online connections.
func (m *ConnectionManager) OnlineList(page, size int) []OnlineEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var entries []OnlineEntry
	for _, conns := range m.connections {
		for _, c := range conns {
			entries = append(entries, OnlineEntry{
				MetaId:      c.MetaId,
				Type:        string(c.ConnType),
				ConnectedAt: c.ConnectedAt.UnixMilli(),
			})
		}
	}

	// Paginate
	start := (page - 1) * size
	if start < 0 {
		start = 0
	}
	if start >= len(entries) {
		return []OnlineEntry{}
	}
	end := start + size
	if end > len(entries) {
		end = len(entries)
	}
	return entries[start:end]
}

// FindStaleConnections returns connections that haven't sent a ping within the timeout.
func (m *ConnectionManager) FindStaleConnections(timeout time.Duration) []*TrackedConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var stale []*TrackedConnection
	for _, conns := range m.connections {
		for _, c := range conns {
			if now.Sub(c.LastPing) > timeout {
				stale = append(stale, c)
			}
		}
	}
	return stale
}

// DisconnectAll disconnects all tracked connections.
func (m *ConnectionManager) DisconnectAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for metaId, conns := range m.connections {
		for _, c := range conns {
			c.Socket.Disconnect(true)
		}
		delete(m.connections, metaId)
	}
	log.Printf("[socket] all connections disconnected")
}

// OnlineEntry represents an online connection for presence APIs.
type OnlineEntry struct {
	MetaId      string           `json:"metaid"`
	Type        string           `json:"type"`
	ConnectedAt int64            `json:"connectedAt"`
	UserInfo    *ProfileSnapshot `json:"userInfo,omitempty"`
}
