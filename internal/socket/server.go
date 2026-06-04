package socket

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	sio "github.com/zishang520/socket.io/v2/socket"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/config"
	"github.com/metaid-developers/meta-socket/internal/presence"
)

// Server wraps the Socket.IO server with connection management and push capabilities.
type Server struct {
	ioServer            *sio.Server
	manager             *ConnectionManager
	cfg                 config.SocketConfig
	pushCh              chan *PushEnvelope
	stopCh              chan struct{}
	profileLookup       ProfileLookup
	profileAssetBaseURL string

	snapshotProviderMu sync.RWMutex
	snapshotProvider   presence.SnapshotProvider

	globalReaderMu sync.RWMutex
	globalReader   presence.GlobalReader
}

// PushEnvelope is the wire format for push messages, matching idchat's contract.
type PushEnvelope struct {
	M string `json:"M"`
	C int    `json:"C"`
	D any    `json:"D"`
}

// NewServer creates and configures a new Socket.IO server.
func NewServer(cfg config.SocketConfig) *Server {
	s := &Server{
		manager: NewConnectionManager(cfg.MaxPCPerUser, cfg.MaxAppPerUser),
		cfg:     cfg,
		pushCh:  make(chan *PushEnvelope, 1024),
		stopCh:  make(chan struct{}),
	}

	// Create the underlying Socket.IO server.
	opts := sio.DefaultServerOptions()
	opts.SetServeClient(false)
	opts.SetPath(cfg.PrimaryPath)
	opts.SetPingInterval(time.Duration(cfg.PingInterval))
	opts.SetPingTimeout(time.Duration(cfg.PingTimeout))
	opts.SetAllowEIO3(cfg.AllowEIO3)

	s.ioServer = sio.NewServer(nil, opts)
	s.ioServer.Of("/", nil).On("connection", s.onConnection)

	return s
}

// Handler returns the http.Handler for the Socket.IO server.
// This can be mounted on a Gin router via gin.WrapH.
func (s *Server) Handler() gin.HandlerFunc {
	handler := s.ioServer.ServeHandler(nil)
	return gin.WrapH(handler)
}

// SetSnapshotProvider configures the provider used by the well-known presence endpoint.
func (s *Server) SetSnapshotProvider(provider presence.SnapshotProvider) {
	s.snapshotProviderMu.Lock()
	defer s.snapshotProviderMu.Unlock()

	s.snapshotProvider = provider
}

func (s *Server) presenceSnapshotProvider() presence.SnapshotProvider {
	s.snapshotProviderMu.RLock()
	defer s.snapshotProviderMu.RUnlock()

	return s.snapshotProvider
}

// SetGlobalReader configures the reader used for global presence list and stats.
func (s *Server) SetGlobalReader(reader presence.GlobalReader) {
	s.globalReaderMu.Lock()
	defer s.globalReaderMu.Unlock()

	s.globalReader = reader
}

func (s *Server) presenceGlobalReader() presence.GlobalReader {
	s.globalReaderMu.RLock()
	defer s.globalReaderMu.RUnlock()

	return s.globalReader
}

// onConnection handles a new Socket.IO connection.
func (s *Server) onConnection(args ...any) {
	sock := args[0].(*sio.Socket)

	// Extract query parameters from the handshake.
	query := sock.Handshake().Query
	metaId := ""
	connType := ConnTypePC

	if vals, ok := query["metaid"]; ok && len(vals) > 0 {
		metaId = vals[0]
	}
	if vals, ok := query["type"]; ok && len(vals) > 0 {
		switch vals[0] {
		case "app":
			connType = ConnTypeApp
		case "pc":
			connType = ConnTypePC
		default:
			connType = ConnTypePC
		}
	}

	// Validate required parameters.
	if metaId == "" {
		log.Printf("[socket] connection rejected: missing metaid, socket=%s", sock.Id())
		sock.Emit("connect_error", "missing metaid parameter")
		sock.Disconnect(true)
		return
	}

	// Register the connection.
	tc := s.manager.Add(metaId, connType, sock)

	// Set up event handlers on the socket.
	sock.On("ping", func(args ...any) {
		s.manager.UpdatePing(tc)
		sock.Emit("heartbeat_ack")
	})

	sock.On("disconnect", func(args ...any) {
		s.manager.Remove(metaId, tc)
	})

	log.Printf("[socket] client connected: metaid=%s type=%s socket=%s", metaId, connType, sock.Id())
}

// StartTimeoutCleanup starts a background goroutine that disconnects clients
// that haven't sent a ping within the timeout period (35s per idchat spec).
func (s *Server) StartTimeoutCleanup() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				stale := s.manager.FindStaleConnections(35 * time.Second)
				for _, tc := range stale {
					log.Printf("[socket] heartbeat timeout: disconnecting metaid=%s socket=%s",
						tc.MetaId, tc.Socket.Id())
					tc.Socket.Disconnect(true)
				}
			case <-s.stopCh:
				return
			}
		}
	}()
}

