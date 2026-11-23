// Package jobs runs collection and NetBox sync jobs in the background.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// Job status values.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusError   = "error"
)

// Job triggers describe what started a job.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// Job is one collection or sync run.
type Job struct {
	ID         string           `json:"id"`
	TargetID   string           `json:"target_id"`
	TargetName string           `json:"target_name"`
	Protocol   string           `json:"protocol"`
	Mode       string           `json:"mode"`
	Trigger    string           `json:"trigger"`
	Status     string           `json:"status"`
	CreatedAt  time.Time        `json:"created_at"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Error      string           `json:"error,omitempty"`
	Log        []string         `json:"log,omitempty"`
	Result     *model.Collected `json:"result,omitempty"`
	Sync       *model.SyncResult `json:"sync,omitempty"`
}

// JobSummary is the lightweight form used by list endpoints.
type JobSummary struct {
	ID         string     `json:"id"`
	TargetID   string     `json:"target_id"`
	TargetName string     `json:"target_name"`
	Protocol   string     `json:"protocol"`
	Mode       string     `json:"mode"`
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Interfaces int        `json:"interfaces"`
	Components int        `json:"components"`
}

// RunFunc performs the actual work. It receives a context bounded by the
// manager timeout and a logging callback.
type RunFunc func(ctx context.Context, logf model.LogFunc) (*model.Collected, *model.SyncResult, error)

// Options configures the manager.
type Options struct {
	MaxConcurrent int
	Timeout       time.Duration
	Retain        int
}

// Manager keeps a bounded history of jobs and enforces concurrency.
type Manager struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	order   []string
	sem     chan struct{}
	timeout time.Duration
	retain  int
}

// New creates a job manager.
func New(opts Options) *Manager {
	if opts.MaxConcurrent < 1 {
		opts.MaxConcurrent = 4
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.Retain < 10 {
		opts.Retain = 200
	}
	return &Manager{
		jobs:    make(map[string]*Job),
		sem:     make(chan struct{}, opts.MaxConcurrent),
		timeout: opts.Timeout,
		retain:  opts.Retain,
	}
}

// Start queues a job and returns its ID. The trigger is "manual" or
// "schedule" and is recorded for display purposes.
func (m *Manager) Start(target model.Target, mode, trigger string, run RunFunc) string {
	if trigger == "" {
		trigger = TriggerManual
	}
	id := newID()
	job := &Job{
		ID:         id,
		TargetID:   target.ID,
		TargetName: target.Name,
		Protocol:   target.Protocol,
		Mode:       mode,
		Trigger:    trigger,
		Status:     StatusQueued,
		CreatedAt:  time.Now().UTC(),
	}
	m.mu.Lock()
	m.jobs[id] = job
	m.order = append(m.order, id)
	m.pruneLocked()
	m.mu.Unlock()

	go m.execute(job, run)
	return id
}

func (m *Manager) execute(job *Job, run RunFunc) {
	m.sem <- struct{}{}
	defer func() { <-m.sem }()

	now := time.Now().UTC()
	m.mu.Lock()
	job.Status = StatusRunning
	job.StartedAt = &now
	m.mu.Unlock()
	m.logf(job, "开始执行 %s 任务", job.Mode)

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	var (
		result  *model.Collected
		syncRes *model.SyncResult
		err     error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		result, syncRes, err = run(ctx, func(format string, args ...any) {
			m.logf(job, format, args...)
		})
	}()

	finished := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	job.FinishedAt = &finished
	job.Result = result
	job.Sync = syncRes
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("任务超时（%s）: %w", m.timeout, err)
		}
		job.Status = StatusError
		job.Error = err.Error()
		m.appendLogLocked(job, "任务失败: "+err.Error())
		return
	}
	job.Status = StatusSuccess
	m.appendLogLocked(job, "任务完成")
}

func (m *Manager) logf(job *Job, format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	m.mu.Lock()
	m.appendLogLocked(job, line)
	m.mu.Unlock()
}

func (m *Manager) appendLogLocked(job *Job, line string) {
	const maxLines = 2000
	if len(job.Log) >= maxLines {
		return
	}
	if len(job.Log) == maxLines-1 {
		line = "... (log truncated)"
	}
	job.Log = append(job.Log, line)
}

// Get returns a snapshot of a job including its log.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return snapshot(job), true
}

// List returns newest jobs first.
func (m *Manager) List(limit int) []JobSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > len(m.order) {
		limit = len(m.order)
	}
	out := make([]JobSummary, 0, limit)
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		job, ok := m.jobs[m.order[i]]
		if !ok {
			continue
		}
		summary := JobSummary{
			ID:         job.ID,
			TargetID:   job.TargetID,
			TargetName: job.TargetName,
			Protocol:   job.Protocol,
			Mode:       job.Mode,
			Trigger:    job.Trigger,
			Status:     job.Status,
			CreatedAt:  job.CreatedAt,
			StartedAt:  job.StartedAt,
			FinishedAt: job.FinishedAt,
			Error:      job.Error,
		}
		if job.Result != nil {
			summary.Interfaces = len(job.Result.Interfaces)
			summary.Components = len(job.Result.Components)
		}
		out = append(out, summary)
	}
	return out
}

// ActiveFor reports whether the target already has a queued or running job.
func (m *Manager) ActiveFor(targetID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.TargetID != targetID {
			continue
		}
		if job.Status == StatusQueued || job.Status == StatusRunning {
			return true
		}
	}
	return false
}

// Stats returns counts by status.
func (m *Manager) Stats() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := map[string]int{
		StatusQueued:  0,
		StatusRunning: 0,
		StatusSuccess: 0,
		StatusError:   0,
	}
	for _, job := range m.jobs {
		stats[job.Status]++
	}
	stats["total"] = len(m.jobs)
	return stats
}

// ClearFinished removes completed jobs from the history.
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	out := m.order[:0]
	for _, id := range m.order {
		job, ok := m.jobs[id]
		if !ok {
			continue
		}
		if job.Status == StatusRunning || job.Status == StatusQueued {
			out = append(out, id)
			continue
		}
		delete(m.jobs, id)
		removed++
	}
	m.order = out
	return removed
}

func (m *Manager) pruneLocked() {
	for len(m.order) > m.retain {
		removed := false
		for i, id := range m.order {
			job, ok := m.jobs[id]
			if !ok {
				m.order = append(m.order[:i], m.order[i+1:]...)
				removed = true
				break
			}
			if job.Status == StatusRunning || job.Status == StatusQueued {
				continue
			}
			delete(m.jobs, id)
			m.order = append(m.order[:i], m.order[i+1:]...)
			removed = true
			break
		}
		if !removed {
			return
		}
	}
}

func snapshot(job *Job) Job {
	out := *job
	if len(job.Log) > 0 {
		out.Log = append([]string(nil), job.Log...)
	}
	return out
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}