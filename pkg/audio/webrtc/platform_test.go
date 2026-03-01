package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

func newTestConnection(t *testing.T) *Connection {
	t.Helper()
	conn := newConnection("room-test", 48000, []string{"stun:stun.l.google.com:19302"})
	t.Cleanup(func() { _ = conn.Disconnect() })
	return conn
}

// waitEvent waits for an event on ch, failing the test if the timeout elapses.
func waitEvent(t *testing.T, ch <-chan audio.Event, d time.Duration) audio.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(d):
		t.Fatalf("timed out waiting for event after %v", d)
		return audio.Event{}
	}
}

// jsonBody encodes v as JSON and returns a *bytes.Buffer suitable for request bodies.
func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

// ─── Platform tests ───────────────────────────────────────────────────────────

// TestPlatform_Connect verifies that Connect returns a non-nil *Connection
// with the correct channelID.
func TestPlatform_Connect(t *testing.T) {
	t.Parallel()

	p := New()
	conn, err := p.Connect(context.Background(), "room-alpha")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn == nil {
		t.Fatal("Connect returned nil connection")
	}

	wc, ok := conn.(*Connection)
	if !ok {
		t.Fatalf("Connect returned %T, want *Connection", conn)
	}
	if wc.channelID != "room-alpha" {
		t.Errorf("channelID = %q, want %q", wc.channelID, "room-alpha")
	}
	if wc.sampleRate != 48000 {
		t.Errorf("sampleRate = %d, want 48000", wc.sampleRate)
	}

	if err = conn.Disconnect(); err != nil {
		t.Errorf("Disconnect: %v", err)
	}
}

// TestPlatform_MultipleRooms verifies that multiple concurrent Connect calls
// each produce an independent Connection.
func TestPlatform_MultipleRooms(t *testing.T) {
	t.Parallel()

	p := New()
	const n = 10

	type result struct {
		conn audio.Connection
		err  error
	}
	results := make([]result, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ch := fmt.Sprintf("room-%d", idx)
			conn, err := p.Connect(context.Background(), ch)
			results[idx] = result{conn: conn, err: err}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Errorf("Connect[%d]: %v", i, r.err)
			continue
		}
		if r.conn == nil {
			t.Errorf("Connect[%d]: nil connection", i)
			continue
		}
		if err := r.conn.Disconnect(); err != nil {
			t.Errorf("Disconnect[%d]: %v", i, err)
		}
	}
}

// ─── Connection tests ─────────────────────────────────────────────────────────

// TestConnection_AddRemovePeer verifies that peers can join and leave, and that
// InputStreams reflects the current set of peers.
func TestConnection_AddRemovePeer(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	ch, err := conn.AddPeer("user-1", "Alice")
	if err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if ch == nil {
		t.Fatal("AddPeer returned nil channel")
	}

	// Peer must appear in InputStreams.
	streams := conn.InputStreams()
	if _, ok := streams["user-1"]; !ok {
		t.Error("InputStreams: peer user-1 not found after AddPeer")
	}

	// Duplicate add must fail.
	if _, err = conn.AddPeer("user-1", "Alice"); err == nil {
		t.Error("AddPeer duplicate: expected error, got nil")
	}

	// Remove the peer.
	if err = conn.RemovePeer("user-1"); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}

	// Peer must be gone from InputStreams.
	streams = conn.InputStreams()
	if _, ok := streams["user-1"]; ok {
		t.Error("InputStreams: peer user-1 still present after RemovePeer")
	}

	// Removing a non-existent peer must fail.
	if err = conn.RemovePeer("user-1"); err == nil {
		t.Error("RemovePeer non-existent: expected error, got nil")
	}
}

