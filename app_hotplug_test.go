package main

import (
	"encoding/json"
	"testing"
)

func TestHotplugDashboard(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("failed to instantiate app")
	}

	report, err := app.GetHotplugDashboard()
	if err != nil {
		t.Fatalf("GetHotplugDashboard failed: %v", err)
	}

	if report == nil {
		t.Fatal("expected non-nil report")
	}

	// 验证微内核算子已注入并可查询
	if report.Summary.TotalTools == 0 {
		t.Errorf("expected registered tools > 0, got %d", report.Summary.TotalTools)
	}
	if report.Summary.ActiveRails == 0 {
		t.Errorf("expected registered rails > 0, got %d", report.Summary.ActiveRails)
	}
	if report.Summary.ActiveProviders == 0 {
		t.Errorf("expected registered providers > 0, got %d", report.Summary.ActiveProviders)
	}

	// 检查特定核心算子是否存在
	toolFound := false
	for _, tool := range report.Tools {
		if tool.ID == "tool.fs" || tool.ID == "tool.git" {
			toolFound = true
			if !tool.Healthy {
				t.Errorf("tool %s should be healthy", tool.ID)
			}
			break
		}
	}
	if !toolFound {
		t.Error("expected tool.fs or tool.git to be registered in tools list")
	}

	// 检查 SafetyRail
	railFound := false
	for _, rail := range report.Rails {
		if rail.ID == "rail.safety" {
			railFound = true
			if rail.Priority != 100 {
				t.Errorf("expected rail.safety priority 100, got %d", rail.Priority)
			}
			break
		}
	}
	if !railFound {
		t.Error("expected rail.safety to be present in rails list")
	}

	// 测试单点探活
	itemInfo, err := app.ProbeHotplugItem("tool.fs", "tool")
	if err != nil {
		t.Fatalf("ProbeHotplugItem failed: %v", err)
	}
	if itemInfo.ID != "tool.fs" {
		t.Errorf("expected probed item ID tool.fs, got %s", itemInfo.ID)
	}

	// 测试导出清单
	manifest, err := app.ExportHotplugManifest()
	if err != nil {
		t.Fatalf("ExportHotplugManifest failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(manifest), &parsed); err != nil {
		t.Fatalf("exported manifest is not valid JSON: %v", err)
	}

	// 测试热重载 (热重载会同步 MCP 服务配置并加载活跃外部算子)
	reloaded, err := app.ReloadHotplugRegistry()
	if err != nil {
		t.Fatalf("ReloadHotplugRegistry failed: %v", err)
	}
	if reloaded.Summary.TotalTools < report.Summary.TotalTools {
		t.Errorf("expected reloaded tools count >= %d, got %d", report.Summary.TotalTools, reloaded.Summary.TotalTools)
	}
}
