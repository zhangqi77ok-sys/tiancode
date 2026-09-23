package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeleteSession 删除会话账本文件。
// 为什么幂等（不存在也返回 nil）：用户会连点/重试，重复删除报错会被读成"删不掉"。
func DeleteSession(dir, sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除会话失败：%w", err)
	}
	return nil
}

// validateSessionID 拒绝会逃出账本目录的会话 ID。
// 为什么必须有：会话 ID 由 UI 传入（新建会话名、外部调用），
// 未校验时 "../config" 这类输入可以读写/删除工作目录之外的任意文件。
func validateSessionID(id string) error {
	if id == "" || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\`) || filepath.Clean(id) != id {
		return fmt.Errorf("非法的会话 ID：%q", id)
	}
	return nil
}
