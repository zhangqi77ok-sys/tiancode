package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var defaultOAuthHTTPClient = &http.Client{Timeout: 15 * time.Second}

// OAuthTokenResult OAuth 刷新返回结构
type OAuthTokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // seconds
}

// RefreshOAuthToken 执行标准 RFC 6749 Refresh Token 换票请求 (支持 Google / OpenAI / 各种 OAuth 2.0 服务商)
func RefreshOAuthToken(ctx context.Context, tokenURL, clientID, clientSecret, refreshToken string) (*OAuthTokenResult, error) {
	rt := strings.TrimSpace(refreshToken)
	if rt == "" {
		return nil, errors.New("refresh_token 不能为空")
	}

	tokenURL = strings.TrimSpace(tokenURL)
	if tokenURL == "" {
		// 自动根据 Token 格式特征探测标准端点
		if strings.HasPrefix(rt, "1//") {
			// Google OAuth 2.0 标准 Refresh Token 格式
			tokenURL = "https://oauth2.googleapis.com/token"
		} else {
			// 默认 OpenAI / Codex 端点
			tokenURL = "https://auth.openai.com/oauth/token"
		}
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", rt)
	if clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("构建 token 刷新请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "tiancode/0.0.1")

	resp, err := defaultOAuthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 OAuth Token 端点失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 OAuth 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OAuth Token 端点响应错误 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("解析 OAuth 响应 JSON 失败: %w", err)
	}
	if res.Error != "" {
		return nil, fmt.Errorf("OAuth 错误: %s - %s", res.Error, res.ErrorDesc)
	}
	if res.AccessToken == "" {
		return nil, errors.New("OAuth 响应中未包含有效 access_token")
	}
	if res.ExpiresIn <= 0 {
		res.ExpiresIn = 3600 // 默认 1 小时
	}

	return &OAuthTokenResult{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		ExpiresIn:    res.ExpiresIn,
	}, nil
}

// PickAPIKey 从可能包含多行密钥的字符串中随机或轮询选取一行有效 Key (支持多 Key 轮询)
func PickAPIKey(raw string) string {
	lines := strings.Split(raw, "\n")
	var valid []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			valid = append(valid, trimmed)
		}
	}
	if len(valid) == 0 {
		return ""
	}
	if len(valid) == 1 {
		return valid[0]
	}
	return valid[rand.Intn(len(valid))]
}

const (
	// DefaultCodexOAuthClientID OpenAI Codex 官方客户端 ID (与 new-api 对齐，免填 client_id)
	DefaultCodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// DefaultCodexTokenURL OpenAI Codex 官方 Token 刷新端点
	DefaultCodexTokenURL = "https://auth.openai.com/oauth/token"
	// DefaultGoogleTokenURL Google OAuth 2.0 官方 Token 刷新端点
	DefaultGoogleTokenURL = "https://oauth2.googleapis.com/token"
)

// ExtractedOAuthCredentials 提取的 OAuth / 凭据包
type ExtractedOAuthCredentials struct {
	AccessToken  string
	RefreshToken string
	ClientID     string
	ClientSecret string
	TokenURI     string
	ProjectID    string
}

// ParseOAuthCredentialsFromRaw 解析用户输入的凭据字符串（支持直接填 RT 或整段 Google/OpenAI OAuth 凭据 JSON）
func ParseOAuthCredentialsFromRaw(raw string) ExtractedOAuthCredentials {
	raw = strings.TrimSpace(raw)
	res := ExtractedOAuthCredentials{}
	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			if at, ok := m["access_token"].(string); ok && at != "" {
				res.AccessToken = at
			} else if at, ok := m["accessToken"].(string); ok && at != "" {
				res.AccessToken = at
			}

			if rt, ok := m["refresh_token"].(string); ok && rt != "" {
				res.RefreshToken = rt
			} else if rt, ok := m["refreshToken"].(string); ok && rt != "" {
				res.RefreshToken = rt
			}

			if cid, ok := m["client_id"].(string); ok && cid != "" {
				res.ClientID = cid
			} else if cid, ok := m["clientId"].(string); ok && cid != "" {
				res.ClientID = cid
			}

			if cs, ok := m["client_secret"].(string); ok && cs != "" {
				res.ClientSecret = cs
			} else if cs, ok := m["clientSecret"].(string); ok && cs != "" {
				res.ClientSecret = cs
			}

			if uri, ok := m["token_uri"].(string); ok && uri != "" {
				res.TokenURI = uri
			} else if uri, ok := m["token_endpoint"].(string); ok && uri != "" {
				res.TokenURI = uri
			}

			if pid, ok := m["project_id"].(string); ok && pid != "" {
				res.ProjectID = pid
			} else if pid, ok := m["quota_project_id"].(string); ok && pid != "" {
				res.ProjectID = pid
			}
		}
	} else {
		res.RefreshToken = raw
	}
	return res
}

