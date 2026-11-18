package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

const (
	version      = "0.1.0"
	pluginName   = "Codex 唤醒任务"
	resourcePath = "/v0/resource/plugins/cpa-codex-wakeup/overview"
)

//go:embed overview.html
var overview string

type envelope struct {
	OK     bool `json:"ok"`
	Result any  `json:"result,omitempty"`
	Error  any  `json:"error,omitempty"`
}

type resourceRequest struct {
	Method string
	Path   string
}

func handle(method string, input []byte) envelope {
	var result any
	switch method {
	case "plugin.register", "plugin.reconfigure":
		result = map[string]any{
			"schema_version": 1,
			"metadata": map[string]string{
				"Name": pluginName, "Version": version, "Author": "bitterswe",
				"GitHubRepository": "https://github.com/bitterswe/cpa-codex-wakeup",
			},
			"capabilities": map[string]bool{"management_api": true},
		}
	case "management.register":
		result = map[string]any{"Resources": []map[string]string{{
			"Path": "/overview", "Menu": pluginName, "Description": "查看唤醒插件版本",
		}}}
	case "management.handle":
		var req resourceRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return failure("invalid_request", "Expected a management request")
		}
		status, mime, body := 404, "text/plain; charset=utf-8", "Not found"
		if req.Path == resourcePath && req.Method == "GET" {
			status, mime = 200, "text/html; charset=utf-8"
			body = strings.ReplaceAll(overview, "{{VERSION}}", version)
		}
		result = map[string]any{
			"StatusCode": status,
			"Headers": map[string][]string{
				"Content-Type":            {mime},
				"Content-Security-Policy": {"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'"},
				"X-Content-Type-Options":  {"nosniff"},
				"Cache-Control":           {"no-store"},
			},
			"Body": []byte(body),
		}
	case "plugin.quiesce", "plugin.shutdown":
		result = struct{}{}
	default:
		return failure("unknown_method", "Unsupported plugin method")
	}
	return envelope{OK: true, Result: result}
}

func failure(code, message string) envelope {
	return envelope{Error: map[string]string{"code": code, "message": message}}
}
