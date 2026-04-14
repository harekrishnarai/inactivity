package analyzer

import "testing"

func TestCheckpointRoundTripPreservesCompletedRepos(t *testing.T) {
	store := NewCheckpointStore(t.TempDir())
	original := Checkpoint{
		RunID:     "run-123",
		Target:    "acme",
		Pending:   []string{"acme/service-b"},
		Completed: map[string]RepoSnapshot{"acme/service-a": {Repository: "acme/service-a"}},
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loaded, err := store.LoadLatest("acme")
	if err != nil {
		t.Fatalf("LoadLatest returned error: %v", err)
	}
	if _, ok := loaded.Completed["acme/service-a"]; !ok {
		t.Fatalf("missing completed repo in loaded checkpoint: %+v", loaded)
	}
}
