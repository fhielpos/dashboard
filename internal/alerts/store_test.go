package alerts

import (
	"path/filepath"
	"testing"
)

func TestFileStore_MissingFileIsNotExisting(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "alerts.json"))
	seen, existed, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Errorf("missing file should report existed=false")
	}
	if len(seen) != 0 {
		t.Errorf("missing file should yield empty set, got %v", seen)
	}
}

func TestFileStore_RoundTrip(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "alerts.json"))
	if err := s.Save(map[string]bool{"github:1": true, "notification:2": true}); err != nil {
		t.Fatal(err)
	}

	seen, existed, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Errorf("saved file should report existed=true")
	}
	if !seen["github:1"] || !seen["notification:2"] || len(seen) != 2 {
		t.Errorf("round-trip mismatch, got %v", seen)
	}
}
