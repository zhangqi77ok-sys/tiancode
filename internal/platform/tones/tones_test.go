package tones

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// practiceOf 取某条内置语气的一句做法（断言"文本里到底带了哪条"用）。
func practiceOf(t *testing.T, id string) string {
	t.Helper()
	e, ok := lookup(id)
	if !ok {
		t.Fatalf("内置语气里没有 %q", id)
	}
	return e.Practice
}

// 缺文件 = 默认设置：fixed + plain，不停用任何一条（首启就是这个状态）。
func TestLoad_MissingFileIsFixedPlain(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "tones.json"))
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != ModeFixed || f.Default != DefaultID || len(f.Disabled) != 0 {
		t.Fatalf("缺文件应为 fixed + plain + 无停用：%+v", f)
	}
	text := Section(f)
	if !strings.Contains(text, practiceOf(t, DefaultID)) {
		t.Fatalf("缺文件时系统提示应带 plain 的做法：%s", text)
	}
	if strings.Contains(text, "自动选择") {
		t.Fatalf("缺文件时是固定模式：%s", text)
	}
}

// 非法值回落到 fixed + plain：手改文件、旧版本残留都可能带非法值。
func TestLoad_IllegalValuesFallBack(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want File
	}{
		{"mode 乱写", `{"mode":"weird","default":"plain","disabled":[]}`, File{Mode: ModeFixed, Default: DefaultID}},
		{"default 不存在", `{"mode":"auto","default":"nope","disabled":[]}`, File{Mode: ModeAuto, Default: DefaultID}},
		{"default 被停用", `{"mode":"fixed","default":"plain","disabled":["plain"]}`, File{Mode: ModeFixed, Default: "concise"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tones.json")
			if err := os.WriteFile(path, []byte(c.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := New(path).Load()
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != c.want.Mode || got.Default != c.want.Default {
				t.Fatalf("回落结果 = %s/%s, want %s/%s", got.Mode, got.Default, c.want.Mode, c.want.Default)
			}
			if got.Disabled == nil {
				t.Fatal("停用列表不得为 nil（界面按数组渲染）")
			}
		})
	}
}

// 坏文件必须报错，不能静默当成"没有语气"（用户会以为自己是照设置回答的）。
func TestLoad_BrokenFileIsVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tones.json")
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path).Load(); err == nil {
		t.Fatal("坏文件必须报错")
	}
}

// 固定模式只带默认那一条的做法；其余 49 条的做法一个字都不出现。
func TestSection_FixedOnlyDefault(t *testing.T) {
	text := Section(File{Mode: ModeFixed, Default: "plain"})
	if !strings.Contains(text, practiceOf(t, "plain")) {
		t.Fatalf("缺默认做法：%s", text)
	}
	for _, e := range Builtin {
		if e.ID == DefaultID {
			continue
		}
		if strings.Contains(text, e.Practice) {
			t.Fatalf("固定模式不该出现 %s 的做法：%s", e.ID, text)
		}
	}
	if !strings.Contains(text, FactConstraint) {
		t.Fatalf("两种模式都要带事实约束：%s", text)
	}
}

// 自动模式带未停用条目的 id / 名称 / 做法；停用 skeptical 后名单里不再有它。
func TestSection_AutoListRespectsDisabled(t *testing.T) {
	text := Section(File{Mode: ModeAuto, Default: "plain", Disabled: []string{"skeptical"}})
	if !strings.Contains(text, "默认语气：plain") {
		t.Fatalf("自动模式要写明默认 id：%s", text)
	}
	for _, e := range Builtin {
		if e.ID == "skeptical" {
			continue
		}
		if !strings.Contains(text, e.ID) || !strings.Contains(text, e.Name) || !strings.Contains(text, e.Practice) {
			t.Fatalf("自动模式名单缺 %s：%s", e.ID, text)
		}
	}
	if strings.Contains(text, "skeptical") {
		t.Fatalf("已停用的不许再出现：%s", text)
	}
	// 选择规则要写清：按最后一条用户消息、只按一条、用户指定优先、不混用、不发明
	for _, want := range []string{"最后一条用户消息", "只按那一条", "用户本条明确指定了语气就听用户的", "不要混用", "不要发明名单外的语气"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺选择规则 %q：%s", want, text)
		}
	}
	if strings.Contains(text, practiceOf(t, "skeptical")) {
		t.Fatalf("停用条目的做法也不许出现：%s", text)
	}
	if !strings.Contains(text, FactConstraint) {
		t.Fatalf("自动模式同样要带事实约束：%s", text)
	}
}

// 停用默认语气时拒绝保存，且不落盘（半合法的设置写进去，读回来与界面不一致）。
func TestSave_RefusesDisabledDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tones.json")
	s := New(path)
	err := s.Save(File{Mode: ModeFixed, Default: "plain", Disabled: []string{"plain"}})
	if err == nil {
		t.Fatal("停用默认语气必须拒绝保存")
	}
	if !strings.Contains(err.Error(), "不能停用") {
		t.Fatalf("错误要说清原因：%v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("被拒绝的保存不得落盘")
	}
}

// 保存 → 读回一致；非法 mode / 未知 id 一律拒绝。
func TestSave_RoundTripAndRejects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tones.json")
	s := New(path)
	want := File{Mode: ModeAuto, Default: "strict", Disabled: []string{"english", "casual"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || got.Default != want.Default || strings.Join(got.Disabled, ",") != "english,casual" {
		t.Fatalf("读回不一致：%+v", got)
	}
	for _, bad := range []File{
		{Mode: "yolo", Default: "plain"},
		{Mode: ModeFixed, Default: "not-a-tone"},
		{Mode: ModeFixed, Default: "plain", Disabled: []string{"not-a-tone"}},
	} {
		if err := s.Save(bad); err == nil {
			t.Fatalf("非法设置必须拒绝：%+v", bad)
		}
	}
}

// 同一设置连续拼接两次逐字相同（prompt cache 与"文本未变不替换"的前提）。
func TestSection_StableAcrossCalls(t *testing.T) {
	f := File{Mode: ModeAuto, Default: "review", Disabled: []string{"english"}}
	first, second := Section(f), Section(f)
	if first != second {
		t.Fatalf("同一设置两次拼接必须逐字相同：\n%s\n---\n%s", first, second)
	}
	if first == Section(File{Mode: ModeAuto, Default: "review"}) {
		t.Fatal("设置变了就该变（否则说明拼接忽略了设置）")
	}
}

// 内置 50 条：数量、id 唯一、名称与做法都非空（常量表被误改时立刻发现）。
func TestBuiltin_WellFormed(t *testing.T) {
	if len(Builtin) != 50 {
		t.Fatalf("内置语气 50 条，实际 %d", len(Builtin))
	}
	seen := map[string]bool{}
	for _, e := range Builtin {
		if e.ID == "" || e.Name == "" || e.Practice == "" {
			t.Fatalf("内置条目的 id/名称/做法都不能为空：%+v", e)
		}
		if seen[e.ID] {
			t.Fatalf("id 重复：%s", e.ID)
		}
		seen[e.ID] = true
	}
	if _, ok := lookup(DefaultID); !ok {
		t.Fatalf("缺省语气 %s 必须是内置条目", DefaultID)
	}
}
