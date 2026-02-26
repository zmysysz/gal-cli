package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gal-cli/gal-cli/internal/provider"
)

const (
	Dir       = "/tmp/gal-sessions"
	MaxAge    = 7 * 24 * time.Hour
)

type Session struct {
	ID        string             `json:"id"`
	Agent     string             `json:"agent"`
	Model     string             `json:"model"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Messages  []provider.Message `json:"messages"`
}

func NewID() string {
	b := make([]byte, 3)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func path(id string) string {
	return filepath.Join(Dir, id+".json")
}

func New(id, agent, model string) *Session {
	now := time.Now()
	return &Session{
		ID: id, Agent: agent, Model: model,
		CreatedAt: now, UpdatedAt: now,
	}
}

func Load(id string) (*Session, error) {
	data, err := os.ReadFile(path(id))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", id, err)
	}
	return &s, nil
}

func (s *Session) Save() error {
	os.MkdirAll(Dir, 0755)
	s.UpdatedAt = time.Now()
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path(s.ID), data, 0644)
}

func Remove(id string) error {
	return os.Remove(path(id))
}

func List() ([]*Session, error) {
	entries, err := os.ReadDir(Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []*Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		s, err := Load(id)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func Cleanup() {
	entries, err := os.ReadDir(Dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-MaxAge)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		s, err := Load(id)
		if err != nil {
			continue
		}
		if s.UpdatedAt.Before(cutoff) {
			os.Remove(path(id))
		}
	}
}
