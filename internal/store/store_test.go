package store

import (
	"os"
	"path/filepath"
	"strings"
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

func TestWALReplayLargeValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.wal")
	value := strings.Repeat("large value\n", 16*1024)
	s := New(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Set("large", value); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("after", "next record"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := New(path)
	if err := reopened.Load(); err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, ok := reopened.Get("large"); !ok || got != value {
		t.Fatalf("large value did not survive replay: found=%v length=%d", ok, len(got))
	}
	if got, ok := reopened.Get("after"); !ok || got != "next record" {
		t.Fatalf("record following large value was lost: %q, %v", got, ok)
	}
}

func TestWALReplayRejectsMalformedRecords(t *testing.T) {
	for _, contents := range []string{
		"{invalid}\n",
		`{"op":"set","key":"partial","value":"unfinished`,
		"\n",
	} {
		path := filepath.Join(t.TempDir(), "store.wal")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		s := New(path)
		if err := s.Load(); err == nil {
			s.Close()
			t.Fatalf("expected replay error for %q", contents)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != contents {
			t.Fatal("failed replay modified the WAL")
		}
	}
}
