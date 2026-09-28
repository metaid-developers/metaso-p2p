package metatask

import (
	"context"
	"net/http"
	"strconv"
	"sync"
)

// HTTP serving of the four frozen §7 endpoints. Bodies are the frozen
// projection shapes verbatim (字段即契约 — no envelope, no extra keys);
// every response carries the §7 headers, and wall-clock/build stamps live
// only in headers, never in a body (D7).

// Snapshot is one completed collection+replay cycle.
type Snapshot struct {
	Raw      map[string][]*Pin
	Scope    *RunScope
	Boundary Boundary
	Enum     Enumeration
	Meta     ReplayMeta
	Index    TaskIndex
	Results  map[string]*ReplayResult
}

// Server keeps the latest snapshot and serves it over HTTP.
type Server struct {
	// BaseURL is the indexer base URL (manapi) for live refresh cycles.
	BaseURL string
	Size    int
	// Version stamps X-Metatask-Indexer-Build (deployment layer; -X main.version).
	Version string
	// ChainHead returns the current best chain height for the freshness
	// header. When nil the header falls back to the evaluated block — the C6
	// diff then reads zero, which is not self-certifying evidence, so a real
	// deployment must wire a chain source.
	ChainHead func() int64

	// Lister/Payloads override the live HTTP lister (tests inject fakes; a
	// frozen snapshot directory serves deterministically).
	Lister   Lister
	Payloads PayloadSource

	mu   sync.Mutex
	snap *Snapshot
}

func (s *Server) current() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

// Refresh runs one collection+replay cycle and atomically swaps the snapshot.
func (s *Server) Refresh(ctx context.Context) error {
	lister, payloads := s.Lister, s.Payloads
	if lister == nil {
		h := NewHTTPLister(s.BaseURL)
		lister, payloads = h, h
	}
	// CollectionPaths adds the roster reference pool (the tenth pool) to the
	// nine event paths; roster bodies arm the H_ACT2 same-side vote filter.
	events, enum, err := EnumerateAll(ctx, lister, CollectionPaths, s.Size)
	if err != nil {
		return err
	}
	for _, path := range CollectionPaths {
		LoadPayloads(ctx, events[path], payloads)
	}
	boundary := DeriveBoundary(events, 0)
	scope := ScopeEvents(events, boundary)
	results := map[string]*ReplayResult{}
	for _, root := range scope.TaskRoots() {
		res, err := Replay(events, root, boundary)
		if err != nil {
			return err
		}
		results[root] = res
	}
	scope.UnresolvedCount = SumUnresolved(results)
	meta := BuildReplayMeta(scope, enum, boundary)
	snap := &Snapshot{
		Raw: events, Scope: scope, Boundary: boundary, Enum: enum,
		Meta: meta, Index: BuildTaskIndex(results, scope, meta), Results: results,
	}
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
	return nil
}

// Handler returns the §7 endpoint mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metatask/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/metatask/tasks/{rootPinId}", s.handlePanorama)
	mux.HandleFunc("GET /api/metatask/tasks/{rootPinId}/nodes/{nodeId}", s.handleNode)
	mux.HandleFunc("GET /api/metatask/replay/{rootPinId}", s.handleReplay)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	return mux
}

// handleHealthz is the deployment-layer liveness probe (contract-external
// route): it stamps the build header so the ops runbook can verify the
// serving binary without touching a projection body.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Metatask-Indexer-Build", s.Version)
	w.WriteHeader(http.StatusOK)
	b, err := CanonicalJSON(map[string]any{"status": "ok", "version": s.Version})
	if err != nil {
		return
	}
	_, _ = w.Write(b)
}

func (s *Server) write(w http.ResponseWriter, status int, boundary Boundary, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	head := boundary.Height
	if s.ChainHead != nil {
		head = s.ChainHead()
	}
	w.Header().Set("X-Metatask-Chain-Head", strconv.FormatInt(head, 10))
	w.Header().Set("X-Metatask-Evaluated-Block", strconv.FormatInt(boundary.Height, 10))
	w.Header().Set("X-Metatask-Indexer-Build", s.Version)
	w.WriteHeader(status)
	b, err := CanonicalJSON(v)
	if err != nil {
		return
	}
	_, _ = w.Write(b)
}