// StartPushConsumer starts the goroutine that reads from aggregator NotifyChannels
// and pushes messages to connected clients.
func (s *Server) StartPushConsumer(registry *aggregator.Registry) {
	go s.consumeNotifyEvents(registry)
}

// consumeNotifyEvents reads from all aggregator NotifyChannels and routes messages.
func (s *Server) consumeNotifyEvents(registry *aggregator.Registry) {
	for _, agg := range registry.All() {
		go func(a aggregator.Aggregator) {
			for {
				select {
				case evt, ok := <-a.NotifyChannel():
					if !ok {
						return
					}
					s.routeNotifyEvent(evt)
				case <-s.stopCh:
					return
				}
			}
		}(agg)
	}

	// Keep goroutine alive until stopped.
	<-s.stopCh
}

// routeNotifyEvent dispatches a NotifyEvent to the appropriate target.
func (s *Server) routeNotifyEvent(evt *aggregator.NotifyEvent) {
	if evt == nil {
		return
	}

	envelope := &PushEnvelope{
		M: evt.Type,
		C: 0,
		D: evt.Payload,
	}

	switch evt.Type {
	case "WS_SERVER_NOTIFY_GROUP_CHAT":
		// Broadcast to room: group:<GroupId>
		if evt.GroupId != "" {
			s.BroadcastToRoom("group:"+evt.GroupId, envelope)
		}
	case "WS_SERVER_NOTIFY_PRIVATE_CHAT":
		// Send to all known user identity aliases.
		for _, targetId := range notifyEventTargetIds(evt) {
			s.SendToUser(targetId, envelope)
		}
	case "WS_SERVER_NOTIFY_GROUP_ROLE":
		// Send to user identity aliases AND broadcast to room.
		for _, targetId := range notifyEventTargetIds(evt) {
			s.SendToUser(targetId, envelope)
		}
		if evt.GroupId != "" {
			s.BroadcastToRoom("group:"+evt.GroupId, envelope)
		}
	default:
		log.Printf("[socket] unknown notify event type: %s", evt.Type)
	}
}

func notifyEventTargetIds(evt *aggregator.NotifyEvent) []string {
	if evt == nil {
		return nil
	}

	targets := make([]string, 0, len(evt.TargetIds)+2)
	seen := make(map[string]bool)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, id)
	}

	for _, id := range evt.TargetIds {
		add(id)
	}
	if len(targets) == 0 {
		add(evt.MetaId)
		add(evt.GlobalMetaId)
	}
	return targets
}

// SendToUser sends a push envelope to all connections of a given metaid.
func (s *Server) SendToUser(metaId string, msg *PushEnvelope) {
	// We need to access the internal connection manager to find sockets.
	// Since we don't expose the manager's internal state, we iterate through
	// all sockets on the default namespace and check their handshake query.
	s.ioServer.Of("/", nil).Sockets().Range(func(_ sio.SocketId, sock *sio.Socket) bool {
		query := sock.Handshake().Query
		if vals, ok := query["metaid"]; ok && len(vals) > 0 && vals[0] == metaId {
			sock.Emit("message", msg)
		}
		return true
	})
}

// SendToUsers sends a push envelope to all connections for each target metaid.
func (s *Server) SendToUsers(metaIds []string, msg *PushEnvelope) {
	for _, metaId := range metaIds {
		s.SendToUser(metaId, msg)
	}
}

// BroadcastToRoom sends a push envelope to all sockets in a room.
func (s *Server) BroadcastToRoom(room string, msg *PushEnvelope) {
	if s.cfg.RoomBroadcastEnabled {
		s.ioServer.Of("/", nil).To(sio.Room(room)).Emit("message", msg)
	}
}

// Shutdown gracefully shuts down the socket server.
func (s *Server) Shutdown() {
	log.Printf("[socket] shutting down socket server...")
	close(s.stopCh)
	s.manager.DisconnectAll()
	s.ioServer.Close(nil)
	log.Printf("[socket] socket server stopped")
}

// Manager returns the connection manager for presence queries.
func (s *Server) Manager() *ConnectionManager {
	return s.manager
}

// SetProfileLookup wires optional profile hydration for presence rows.
func (s *Server) SetProfileLookup(lookup ProfileLookup) {
	s.profileLookup = lookup
}

// SetProfileAssetBaseURL configures avatar URL expansion for presence profiles.
func (s *Server) SetProfileAssetBaseURL(baseURL string) {
	s.profileAssetBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// IOServer returns the underlying Socket.IO server.
func (s *Server) IOServer() *sio.Server {
	return s.ioServer
}

// PushChannel returns a channel that external callers can use to push messages directly.
// Messages sent to this channel are broadcast to the appropriate targets.
// The envelope M field acts as the routing key: if empty, it's a broadcast; otherwise,
// it's routed through the standard notify logic.
func (s *Server) PushChannel() chan<- *PushEnvelope {
	return s.pushCh
}
