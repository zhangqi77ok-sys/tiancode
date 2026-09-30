package app

import "tiancode/internal/platform/tones"

// TonesView 是语气面板要的全部数据：当前设置 + 内置 50 条（id / 名称 / 做法）。
// 名单由后端给出：前端不复制一份——复制迟早与注入系统提示用的文本不一致。
type TonesView struct {
	Mode     string        `json:"mode"`     // fixed / auto
	Default  string        `json:"default"`  // 默认语气 id（不能是被停用的那条）
	Disabled []string      `json:"disabled"` // 被停用的内置 id
	Builtin  []tones.Entry `json:"builtin"`  // 内置语气（界面渲染 + 侧栏状态）
}

// GetTones 返回语气设置与内置语气名单（界面渲染用；只读，不产生任何副作用）。
func (b *Bind) GetTones() (TonesView, error) {
	f, builtin, err := b.chat.Tones()
	if err != nil {
		return TonesView{}, err
	}
	return TonesView{Mode: f.Mode, Default: f.Default, Disabled: f.Disabled, Builtin: builtin}, nil
}

// SaveTones 保存语气设置：固定还是自动、默认哪一条、停用了哪几条。
// 非法值（未知模式 / 未知 id / 停用默认语气）报错且不落盘；保存成功对**下一轮**
// 对话生效——系统提示里的语气段每轮重新拼接，同一回合内逐字不变。
func (b *Bind) SaveTones(f tones.File) error {
	return b.chat.SaveTones(f)
}
