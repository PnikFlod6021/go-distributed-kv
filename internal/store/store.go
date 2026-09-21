package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type record struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	data    map[string]string
	walPath string
	walFile *os.File
}

func New(walPath string) *Store {
	return &Store{data: make(map[string]string), walPath: walPath}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.walPath), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(s.walPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}

	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return err
	}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var rec record
		if err := json.Unmarshal(scan.Bytes(), &rec); err != nil {
			f.Close()
			return fmt.Errorf("replay WAL: %w", err)
		}
		s.applyLocked(rec)
	}
	if err := scan.Err(); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Seek(0, 2); err != nil {
		f.Close()
		return err
	}
	s.walFile = f
	return nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.walFile == nil {
		return nil
	}
	return s.walFile.Close()
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := record{Op: "set", Key: key, Value: value}
	if err := s.appendLocked(rec); err != nil {
		return err
	}
	s.applyLocked(rec)
	return nil
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := record{Op: "delete", Key: key}
	if err := s.appendLocked(rec); err != nil {
		return err
	}
	s.applyLocked(rec)
	return nil
}

func (s *Store) Len() int { s.mu.RLock(); defer s.mu.RUnlock(); return len(s.data) }

func (s *Store) appendLocked(rec record) error {
	if s.walFile == nil {
		return errors.New("store not loaded")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := s.walFile.Write(append(b, '\n')); err != nil {
		return err
	}
	return s.walFile.Sync()
}

func (s *Store) applyLocked(rec record) {
	switch rec.Op {
	case "set":
		s.data[rec.Key] = rec.Value
	case "delete":
		delete(s.data, rec.Key)
	}
}
