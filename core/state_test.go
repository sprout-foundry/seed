package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewState_Empty(t *testing.T) {
	s := NewState()

	if s == nil {
		t.Fatal("NewState returned nil")
	}
	if s.Len() != 0 {
		t.Errorf("expected 0 messages, got %d", s.Len())
	}
	if s.SessionID() != "" {
		t.Errorf("expected empty session ID, got %q", s.SessionID())
	}
	if s.TotalTokens() != 0 {
		t.Errorf("expected 0 total tokens, got %d", s.TotalTokens())
	}
	if s.TotalCost() != 0 {
		t.Errorf("expected 0 cost, got %f", s.TotalCost())
	}
}

func TestState_AddMessage(t *testing.T) {
	s := NewState()

	msg := Message{Role: "user", Content: "hello"}
	s.AddMessage(msg)

	if s.Len() != 1 {
		t.Errorf("expected 1 message, got %d", s.Len())
	}
}

func TestState_MessagesReturnsCopy(t *testing.T) {
	s := NewState()
	s.AddMessage(Message{Role: "user", Content: "msg1"})
	s.AddMessage(Message{Role: "assistant", Content: "msg2"})

	// Get a copy of messages
	copied := s.Messages()

	// Mutate the copy
	copied[0].Content = "modified"

	// Original should be unchanged
	messages := s.Messages()
	if messages[0].Content != "msg1" {
		t.Errorf("Messages() should return a copy; expected 'msg1', got %q", messages[0].Content)
	}
}

func TestState_SetMessages(t *testing.T) {
	s := NewState()
	s.AddMessage(Message{Role: "user", Content: "old"})

	s.SetMessages([]Message{
		{Role: "user", Content: "new1"},
		{Role: "assistant", Content: "new2"},
		{Role: "assistant", Content: "new3"},
	})

	if s.Len() != 3 {
		t.Errorf("expected 3 messages after SetMessages, got %d", s.Len())
	}
	if s.Messages()[0].Content != "new1" {
		t.Errorf("expected first message 'new1', got %q", s.Messages()[0].Content)
	}
}

func TestState_MessagesWithNilSlice(t *testing.T) {
	s := NewState()
	s.SetMessages(nil)

	// Should handle nil gracefully — Len should be 0
	if s.Len() != 0 {
		t.Errorf("expected 0 messages for nil slice, got %d", s.Len())
	}
}

func TestState_SessionID(t *testing.T) {
	s := NewState()

	// Initially empty
	if s.SessionID() != "" {
		t.Errorf("expected empty session ID, got %q", s.SessionID())
	}

	// SetSessionID works
	s.SetSessionID("test-session-123")
	if s.SessionID() != "test-session-123" {
		t.Errorf("expected 'test-session-123', got %q", s.SessionID())
	}
}

func TestState_EnsureSessionID_GeneratesIfEmpty(t *testing.T) {
	s := NewState()

	s.EnsureSessionID()
	id := s.SessionID()

	if id == "" {
		t.Error("EnsureSessionID should generate a non-empty session ID")
	}
	if len(id) < len("session_") {
		t.Errorf("session ID too short: %q", id)
	}
	if id[:8] != "session_" {
		t.Errorf("expected session ID to start with 'session_', got %q", id)
	}
}

func TestState_EnsureSessionID_DoesNotOverwriteExisting(t *testing.T) {
	s := NewState()
	s.SetSessionID("my-custom-id")

	s.EnsureSessionID()

	if s.SessionID() != "my-custom-id" {
		t.Errorf("EnsureSessionID should not overwrite existing ID; expected 'my-custom-id', got %q", s.SessionID())
	}
}

func TestState_AddTokens(t *testing.T) {
	s := NewState()

	s.AddTokens(10, 5, 15)
	if s.TotalTokens() != 15 {
		t.Errorf("expected 15 total tokens, got %d", s.TotalTokens())
	}

	// Add more tokens
	s.AddTokens(20, 10, 30)
	if s.TotalTokens() != 45 {
		t.Errorf("expected 45 total tokens after cumulative adds, got %d", s.TotalTokens())
	}
}

func TestState_AddCost(t *testing.T) {
	s := NewState()

	s.AddCost(0.01)
	if s.TotalCost() != 0.01 {
		t.Errorf("expected cost 0.01, got %f", s.TotalCost())
	}

	s.AddCost(0.03)
	if s.TotalCost() != 0.04 {
		t.Errorf("expected cost 0.04 after cumulative adds, got %f", s.TotalCost())
	}
}

