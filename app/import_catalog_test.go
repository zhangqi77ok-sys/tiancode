package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectSkillFiles_FindsSkillMdAndSkipsVendor(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "review", "SKILL.md"), "---\nname: review\n---\nbody")
	mustWrite(t, filepath.Join(root, "vendor", "x", "SKILL.md"), "skip me")
	mustWrite(t, filepath.Join(root, "notes.md"), "not a skill")

	got, err := collectSkillFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Name, "SKILL.md") || !strings.Contains(got[0].Body, "review") {
		t.Fatalf("files = %+v", got)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
