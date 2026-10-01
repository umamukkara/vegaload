package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	res := &Result{Executor: "fixed-vus", Total: 10, Failed: 1, ErrorRate: 0.1, Elapsed: time.Second}

	if err := WriteJSON(path, res); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshalling written JSON: %v", err)
	}
	if got.Total != 10 || got.Failed != 1 || got.Executor != "fixed-vus" {
		t.Errorf("round-tripped Result = %+v, want Total:10 Failed:1 Executor:fixed-vus", got)
	}
}
