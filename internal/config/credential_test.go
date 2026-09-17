package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPickAPIKey(t *testing.T) {
	if got := PickAPIKey(""); got != "" {
		t.Fatalf("expected empty for empty input, got %q", got)
	}

	single := "sk-single123"
	if got := PickAPIKey(single); got != single {
		t.Fatalf("expected %q, got %q", single, got)
	}

	multi := "sk-1\n# comment\n  sk-2  \n\n"
	picked := PickAPIKey(multi)
	if picked != "sk-1" && picked != "sk-2" {
		t.Fatalf("unexpected picked key: %q", picked)
	}
}

func TestParseOAuthCredentialsFromRaw(t *testing.T) {
	raw := "1//mock_google_rt_sample_string_for_testing_only"
	extracted := ParseOAuthCredentialsFromRaw(raw)
	if extracted.RefreshToken != raw {
		t.Fatalf("expected %q, got %q", raw, extracted.RefreshToken)
	}

	googleJSON := `{
		"client_id": "google-client-123.apps.googleusercontent.com",
		"client_secret": "secret-xyz",
		"refresh_token": "1//test_google_rt",
		"type": "authorized_user"
	}`
	extracted = ParseOAuthCredentialsFromRaw(googleJSON)
	if extracted.RefreshToken != "1//test_google_rt" {
		t.Fatalf("expected 1//test_google_rt, got %q", extracted.RefreshToken)
	}
	if extracted.ClientID != "google-client-123.apps.googleusercontent.com" {
		t.Fatalf("expected client ID, got %q", extracted.ClientID)
	}
	if extracted.ClientSecret != "secret-xyz" {
		t.Fatalf("expected client Secret, got %q", extracted.ClientSecret)
	}
}

func TestResolveChannelCredentials(t *testing.T) {
	ctx := context.Background()

	// 1. None / Ollama
	chNone := &ChannelConfig{
		Name:     "ollama",
		Protocol: "ollama",
		AuthType: "none",
		Endpoint: "http://localhost:11434",
	}
	key, ep, prov, err := ResolveChannelCredentials(ctx, nil, chNone)
	if err != nil {
		t.Fatalf("unexpected err for none: %v", err)
	}
	if key != "none" || ep != "http://localhost:11434" || prov != "ollama" {
		t.Fatalf("unexpected result for none: key=%s, ep=%s, prov=%s", key, ep, prov)
	}

	// 2. Azure
	chAzure := &ChannelConfig{
		Name:     "azure-gpt4",
		Protocol: "azure",
		AuthType: "azure",
		Endpoint: "https://my-azure.openai.azure.com",
		APIKey:   "azure_key_123",
	}
	key, ep, prov, err = ResolveChannelCredentials(ctx, nil, chAzure)
	if err != nil {
		t.Fatalf("unexpected err for azure: %v", err)
	}
	if key != "azure_key_123" || ep != "https://my-azure.openai.azure.com" || prov != "azure" {
		t.Fatalf("unexpected result for azure: key=%s, ep=%s, prov=%s", key, ep, prov)
	}

	// 3. API Key
	chAPI := &ChannelConfig{
		Name:     "openai-main",
		Protocol: "openai",
		AuthType: "api_key",
		Endpoint: "https://api.openai.com/v1",
		APIKey:   "sk-test-key",
	}
	key, ep, prov, err = ResolveChannelCredentials(ctx, nil, chAPI)
	if err != nil {
		t.Fatalf("unexpected err for api_key: %v", err)
	}
	if key != "sk-test-key" || ep != "https://api.openai.com/v1" || prov != "openai" {
		t.Fatalf("unexpected result for api_key: key=%s, ep=%s, prov=%s", key, ep, prov)
	}

	// 4. Refresh Token - Valid cache
	chRT := &ChannelConfig{
		Name:     "codex-rt",
		Protocol: "openai",
		AuthType: "refresh_token",
		Endpoint: "https://api.openai.com/v1",
		APIKey:   "rt_test_token",
		ExtraConfig: map[string]string{
			"cached_access_token": "at_cached_valid",
			"expires_at":          strconv.FormatInt(time.Now().Unix()+3600, 10),
		},
	}
	key, ep, prov, err = ResolveChannelCredentials(ctx, nil, chRT)
	if err != nil {
		t.Fatalf("unexpected err for cached RT: %v", err)
	}
	if key != "at_cached_valid" {
		t.Fatalf("expected cached access token, got %s", key)
	}

	// 5. JSON in API Key field (new-api 模式：单输入框直接粘贴 OAuth JSON)
	chJSON := &ChannelConfig{
		Name:     "codex-json",
		Protocol: "openai",
		AuthType: "api_key", // 用户未切换模式，只在 Key 框中粘贴了 JSON
		Endpoint: "https://api.openai.com/v1",
		APIKey:   `{"access_token": "at_from_json_direct", "refresh_token": ""}`,
	}
	key, ep, prov, err = ResolveChannelCredentials(ctx, nil, chJSON)
	if err != nil {
		t.Fatalf("unexpected err for JSON credentials: %v", err)
	}
	if key != "at_from_json_direct" {
		t.Fatalf("expected at_from_json_direct, got %s", key)
	}
}

func TestRefreshOAuthToken_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form err: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("expected grant_type=refresh_token, got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "my_rt_token" {
			t.Errorf("expected refresh_token=my_rt_token, got %s", r.FormValue("refresh_token"))
		}
		if r.FormValue("client_secret") != "test_secret" {
			t.Errorf("expected client_secret=test_secret, got %s", r.FormValue("client_secret"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new_refreshed_at_999",
			"refresh_token": "new_rotated_rt_888",
			"expires_in":    7200,
		})
	}))
	defer ts.Close()

	ctx := context.Background()
	res, err := RefreshOAuthToken(ctx, ts.URL, "test_client", "test_secret", "my_rt_token")
	if err != nil {
		t.Fatalf("RefreshOAuthToken failed: %v", err)
	}
	if res.AccessToken != "new_refreshed_at_999" {
		t.Fatalf("expected new access token, got %s", res.AccessToken)
	}
	if res.RefreshToken != "new_rotated_rt_888" {
		t.Fatalf("expected new rotated refresh token, got %s", res.RefreshToken)
	}
	if res.ExpiresIn != 7200 {
		t.Fatalf("expected expiresIn 7200, got %d", res.ExpiresIn)
	}
}

func TestRefreshOAuthToken_GoogleAutoEndpoint(t *testing.T) {
	// Google token starts with 1//
	googleRT := "1//mock_google_rt"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Testing with empty tokenURL should automatically pick https://oauth2.googleapis.com/token
	// (we expect it to fail with connection or timeout, but NOT invalid tokenURL)
	_, err := RefreshOAuthToken(ctx, "", "test_client", "test_secret", googleRT)
	if err == nil {
		t.Fatal("expected error connecting to google without network/credentials, got nil")
	}
	if !strings.Contains(err.Error(), "oauth2.googleapis.com") && !strings.Contains(err.Error(), "OAuth") {
		t.Fatalf("expected google oauth endpoint in error, got %v", err)
	}
}

func TestLiveGoogleUserRT(t *testing.T) {
	// 验证对 Google RT 格式特征的自动识别
	mockRT := "1//mock_google_user_refresh_token_for_test_purposes_only"
	if !strings.HasPrefix(mockRT, "1//") {
		t.Fatalf("expected Google RT to start with 1//")
	}
	extracted := ParseOAuthCredentialsFromRaw(mockRT)
	if extracted.RefreshToken != mockRT {
		t.Fatalf("expected extracted RT to match")
	}
}


