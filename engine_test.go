package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestSchedule(t *testing.T) {
	zone, _ := time.LoadLocation("Asia/Shanghai")
	fri := time.Date(2026, 11, 20, 10, 0, 0, 0, zone)
	task := Task{Kind: "weekly", Zone: zone.String(), Clock: "10:00", Days: []int{1, 2, 3, 4, 5}}
	if nextTime(task, fri) != fri.AddDate(0, 0, 3).Unix() {
		t.Fatal("weekend was not skipped")
	}
	task.Kind = "daily"
	if nextTime(task, fri) != fri.AddDate(0, 0, 1).Unix() {
		t.Fatal("daily")
	}
	task.Kind, task.Hours = "interval", 3
	if nextTime(task, fri) != fri.Add(3*time.Hour).Unix() {
		t.Fatal("interval")
	}
	task.Kind, task.Delay = "startup", 7
	schedule(&task, &State{}, fri, true)
	if task.Next != fri.Add(7*time.Minute).Unix() {
		t.Fatal("startup delay")
	}
}

func TestQuotaAndConfirmation(t *testing.T) {
	now := time.Now()
	q := Quota{Primary: &Window{Reset: now.Unix() + 60}, Secondary: &Window{Reset: now.Unix() + 600}}
	task := Task{Kind: "quota", Window: "primary", Delay: 2, Enabled: true, Pool: true, Mode: "confirm", Timeout: 1}
	e := Engine{file: filepath.Join(t.TempDir(), "state.json"), state: State{Pool: []string{"a", "b"}, Quotas: map[string]Quota{"a": q}}}
	schedule(&task, &e.state, now, false)
	e.state.Tasks = []Task{task}
	at := now.Add(3 * time.Minute)
	e.tick(at)
	e.tick(at)
	if len(e.state.Runs) != 1 || !slices.Equal(e.state.Runs[0].Targets, []string{"a"}) || e.state.Runs[0].Status != "pending" {
		t.Fatal(e.state.Runs)
	}
	e.tick(at.Add(time.Minute))
	if e.state.Runs[0].Status != "expired" {
		t.Fatal("expired confirmation executed")
	}
	task.Window = "secondary"
	if resetAt(task, q, now.Unix()) != q.Secondary.Reset+120 {
		t.Fatal("wrong secondary window")
	}
	task.Window = "either"
	if resetAt(task, q, now.Unix()) != q.Primary.Reset+120 {
		t.Fatal("wrong earliest window")
	}
}

func TestProvider(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("ChatGPT-Account-Id") != "demo" {
			t.Error("credential selection")
		}
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			fmt.Fprint(w, `{"plan_type":"team","rate_limit":{"primary_window":{"used_percent":15,"reset_at":1234}}}`)
		case "/backend-api/codex/responses":
			var request struct {
				Model     string
				Reasoning struct{ Effort string }
				Input     []struct{ Content []struct{ Text string } }
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Reasoning.Effort != "medium" || len(request.Input) != 1 || len(request.Input[0].Content) != 1 || request.Input[0].Content[0].Text != "hi" {
				t.Error("request parameters")
			}
			if request.Model == "working" {
				fmt.Fprintln(w, `data: {"type":"response.completed"}`)
			} else {
				fmt.Fprintln(w, `data: {"type":"response.failed"}`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	oldTransport, oldHost := http.DefaultTransport, hostCall
	defer func() { http.DefaultTransport, hostCall = oldTransport, oldHost }()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig.ServerName = "example.com" // Go httptest certificate.
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	http.DefaultTransport = tr
	hostCall = func(method string, payload, target any) error {
		if method == "host.auth.list" {
			return json.Unmarshal([]byte(`{"files":[{"auth_index":"one","provider":"codex"},{"auth_index":"two","provider":"other"},{"auth_index":"three","type":"codex","account_type":"api_key"}]}`), target)
		}
		if payload.(map[string]string)["auth_index"] != "one" {
			t.Fatal("wrong credential index")
		}
		return json.Unmarshal([]byte(`{"json":{"tokens":{"access_token":"fixture","account_id":"demo","proxy_url":"direct"}}}`), target)
	}
	list, err := accounts()
	if err != nil || len(list) != 1 || list[0].ID != "one" {
		t.Fatal(list, err)
	}
	q, err := readQuota(context.Background(), "one")
	if err != nil || q.Plan != "team" || q.Primary.Remaining != 85 {
		t.Fatal(q, err)
	}
	if err := activate(context.Background(), "one", Task{Model: "working", Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	if err := activate(context.Background(), "one", Task{Model: "failed", Effort: "medium"}); err == nil {
		t.Fatal("failed stream reported success")
	}
}