// resolved carries the snapshot plus the request's evaluation boundary — the
// snapshot boundary when no to_block is given, else a fresh replay set at the
// requested height (F(E,B) is a pure function, so an on-demand recompute is
// byte-identical to a dedicated collection pass).
type resolved struct {
	snap     *Snapshot
	boundary Boundary
	scope    *RunScope
	results  map[string]*ReplayResult
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) (*resolved, bool) {
	snap := s.current()
	if snap == nil {
		s.write(w, http.StatusServiceUnavailable, Boundary{}, map[string]any{"error": "no completed collection cycle yet"})
		return nil, false
	}
	tb := r.URL.Query().Get("to_block")
	if tb == "" {
		return &resolved{snap: snap, boundary: snap.Boundary, scope: snap.Scope, results: snap.Results}, true
	}
	h, err := strconv.ParseInt(tb, 10, 64)
	if err != nil || h <= 0 {
		s.write(w, http.StatusBadRequest, snap.Boundary, map[string]any{"error": "invalid to_block"})
		return nil, false
	}
	if h == snap.Boundary.Height {
		return &resolved{snap: snap, boundary: snap.Boundary, scope: snap.Scope, results: snap.Results}, true
	}
	boundary := DeriveBoundary(snap.Raw, h)
	scope := ScopeEvents(snap.Raw, boundary)
	results := map[string]*ReplayResult{}
	for _, root := range scope.TaskRoots() {
		if res, err := Replay(snap.Raw, root, boundary); err == nil {
			results[root] = res
		}
	}
	scope.UnresolvedCount = SumUnresolved(results)
	return &resolved{snap: snap, boundary: boundary, scope: scope, results: results}, true
}

func (s *Server) metaFor(rs *resolved) ReplayMeta {
	if rs.boundary == rs.snap.Boundary {
		return rs.snap.Meta
	}
	// Same collection, different replay boundary: the enumeration report
	// (completeness) is a collection property and carries over.
	return BuildReplayMeta(rs.scope, rs.snap.Enum, rs.boundary)
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.resolve(w, r)
	if !ok {
		return
	}
	s.write(w, http.StatusOK, rs.boundary, BuildTaskIndex(rs.results, rs.scope, s.metaFor(rs)))
}

func (s *Server) handlePanorama(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.resolve(w, r)
	if !ok {
		return
	}
	res, known := rs.results[r.PathValue("rootPinId")]
	if !known {
		s.write(w, http.StatusNotFound, rs.boundary, map[string]any{
			"error": "unknown task root", "evaluatedAtBlock": rs.boundary.Height, "coverage": rs.snap.Meta.Coverage,
		})
		return
	}
	s.write(w, http.StatusOK, rs.boundary, BuildPanorama(res, rs.scope, s.metaFor(rs)))
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.resolve(w, r)
	if !ok {
		return
	}
	res, known := rs.results[r.PathValue("rootPinId")]
	if !known {
		s.write(w, http.StatusNotFound, rs.boundary, map[string]any{
			"error": "unknown task root", "evaluatedAtBlock": rs.boundary.Height, "coverage": rs.snap.Meta.Coverage,
		})
		return
	}
	ne, err := BuildNodeEvents(res, r.PathValue("nodeId"))
	if err != nil {
		s.write(w, http.StatusNotFound, rs.boundary, map[string]any{
			"error": "unknown node", "evaluatedAtBlock": rs.boundary.Height, "coverage": rs.snap.Meta.Coverage,
		})
		return
	}
	s.write(w, http.StatusOK, rs.boundary, ne)
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.resolve(w, r)
	if !ok {
		return
	}
	res, known := rs.results[r.PathValue("rootPinId")]
	if !known {
		s.write(w, http.StatusNotFound, rs.boundary, map[string]any{
			"error": "unknown task root", "evaluatedAtBlock": rs.boundary.Height, "coverage": rs.snap.Meta.Coverage,
		})
		return
	}
	s.write(w, http.StatusOK, rs.boundary, res)
}
