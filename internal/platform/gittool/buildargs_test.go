package gittool

import (
	"reflect"
	"testing"
)

// 0.0.06：log 必须尊重 path（pathspec 跟在 "--" 后），limit 有 50 硬顶；
// status/diff 的 path 行为不变（既有用例覆盖，这里一并锁死参数形态）。
func TestBuildArgs_LogWithPath(t *testing.T) {
	cases := []struct {
		name   string
		action string
		path   string
		limit  int
		want   []string
	}{
		{"log 带 path", "log", "src/a.go", 0,
			[]string{"log", "--oneline", "-n", "10", "--", "src/a.go"}},
		{"log 硬顶 50", "log", "", 100,
			[]string{"log", "--oneline", "-n", "50"}},
		{"log 缺省 10", "log", "", 0,
			[]string{"log", "--oneline", "-n", "10"}},
		{"log 自定义", "log", "pkg", 3,
			[]string{"log", "--oneline", "-n", "3", "--", "pkg"}},
		{"status 带 path", "status", "src", 0,
			[]string{"status", "--porcelain", "--", "src"}},
		{"diff 带 path", "diff", "src", 0,
			[]string{"diff", "--", "src"}},
		{"diff 无 path", "diff", "", 0,
			[]string{"diff"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := buildArgs(c.action, c.path, c.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("args = %v, want %v", got, c.want)
			}
		})
	}
}
