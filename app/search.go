// 跨会话搜索绑定（0.0.23）：Ctrl+Shift+F 面板的数据源。
package app

import "tiancode/internal/app"

// SearchSessionsResult 是一次跨会话搜索的响应（hits 按最后活跃倒序）。
type SearchSessionsResult struct {
	Hits  []app.SearchHit `json:"hits"`
	Query string          `json:"query"`
}

// SearchSessions 在所有会话账本里搜 query（大小写不敏感子串）。
// workspace 非空 = 只搜该工作区（纯对话会话传空串）。
// 只读：绝不打开账本写句柄，坏行/半行跳过（回合进行中也安全）。
func (b *Bind) SearchSessions(query, workspace string, limit int) (SearchSessionsResult, error) {
	hits, err := b.chat.SearchSessions(query, workspace, limit)
	if err != nil {
		return SearchSessionsResult{}, err
	}
	if hits == nil {
		hits = []app.SearchHit{} // 空结果序列化为 [] 而不是 null（前端直接 map）
	}
	return SearchSessionsResult{Hits: hits, Query: query}, nil
}
