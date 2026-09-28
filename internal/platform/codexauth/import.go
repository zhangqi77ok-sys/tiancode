package codexauth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ParseImport 解析 Token / JSON 粘贴输入 → 凭证（必要时用 refresh_token 换取新 token）。
//
// 支持的输入形态（对齐参考实现的导入规则，键名多形态兼容）：
//  1. auth.json / 账号 JSON：{"tokens":{"access_token":…,"refresh_token":…}} 或平铺/顶层任意组合；
//  2. 裸 access_token（JWT 形态）→ 从 claims 提取 email / account / exp；
//  3. 裸 refresh_token（非 JWT）→ 调刷新端点换取 access_token（同时验证有效性）。
func ParseImport(ctx context.Context, client *Client, raw string) (Credential, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Credential{}, fmt.Errorf("请粘贴凭证内容")
	}
	if strings.HasPrefix(s, "{") {
		return parseJSONImport(ctx, client, s)
	}
	// 裸串：JWT 形态当 access_token；否则当 refresh_token 刷新验证
	if _, ok := JWTExpiry(s); ok || strings.Count(s, ".") == 2 {
		return credFromAccessToken(s), nil
	}
	resp, err := client.RefreshAccess(ctx, s, ClientID)
	if err != nil {
		return Credential{}, fmt.Errorf("该输入既不是 JSON、也不像 access_token，按 refresh_token 处理又失败：%w", err)
	}
	if resp.RefreshToken == "" {
		resp.RefreshToken = s // 刷新响应未带新 RT：保留原值
	}
	return TokenToCredential(resp, ClientID), nil
}

// credFromAccessToken 由裸 access_token 构建凭证（尽可能从 JWT 提取身份信息）。
func credFromAccessToken(at string) Credential {
	cred := Credential{
		Type:        CredentialType,
		AccessToken: at,
		ClientID:    ClientID,
	}
	if exp, ok := JWTExpiry(at); ok {
		cred.ExpiresAt = exp
	}
	if claims, err := ParseIDToken(at); err == nil { // access_token 同构可解（取得到的字段照用）
		cred.Email = claims.Email
		cred.ChatGPTAccountID = claims.ChatGPTAccountID
		cred.ChatGPTUserID = claims.ChatGPTUserID
		cred.PlanType = claims.PlanType
		cred.OrganizationID = claims.OrganizationID
	}
	return cred
}

func parseJSONImport(ctx context.Context, client *Client, raw string) (Credential, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Credential{}, fmt.Errorf("JSON 解析失败：%w", err)
	}
	tokens, _ := m["tokens"].(map[string]any)

	cred := Credential{Type: CredentialType, ClientID: ClientID}
	cred.AccessToken = firstString(m, tokens, "access_token", "accessToken", "token")
	cred.RefreshToken = firstString(m, tokens, "refresh_token", "refreshToken")
	cred.IDToken = firstString(m, tokens, "id_token", "idToken")
	cred.Email = firstString(m, nil, "email")
	cred.PlanType = firstString(m, nil, "plan_type", "planType")
	cred.OrganizationID = firstString(m, nil, "organization_id", "org_id")

	// 账号/用户 ID：平铺与嵌套（account/user 子对象）多形态
	if v := firstString(m, nil, "chatgpt_account_id", "account_id"); v != "" {
		cred.ChatGPTAccountID = v
	} else if acc, ok := m["account"].(map[string]any); ok {
		cred.ChatGPTAccountID = firstString(acc, nil, "id", "account_id", "chatgpt_account_id")
	}
	if v := firstString(m, nil, "chatgpt_user_id", "user_id"); v != "" {
		cred.ChatGPTUserID = v
	} else if u, ok := m["user"].(map[string]any); ok {
		cred.ChatGPTUserID = firstString(u, nil, "id", "user_id", "chatgpt_user_id")
		if cred.Email == "" {
			cred.Email = firstString(u, nil, "email")
		}
	}

	// 过期时间：显式字段优先，其次 JWT exp
	if exp := firstInt(m, "expires_at", "expires"); exp > 0 {
		// 毫秒时间戳（前端习惯）与秒时间戳都接受
		if exp > 1e12 {
			exp /= 1000
		}
		cred.ExpiresAt = exp
	} else if exp, ok := JWTExpiry(cred.AccessToken); ok {
		cred.ExpiresAt = exp
	}

	switch {
	case cred.AccessToken != "" && cred.NeedsRefresh(time.Now()):
		// 有 AT 但已（将）过期：用 RT 换新（无 RT 则报错，不静默使用过期凭证）
		if cred.RefreshToken == "" {
			return Credential{}, fmt.Errorf("access_token 已过期且没有 refresh_token，无法自动续期（请提供含 refresh_token 的 auth.json）")
		}
		resp, err := client.RefreshAccess(ctx, cred.RefreshToken, cred.ClientID)
		if err != nil {
			return Credential{}, fmt.Errorf("access_token 已过期，刷新失败：%w", err)
		}
		cred.applyRefresh(resp)
	case cred.AccessToken != "":
		// 直接可用
	case cred.RefreshToken != "":
		// 只有 RT：刷新换取 AT（顺带验证有效性）
		resp, err := client.RefreshAccess(ctx, cred.RefreshToken, cred.ClientID)
		if err != nil {
			return Credential{}, fmt.Errorf("refresh_token 无效或已失效：%w", err)
		}
		if resp.RefreshToken == "" {
			resp.RefreshToken = cred.RefreshToken
		}
		cred.applyRefresh(resp)
	default:
		return Credential{}, fmt.Errorf("内容里没有 access_token 或 refresh_token")
	}
	if cred.ExpiresAt == 0 {
		cred.ExpiresAt = time.Now().Add(time.Hour).Unix()
	}
	return cred, nil
}

// applyRefresh 把刷新响应合并进凭证（**仅当响应带回新 refresh_token 才覆盖**——防止把
// 可用的 RT 用空值覆盖掉）。
func (c *Credential) applyRefresh(t TokenResponse) {
	c.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		c.RefreshToken = t.RefreshToken
	}
	if t.IDToken != "" {
		c.IDToken = t.IDToken
	}
	c.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).Unix()
	if claims, err := ParseIDToken(t.IDToken); err == nil {
		if c.Email == "" {
			c.Email = claims.Email
		}
		if c.ChatGPTAccountID == "" {
			c.ChatGPTAccountID = claims.ChatGPTAccountID
		}
		if c.ChatGPTUserID == "" {
			c.ChatGPTUserID = claims.ChatGPTUserID
		}
		if c.PlanType == "" {
			c.PlanType = claims.PlanType
		}
		if c.OrganizationID == "" {
			c.OrganizationID = claims.OrganizationID
		}
	}
}

// RefreshCredential 刷新一个已有凭证（请求前自动续期用）。
func RefreshCredential(ctx context.Context, client *Client, cred Credential) (Credential, error) {
	resp, err := client.RefreshAccess(ctx, cred.RefreshToken, cred.ClientID)
	if err != nil {
		return Credential{}, err
	}
	if resp.RefreshToken == "" {
		resp.RefreshToken = cred.RefreshToken
	}
	cred.applyRefresh(resp)
	return cred, nil
}

func firstString(primary, secondary map[string]any, keys ...string) string {
	for _, src := range []map[string]any{primary, secondary} {
		if src == nil {
			continue
		}
		for _, k := range keys {
			if v, ok := src[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func firstInt(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int64(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return n
			}
		case string:
			var n int64
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n
			}
		}
	}
	return 0
}
