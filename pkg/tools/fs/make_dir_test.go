package fstools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMakeDirStaysInWorkspace(t *testing.T) {
	ws := t.TempDir()
	tool := NewMakeDirTool(ws, true)

	if r := tool.Execute(context.Background(), map[string]any{"path": "projects/demo"}); r.IsError {
		t.Fatalf("create failed: %s", r.ForLLM)
	}
	if st, err := os.Stat(filepath.Join(ws, "projects", "demo")); err != nil || !st.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}

	if r := tool.Execute(context.Background(), map[string]any{"path": "../escaped"}); !r.IsError {
		t.Fatal("path outside the workspace was allowed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escaped")); err == nil {
		t.Fatal("directory created outside the workspace")
	}
}
