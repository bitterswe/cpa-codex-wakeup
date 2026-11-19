package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const managementPath = "/plugins/cpa-codex-wakeup/tasks"

type Task struct {
	Models                                                           []string `json:",omitempty"`
	ID, Name, Model, Effort, Prompt, Kind, Clock, Zone, Window, Mode string
	Enabled, Pool                                                    bool
	IDs                                                              []string
	Days                                                             []int
	Hours, Delay, Timeout                                            int
	Next                                                             int64
	// Each account has its own observed reset time, retained across quota refreshes.
	Resets map[string]int64
}
type Preset struct {
	Name, Model, Default string
	Efforts              []string
}
type Record struct {
	Account, Status string
	Model           string  `json:",omitempty"`
	Reply           *string `json:",omitempty"`
}
type Run struct {
	ID, Name, Source, Status string
	At, Deadline             int64
	Task                     Task
	Targets                  []string
	Records                  []Record
}
type State struct {
	Tasks   []Task
	Presets []Preset
	Pool    []string
	Tags    map[string][]string
	Quotas  map[string]Quota
	Runs    []Run
}
type Engine struct {
	mu           sync.Mutex
	state        State
	file         string
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	storageError string
}

var lifecycle sync.RWMutex
var engine *Engine

func uid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func nextTime(t Task, after time.Time) int64 {
	switch t.Kind {
	case "interval":
		return after.Add(time.Duration(t.Hours) * time.Hour).Unix()
	case "daily", "weekly":
		zone, err := time.LoadLocation(t.Zone)
		if err != nil {
			return 0
		}
		clock, err := time.Parse("15:04", t.Clock)
		if err != nil {
			return 0
		}
		local := after.In(zone)
		for n := 0; n <= 8; n++ {
			day := local.AddDate(0, 0, n)
			if t.Kind == "weekly" && !slices.Contains(t.Days, (int(day.Weekday())+6)%7+1) {
				continue
			}
			when := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, zone)
			if when.Hour() == clock.Hour() && when.Minute() == clock.Minute() && when.After(after) {
				return when.Unix()
			}
		}
	}
	return 0
}
func validate(t Task) error {
	if strings.TrimSpace(t.Name) == "" || len(t.Name) > 180 || strings.TrimSpace(t.Model) == "" || len(t.Model) > 256 || len(t.Prompt) > 16000 {
		return errors.New("请填写有效的任务名称、模型和提示词")
	}
	if !t.Pool && len(t.IDs) == 0 {
		return errors.New("请至少选择一个账号")
	}
	if _, e := time.LoadLocation(t.Zone); e != nil || t.Zone == "" {
		return errors.New("无效时区")
	}
	if t.Mode != "auto" && t.Mode != "confirm" || t.Mode == "confirm" && (t.Timeout < 1 || t.Timeout > 1440) {
		return errors.New("确认等待时间应为 1 至 1440 分钟")
	}
	if t.Delay < 0 || t.Delay > 10080 {
		return errors.New("延迟应为 0 至 10080 分钟")
	}
	switch t.Kind {
	case "daily", "weekly":
		if _, e := time.Parse("15:04", t.Clock); e != nil {
			return errors.New("无效时间")
		}
		if t.Kind == "weekly" {
			if len(t.Days) == 0 {
				return errors.New("请选择执行日")
			}
			for _, d := range t.Days {
				if d < 1 || d > 7 {
					return errors.New("无效执行日")
				}
			}
		}
	case "interval":
		if t.Hours < 1 || t.Hours > 8760 {
			return errors.New("间隔应为 1 至 8760 小时")
		}
	case "quota":
		if !slices.Contains([]string{"either", "primary", "secondary"}, t.Window) {
			return errors.New("请选择额度窗口")
		}
	case "startup":
	default:
		return errors.New("无效调度方式")
	}
	return nil
}
func selected(t Task, s *State) []string {
	if t.Pool {
		return s.Pool
	}
	return t.IDs
}
func resetAt(t Task, q Quota, after int64) int64 {
	var best int64
	for name, w := range map[string]*Window{"primary": q.Primary, "secondary": q.Secondary} {
		if w == nil || w.Reset <= 0 || t.Window != "either" && t.Window != name {
			continue
		}
		at := w.Reset + int64(t.Delay)*60
		if at > after && (best == 0 || at < best) {
			best = at
		}
	}
	return best
}
func schedule(t *Task, s *State, now time.Time, onStart bool) {
	t.Next = nextTime(*t, now)
	t.Resets = map[string]int64{}
	if t.Kind == "startup" && onStart {
		t.Next = now.Unix() + int64(t.Delay)*60
	}
	if t.Kind == "quota" {
		for _, id := range selected(*t, s) {
			if at := resetAt(*t, s.Quotas[id], now.Unix()); at > 0 {
				t.Resets[id] = at
			}
		}
	}
}
func (e *Engine) update(fn func(*State) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	raw, err := json.Marshal(e.state)
	if err != nil {
		return err
	}
	var s State
	if err = json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if err = fn(&s); err != nil {
		return err
	}
	raw, err = json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.file), ".wakeup-*")
	if err == nil {
		defer os.Remove(tmp.Name())
		_, err = tmp.Write(raw)
		if err == nil {
			err = tmp.Sync()
		}
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), e.file)
		}
	}
	if err != nil {
		e.storageError = err.Error()
		return err
	}
	e.state = s
	e.storageError = ""
	return nil
}
func startEngine() error {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	if engine != nil {
		return nil
	}
	dir := os.Getenv("CPA_CODEX_WAKEUP_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".cli-proxy-api", "plugins", "cpa-codex-wakeup")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	e := &Engine{file: filepath.Join(dir, "tasks.json")}
	raw, err := os.ReadFile(e.file)
	if err == nil {
		if err = json.Unmarshal(raw, &e.state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if e.state.Tags == nil {
		e.state.Tags = map[string][]string{}
	}
	if e.state.Quotas == nil {
		e.state.Quotas = map[string]Quota{}
	}
	if e.state.Presets == nil {
		e.state.Presets = []Preset{{"GPT-6.1 Sol", "gpt-6.1-sol", "medium", []string{"low", "medium", "high", "xhigh"}}}
	}
	err = e.update(func(s *State) error {
		now := time.Now()
		for i := range s.Tasks {
			t := &s.Tasks[i]
			if t.Enabled && (t.Kind == "startup" || t.Kind != "quota" && t.Next == 0) {
				schedule(t, s, now, true)
			}
		}
		for i := range s.Runs {
			if s.Runs[i].Status == "running" {
				s.Runs[i].Status = "interrupted"
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	engine = e
	e.wg.Add(3)
	go e.loop()
	go e.worker()
	go e.quotaLoop()
	return nil
}
func stopEngine() {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	if engine != nil {
		engine.cancel()
		engine.wg.Wait()
		engine = nil
	}
}
func queue(s *State, t Task, ids []string, source string, now int64) error {
	if len(ids) == 0 {
		return errors.New("账号池为空")
	}
	r := Run{ID: uid(), Task: t, Name: t.Name, Targets: slices.Clone(ids), Source: source, At: now, Status: "queued"}
	if t.Mode == "confirm" {
		r.Status = "pending"
		r.Deadline = now + int64(t.Timeout)*60
	}
	s.Runs = append(s.Runs, r)
	for len(s.Runs) > 500 {
		idx := slices.IndexFunc(s.Runs, func(r Run) bool { return !slices.Contains([]string{"running", "pending", "queued"}, r.Status) })
		if idx < 0 {
			break
		}
		s.Runs = slices.Delete(s.Runs, idx, idx+1)
	}
	return nil
}
func due(s *State, now int64) bool {
	for _, r := range s.Runs {
		if r.Status == "pending" && r.Deadline <= now {
			return true
		}
	}
	for _, t := range s.Tasks {
		if !t.Enabled {
			continue
		}
		if t.Next > 0 && t.Next <= now {
			return true
		}
		for _, at := range t.Resets {
			if at > 0 && at <= now {
				return true
			}
		}
	}
	return false
}
func (e *Engine) tick(now time.Time) {
	stamp := now.Unix()
	e.mu.Lock()
	ready := due(&e.state, stamp)
	e.mu.Unlock()
	if !ready {
		return
	}
	e.update(func(s *State) error {
		for i := range s.Runs {
			r := &s.Runs[i]
			if r.Status == "pending" && r.Deadline <= stamp {
				r.Status = "expired"
			}
		}
		for i := range s.Tasks {
			t := &s.Tasks[i]
			if !t.Enabled {
				continue
			}
			ids := []string{}
			if t.Kind == "quota" {
				for _, id := range selected(*t, s) {
					if at := t.Resets[id]; at > 0 && at <= stamp {
						ids = append(ids, id)
						delete(t.Resets, id)
						if next := resetAt(*t, s.Quotas[id], stamp); next > 0 {
							t.Resets[id] = next
						}
					}
				}
			} else if t.Next > 0 && t.Next <= stamp {
				ids = selected(*t, s)
				t.Next = nextTime(*t, now)
			}
			if len(ids) > 0 {
				queue(s, *t, ids, t.Kind, stamp)
			}
		}
		return nil
	})
}
func (e *Engine) loop() {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-ticker.C:
			e.tick(now)
		}
	}
}
func (e *Engine) worker() {
	defer e.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.mu.Lock()
			idx := slices.IndexFunc(e.state.Runs, func(r Run) bool { return r.Status == "queued" })
			e.mu.Unlock()
			if idx < 0 {
				continue
			}
			var run Run
			err := e.update(func(s *State) error {
				for i := range s.Runs {
					if s.Runs[i].Status == "queued" {
						s.Runs[i].Status = "running"
						run = s.Runs[i]
						break
					}
				}
				return nil
			})
			if err != nil || run.ID == "" {
				continue
			}
			list, listErr := accounts()
			successful := 0
			models := run.Task.Models
			if len(models) == 0 {
				models = []string{run.Task.Model}
			}
		targets:
			for _, id := range run.Targets {
				for _, model := range models {
					if e.ctx.Err() != nil {
						return
					}
					label := id
					reply := ""
					status := "CPA 账号列表中未找到该账号"
					a := slices.IndexFunc(list, func(a Account) bool { return a.ID == id })
					if listErr != nil {
						status = listErr.Error()
					} else if a >= 0 {
						label = list[a].Email
						request := run.Task
						request.Model = model
						var sendErr error
						reply, sendErr = activate(e.ctx, id, request)
						if sendErr != nil {
							status = sendErr.Error()
						} else {
							status = "success"
							successful++
						}
					}
					if err = e.update(func(s *State) error {
						for i := range s.Runs {
							if s.Runs[i].ID == run.ID {
								s.Runs[i].Records = append(s.Runs[i].Records, Record{Account: label, Status: status, Model: model, Reply: &reply})
								break
							}
						}
						return nil
					}); err != nil {
						break targets
					}
				}
			}
			final := "failed"
			if successful == len(run.Targets)*len(models) {
				final = "success"
			} else if successful > 0 {
				final = "partial"
			}
			e.update(func(s *State) error {
				for i := range s.Runs {
					if s.Runs[i].ID == run.ID {
						s.Runs[i].Status = final
					}
				}
				return nil
			})
		}
	}
}
func (e *Engine) refresh(ctx context.Context, id string) error {
	q, err := readQuota(ctx, id)
	if err != nil {
		return err
	}
	return e.update(func(s *State) error {
		s.Quotas[id] = q
		for i := range s.Tasks {
			t := &s.Tasks[i]
			if !t.Enabled || t.Kind != "quota" || !slices.Contains(selected(*t, s), id) {
				continue
			}
			if t.Resets == nil {
				t.Resets = map[string]int64{}
			}
			at := resetAt(*t, q, time.Now().Unix())
			if at > 0 && (t.Resets[id] == 0 || at < t.Resets[id]) {
				t.Resets[id] = at
			}
		}
		return nil
	})
}
func (e *Engine) quotaLoop() {
	defer e.wg.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-timer.C:
			ids := map[string]bool{}
			e.mu.Lock()
			for _, t := range e.state.Tasks {
				if t.Enabled && t.Kind == "quota" {
					for _, id := range selected(t, &e.state) {
						if e.state.Quotas[id].Checked < time.Now().Unix()-240 {
							ids[id] = true
						}
					}
				}
			}
			e.mu.Unlock()
			if len(ids) > 0 {
				list, _ := accounts()
				for _, a := range list {
					if e.ctx.Err() != nil {
						return
					}
					if ids[a.ID] {
						_ = e.refresh(e.ctx, a.ID)
					}
				}
			}
			timer.Reset(time.Minute)
		}
	}
}

func preview(t Task, s *State) []int64 {
	now := time.Now()
	times := []int64{}
	if t.Kind == "quota" {
		for _, id := range selected(t, s) {
			at := t.Resets[id]
			if at <= now.Unix() {
				at = resetAt(t, s.Quotas[id], now.Unix())
			}
			if at > 0 && !slices.Contains(times, at) {
				times = append(times, at)
			}
		}
		slices.Sort(times)
		return times[:min(5, len(times))]
	}
	if t.Kind == "startup" {
		if t.Next > now.Unix() {
			return []int64{t.Next}
		}
		return times
	}
	for len(times) < 5 {
		at := nextTime(t, now)
		if len(times) == 0 && t.Kind == "interval" && t.Next > now.Unix() {
			at = t.Next
		}
		if at <= now.Unix() {
			break
		}
		times = append(times, at)
		now = time.Unix(at, 0)
	}
	return times
}
func jsonReply(code int, data any) (int, string) {
	raw, _ := json.Marshal(data)
	return code, string(raw)
}
func problem(code int, err error) (int, string) {
	return jsonReply(code, map[string]string{"error": err.Error()})
}
func management(req resourceRequest) (int, string) {
	lifecycle.RLock()
	defer lifecycle.RUnlock()
	e := engine
	if e == nil {
		return problem(503, errors.New("插件尚未运行"))
	}
	if req.Method == "GET" {
		e.mu.Lock()
		defer e.mu.Unlock()
		return jsonReply(200, map[string]any{"state": e.state, "storageError": e.storageError})
	}
	var cmd struct {
		Action, ID string
		Task       Task
		IDs, Tags  []string
		Presets    []Preset
	}
	if req.Method != "POST" || len(req.Body) > 1<<20 || json.Unmarshal(req.Body, &cmd) != nil {
		return problem(400, errors.New("无效操作"))
	}
	switch cmd.Action {
	case "models":
		var catalog struct {
			Credentials []struct {
				Index    string   `json:"auth_index"`
				Provider string   `json:"provider"`
				Models   []string `json:"models"`
			} `json:"credentials"`
		}
		if err := hostCall("host.auth.models", struct{}{}, &catalog); err != nil {
			return problem(501, err)
		}
		byAccount := map[string][]string{}
		for _, c := range catalog.Credentials {
			if strings.EqualFold(c.Provider, "codex") {
				byAccount[c.Index] = unique(c.Models)
			}
		}
		return jsonReply(200, byAccount)
	case "accounts":
		list, err := accounts()
		if err != nil {
			return problem(502, err)
		}
		return jsonReply(200, list)
	case "preview":
		if err := validateScheduleDraft(cmd.Task); err != nil {
			return problem(400, err)
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		return jsonReply(200, preview(cmd.Task, &e.state))
	case "quota":
		list, err := accounts()
		if err != nil {
			return problem(502, err)
		}
		if !slices.ContainsFunc(list, func(a Account) bool { return a.ID == cmd.ID }) {
			return problem(400, errors.New("CPA 账号列表中未找到该账号"))
		}
		if err = e.refresh(e.ctx, cmd.ID); err != nil {
			return problem(502, err)
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		return jsonReply(200, e.state.Quotas[cmd.ID])
	}
	err := e.update(func(s *State) error {
		idx := slices.IndexFunc(s.Tasks, func(t Task) bool { return t.ID == cmd.ID })
		switch cmd.Action {
		case "save":
			t := cmd.Task
			if t.Models != nil {
				t.Models = unique(t.Models)
				if len(t.Models) == 0 {
					return errors.New("请选择模型")
				}
				for _, model := range t.Models {
					if len(model) > 256 {
						return errors.New("模型 ID 过长")
					}
				}
				t.Model = t.Models[0]
			}
			if err := validate(t); err != nil {
				return err
			}
			if t.Pool && len(s.Pool) == 0 {
				return errors.New("请先设置 API 服务账号池")
			}
			t.ID = cmd.ID
			if idx < 0 && t.ID != "" {
				return errors.New("任务已删除")
			}
			if t.ID == "" {
				t.ID = uid()
			}
			t.IDs = unique(t.IDs)
			schedule(&t, s, time.Now(), false)
			if idx >= 0 {
				old := s.Tasks[idx]
				if old.Enabled == t.Enabled && old.Kind == t.Kind && old.Hours == t.Hours && old.Delay == t.Delay && old.Clock == t.Clock && old.Zone == t.Zone && slices.Equal(old.Days, t.Days) {
					t.Next = old.Next
				}
				s.Tasks[idx] = t
			} else {
				s.Tasks = append(s.Tasks, t)
			}
		case "toggle", "delete", "run":
			if idx < 0 {
				return errors.New("任务不存在")
			}
			t := &s.Tasks[idx]
			if cmd.Action == "run" {
				return queue(s, *t, selected(*t, s), "manual", time.Now().Unix())
			}
			if cmd.Action == "delete" {
				s.Tasks = slices.Delete(s.Tasks, idx, idx+1)
			} else {
				t.Enabled = !t.Enabled
				schedule(t, s, time.Now(), false)
			}
			for i := range s.Runs {
				r := &s.Runs[i]
				if r.Task.ID == cmd.ID && (r.Status == "queued" || r.Status == "pending") {
					r.Status = "skipped"
				}
			}
		case "approve", "skip":
			for i := range s.Runs {
				r := &s.Runs[i]
				if r.ID == cmd.ID && r.Status == "pending" && r.Deadline > time.Now().Unix() {
					r.Status = "skipped"
					if cmd.Action == "approve" {
						r.Status = "queued"
					}
					return nil
				}
			}
			return errors.New("确认已失效")
		case "pool":
			s.Pool = unique(cmd.IDs)
			for i := range s.Tasks {
				t := &s.Tasks[i]
				if t.Pool && t.Kind == "quota" {
					schedule(t, s, time.Now(), false)
				}
			}
		case "tags":
			s.Tags[cmd.ID] = unique(cmd.Tags)
		case "presets":
			if len(cmd.Presets) == 0 {
				return errors.New("至少保留一个模型预设")
			}
			seen := map[string]bool{}
			for _, p := range cmd.Presets {
				if strings.TrimSpace(p.Model) == "" || strings.TrimSpace(p.Name) == "" || seen[p.Model] || !slices.Contains(p.Efforts, p.Default) {
					return errors.New("预设需要唯一模型 ID、名称和有效默认强度")
				}
				seen[p.Model] = true
			}
			s.Presets = cmd.Presets
		default:
			return fmt.Errorf("未知操作")
		}
		return nil
	})
	if err != nil {
		return problem(400, err)
	}
	return jsonReply(200, true)
}
func validateScheduleDraft(t Task) error {
	t.Name = "preview"
	t.Model = "preview"
	t.Pool = true
	return validate(t)
}
func unique(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
