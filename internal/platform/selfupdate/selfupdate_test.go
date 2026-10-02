package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.0.20", "0.0.19", 1},
		{"v0.0.20", "0.0.19", 1},
		{"0.0.19", "0.0.19", 0},
		{"0.0.9", "0.0.10", -1}, // 数字比较，不是字典序
		{"0.1.0", "0.0.99", 1},
		{"1.0", "1.0.0", 0},           // 缺段按 0
		{"0.0.20-beta", "0.0.19", -1}, // 非数字段按 0（宽容解析不 panic）；发布流程不打预发布 tag，此语义不会被走到
		{"dev", "0.0.99", -1},         // 非数字段按 0；"dev" 的比较在 Check 里被短路，这里只锁宽容解析
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if (got > 0) != (c.want > 0) || (got < 0) != (c.want < 0) {
			t.Fatalf("CompareVersions(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheck_FindsSetupAssetAndCompares(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/repos/") || !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v0.0.20",
			"html_url": "https://github.com/x/releases/tag/v0.0.20",
			"assets": [
				{"name": "tiancode-v0.0.20-portable.zip", "browser_download_url": "` + srv.URL + `/dl/zip", "size": 1},
				{"name": "tiancode-setup-v0.0.20.exe", "browser_download_url": "` + srv.URL + `/dl/setup", "size": 123}
			]
		}`))
	}))
	defer srv.Close()

	// 把包级 API 基址换成测试服务器：小变量注入，避免为测试改产品常量
	restore := apiBase
	apiBase = srv.URL + "/api/repos/" + Repo + "/releases/latest"
	defer func() { apiBase = restore }()

	info, err := Check("0.0.19", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasUpdate || info.Latest != "0.0.20" {
		t.Fatalf("应判定有更新：%+v", info)
	}
	if info.AssetName != "tiancode-setup-v0.0.20.exe" || info.AssetSize != 123 || info.AssetURL == "" {
		t.Fatalf("应选中安装包资产：%+v", info)
	}
	// 同版本：无更新
	info, err = Check("0.0.20", nil)
	if err != nil || info.HasUpdate {
		t.Fatalf("同版本不应有更新：%+v %v", info, err)
	}
	// dev 构建：不检查、无更新
	info, err = Check("dev", nil)
	if err != nil || info.HasUpdate || info.Latest != "" {
		t.Fatalf("dev 构建不应给出更新：%+v %v", info, err)
	}
}

func TestDownload_WritesFileAndValidatesSize(t *testing.T) {
	body := strings.Repeat("Mz", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "setup.exe")
	if err := Download(context.Background(), srv.URL+"/setup", dest, "", int64(len(body)), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != body {
		t.Fatalf("下载内容不符：%v", err)
	}
	// 尺寸不匹配 → 删除并报错（半截安装包绝不能交给安装器）
	if err := Download(context.Background(), srv.URL+"/setup", dest, "", 999, nil); err == nil {
		t.Fatal("尺寸不匹配必须报错")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("失败的下载必须删除残留文件")
	}
}

// 0.0.21：带 .sha256 清单的强校验路径——匹配放行、不匹配删除；
// 旧 release（shaURL 为空）回退尺寸校验，行为不变。
func TestDownload_SHA256(t *testing.T) {
	body := []byte("installer-bytes-0.0.21")
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	bad := strings.Repeat("0", 64)

	var mu sync.Mutex
	servedSHA := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			mu.Lock()
			fmt.Fprintf(w, "%s  tiancode-setup.exe\n", servedSHA) // sha256sum 标准行
			mu.Unlock()
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "setup.exe")
	base := srv.URL

	// 哈希匹配：放行，文件保留
	if err := Download(context.Background(), base+"/setup", dest, base+"/setup.exe.sha256", int64(len(body)), nil); err != nil {
		t.Fatalf("哈希匹配必须放行：%v", err)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != string(body) {
		t.Fatalf("放行的包内容不符：%v", err)
	}

	// 哈希不匹配：删除 + 报错
	mu.Lock()
	servedSHA = bad
	mu.Unlock()
	if err := Download(context.Background(), base+"/setup", dest, base+"/setup.exe.sha256", int64(len(body)), nil); err == nil {
		t.Fatal("哈希不匹配必须报错")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("校验失败的包必须删除")
	}

	// 清单内容坏（非 64 位哈希）：硬失败
	mu.Lock()
	servedSHA = "short"
	mu.Unlock()
	if err := Download(context.Background(), base+"/setup", dest, base+"/setup.exe.sha256", int64(len(body)), nil); err == nil {
		t.Fatal("清单格式不对必须报错")
	}

	// 旧 release 无清单（shaURL 空）：回退尺寸校验，行为不变
	mu.Lock()
	servedSHA = good
	mu.Unlock()
	if err := Download(context.Background(), base+"/setup", dest, "", int64(len(body)), nil); err != nil {
		t.Fatalf("无清单回退必须可用：%v", err)
	}
}
