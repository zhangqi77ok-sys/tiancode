package config

import (
	"io"
	"os"
	"path/filepath"
)

// UserDataDir 返回 ~/.tiancode。若新目录缺少文件而 ~/.tcode 仍在，则一次性拷贝迁移。
func UserDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	dest := filepath.Join(home, ".tiancode")
	_ = os.MkdirAll(dest, 0700)
	legacy := filepath.Join(home, ".tcode")
	migrateLegacyDir(legacy, dest)
	return dest
}

func migrateLegacyDir(legacy, dest string) {
	if legacy == dest {
		return
	}
	info, err := os.Stat(legacy)
	if err != nil || !info.IsDir() {
		return
	}
	for _, name := range []string{"channels.json", "mcp_servers.json", "skills.json", "rules.json"} {
		copyIfMissing(filepath.Join(legacy, name), filepath.Join(dest, name))
	}
	legacySess := filepath.Join(legacy, "sessions")
	destSess := filepath.Join(dest, "sessions")
	if st, err := os.Stat(legacySess); err == nil && st.IsDir() {
		_ = os.MkdirAll(destSess, 0700)
		entries, _ := os.ReadDir(legacySess)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			copyIfMissing(filepath.Join(legacySess, e.Name()), filepath.Join(destSess, e.Name()))
		}
	}
}

func copyIfMissing(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return
	}
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	_ = os.MkdirAll(filepath.Dir(dst), 0700)
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer out.Close()
	_, _ = io.Copy(out, in)
}
