package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/billyoftea/wxagent/go_backend/internal/atomicfile"
)

// Store mirrors wx_agent.state.PipelineState for incremental tracking.
type Store struct {
	path     string
	mu       sync.Mutex
	loaded   bool
	Sessions map[string]SessionState `json:"sessions"`
	legacy   map[string]json.RawMessage
	Meta     map[string]string `json:"meta"`
}

// SessionState captures the latest timestamp processed per session.
type SessionState struct {
	LastTimestamp int64 `json:"last_timestamp"`
	LastCount     int   `json:"last_count,omitempty"`
}

// New returns an empty Store bound to path.
func New(path string) *Store {
	return &Store{
		path:     path,
		Sessions: make(map[string]SessionState),
		legacy:   make(map[string]json.RawMessage),
		Meta:     make(map[string]string),
	}
}

// Load populates the store from disk, ignoring missing files.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return nil
	}
	if s.path == "" {
		return errors.New("state path is empty")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loaded = true
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	if legacy == nil {
		return fmt.Errorf("parse state: expected JSON object")
	}
	var payload struct {
		Sessions map[string]SessionState `json:"sessions"`
		Meta     map[string]string       `json:"meta"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	s.Sessions, s.Meta, s.legacy = payload.Sessions, payload.Meta, legacy
	if s.Sessions == nil {
		s.Sessions = make(map[string]SessionState)
	}
	if s.Meta == nil {
		s.Meta = make(map[string]string)
	}
	s.loaded = true
	return nil
}

// Save writes the store back to disk.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return errors.New("state path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	// Retain old summary progress and any unknown fields without executing the
	// removed feature or discarding data when native export progress is saved.
	payload := make(map[string]json.RawMessage, len(s.legacy)+2)
	for key, value := range s.legacy {
		payload[key] = value
	}
	sessions, err := json.Marshal(s.Sessions)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(s.Meta)
	if err != nil {
		return err
	}
	payload["sessions"], payload["meta"] = sessions, meta
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := atomicfile.Write(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// LastTimestamp returns the stored timestamp for a session.
func (s *Store) LastTimestamp(sessionID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.Sessions[sessionID]; ok {
		return entry.LastTimestamp
	}
	return 0
}

// UpdateSession records the latest timestamp and message count.
func (s *Store) UpdateSession(sessionID string, timestamp int64, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.Sessions[sessionID]
	if timestamp > current.LastTimestamp {
		current.LastTimestamp = timestamp
	}
	if count > 0 {
		current.LastCount = count
	}
	s.Sessions[sessionID] = current
}
