package session

import (
	"os"
	"strings"
)

// ListSessions 返回账本目录下的全部会话 ID（按文件名排序）。
// 目录不存在视为无会话（返回空切片而非错误），与 OpenLedger 的懒创建语义一致。
func ListSessions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".jsonl"))
	}
	return ids, nil
}
