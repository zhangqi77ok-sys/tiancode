package agent

import "testing"

// 阶段 5-2：预算的"来源"（渠道声明 vs 渠道未声明时的默认值）只影响读数，不改变折叠。
// 油表要能写明「未配置，按默认值」——否则用户会以为渠道里填过这个数。
func TestContextEvent_CarriesBudgetSource(t *testing.T) {
	byDefault := NewLoop(nil, "m", nil)
	byDefault.SetContextBudget(32000, true)
	ev := byDefault.contextEvent(DeriveInfo{EstimatedTokens: 100, BudgetTokens: 32000})
	if ev == nil {
		t.Fatal("配了预算就该上报读数（界面要显示剩余比例）")
	}
	if ev.BudgetTokens != 32000 || !ev.BudgetDefault {
		t.Fatalf("默认预算必须标记来源：%+v", ev)
	}

	declared := NewLoop(nil, "m", nil)
	declared.SetContextBudget(32000, false)
	ev2 := declared.contextEvent(DeriveInfo{EstimatedTokens: 100, BudgetTokens: 32000})
	if ev2 == nil || ev2.BudgetDefault {
		t.Fatalf("渠道声明的预算不该标成默认：%+v", ev2)
	}

	// 没配预算且没折叠：保持旧行为（不上报，零噪声）
	if ev := NewLoop(nil, "m", nil).contextEvent(DeriveInfo{EstimatedTokens: 100}); ev != nil {
		t.Fatalf("无预算无折叠时不该上报：%+v", ev)
	}

	// 预算传 0 时来源标记必须归零：不能出现"标记为默认但预算为 0"的矛盾状态
	zero := NewLoop(nil, "m", nil)
	zero.SetContextBudget(0, true)
	if zero.ctxBudgetDefault {
		t.Fatal("预算为 0 时不该保留默认来源标记")
	}
}
