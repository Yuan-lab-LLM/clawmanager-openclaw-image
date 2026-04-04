package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type State struct {
	AgentID                    string         `json:"agent_id"`
	SessionToken               string         `json:"session_token"`
	SessionExpiresAt           time.Time      `json:"session_expires_at"`
	LastRegisterTime           time.Time      `json:"last_register_time"`
	CurrentConfigRevisionID    string         `json:"current_config_revision_id"`
	LastGoodConfigRevisionID   string         `json:"last_good_config_revision_id"`
	LastApplyAttemptRevisionID string         `json:"last_apply_attempt_revision_id"`
	LastApplyTime              time.Time      `json:"last_apply_time"`
	LastCommandExecutionCache  map[string]any `json:"last_command_execution_cache"`
	PendingEventQueue          []any          `json:"pending_event_queue"`
}

type Store struct {
	path  string
	state State
	mu    sync.RWMutex
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir store dir: %w", err)
	}
	s := &Store{
		path: filepath.Join(dir, "state.json"),
		state: State{
			LastCommandExecutionCache: map[string]any{},
			PendingEventQueue:         []any{},
		},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s.persistLocked()
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := json.Unmarshal(data, &s.state); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	if s.state.LastCommandExecutionCache == nil {
		s.state.LastCommandExecutionCache = map[string]any{}
	}
	if s.state.PendingEventQueue == nil {
		s.state.PendingEventQueue = []any{}
	}
	return nil
}

func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.state
	return out
}

func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write tmp state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}