// ResolveChannelCredentials 核心鉴权与凭据解析入口
// 返回: (apiKey, endpoint, providerName, err)
func ResolveChannelCredentials(ctx context.Context, store *ChannelStore, ch *ChannelConfig) (string, string, string, error) {
	if ch == nil {
		return "", "", "", errors.New("渠道配置为空")
	}

	provider := ch.Protocol
	if provider == "" {
		switch ch.AuthType {
		case "anthropic", "gemini", "ollama", "azure", "grok":
			provider = ch.AuthType
		default:
			provider = "openai"
		}
	}

	endpoint := strings.TrimSpace(ch.Endpoint)
	if endpoint == "" {
		return "", "", "", fmt.Errorf("渠道 [%s] 未填写 Base URL", ch.Name)
	}

	authType := ch.AuthType
	if authType == "" {
		if provider == "ollama" {
			authType = "none"
		} else {
			authType = "api_key"
		}
	}

	switch authType {
	case "none":
		// 本地私有化或免鉴权
		return "none", endpoint, provider, nil

	case "azure":
		// Azure OpenAI
		rawKey := PickAPIKey(ch.APIKey)
		if rawKey == "" {
			return "", "", "", fmt.Errorf("Azure 渠道 [%s] 未配置 API Key", ch.Name)
		}
		return rawKey, endpoint, "azure", nil

	case "refresh_token":
		extracted := ParseOAuthCredentialsFromRaw(ch.APIKey)
		return resolveOAuthCredentials(ctx, store, ch, endpoint, provider, extracted)

	case "api_key":
		fallthrough
	default:
		// 检查用户是否直接在 Key 输入框粘贴了 JSON 凭据 (与 new-api 保持一致体验)
		rawTrimmed := strings.TrimSpace(ch.APIKey)
		if strings.HasPrefix(rawTrimmed, "{") && strings.HasSuffix(rawTrimmed, "}") {
			extracted := ParseOAuthCredentialsFromRaw(rawTrimmed)
			if extracted.RefreshToken != "" || extracted.AccessToken != "" {
				return resolveOAuthCredentials(ctx, store, ch, endpoint, provider, extracted)
			}
		}

		// 基础 API Key 模式，支持换行输入多 Key 随机/轮询
		picked := PickAPIKey(ch.APIKey)
		if picked == "" {
			return "", "", "", fmt.Errorf("渠道 [%s] 未填写 API Key", ch.Name)
		}
		return picked, endpoint, provider, nil
	}
}

// resolveOAuthCredentials 处理 OAuth Refresh Token 刷新或凭据直出流程
func resolveOAuthCredentials(ctx context.Context, store *ChannelStore, ch *ChannelConfig, endpoint, provider string, extracted ExtractedOAuthCredentials) (string, string, string, error) {
	if ch.ExtraConfig == nil {
		ch.ExtraConfig = make(map[string]string)
	}

	// 若直接提供了有效的 access_token 且没有 refresh_token，直接复用
	if extracted.AccessToken != "" && extracted.RefreshToken == "" {
		return extracted.AccessToken, endpoint, provider, nil
	}

	now := time.Now().Unix()
	cachedToken := ch.ExtraConfig["cached_access_token"]
	expiresAtStr := ch.ExtraConfig["expires_at"]
	expiresAt, _ := strconv.ParseInt(expiresAtStr, 10, 64)

	// 提前 60 秒刷新缓冲
	if cachedToken != "" && expiresAt > now+60 {
		return cachedToken, endpoint, provider, nil
	}

	rt := extracted.RefreshToken
	if rt == "" {
		return "", "", "", fmt.Errorf("渠道 [%s] 开启了 Refresh Token / OAuth 模式，但未填写有效 Refresh Token", ch.Name)
	}

	tokenURL := ch.ExtraConfig["token_endpoint"]
	if tokenURL == "" && extracted.TokenURI != "" {
		tokenURL = extracted.TokenURI
	}

	clientID := ch.ExtraConfig["client_id"]
	if clientID == "" && extracted.ClientID != "" {
		clientID = extracted.ClientID
	}

	clientSecret := ch.ExtraConfig["client_secret"]
	if clientSecret == "" && extracted.ClientSecret != "" {
		clientSecret = extracted.ClientSecret
	}

	// 自动补齐已知平台的默认 Client ID / Token 端点 (与 new-api 保持一致)
	if strings.HasPrefix(rt, "1//") {
		// Google OAuth Refresh Token
		if tokenURL == "" {
			tokenURL = DefaultGoogleTokenURL
		}
		if clientID == "" {
			return "", "", "", fmt.Errorf("检测到 Google OAuth Refresh Token，Google 要求必须提供 Client ID (建议直接将 Google 凭据 JSON 完整粘贴在密钥框中，如 application_default_credentials.json)")
		}
	} else {
		// 默认 OpenAI / Codex 端点与客户端
		if tokenURL == "" {
			tokenURL = DefaultCodexTokenURL
		}
		if clientID == "" {
			clientID = DefaultCodexOAuthClientID
		}
	}

	result, err := RefreshOAuthToken(ctx, tokenURL, clientID, clientSecret, rt)
	if err != nil {
		if cachedToken != "" {
			return cachedToken, endpoint, provider, nil
		}
		return "", "", "", fmt.Errorf("渠道 [%s] 刷新 Token 失败: %w", ch.Name, err)
	}

	// 更新缓存
	ch.ExtraConfig["cached_access_token"] = result.AccessToken
	ch.ExtraConfig["expires_at"] = strconv.FormatInt(now+result.ExpiresIn, 10)
	if result.RefreshToken != "" && result.RefreshToken != rt {
		ch.APIKey = result.RefreshToken
	}

	// 若持久化存储可用，落盘更新以供后续复用
	if store != nil {
		_ = store.Save(*ch)
	}

	return result.AccessToken, endpoint, provider, nil
}

