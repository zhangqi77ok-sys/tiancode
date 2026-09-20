package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteConfig_Perms(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sub", "test.json")
	data := []byte(`{"hello":"world"}`)

	if err := atomicWriteConfig(filePath, data); err != nil {
		t.Fatalf("atomicWriteConfig failed: %v", err)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		// On Unix systems, file mode must be 0600 and dir must be 0700
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected file perm 0600, got %o", perm)
		}
		dirInfo, err := os.Stat(filepath.Dir(filePath))
		if err != nil {
			t.Fatalf("stat dir failed: %v", err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0700 {
			t.Errorf("expected dir perm 0700, got %o", perm)
		}
	}
}
