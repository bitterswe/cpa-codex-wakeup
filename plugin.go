package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

const (
	version      = "0.2.0"
	pluginName   = "Codex 唤醒任务"
	resourcePath = "/v0/resource/plugins/cpa-codex-wakeup/overview"
)

//go:embed overview.html
var overview string

//go:embed ui.js
var uiScript string

type envelope struct {
	OK     bool `json:"ok"`
	Result any  `json:"result,omitempty"`
	Error  any  `json:"error,omitempty"`
}

type resourceRequest struct {
	Method string
	Path   string
	Body   []byte
}

func handle(method string, input []byte) envelope {
	var result any
	switch method {
	case "plugin.register", "plugin.reconfigure":
		if err := startEngine(); err != nil {
			return failure("storage", err.Error())
		}
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
			"Path": "/overview", "Menu": pluginName, "Description": "编辑和运行 Codex 唤醒任务",
		}}, "Routes": []map[string]string{{"Method": "GET", "Path": managementPath}, {"Method": "POST", "Path": managementPath}}}
	case "management.handle":
		var req resourceRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return failure("invalid_request", "Expected a management request")
		}
		status, mime, body := 404, "text/plain; charset=utf-8", "Not found"
		if req.Path == resourcePath && req.Method == "GET" {
			status, mime = 200, "text/html; charset=utf-8"
			body = strings.ReplaceAll(overview, "{{VERSION}}", version)
			body = strings.Replace(body, "/*PLUGIN_UI*/", uiScript, 1)
		} else if req.Path == "/v0/management"+managementPath || req.Path == managementPath {
			status, body = management(req)
			mime = "application/json; charset=utf-8"
		}
		result = map[string]any{
			"StatusCode": status,
			"Headers": map[string][]string{
				"Content-Type":            {mime},
				"Content-Security-Policy": {"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'"},
				"X-Content-Type-Options":  {"nosniff"},
				"Cache-Control":           {"no-store"},
			},
			"Body": []byte(body),
		}
	case "plugin.quiesce", "plugin.shutdown":
		stopEngine()
		result = struct{}{}
	default:
		return failure("unknown_method", "Unsupported plugin method")
	}
	return envelope{OK: true, Result: result}
}

func failure(code, message string) envelope {
	return envelope{Error: map[string]string{"code": code, "message": message}}
}
