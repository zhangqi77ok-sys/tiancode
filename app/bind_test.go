package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// 回归锁定：绑定方法不得声明 context.Context 参数。
//
// 为什么：本版本 Wails 的 boundMethod.ParseArgs 要求 JS 实参个数**严格等于**方法声明参数个数，
// 且不做 ctx 注入。曾把 ctx 写成 Send 的首参，结果每次发送都失败：
//
//	error parsing arguments: received 2 arguments to method 'app.Bind.Send', expected 3
//
// 应用上下文改由 AppCtx 字段承载（字段不参与绑定校验）。
func TestBindMethods_MustNotTakeContext(t *testing.T) {
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	// 注意用指针类型：Bind 的方法都是指针接收者，结构体类型的方法集为空
	bindType := reflect.TypeOf(&Bind{})

	checked := 0
	for i := 0; i < bindType.NumMethod(); i++ {
		m := bindType.Method(i)
		if !m.IsExported() {
			continue
		}
		checked++
		for j := 0; j < m.Type.NumIn(); j++ {
			if m.Type.In(j) == ctxType {
				t.Fatalf("绑定方法 %s 的第 %d 个参数是 context.Context：Wails 不会注入 ctx，"+
					"前端调用必然报参数个数不匹配；请改用 Bind.AppCtx", m.Name, j+1)
			}
		}
	}
	if checked == 0 {
		t.Fatal("未发现任何导出方法：测试写错了（Bind 至少应有 Send/ListSessions 等）")
	}
}

// 无 AppCtx 时不得 panic（事件桥拿到 Background 也不能打断数据流）。
func TestBind_AppCtxFallback(t *testing.T) {
	b := New(nil)
	if b.appCtx() == nil {
		t.Fatal("appCtx 必须永不返回 nil")
	}
	if got := b.appCtx().Err(); got != nil {
		t.Fatalf("退化上下文不应已取消：%v", got)
	}
}

// 断言 IPC 端点清单稳定：前端契约（wails.ts）与本清单一一对应，
// 少一个会让 UI 静默失效，多一个说明有未接线方法。
func TestBind_SurfaceIsExpected(t *testing.T) {
	want := []string{
		"AddChannel", "ChannelPresets", "DeleteChannel", "DeleteSession", "DiscoverModels",
		"ExportSessionMarkdown", "GetWorkspace", "ListChannels", "ListSessionSummaries",
		"ListSessions", "RenameSession", "Replay", "Send", "SetActiveChannel",
		"SetWorkspace", "Stop", "UpdateChannel",
	}
	bindType := reflect.TypeOf(&Bind{})
	got := make([]string, 0, bindType.NumMethod())
	for i := 0; i < bindType.NumMethod(); i++ {
		got = append(got, bindType.Method(i).Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("IPC 端点清单变了：\n got %v\nwant %v\n（同步更新 frontend/src/wails.ts）", got, want)
	}
}
