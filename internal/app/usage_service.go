// 用量沉淀（0.0.19）：上游 token 用量落账本，列表按会话聚合。
//
// 此前 usage 只透传给前端油表（进程内一次性），重启即清零，"这个项目花了多少
// token"无从回答。现在 drainTurn 每收到一次 usage 块就落一行 usage 事件
// （ReAct 一轮多次模型调用 = 多行，聚合即累计），Meta 扫描时求和。
package app

import (
	"tiancode/internal/core/session"
	"tiancode/internal/platform/applog"
)

// RecordUsage 把一次上游 usage 落进会话账本（统计事件，绝不影响对话重放）。
// 失败只记日志不阻断：统计缺一行可以接受，为它打断正在进行的对话不行——
// 与"审批答复失败必须上抛"不同类，这里没有用户在等一个必须成功的结果。
func (s *ChatService) RecordUsage(sessionID string, prompt, completion, total int64) {
	l, err := s.ledgerFor(sessionID)
	if err != nil {
		applog.Errorf("usage 落账失败（会话不可用）session=%s err=%v", sessionID, err)
		return
	}
	if _, err := l.Append(session.EventUsage, map[string]int64{
		"prompt": prompt, "completion": completion, "total": total,
	}); err != nil {
		applog.Errorf("usage 落账失败 session=%s err=%v", sessionID, err)
	}
}