// TestConnection_InputStreams verifies that audio arriving from a peer's
// transport is delivered to the per-peer input channel.
func TestConnection_InputStreams(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	// Initially empty.
	if n := len(conn.InputStreams()); n != 0 {
		t.Fatalf("InputStreams before AddPeer: want 0, got %d", n)
	}

	inputCh, err := conn.AddPeer("user-2", "Bob")
	if err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Retrieve the mock transport and push a frame into its audioIn side.
	conn.mu.RLock()
	mt := conn.peers["user-2"].transport.(*mockTransport)
	conn.mu.RUnlock()

	want := audio.AudioFrame{Data: []byte{1, 2, 3}, SampleRate: 48000, Channels: 1}
	mt.audioIn <- want

	// Frame must arrive on the connection's input channel for this peer.
	select {
	case got := <-inputCh:
		if string(got.Data) != string(want.Data) {
			t.Errorf("input frame data: got %v, want %v", got.Data, want.Data)
		}
		if got.SampleRate != want.SampleRate {
			t.Errorf("input frame SampleRate: got %d, want %d", got.SampleRate, want.SampleRate)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio frame on input channel")
	}
}

// TestConnection_OutputStream verifies that frames written to OutputStream
// are forwarded to all connected peers via their transports.
func TestConnection_OutputStream(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	if _, err := conn.AddPeer("user-3", "Charlie"); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	conn.mu.RLock()
	mt := conn.peers["user-3"].transport.(*mockTransport)
	conn.mu.RUnlock()

	// Write an NPC frame to the output channel (stereo, even byte count).
	frame := audio.AudioFrame{Data: []byte{10, 20, 30, 40}, SampleRate: 48000, Channels: 2}
	conn.OutputStream() <- frame

	// forwardOutput should deliver it to the mock transport (already in target format).
	select {
	case got := <-mt.audioOut:
		if string(got.Data) != string(frame.Data) {
			t.Errorf("output frame data: got %v, want %v", got.Data, frame.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio frame in mock transport output")
	}
}

// TestConnection_OnParticipantChange verifies that join and leave events are
// delivered to the registered callback.
func TestConnection_OnParticipantChange(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	joins := make(chan audio.Event, 4)
	leaves := make(chan audio.Event, 4)

	conn.OnParticipantChange(func(ev audio.Event) {
		switch ev.Type {
		case audio.EventJoin:
			joins <- ev
		case audio.EventLeave:
			leaves <- ev
		}
	})

	// AddPeer must trigger a join event.
	if _, err := conn.AddPeer("user-4", "Dana"); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	ev := waitEvent(t, joins, time.Second)
	if ev.UserID != "user-4" {
		t.Errorf("join event UserID: got %q, want %q", ev.UserID, "user-4")
	}
	if ev.Username != "Dana" {
		t.Errorf("join event Username: got %q, want %q", ev.Username, "Dana")
	}
	if ev.Type != audio.EventJoin {
		t.Errorf("join event Type: got %v, want EventJoin", ev.Type)
	}

	// RemovePeer must trigger a leave event.
	if err := conn.RemovePeer("user-4"); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	ev = waitEvent(t, leaves, time.Second)
	if ev.UserID != "user-4" {
		t.Errorf("leave event UserID: got %q, want %q", ev.UserID, "user-4")
	}
	if ev.Type != audio.EventLeave {
		t.Errorf("leave event Type: got %v, want EventLeave", ev.Type)
	}
}

// TestConnection_Disconnect verifies clean teardown and that subsequent
// AddPeer/RemovePeer calls return errors.
func TestConnection_Disconnect(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	if _, err := conn.AddPeer("user-5", "Eve"); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	if err := conn.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	// After disconnect, AddPeer must return an error.
	if _, err := conn.AddPeer("user-6", "Frank"); err == nil {
		t.Error("AddPeer after disconnect: expected error, got nil")
	}

	// After disconnect, RemovePeer must return an error.
	if err := conn.RemovePeer("user-5"); err == nil {
		t.Error("RemovePeer after disconnect: expected error, got nil")
	}
}

// TestConnection_DisconnectIdempotent verifies that calling Disconnect multiple
// times is safe and always returns nil.
func TestConnection_DisconnectIdempotent(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	for i := range 3 {
		if err := conn.Disconnect(); err != nil {
			t.Fatalf("Disconnect[%d]: %v", i, err)
		}
	}
}

// TestConnection_ConcurrentPeerOperations exercises AddPeer/RemovePeer from
// many goroutines simultaneously to detect data races (run with -race).
func TestConnection_ConcurrentPeerOperations(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	const workers = 20
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			userID := fmt.Sprintf("concurrent-user-%d", idx)
			if _, err := conn.AddPeer(userID, "User"); err != nil {
				return // already disconnected or some other race; acceptable
			}
			// Small delay to interleave goroutines.
			time.Sleep(time.Millisecond)
			_ = conn.RemovePeer(userID)
		}(i)
	}
	wg.Wait()

	// All peers should have been removed.
	if n := len(conn.InputStreams()); n != 0 {
		t.Errorf("InputStreams after concurrent ops: got %d entries, want 0", n)
	}
}

// ─── OutputWriter tests ────────────────────────────────────────────────────────────

// TestOutputWriter_SendBeforeDisconnect verifies that OutputWriter.Send
// successfully writes frames before the connection is disconnected.
func TestOutputWriter_SendBeforeDisconnect(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	if _, err := conn.AddPeer("ow-user-1", "Writer"); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	conn.mu.RLock()
	mt := conn.peers["ow-user-1"].transport.(*mockTransport)
	conn.mu.RUnlock()

	w := conn.OutputWriter()
	frame := audio.AudioFrame{Data: []byte{0xAA, 0xBB, 0xCC, 0xDD}, SampleRate: 48000, Channels: 2}
	if ok := w.Send(frame); !ok {
		t.Fatal("Send returned false before disconnect")
	}

	// Frame should reach the mock transport via forwardOutput (already in target format).
	select {
	case got := <-mt.audioOut:
		if string(got.Data) != string(frame.Data) {
			t.Errorf("output frame data: got %v, want %v", got.Data, frame.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame in mock transport output")
	}
}

// TestOutputWriter_SendAfterDisconnect verifies that OutputWriter.Send
// safely drops frames after Disconnect without panicking.
func TestOutputWriter_SendAfterDisconnect(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)

	w := conn.OutputWriter()

	if err := conn.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	// Must not panic.
	frame := audio.AudioFrame{Data: []byte{0xFF, 0x00}, SampleRate: 48000, Channels: 1}
	if ok := w.Send(frame); ok {
		t.Error("Send returned true after disconnect; want false (frame should be dropped)")
	}
}

// TestOutputWriter_NotNil verifies that OutputWriter returns a non-nil value.
func TestOutputWriter_NotNil(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	if conn.OutputWriter() == nil {
		t.Fatal("OutputWriter() returned nil")
	}
}

// TestOutputStream_StillWorksAfterOutputWriterAdded verifies backward compatibility:
// OutputStream() continues to return a usable channel.
func TestOutputStream_StillWorksAfterOutputWriterAdded(t *testing.T) {
	t.Parallel()

	conn := newTestConnection(t)
	defer func() { _ = conn.Disconnect() }()

	ch := conn.OutputStream()
	if ch == nil {
		t.Fatal("OutputStream() returned nil")
	}

	// Verify we can still write to it (basic smoke test).
	if _, err := conn.AddPeer("ow-compat-user", "Compat"); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	conn.mu.RLock()
	mt := conn.peers["ow-compat-user"].transport.(*mockTransport)
	conn.mu.RUnlock()

	frame := audio.AudioFrame{Data: []byte{0x42, 0x00, 0x42, 0x00}, SampleRate: 48000, Channels: 2}
	ch <- frame

	select {
	case got := <-mt.audioOut:
		if string(got.Data) != string(frame.Data) {
			t.Errorf("output frame data: got %v, want %v", got.Data, frame.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame from OutputStream")
	}
}

// ─── SignalingServer tests ────────────────────────────────────────────────────

// TestSignalingServer_Handler exercises all three HTTP endpoints of the
// signaling server and verifies correct status codes.
func TestSignalingServer_Handler(t *testing.T) {
	t.Parallel()

	// Shared handler for tests that need a clean-slate room per sub-test.
	newHandler := func() http.Handler {
		return NewSignalingServer(New()).Handler()
	}

	t.Run("join_ok", func(t *testing.T) {
		t.Parallel()
		h := newHandler()
		body := jsonBody(t, joinRequest{UserID: "u1", Username: "Alice", SDPOffer: "offer"})
		req := httptest.NewRequest(http.MethodPost, "/rooms/sig-room/join", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("join_ok: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
	})

	t.Run("join_missing_user_id", func(t *testing.T) {
		t.Parallel()
		h := newHandler()
		body := jsonBody(t, joinRequest{Username: "NoID"})
		req := httptest.NewRequest(http.MethodPost, "/rooms/nouid-room/join", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("join_missing_user_id: status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("join_duplicate", func(t *testing.T) {
		t.Parallel()
		h := newHandler()

		// First join.
		b1 := jsonBody(t, joinRequest{UserID: "dup", Username: "X"})
		r1 := httptest.NewRequest(http.MethodPost, "/rooms/dup-room/join", b1)
		r1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		h.ServeHTTP(w1, r1)
		if w1.Code != http.StatusOK {
			t.Fatalf("first join failed: %d %s", w1.Code, w1.Body.String())
		}

		// Duplicate join must return 409 Conflict.
		b2 := jsonBody(t, joinRequest{UserID: "dup", Username: "X"})
		r2 := httptest.NewRequest(http.MethodPost, "/rooms/dup-room/join", b2)
		r2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		h.ServeHTTP(w2, r2)
		if w2.Code != http.StatusConflict {
			t.Errorf("join_duplicate: status = %d, want %d", w2.Code, http.StatusConflict)
		}
	})

	t.Run("ice_ok", func(t *testing.T) {
		t.Parallel()
		h := newHandler()

		// Join first.
		b1 := jsonBody(t, joinRequest{UserID: "ice-user", Username: "Y"})
		r1 := httptest.NewRequest(http.MethodPost, "/rooms/ice-room/join", b1)
		r1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		h.ServeHTTP(w1, r1)
		if w1.Code != http.StatusOK {
			t.Fatalf("join for ice test: %d %s", w1.Code, w1.Body.String())
		}

		// Send ICE candidate.
		b2 := jsonBody(t, iceRequest{UserID: "ice-user", Candidate: "candidate:udp 1 192.168.1.1 12345 typ host"})
		r2 := httptest.NewRequest(http.MethodPost, "/rooms/ice-room/ice", b2)
		r2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		h.ServeHTTP(w2, r2)
		if w2.Code != http.StatusOK {
			t.Errorf("ice_ok: status = %d, want %d; body: %s", w2.Code, http.StatusOK, w2.Body.String())
		}
	})

	t.Run("ice_room_not_found", func(t *testing.T) {
		t.Parallel()
		h := newHandler()
		b := jsonBody(t, iceRequest{UserID: "nobody", Candidate: "x"})
		req := httptest.NewRequest(http.MethodPost, "/rooms/ghost-room/ice", b)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("ice_room_not_found: status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("ice_peer_not_found", func(t *testing.T) {
		t.Parallel()
		h := newHandler()

		// Create the room by joining with a different user.
		b1 := jsonBody(t, joinRequest{UserID: "someone", Username: "Z"})
		r1 := httptest.NewRequest(http.MethodPost, "/rooms/ice-peer-room/join", b1)
		r1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		h.ServeHTTP(w1, r1)
		if w1.Code != http.StatusOK {
			t.Fatalf("setup join: %d %s", w1.Code, w1.Body.String())
		}

		// ICE for unknown peer must return 404.
		b2 := jsonBody(t, iceRequest{UserID: "ghost-peer", Candidate: "x"})
		r2 := httptest.NewRequest(http.MethodPost, "/rooms/ice-peer-room/ice", b2)
		r2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		h.ServeHTTP(w2, r2)
		if w2.Code != http.StatusNotFound {
			t.Errorf("ice_peer_not_found: status = %d, want %d", w2.Code, http.StatusNotFound)
		}
	})

	t.Run("leave_ok", func(t *testing.T) {
		t.Parallel()
		h := newHandler()

		// Join first.
		b1 := jsonBody(t, joinRequest{UserID: "leave-user", Username: "W"})
		r1 := httptest.NewRequest(http.MethodPost, "/rooms/leave-room/join", b1)
		r1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		h.ServeHTTP(w1, r1)
		if w1.Code != http.StatusOK {
			t.Fatalf("join for leave test: %d %s", w1.Code, w1.Body.String())
		}

		// Leave.
		b2 := jsonBody(t, leaveRequest{UserID: "leave-user"})
		r2 := httptest.NewRequest(http.MethodDelete, "/rooms/leave-room/leave", b2)
		r2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		h.ServeHTTP(w2, r2)
		if w2.Code != http.StatusOK {
			t.Errorf("leave_ok: status = %d, want %d; body: %s", w2.Code, http.StatusOK, w2.Body.String())
		}
	})

	t.Run("leave_room_not_found", func(t *testing.T) {
		t.Parallel()
		h := newHandler()
		b := jsonBody(t, leaveRequest{UserID: "nobody"})
		req := httptest.NewRequest(http.MethodDelete, "/rooms/ghost-room/leave", b)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("leave_room_not_found: status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("leave_peer_not_found", func(t *testing.T) {
		t.Parallel()
		h := newHandler()

		// Create room.
		b1 := jsonBody(t, joinRequest{UserID: "someone", Username: "V"})
		r1 := httptest.NewRequest(http.MethodPost, "/rooms/leave-peer-room/join", b1)
		r1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		h.ServeHTTP(w1, r1)
		if w1.Code != http.StatusOK {
			t.Fatalf("setup join: %d %s", w1.Code, w1.Body.String())
		}

		// Leave for unknown peer must return 404.
		b2 := jsonBody(t, leaveRequest{UserID: "ghost-peer"})
		r2 := httptest.NewRequest(http.MethodDelete, "/rooms/leave-peer-room/leave", b2)
		r2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		h.ServeHTTP(w2, r2)
		if w2.Code != http.StatusNotFound {
			t.Errorf("leave_peer_not_found: status = %d, want %d", w2.Code, http.StatusNotFound)
		}
	})
}
