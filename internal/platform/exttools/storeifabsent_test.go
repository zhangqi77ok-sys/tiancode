package exttools

import (
	"net"
	"testing"

	"tiancode/internal/platform/mcpclient"
)

// 0.2.37 审计：Probe 改连接表必须与并发调用安全交错——
//   - storeIfAbsent：hub 已有实例时不覆盖（落败方关闭自己刚建的实例）；
//   - dropClient：同实例摘除、异实例只关自己（既有语义，此处锁死与 storeIfAbsent 组合）。
func TestStoreIfAbsent_NeverOverwritesExistingInstance(t *testing.T) {
	mcp := NewMCP(nil)

	mkClient := func() *mcpclient.Client {
		l, r := net.Pipe()
		t.Cleanup(func() { l.Close(); r.Close() })
		return mcpclient.New(l)
	}
	leftA, rightA := net.Pipe()
	defer leftA.Close()
	defer rightA.Close()
	clientA := mcpclient.New(leftA)
	mcp.hub.Store("srv", clientA)

	newC := mkClient()
	winner, mine, err := mcp.storeIfAbsent("srv", newC)
	if err != nil {
		t.Fatalf("落败路径不应报错：%v", err)
	}
	if mine {
		t.Fatal("hub 已有实例时不得宣称自己赢了")
	}
	if winner != clientA {
		t.Fatal("hub 里应保留原实例")
	}
	if _, ok := mcp.hub.Load("srv"); !ok {
		t.Fatal("原实例不得被覆盖删除")
	}

	// 空位路径：写入成功且留在 hub
	clientB := mkClient()
	winner, mine, err = mcp.storeIfAbsent("srv2", clientB)
	if err != nil || !mine || winner != clientB {
		t.Fatalf("空位写入 = %v mine=%v winner==B=%v", err, mine, winner == clientB)
	}
	if v, ok := mcp.hub.Load("srv2"); !ok || v != clientB {
		t.Fatal("空位写入后 hub 应保留该实例")
	}
}
