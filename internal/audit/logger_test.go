package audit

import (
	"testing"
	"time"
)

func TestLogAndQuery(t *testing.T) {
	store := NewMemoryStore()
	logger, err := NewLogger(store)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Stop()

	logger.Log("admin-key", "admin", "rollout.create", "rollout:r-1", "{}", "tenant-1", "gpt-4o")
	time.Sleep(100 * time.Millisecond) // wait for async writer

	entries, err := logger.Query(QueryFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "rollout.create" {
		t.Errorf("expected rollout.create, got %s", entries[0].Action)
	}
	if entries[0].EntryHash == "" {
		t.Error("expected non-empty hash")
	}
	if entries[0].PreviousHash != "" {
		t.Error("first entry should have empty previous hash")
	}
}

func TestHashChain(t *testing.T) {
	store := NewMemoryStore()
	logger, err := NewLogger(store)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Stop()

	logger.Log("key-1", "admin", "action.one", "res-1", "{}", "t1", "")
	time.Sleep(50 * time.Millisecond)
	logger.Log("key-2", "operator", "action.two", "res-2", "{}", "t1", "")
	time.Sleep(50 * time.Millisecond)

	entries, _ := logger.Query(QueryFilters{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].PreviousHash != entries[0].EntryHash {
		t.Error("second entry's previous_hash should equal first entry's hash")
	}
}

func TestVerifyIntact(t *testing.T) {
	store := NewMemoryStore()
	logger, err := NewLogger(store)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Stop()

	logger.Log("k1", "admin", "a1", "r1", "{}", "t1", "")
	logger.Log("k2", "admin", "a2", "r2", "{}", "t1", "")
	time.Sleep(100 * time.Millisecond)

	result, err := logger.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got: %s", result.Message)
	}
}

func TestVerifyEmpty(t *testing.T) {
	store := NewMemoryStore()
	logger, err := NewLogger(store)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Stop()

	result, _ := logger.Verify()
	if !result.Valid {
		t.Error("empty log should be valid")
	}
}

func TestComputeHashDeterministic(t *testing.T) {
	e := Entry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Actor:     "test", ActorRole: "admin", Action: "test.action",
		Resource: "res-1", Detail: "{}", TenantID: "t1", PreviousHash: "",
	}
	h1 := computeHash(e)
	h2 := computeHash(e)
	if h1 != h2 {
		t.Error("hash should be deterministic")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64 char hex hash, got %d", len(h1))
	}
}
