package openclawinspector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountSkillsIncludesWorkspaceAndBuiltin(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	builtin := filepath.Join(root, "builtin")

	if err := os.MkdirAll(filepath.Join(workspace, "skills", "custom-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(builtin, "apple-notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "skills", "README.md"), []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(builtin, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	inspector := New(filepath.Join(root, "openclaw.json"), workspace, builtin)
	if got := inspector.countSkills(); got != 3 {
		t.Fatalf("expected 3 skills, got %d", got)
	}
}