func TestState_ExportState(t *testing.T) {
	s := NewState()
	s.SetSessionID("test-session")
	s.AddMessage(Message{Role: "user", Content: "hello"})
	s.AddTokens(100, 50, 150)
	s.AddCost(0.005)

	data, err := s.ExportState()
	if err != nil {
		t.Fatalf("ExportState returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ExportState returned empty data")
	}

	// Verify it's valid JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("ExportState JSON is invalid: %v", err)
	}
}

func TestState_ImportState(t *testing.T) {
	s := NewState()

	importData := `{
		"messages": [{"role":"user","content":"imported"}],
		"session_id": "imported-session",
		"total_tokens": 200,
		"total_cost": 0.01,
		"prompt_tokens": 100,
		"completion_tokens": 100
	}`

	err := s.ImportState([]byte(importData))
	if err != nil {
		t.Fatalf("ImportState returned error: %v", err)
	}

	if s.SessionID() != "imported-session" {
		t.Errorf("expected session 'imported-session', got %q", s.SessionID())
	}
	if s.Len() != 1 {
		t.Errorf("expected 1 message, got %d", s.Len())
	}
	if s.Messages()[0].Content != "imported" {
		t.Errorf("expected message content 'imported', got %q", s.Messages()[0].Content)
	}
	if s.TotalTokens() != 200 {
		t.Errorf("expected 200 total tokens, got %d", s.TotalTokens())
	}
	if s.TotalCost() != 0.01 {
		t.Errorf("expected cost 0.01, got %f", s.TotalCost())
	}
}

func TestState_ExportImport_Roundtrip(t *testing.T) {
	s := NewState()
	s.SetSessionID("roundtrip-session")
	s.AddMessage(Message{Role: "user", Content: "first"})
	s.AddMessage(Message{Role: "assistant", Content: "second"})
	s.AddTokens(100, 50, 150)
	s.AddCost(0.005)

	data, err := s.ExportState()
	if err != nil {
		t.Fatalf("ExportState error: %v", err)
	}

	s2 := NewState()
	if err := s2.ImportState(data); err != nil {
		t.Fatalf("ImportState error: %v", err)
	}

	if s2.SessionID() != s.SessionID() {
		t.Errorf("session mismatch: expected %q, got %q", s.SessionID(), s2.SessionID())
	}
	if s2.Len() != s.Len() {
		t.Errorf("message count mismatch: expected %d, got %d", s.Len(), s2.Len())
	}
	if s2.TotalTokens() != s.TotalTokens() {
		t.Errorf("total tokens mismatch: expected %d, got %d", s.TotalTokens(), s2.TotalTokens())
	}
	if s2.TotalCost() != s.TotalCost() {
		t.Errorf("cost mismatch: expected %f, got %f", s.TotalCost(), s2.TotalCost())
	}
}

func TestState_ImportState_InvalidJSON(t *testing.T) {
	s := NewState()
	err := s.ImportState([]byte("not valid json"))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestState_ImportState_EmptyData(t *testing.T) {
	s := NewState()
	err := s.ImportState([]byte(""))
	if err == nil {
		t.Error("expected error for empty data, got nil")
	}
}

func TestState_ConcurrentAccess(t *testing.T) {
	s := NewState()

	done := make(chan struct{})

	// Concurrent readers
	go func() {
		for i := 0; i < 100; i++ {
			_ = s.Len()
			_ = s.Messages()
			_ = s.TotalTokens()
			_ = s.SessionID()
			_ = s.TotalCost()
		}
		done <- struct{}{}
	}()

	// Concurrent writers
	go func() {
		for i := 0; i < 100; i++ {
			s.AddMessage(Message{Role: "user", Content: string(rune('a' + i%26))})
			s.AddTokens(1, 1, 2)
			s.AddCost(0.001)
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	// Verify state is consistent
	if s.Len() != 100 {
		t.Errorf("expected 100 messages, got %d", s.Len())
	}
	if s.TotalTokens() != 200 {
		t.Errorf("expected 200 total tokens, got %d", s.TotalTokens())
	}
}

func TestState_EnsureSessionID_WithinTimeout(t *testing.T) {
	// Ensure session generation completes promptly
	s := NewState()

	done := make(chan struct{})
	go func() {
		s.EnsureSessionID()
		done <- struct{}{}
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("EnsureSessionID blocked for too long")
	}
}
