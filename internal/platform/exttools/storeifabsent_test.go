package exttools

import (
	"fmt"
	"net"
	"sync"
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

// 0.0.05：复刻用户指出的竞窗场景——storeIfAbsent 曾是"hubMu 锁内 Load→Store"
// 两步，而 stdioClient 的 LoadOrStore 不拿 hubMu：两步之间另一路可先放入 A，
// 随后这次 Store 用 B 盖掉（A 不在表里也没人 Close）。改为单次原子 LoadOrStore
// 后：N 路并发（含模拟 stdioClient 的裸 LoadOrStore）下 hub 恰好一份、其余全部
// 落败（收到非自己实例或 mine=false），绝不出现"写入被覆盖丢失"。
func TestStoreIfAbsent_ConcurrentWithLoadOrStore_NoLostWrite(t *testing.T) {
	mcp := NewMCP(nil)
	const n = 32

	// 落败方需要 Close：net.Pipe 客户端 Close 安全（无真实进程）
	mkClient := func() *mcpclient.Client {
		l, r := net.Pipe()
		t.Cleanup(func() { l.Close(); r.Close() })
		return mcpclient.New(l)
	}

	const rounds = 20
	for round := 0; round < rounds; round++ {
		name := fmt.Sprintf("srv-%d", round)
		var wg sync.WaitGroup
		start := make(chan struct{})
		winners := make([]*mcpclient.Client, n)
		mine := make([]bool, n)

		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := mkClient()
				<-start
				// 偶数路走 storeIfAbsent（Probe 形态），奇数路走裸 LoadOrStore
				//（stdioClient 形态）——两条写入路径与 0.0.04 的竞窗完全同构
				if i%2 == 0 {
					w, m, err := mcp.storeIfAbsent(name, c)
					if err != nil {
						t.Errorf("round %d: %v", round, err)
						return
					}
					winners[i], mine[i] = w, m
				} else {
					actual, loaded := mcp.hub.LoadOrStore(name, c)
					if loaded {
						winners[i] = actual.(*mcpclient.Client)
						_ = c.Close() // 落败方自关（stdioClient 同款）
					} else {
						winners[i], mine[i] = c, true
					}
				}
			}(i)
		}
		close(start)
		wg.Wait()

		// hub 里恰好一份，且所有胜者指向同一实例（没有任何一路的写入被覆盖丢失）
		v, ok := mcp.hub.Load(name)
		if !ok {
			t.Fatalf("round %d: hub 空了", round)
		}
		theOne := v.(*mcpclient.Client)
		for i := 0; i < n; i++ {
			if winners[i] == nil {
				continue
			}
			if winners[i] != theOne {
				t.Fatalf("round %d: 第 %d 路拿到的赢家与 hub 不一致（写入被覆盖丢失）", round, i)
			}
		}
		// 清场供下一轮
		mcp.hub.Delete(name)
	}
}
