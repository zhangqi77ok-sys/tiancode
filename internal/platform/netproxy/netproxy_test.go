// netproxy 测试（0.0.19 底层债补齐：本包此前零测试）。
// 覆盖四条分支：空代理直连、合法代理解析+缓存复用、非法地址显式报错、
// 不支持的协议显式报错（"配了代理仍直连"是 0.2.21 实机事故，绝不静默降级）；
// 以及 Reset 后缓存清空（代理配置变更必须换连接池，不能复用旧代理的连接）。
package netproxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestClient_EmptyProxyReturnsBase(t *testing.T) {
	base := &http.Client{}
	c, err := Client(base, "")
	if err != nil {
		t.Fatalf("空代理不应报错：%v", err)
	}
	if c != base {
		t.Fatal("空代理必须原样返回 base（直连）")
	}
	// base 为 nil：回落默认客户端（也不报错）
	if c, err = Client(nil, ""); err != nil || c == nil {
		t.Fatalf("空代理 + nil base 应回落默认客户端：%v", err)
	}
}

func TestClient_ValidProxyCached(t *testing.T) {
	Reset()
	const proxy = "http://127.0.0.1:19999"
	c1, err := Client(nil, proxy)
	if err != nil {
		t.Fatalf("合法代理不应报错：%v", err)
	}
	if c1 == http.DefaultClient {
		t.Fatal("代理客户端不能是默认客户端（必须带代理 Transport）")
	}
	c2, err := Client(nil, proxy)
	if err != nil {
		t.Fatalf("第二次取客户端不应报错：%v", err)
	}
	if c1 != c2 {
		t.Fatal("同一代理必须复用同一客户端（连接池）")
	}
	// 带空格的输入照样命中缓存（TrimSpace 语义）
	c3, err := Client(nil, "  "+proxy+"  ")
	if err != nil || c3 != c1 {
		t.Fatalf("带空格代理未复用缓存：%v", err)
	}
}

func TestClient_InvalidProxyErrors(t *testing.T) {
	for _, bad := range []string{"not a url", "http://"} {
		_, err := Client(nil, bad)
		if err == nil {
			t.Fatalf("非法代理 %q 必须显式报错（绝不静默直连）", bad)
		}
		if !strings.Contains(err.Error(), "代理地址无效") {
			t.Fatalf("错误应说明代理地址无效：%v", err)
		}
	}
}

func TestClient_UnsupportedSchemeErrors(t *testing.T) {
	for _, bad := range []string{"socks5://127.0.0.1:1080", "ftp://x:21"} {
		_, err := Client(nil, bad)
		if err == nil {
			t.Fatalf("协议 %q 必须显式报错", bad)
		}
		if !strings.Contains(err.Error(), "不支持的代理协议") {
			t.Fatalf("错误应说明协议不支持：%v", err)
		}
	}
	// 无 Host 的 URL 在"地址无效"分支被拒（同样显式报错，只是文案不同）
	if _, err := Client(nil, "file:///etc"); err == nil {
		t.Fatal("file 协议必须显式报错")
	}
	// HTTPS 代理是合法的
	if _, err := Client(nil, "https://127.0.0.1:19999"); err != nil {
		t.Fatalf("https 代理合法：%v", err)
	}
}

func TestReset_ClearsCache(t *testing.T) {
	Reset()
	const proxy = "http://127.0.0.1:19998"
	c1, err := Client(nil, proxy)
	if err != nil {
		t.Fatal(err)
	}
	Reset()
	c2, err := Client(nil, proxy)
	if err != nil {
		t.Fatal(err)
	}
	if c1 == c2 {
		t.Fatal("Reset 后必须重建客户端（旧代理的连接池不能复用）")
	}
}
