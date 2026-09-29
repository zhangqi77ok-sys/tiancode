package exttools

import (
	"net"
	"testing"

	"tiancode/internal/platform/mcpclient"
)

// 审计#9：dropClient 的读-比-删在 hubMu 内——hub 里已是新实例时，
// 摘除旧实例的操作绝不误伤新实例（此前 LoadAndDelete+Store 有覆盖窗口）。
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

	// 用"另一个实例"去摘除：必须被拒绝，hub 里的 A 完好
	if mcp.dropClient("srv", clientB) {
		t.Fatal("不同实例的摘除应被拒绝")
	}
	if _, ok := mcp.hub.Load("srv"); !ok {
		t.Fatal("hub 里的实例不应被误删")
	}

	// 用同一实例摘除：成功且已关闭
	if !mcp.dropClient("srv", clientA) {
		t.Fatal("同实例的摘除应成功")
	}
	if _, ok := mcp.hub.Load("srv"); ok {
		t.Fatal("同实例摘除后 hub 应为空")
	}
}
