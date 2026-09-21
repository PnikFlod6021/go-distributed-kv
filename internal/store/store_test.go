package store

import (
	"path/filepath"
	"testing"
)

func TestWALReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.wal")
	s := New(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("gpu", "H100"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("rack", "7"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("rack"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := New(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	v, ok := s2.Get("gpu")
	if !ok || v != "H100" {
		t.Fatalf("expected H100; got %q %v", v, ok)
	}
	if _, ok := s2.Get("rack"); ok {
		t.Fatal("deleted key returned after WAL replay")
	}
}
