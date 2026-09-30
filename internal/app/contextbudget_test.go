package app

import "testing"

// 阶段 5-2：渠道未声明上下文上限时必须有保守默认预算——"不折叠"等于每轮重发整段历史
// （实测请求体 510KB 里约 400KB 是历史，上游 90 秒不吐首字节）。用户填了就以用户的为准。
func TestResolveContextBudget(t *testing.T) {
	// 渠道声明了：原样采用，且标记为"非默认"
	if got, byDefault := resolveContextBudget(100000); got != 100000 || byDefault {
		t.Fatalf("渠道声明应原样采用：got=%d byDefault=%v", got, byDefault)
	}
	// 一条都没声明（MinContextLimit 的 0）与非法负值：退回保守默认值并标记来源
	for _, declared := range []int{0, -1} {
		got, byDefault := resolveContextBudget(declared)
		if got != defaultContextLimit || !byDefault {
			t.Fatalf("未声明应退回默认预算：declared=%d got=%d byDefault=%v", declared, got, byDefault)
		}
	}
	// 默认值必须是"保守"的：比常见云模型窗口小，落在实测能建流的体量一侧
	if defaultContextLimit <= 0 || defaultContextLimit > 64<<10 {
		t.Fatalf("默认预算取值不合理：%d", defaultContextLimit)
	}
}
