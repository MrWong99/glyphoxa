package webrtc

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

// SignalingServer handles WebRTC signaling via HTTP endpoints.
// In production this would use WebSocket for real-time signaling;
// for the alpha, simple HTTP POST/DELETE endpoints suffice.
type SignalingServer struct {
	platform *Platform

	mu    sync.Mutex
	rooms map[string]*Connection
}

// NewSignalingServer creates a signaling server backed by the given platform.
func NewSignalingServer(platform *Platform) *SignalingServer {
	return &SignalingServer{
		platform: platform,
		rooms:    make(map[string]*Connection),
	}
}

// Handler returns an http.Handler that serves the signaling endpoints:
//
//	POST   /rooms/{roomID}/join    — peer sends SDP offer, gets SDP answer
//	POST   /rooms/{roomID}/ice     — peer sends ICE candidate
//	DELETE /rooms/{roomID}/leave   — peer disconnects
func (s *SignalingServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rooms/{roomID}/join", s.handleJoin)
	mux.HandleFunc("POST /rooms/{roomID}/ice", s.handleICE)
	mux.HandleFunc("DELETE /rooms/{roomID}/leave", s.handleLeave)
	return mux
}

// joinRequest is the JSON body for the join endpoint.
type joinRequest struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	SDPOffer string `json:"sdp_offer"`
}

// joinResponse is the JSON body returned from the join endpoint.
type joinResponse struct {
	SDPAnswer string `json:"sdp_answer"`
}

// handleJoin handles POST /rooms/{roomID}/join.
// The peer sends an SDP offer and receives a stub SDP answer.
func (s *SignalingServer) handleJoin(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomID")

	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	conn, err := s.getOrCreateRoom(r.Context(), roomID)
	if err != nil {
		http.Error(w, "failed to create room: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err = conn.AddPeer(req.UserID, req.Username); err != nil {
		http.Error(w, "failed to add peer: "+err.Error(), http.StatusConflict)
		return
	}

	// Retrieve the stub SDP answer from the mock transport.
	conn.mu.RLock()
	p, ok := conn.peers[req.UserID]
	conn.mu.RUnlock()

	var answer string
	if ok {
		answer, _ = p.transport.CreateOffer(r.Context())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(joinResponse{SDPAnswer: answer})
}

// iceRequest is the JSON body for the ICE candidate endpoint.
type iceRequest struct {
	UserID    string `json:"user_id"`
	Candidate string `json:"candidate"`
}

// handleICE handles POST /rooms/{roomID}/ice.
func (s *SignalingServer) handleICE(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomID")

	var req iceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	conn, ok := s.rooms[roomID]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	conn.mu.RLock()
	p, exists := conn.peers[req.UserID]
	conn.mu.RUnlock()
	if !exists {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}

	if err := p.transport.AddICECandidate(req.Candidate); err != nil {
		http.Error(w, "failed to add ICE candidate: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// leaveRequest is the JSON body for the leave endpoint.
type leaveRequest struct {
	UserID string `json:"user_id"`
}

// handleLeave handles DELETE /rooms/{roomID}/leave.
func (s *SignalingServer) handleLeave(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomID")

	var req leaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	conn, ok := s.rooms[roomID]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	if err := conn.RemovePeer(req.UserID); err != nil {
		http.Error(w, "failed to remove peer: "+err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// getOrCreateRoom returns an existing Connection for roomID, or creates one
// via the platform. Safe for concurrent use.
func (s *SignalingServer) getOrCreateRoom(ctx context.Context, roomID string) (*Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conn, ok := s.rooms[roomID]; ok {
		return conn, nil
	}

	raw, err := s.platform.Connect(ctx, roomID)
	if err != nil {
		return nil, err
	}
	conn := raw.(*Connection) //nolint:forcetypeassert // Platform.Connect always returns *Connection
	s.rooms[roomID] = conn
	return conn, nil
}
