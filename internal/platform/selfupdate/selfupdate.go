// Package selfupdate 实现应用自更新（GitHub Releases 为源）。
//
// 做什么：查最新 release（GitHub API）→ 版本比较 → 下载安装包到临时目录。
// 真正的安装复用既有安装器（`-quiet -relaunch`：优雅关闭旧版 → 覆盖安装 → 重启）。
//
// 完整性边界（0.0.21 升级）：发布流程随包上传 `.sha256` 清单资产，下载后验哈希
// ——不匹配的包当场删除并报错。旧 release 没有 `.sha256` 资产时回退为"TLS + 尺寸"
// 旧行为（wantSHA 为空，调用方写日志留痕）；0.0.21 起的 release 全部强制验哈希。
//
// 为什么单独成包：netproxy 同理——出网走全局代理（与 gateway 同源），且版本比较
// 是纯函数要单测；Bind 层只编排（检查 → 询问用户 → 下载 → 拉起安装器 → 退出）。
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tiancode/internal/platform/netproxy"
)

// Repo 是 GitHub 仓库（release 资产的唯一来源）。
const Repo = "zhangqi77ok-sys/tiancode"

const (
	checkTimeout = 20 * time.Second
	// downloadTimeout 按带宽 2MB/s 估算：安装包 ~18MB，60s 内拿不到就是网络问题。
	downloadTimeout = 120 * time.Second
	// maxAssetBytes 是安装包下载上限：现在的包 ~18MB，给 128MB 硬顶防失控。
	maxAssetBytes = 128 << 20
)

// Info 是一次更新检查的结果。
type Info struct {
	Current     string `json:"current"`   // 当前运行版本（如 0.0.19）
	Latest      string `json:"latest"`    // 最新 release 版本（如 0.0.20）
	HasUpdate   bool   `json:"hasUpdate"` // Latest > Current
	PageURL     string `json:"pageUrl"`   // release 页面（用户手动下载的出口）
	AssetName   string `json:"assetName"` // 安装包资产名（空 = 该 release 没挂安装包）
	AssetURL    string `json:"assetUrl"`
	AssetSize   int64  `json:"assetSize"`
	AssetSHA256 string `json:"assetSha256"` // .sha256 清单资产的下载地址（0.0.21；空 = 旧 release 无清单，回退尺寸校验）
}

// releasesLatest 是 GitHub API 的响应切片（只解需要的字段）。
type releasesLatest struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// apiBase 故意做成包级变量：测试把它指到 httptest 服务器，绝不真打 GitHub。
var apiBase = "https://api.github.com/repos/" + Repo + "/releases/latest"

// Check 查询最新 release 并与当前版本比较。current 为 "dev"（本地构建）时
// HasUpdate 恒 false——没有基线就不胡乱建议升级。
func Check(current string, proxy func() string) (Info, error) {
	info := Info{Current: current}
	if strings.TrimSpace(current) == "" || current == "dev" {
		return info, nil
	}
	client, err := netproxy.Client(nil, proxyStr(proxy))
	if err != nil {
		return info, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tiancode-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return info, fmt.Errorf("检查更新失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("检查更新失败：GitHub 返回 %s", resp.Status)
	}
	var rel releasesLatest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return info, fmt.Errorf("解析更新信息失败：%w", err)
	}
	if rel.TagName == "" {
		return info, fmt.Errorf("GitHub 返回的 release 缺少 tag_name")
	}
	info.Latest = strings.TrimPrefix(rel.TagName, "v")
	info.PageURL = rel.HTMLURL
	info.HasUpdate = CompareVersions(info.Latest, info.Current) > 0
	// 找安装包资产：tiancode-setup-v<版本>.exe（0.0.21 起同 release 挂同名 .sha256 清单）
	want := "tiancode-setup-v" + info.Latest + ".exe"
	for _, a := range rel.Assets {
		if a.Name == want {
			info.AssetName, info.AssetURL, info.AssetSize = a.Name, a.BrowserDownloadURL, a.Size
		}
		if a.Name == want+".sha256" {
			info.AssetSHA256 = a.BrowserDownloadURL
		}
	}
	return info, nil
}

// Download 把安装包下载到 dest（临时目录内，调用方负责拉起安装器后清理）。
// shaURL 非空时下载该清单并验哈希（0.0.21）：不匹配的包当场删除并报错——
// 半截/被篡改的安装包绝不进入安装器。shaURL 为空（旧 release 无清单）回退为
// 仅尺寸校验，由调用方写日志留痕。尺寸超限/不匹配都显式报错。
func Download(ctx context.Context, url, dest, shaURL string, wantSize int64, proxy func() string) error {
	client, err := netproxy.Client(nil, proxyStr(proxy))
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "tiancode-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载更新失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载更新失败：GitHub 返回 %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxAssetBytes+1))
	// 先关句柄再做失败清理：Windows 上删除已打开的文件会静默失败，
	// 半截安装包残留在临时目录就会被人误用。
	closeErr := f.Close()
	if err != nil {
		return abandon(dest, fmt.Errorf("下载中断：%w", err))
	}
	if closeErr != nil {
		return abandon(dest, fmt.Errorf("写入临时文件失败：%w", closeErr))
	}
	if n > maxAssetBytes {
		return abandon(dest, fmt.Errorf("安装包超过 %d MB 上限，已中止", maxAssetBytes>>20))
	}
	if wantSize > 0 && n != wantSize {
		return abandon(dest, fmt.Errorf("下载不完整（%d/%d 字节），已删除，可重试", n, wantSize))
	}
	// 验哈希（0.0.21）：清单非空才算强校验路径；解析失败/哈希不匹配都是硬失败——
	// 宁可重下，绝不把来路不明的包交给安装器（安装器以当前用户权限运行）。
	if shaURL != "" {
		wantHash, err := fetchSHA256(ctx, client, shaURL)
		if err != nil {
			return abandon(dest, fmt.Errorf("获取校验清单失败：%w", err))
		}
		got, err := fileSHA256(dest)
		if err != nil {
			return abandon(dest, fmt.Errorf("计算安装包哈希失败：%w", err))
		}
		if !strings.EqualFold(got, wantHash) {
			return abandon(dest, fmt.Errorf("安装包校验失败（sha256 不匹配，可能下载损坏）：已删除，可重试"))
		}
	}
	return nil
}

// fetchSHA256 下载 .sha256 清单并解析出十六进制哈希。兼容两种行格式：
// 裸哈希、"hash  文件名"（sha256sum 标准输出）；取第一个空白分隔字段。
func fetchSHA256(ctx context.Context, client *http.Client, shaURL string) (string, error) {
	sctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, shaURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tiancode-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub 返回 %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("校验清单格式不对（应为 64 位十六进制哈希）")
	}
	return fields[0], nil
}

// fileSHA256 计算本地文件 SHA256（十六进制小写）。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// abandon 删除失败的半截下载并返回主错误；清理失败（权限/占用）不吞主错误，
// 但要留痕——残留会被误用，排障时必须看得见。
func abandon(dest string, cause error) error {
	if rmErr := os.Remove(dest); rmErr != nil {
		cause = fmt.Errorf("%w（且残留文件清理失败：%v）", cause, rmErr)
	}
	return cause
}

// CompareVersions 比较点分版本号（"0.0.19" vs "v0.0.20"，前导 v 容忍）：
// 返回 >0 / 0 / <0。非数字段按 0 处理（宽容解析，绝不 panic）。
func CompareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

func splitVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

func proxyStr(proxy func() string) string {
	if proxy == nil {
		return ""
	}
	return proxy()
}
