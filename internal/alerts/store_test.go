package alerts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	now := time.Now().Truncate(time.Second)
	want := map[string]SeenEntry{
		"github:1":       {UpdatedAt: now, LastSeenAt: now},
		"notification:2": {LastSeenAt: now},
	}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}

	seen, existed, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Errorf("saved file should report existed=true")
	}
	if len(seen) != 2 {
		t.Fatalf("round-trip mismatch, got %v", seen)
	}
	if !seen["github:1"].UpdatedAt.Equal(now) || !seen["github:1"].LastSeenAt.Equal(now) {
		t.Errorf("github:1 round-trip mismatch, got %+v", seen["github:1"])
	}
	if !seen["notification:2"].LastSeenAt.Equal(now) {
		t.Errorf("notification:2 round-trip mismatch, got %+v", seen["notification:2"])
	}
}

func TestFileStore_LoadRecoversFromCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.json")
	if err := os.WriteFile(path, []byte(`{"not json`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewFileStore(path)
	seen, existed, err := s.Load()
	if err != nil {
		t.Fatalf("corrupt file should not error, got %v", err)
	}
	if existed {
		t.Errorf("corrupt file should report existed=false so Process reseeds a baseline")
	}
	if len(seen) != 0 {
		t.Errorf("corrupt file should yield empty set, got %v", seen)
	}
}

func TestFileStore_SaveDoesNotLeaveTempFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.json")
	s := NewFileStore(path)
	if err := s.Save(map[string]SeenEntry{"a": {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("Save should rename the temp file away, stat err = %v", err)
	}
}

func TestFileStore_LoadMigratesLegacyArrayFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.json")
	if err := os.WriteFile(path, []byte(`["github:1","notification:2"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewFileStore(path)
	seen, existed, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Errorf("legacy file should report existed=true")
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 migrated entries, got %v", seen)
	}
	if seen["github:1"].LastSeenAt.IsZero() {
		t.Errorf("migrated entry should have a non-zero LastSeenAt")
	}
}
