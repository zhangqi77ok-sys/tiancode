package astguard

import (
	"context"
	"encoding/json"
	"testing"

	v1 "tiancode/pkg/plugin/v1"
)

func writeArgs(action, path, content string) []byte {
	b, _ := json.Marshal(map[string]any{"action": action, "path": path, "content": content})
	return b
}

func TestOnBeforeAct_AllowsValidGo(t *testing.T) {
	r := New()
	valid := "package main\n\nfunc main() { println(\"hi\") }\n"
	d, err := r.OnBeforeAct(context.Background(), "s1", "tool.fs", writeArgs("write", "main.go", valid))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow {
		t.Fatalf("合法 Go 应被放行，却拦截: %s", d.Reason)
	}
}

func TestOnBeforeAct_BlocksInvalidGo(t *testing.T) {
	r := New()
	bad := "package main\nfunc main( { println(\"hi\")\n"
	d, err := r.OnBeforeAct(context.Background(), "s1", "tool.fs", writeArgs("write", "main.go", bad))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("非法 Go 必须被拦截")
	}
	if !d.Intercepted {
		t.Fatal("期望 Intercepted=true")
	}
}

func TestOnBeforeAct_NonGoPassthrough(t *testing.T) {
	r := New()
	d, _ := r.OnBeforeAct(context.Background(), "s1", "tool.fs", writeArgs("write", "README.md", "not go at all"))
	if !d.Allow {
		t.Fatalf("非 .go 文件应放行: %s", d.Reason)
	}
}

func TestOnBeforeAct_ReplacePassthrough(t *testing.T) {
	r := New()
	// replace 动作仅给替换片段，无法做整文件语法校验，应放行
	bad := "func main( {"
	d, _ := r.OnBeforeAct(context.Background(), "s1", "tool.fs", writeArgs("replace", "main.go", bad))
	if !d.Allow {
		t.Fatalf("replace 动作应放行(全量内容不可用): %s", d.Reason)
	}
}

func TestOnBeforeAct_NonFsPassthrough(t *testing.T) {
	r := New()
	d, _ := r.OnBeforeAct(context.Background(), "s1", "tool.git", writeArgs("write", "main.go", "x"))
	if !d.Allow {
		t.Fatalf("非 fs 工具应放行: %s", d.Reason)
	}
}

func TestRailMetadata(t *testing.T) {
	r := New()
	if r.ID() != "rail.astguard" || r.Type() != v1.TypeRail || r.Priority() != 90 {
		t.Fatalf("rail 元数据不符: id=%s type=%s prio=%d", r.ID(), r.Type(), r.Priority())
	}
}
