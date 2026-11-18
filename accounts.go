package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var hostCall = nativeHost

type Account struct {
	ID, Email, Label, Plan string
	Disabled               bool
}
type Window struct {
	Remaining float64
	Reset     int64
}
type Quota struct {
	Primary, Secondary *Window
	Plan               string
	Checked            int64
}

func accounts() ([]Account, error) {
	var result struct {
		Files []struct {
			AuthIndex                          string `json:"auth_index"`
			AccountType                        string `json:"account_type"`
			Email, Label, Name, Provider, Type string
			Disabled, Unavailable              bool
		}
	}
	if err := hostCall("host.auth.list", struct{}{}, &result); err != nil {
		return nil, err
	}
	list := []Account{}
	for _, a := range result.Files {
		if !(strings.EqualFold(a.Provider, "codex") || strings.EqualFold(a.Type, "codex")) || a.AuthIndex == "" || strings.Contains(strings.ToLower(a.AccountType), "key") {
			continue
		}
		name := a.Email
		if name == "" {
			name = a.Name
		}
		list = append(list, Account{ID: a.AuthIndex, Email: name, Label: a.Label, Disabled: a.Disabled || a.Unavailable})
	}
	return list, nil
}

// Credentials stay within the server and are only sent to the two fixed provider endpoints.
func upstream(ctx context.Context, id string, usage bool, t Task) (*http.Response, error) {
	var auth struct{ JSON map[string]any }
	if err := hostCall("host.auth.get", map[string]string{"auth_index": id}, &auth); err != nil {
		return nil, err
	}
	groups := []map[string]any{auth.JSON}
	for _, name := range []string{"tokens", "credentials", "auth", "oauth", "session"} {
		if group, ok := auth.JSON[name].(map[string]any); ok {
			groups = append(groups, group)
		}
	}
	value := func(keys ...string) string {
		for _, g := range groups {
			for _, k := range keys {
				if s, ok := g[k].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	token := value("access_token", "accessToken", "oauth_access_token", "oauthAccessToken")
	account := value("account_id", "chatgpt_account_id", "accountId", "chatgptAccountId")
	proxy := value("proxy_url", "proxy-url", "proxyURL")
	if token == "" {
		return nil, errors.New("账号缺少 OAuth 访问令牌")
	}
	endpoint, method := "https://chatgpt.com/backend-api/wham/usage", "GET"
	var body []byte
	if !usage {
		endpoint, method = "https://chatgpt.com/backend-api/codex/responses", "POST"
		prompt := t.Prompt
		if strings.TrimSpace(prompt) == "" {
			prompt = "hi"
		}
		request := map[string]any{"model": t.Model, "instructions": "Respond briefly.", "stream": true, "store": false,
			"input": []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": prompt}}}}}
		if t.Effort != "" {
			request["reasoning"] = map[string]string{"effort": t.Effort}
		}
		body, _ = json.Marshal(request)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Originator", "codex_cli_rs")
	if account != "" {
		req.Header.Set("ChatGPT-Account-Id", account)
	}
	req.Header.Set("Accept", "application/json")
	if !usage {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("OpenAI-Beta", "responses=v1")
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	}
	tr.DisableKeepAlives = true
	if strings.EqualFold(proxy, "direct") || strings.EqualFold(proxy, "none") {
		tr.Proxy = nil
	} else if proxy != "" {
		p, e := url.Parse(proxy)
		if e != nil || p.Host == "" {
			return nil, errors.New("凭据代理配置无效")
		}
		tr.Proxy = http.ProxyURL(p)
	}
	client := http.Client{Transport: tr, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("上游连接失败或超时")
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("上游 HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func readQuota(ctx context.Context, id string) (Quota, error) {
	resp, err := upstream(ctx, id, true, Task{})
	if err != nil {
		return Quota{}, err
	}
	defer resp.Body.Close()
	type rawWindow struct {
		Used  *float64 `json:"used_percent"`
		Reset int64    `json:"reset_at"`
	}
	var data struct {
		Plan  string `json:"plan_type"`
		Limit struct {
			Primary   *rawWindow `json:"primary_window"`
			Secondary *rawWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&data) != nil {
		return Quota{}, errors.New("额度响应格式无效")
	}
	window := func(w *rawWindow) *Window {
		if w == nil || w.Used == nil {
			return nil
		}
		return &Window{100 - *w.Used, w.Reset}
	}
	q := Quota{window(data.Limit.Primary), window(data.Limit.Secondary), data.Plan, time.Now().Unix()}
	if q.Primary == nil && q.Secondary == nil {
		return q, errors.New("账号未返回额度窗口")
	}
	return q, nil
}

func activate(ctx context.Context, id string, t Task) error {
	resp, err := upstream(ctx, id, false, t)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct{ Type string }
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		switch event.Type {
		case "response.completed":
			return nil
		case "response.failed", "response.incomplete", "error":
			return errors.New("模型请求失败或未完成")
		}
	}
	return errors.New("连接结束前未收到完成事件")
}
