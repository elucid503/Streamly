package workers

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

type Entry struct {

	Token string `json:"token"`
}

type File map[string]Entry

type Store struct {

	path string
	mu sync.RWMutex
	data File
}

func NewStore(path string) *Store {

	return &Store{

		path: path,
		data: make(File),
	}

}

func (s *Store) Load() error {

	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)

	if errors.Is(err, os.ErrNotExist) {

		s.data = make(File)
		return nil
	}

	if err != nil {

		return err
	}

	if len(raw) == 0 {

		s.data = make(File)
		return nil
	}

	var file File

	if err := json.Unmarshal(raw, &file); err != nil {

		return err
	}

	if file == nil {

		file = make(File)
	}

	s.data = file

	return nil

}

func (s *Store) Save() error {

	s.mu.RLock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()

	if err != nil {

		return err
	}

	return os.WriteFile(s.path, append(data, '\n'), 0600)

}

func (s *Store) Set(guildID, token string) error {

	s.mu.Lock()
	s.data[guildID] = Entry{

		Token: token,
	}
	s.mu.Unlock()

	return s.Save()

}

func (s *Store) Get(guildID string) (Entry, bool) {

	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.data[guildID]

	return entry, ok

}

func (s *Store) All() File {

	s.mu.RLock()
	defer s.mu.RUnlock()

	copy := make(File, len(s.data))

	for guildID, entry := range s.data {

		copy[guildID] = entry
	}

	return copy

}
