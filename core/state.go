package core

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// State holds the conversation state for an agent.
type State struct {
	mu sync.RWMutex

	messages  []Message
	sessionID string

	// Token and cost tracking
	totalTokens      int
	totalCost        float64
	promptTokens     int
	completionTokens int
}

// NewState creates a new State.
func NewState() *State {
	return &State{
		messages: make([]Message, 0, 64),
	}
}

// Messages returns a copy of the message list.
func (s *State) Messages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// SetMessages replaces the message list.
func (s *State) SetMessages(msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = msgs
}

// AddMessage appends a message to the conversation.
func (s *State) AddMessage(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

// Len returns the number of messages.
func (s *State) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// LastAssistantMessage returns the most recent assistant message, or nil if none exists.
func (s *State) LastAssistantMessage() *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == "assistant" {
			msg := s.messages[i]
			return &msg
		}
	}
	return nil
}

// SessionID returns the current session ID.
func (s *State) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

// SetSessionID sets the session ID.
func (s *State) SetSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// EnsureSessionID generates a session ID if not already set.
func (s *State) EnsureSessionID() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == "" {
		s.sessionID = fmt.Sprintf("session_%d", time.Now().Unix())
	}
}

// TotalTokens returns the total token count.
func (s *State) TotalTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalTokens
}

// AddTokens adds to the token counters.
func (s *State) AddTokens(prompt, completion, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptTokens += prompt
	s.completionTokens += completion
	s.totalTokens += total
}

// TotalCost returns the total cost.
func (s *State) TotalCost() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalCost
}

// AddCost adds to the cost counter.
func (s *State) AddCost(cost float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalCost += cost
}

// ExportState serializes the state to JSON.
func (s *State) ExportState() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := AgentState{
		Messages:         s.messages,
		SessionID:        s.sessionID,
		TotalTokens:      s.totalTokens,
		TotalCost:        s.totalCost,
		PromptTokens:     s.promptTokens,
		CompletionTokens: s.completionTokens,
	}
	return json.Marshal(state)
}

// ImportState deserializes state from JSON.
func (s *State) ImportState(data []byte) error {
	var state AgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("import state: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = state.Messages
	s.sessionID = state.SessionID
	s.totalTokens = state.TotalTokens
	s.totalCost = state.TotalCost
	s.promptTokens = state.PromptTokens
	s.completionTokens = state.CompletionTokens
	return nil
}
