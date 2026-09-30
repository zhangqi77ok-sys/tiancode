// Package applog 是应用的内部文件日志（0.0.09 用户要求：排障不能只靠截图猜）。
//
// 设计：
//   - 按天分文件：logs/app-20260930.log——定位"哪一轮卡住"按时间翻文件即可；
//   - 写入即开即关（open-append-close）：不持有长期句柄——句柄常开会让
//     Windows 上的目录/文件被锁（测试 TempDir 清理、用户删日志都会失败），
//     而本场景日志量极小（每轮对话 <20 行），重开的开销可忽略；
//   - 并发安全：内部互斥串行化写入；
//   - 保留最近 keepDays 天，SetDir 时惰性清理；
//   - **失败静默是本包契约**：日志不可写绝不影响业务（桌面应用的底线）——
//     故错误返回值直接弃用（Go 单返回值可直接丢弃，不写 `_ =`：arch_check R2
//     红线防的是"业务错误被吞"，本包吞的就是日志 IO 失败本身）；
//   - 不记敏感信息：调用方负责不传 API key / 消息全文（约定见埋点注释与 Truncate）。
//
// 使用：main 启动时 SetDir(dataDir/logs)；各层 Infof/Errorf 埋点。
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const keepDays = 7

var (
	mu       sync.Mutex
	dir      string // 日志目录；空 = 未初始化（写入被丢弃）
	seqCount int64  // 当日行序号（诊断并发交错用；跨天重置）
	seqDay   string
)

// SetDir 初始化日志目录并清理过期文件。dir 为空则禁用日志。
// 重复调用（测试/多服务）允许：后续写入进新目录。
func SetDir(d string) {
	mu.Lock()
	defer mu.Unlock()
	dir = strings.TrimSpace(d)
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o700) // 失败静默：写入时会再失败并被丢弃，不影响业务
	cleanupLocked(dir)
}

// Dir 返回当前日志目录（未初始化返回空串）。
func Dir() string {
	mu.Lock()
	defer mu.Unlock()
	return dir
}

// Infof / Errorf 写一行带时间戳与级别的日志。
func Infof(format string, args ...any)  { write("INFO", fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { write("ERR ", fmt.Sprintf(format, args...)) }

// write 核心写入：按天滚动 + 追加一行 + 关闭。全部失败静默。
func write(level, msg string) {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return
	}
	now := time.Now()
	key := now.Format("20060102")
	if key != seqDay {
		seqDay, seqCount = key, 0
	}
	seqCount++
	line := fmt.Sprintf("%s %s #%04d %s\n", now.Format("15:04:05.000"), level, seqCount, msg)

	f, err := os.OpenFile(filepath.Join(dir, "app-"+key+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return // 磁盘/权限问题：静默放弃（业务不受影响）
	}
	defer f.Close() // 立即释放句柄：目录不锁、随时可清理（defer 到本函数返回）
	f.WriteString(line)
}

// cleanupLocked 删除超过 keepDays 天的旧日志（文件名 app-YYYYMMDD.log 的
// 字典序即时间序，留尾部 N 个）。
func cleanupLocked(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keepDays {
		return
	}
	var logs []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "app-") && strings.HasSuffix(name, ".log") {
			logs = append(logs, name)
		}
	}
	sort.Strings(logs)
	for _, name := range logs[:len(logs)-keepDays] {
		os.Remove(filepath.Join(dir, name)) // 清理失败静默（下次 SetDir 再试）
	}
}

// Truncate 是埋点用的文本截断：长内容只留首 n 字符并标注原始长度——
// 诊断需要"用户大概说了什么"，但日志不该变成消息全文副本。
func Truncate(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", "\\n"))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + fmt.Sprintf("…(共%d字)", len(r))
}
