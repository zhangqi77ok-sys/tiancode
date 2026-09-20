package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// NormalizeNewlines 统一行尾为 LF (\n)，规避 Windows \r\n 跨平台字节级哈希漂移
func NormalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// CanonicalizeValue 递归规范化任意数据结构：
// 1. 字符串内部统一 CRLF 为 LF；
// 2. 将 map 中的所有 key 提取并以规范有序结构排布；
// 3. 递归处理 slice、array、map 与 struct。
func CanonicalizeValue(v any) any {
	if v == nil {
		return nil
	}

	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.String:
		return NormalizeNewlines(val.String())

	case reflect.Map:
		out := make(map[string]any)
		keys := make([]string, 0, val.Len())
		keyMap := make(map[string]reflect.Value)
		for _, k := range val.MapKeys() {
			kStr := fmt.Sprintf("%v", k.Interface())
			keys = append(keys, kStr)
			keyMap[kStr] = val.MapIndex(k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			valElem := keyMap[k]
			if valElem.IsValid() {
				out[k] = CanonicalizeValue(valElem.Interface())
			}
		}
		return out

	case reflect.Slice, reflect.Array:
		// 如果是 []byte，保留原生字节
		if val.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		out := make([]any, val.Len())
		for i := 0; i < val.Len(); i++ {
			out[i] = CanonicalizeValue(val.Index(i).Interface())
		}
		return out

	case reflect.Pointer, reflect.Interface:
		if val.IsNil() {
			return nil
		}
		return CanonicalizeValue(val.Elem().Interface())

	case reflect.Struct:
		// 若为结构体，先转为 map[string]any 再规范化
		b, err := json.Marshal(v)
		if err != nil {
			return v
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err == nil {
			return CanonicalizeValue(m)
		}
		return v

	default:
		return v
	}
}

// MarshalCanonical 将任意数据结构按 Key 字典序严格稳定排序输出标准 JSON 字节流
// 消除任何跨系统、跨轮次因 Go Map 迭代随机化或换行符差异带来的 Token 字节漂移
func MarshalCanonical(v any) ([]byte, error) {
	canonicalObj := CanonicalizeValue(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // 避免转义 <, >, & 破坏提示词与工具参数原始语义
	if err := enc.Encode(canonicalObj); err != nil {
		return nil, err
	}
	res := buf.Bytes()
	// 去除 json.Encoder 默认追加的尾部换行符 '\n'
	if len(res) > 0 && res[len(res)-1] == '\n' {
		res = res[:len(res)-1]
	}
	return res, nil
}
