package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/neman/janus/internal/lock"
	raftpkg "github.com/neman/janus/internal/raft"
)

type Server struct {
	node *raftpkg.Node
	mux  *http.ServeMux
}

func New(node *raftpkg.Node) *Server {
	s := &Server{node: node, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/lease", s.createLease)
	s.mux.HandleFunc("POST /v1/lease/renew", s.renewLease)
	s.mux.HandleFunc("POST /v1/lock/acquire", s.acquireLock)
	s.mux.HandleFunc("POST /v1/lock/release", s.releaseLock)
	s.mux.HandleFunc("GET /v1/status", s.getStatus)
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) leaderCheck(w http.ResponseWriter) bool {
	if s.node.IsLeader() {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error":       "not leader",
		"leader_addr": s.node.LeaderAddr(),
	})
	return false
}

func (s *Server) createLease(w http.ResponseWriter, r *http.Request) {
	if !s.leaderCheck(w) {
		return
	}
	var req struct {
		OwnerID    string `json:"owner_id"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp, err := s.node.Apply(lock.Command{
		Type:    lock.CmdCreateLease,
		OwnerID: req.OwnerID,
		TTL:     time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result := resp.(*lock.CreateLeaseResponse)
	writeJSON(w, http.StatusOK, map[string]any{"lease_id": result.LeaseID})
}

func (s *Server) renewLease(w http.ResponseWriter, r *http.Request) {
	if !s.leaderCheck(w) {
		return
	}
	var req struct {
		LeaseID uint64 `json:"lease_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp, err := s.node.Apply(lock.Command{Type: lock.CmdRenewLease, LeaseID: req.LeaseID})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result := resp.(*lock.RenewLeaseResponse)
	writeJSON(w, http.StatusOK, map[string]any{"ttl_seconds": int64(result.TTL.Seconds())})
}

func (s *Server) acquireLock(w http.ResponseWriter, r *http.Request) {
	if !s.leaderCheck(w) {
		return
	}
	var req struct {
		LockName string `json:"lock_name"`
		OwnerID  string `json:"owner_id"`
		LeaseID  uint64 `json:"lease_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp, err := s.node.Apply(lock.Command{
		Type:     lock.CmdAcquireLock,
		LockName: req.LockName,
		OwnerID:  req.OwnerID,
		LeaseID:  req.LeaseID,
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	result := resp.(*lock.AcquireLockResponse)
	writeJSON(w, http.StatusOK, map[string]any{"fencing_token": result.FencingToken})
}

func (s *Server) releaseLock(w http.ResponseWriter, r *http.Request) {
	if !s.leaderCheck(w) {
		return
	}
	var req struct {
		LockName string `json:"lock_name"`
		LeaseID  uint64 `json:"lease_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp, err := s.node.Apply(lock.Command{Type: lock.CmdReleaseLock, LockName: req.LockName, LeaseID: req.LeaseID})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result := resp.(*lock.ReleaseLockResponse)
	writeJSON(w, http.StatusOK, map[string]any{"released": result.Released})
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	stats := s.node.FSM().Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":      s.node.NodeID(),
		"is_leader":    s.node.IsLeader(),
		"leader_addr":  s.node.LeaderAddr(),
		"state":        fmt.Sprintf("%s", s.node.State()),
		"cluster_size": s.node.ClusterSize(),
		"stats": map[string]any{
			"active_locks":   stats.ActiveLocks,
			"active_leases":  stats.ActiveLeases,
			"fencing_counter": stats.FencingCounter,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
