package exttools

import (
	"net"
	"testing"

	"tiancode/internal/platform/mcpclient"
)

// 审计 R7：dropClient 的读-比-删只持锁一瞬间——hub 里已是新实例时绝不误删；
// 但**无论是否在 hub 都要关掉自己的实例**（被替换者不关 = 无主进程）。
func TestDropClient_NeverRemovesDifferentInstance(t *testing.T) {
	mcp := NewMCP(nil)

	leftA, rightA := net.Pipe()
	defer leftA.Close()
	defer rightA.Close()
	clientA := mcpclient.New(leftA)

	leftB, rightB := net.Pipe()
	defer leftB.Close()
	defer rightB.Close()
	clientB := mcpclient.New(leftB)

	mcp.hub.Store("srv", clientA)

	// 用"另一个实例"去摘除：hub 里的 A 完好；自己（B）被关闭（无主进程不残留）
	if err := mcp.dropClient("srv", clientB); err != nil {
		t.Fatalf("被替换路径的关闭不应失败：%v", err)
	}
	if _, ok := mcp.hub.Load("srv"); !ok {
		t.Fatal("hub 里的实例不应被误删")
	}

	// 用同一实例摘除：hub 移除且实例被关闭
	if err := mcp.dropClient("srv", clientA); err != nil {
		t.Fatalf("同实例摘除应成功：%v", err)
	}
	if _, ok := mcp.hub.Load("srv"); ok {
		t.Fatal("同实例摘除后 hub 应为空")
	}
}
