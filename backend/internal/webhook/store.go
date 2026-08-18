// Package webhook stores the configured Teams webhook URL and sends
// messages to it.
package webhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Routine struct {
	Url          string     `json:"Url"`
	UpdatedAtUtc *time.Time `json:"UpdatedAtUtc"`
}

// Store persists the webhook URL a caller configured via PUT /api/webhook.
type Store struct {
	filePath   string
	defaultUrl string
	mu         sync.Mutex
}

// NewStore resolves the data directory the same way the old backend did:
// an explicit dataDirectory if given, otherwise a "data" folder next to
// wherever the process is running from.
func NewStore(dataDirectory string, defaultUrl string) (*Store, error) {
	if dataDirectory == "" {
		dataDirectory = "data"
	}
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		filePath:   filepath.Join(dataDirectory, "webhook-routine.json"),
		defaultUrl: defaultUrl,
	}, nil
}

func (s *Store) Get() (Routine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

func (s *Store) Save(url string) (Routine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	routine := Routine{Url: url, UpdatedAtUtc: &now}

	data, err := json.MarshalIndent(routine, "", "  ")
	if err != nil {
		return Routine{}, err
	}
	if err := os.WriteFile(s.filePath, data, 0o644); err != nil {
		return Routine{}, err
	}
	return routine, nil
}

func (s *Store) read() (Routine, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s.defaultRoutine(), nil
		}
		return Routine{}, err
	}
	if len(data) == 0 {
		return s.defaultRoutine(), nil
	}

	var routine Routine
	if err := json.Unmarshal(data, &routine); err != nil {
		return s.defaultRoutine(), nil
	}
	if routine.Url == "" {
		return s.defaultRoutine(), nil
	}
	return routine, nil
}

func (s *Store) defaultRoutine() Routine {
	return Routine{Url: s.defaultUrl}
}
