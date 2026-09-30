// 语气服务（第 8 批）：tones.json 的读 / 写，以及拼进系统提示的那一段。
//
// 为什么放在编排层而不是 tones 包：tones 包只管"文件 + 常量 + 一段文本"，
// 什么时候读、拼在系统提示的哪个位置是对话编排的事（与扩展清单同一分工）。
package app

import (
	"errors"

	"tiancode/internal/platform/tones"
)

// toneSection 取本轮的系统提示语气段：每轮读一次。
// 读不出来返回错误（本轮直接失败，可见）——不静默降级成"没有语气"，
// 否则用户会以为模型是在照自己的设置回答。
func (s *ChatService) toneSection() (string, error) {
	if s.tones == nil {
		return "", errors.New("语气存储未初始化")
	}
	f, err := s.tones.Load()
	if err != nil {
		return "", err
	}
	return tones.Section(f), nil
}

// Tones 返回当前语气设置与内置 50 条（id / 名称 / 做法）。
// 内置名单由后端给出：前端不复制一份，复制迟早会与注入用的文本不一致。
func (s *ChatService) Tones() (tones.File, []tones.Entry, error) {
	if s.tones == nil {
		return tones.File{}, nil, errors.New("语气存储未初始化")
	}
	f, err := s.tones.Load()
	if err != nil {
		return tones.File{}, nil, err
	}
	return f, tones.Builtin, nil
}

// SaveTones 保存语气设置。非法值（未知模式 / 未知默认 / 停用默认语气 / 未知 id）
// 在 tones.Validate 当场报错且不落盘。
func (s *ChatService) SaveTones(f tones.File) error {
	if s.tones == nil {
		return errors.New("语气存储未初始化")
	}
	return s.tones.Save(f)
}
