package server

import (
	"context"
	"time"

	"github.com/Songxwn/netbox-glean/internal/jobs"
)

// Scheduler timing. The first check happens shortly after startup so a due
// target runs soon without delaying the HTTP server.
const (
	schedulerTick  = 30 * time.Second
	schedulerDelay = 5 * time.Second
)

// StartScheduler checks for due targets until ctx is cancelled. Scheduled
// runs always collect and sync; targets with an active job are skipped.
func (s *Server) StartScheduler(ctx context.Context) {
	go func() {
		timer := time.NewTimer(schedulerDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.runDueTargets()
				timer.Reset(schedulerTick)
			}
		}
	}()
	s.logger.Info("scheduler started", "tick", schedulerTick.String())
}

// runDueTargets starts a job for every enabled target whose interval elapsed.
func (s *Server) runDueTargets() {
	now := time.Now().UTC()
	for _, target := range s.store.Targets() {
		if !target.ScheduleDue(now) {
			continue
		}
		if s.jobs.ActiveFor(target.ID) {
			continue
		}
		if err := s.store.MarkTargetRun(target.ID, now); err != nil {
			s.logger.Error("record scheduled run", "target", target.Name, "error", err)
			continue
		}
		jobID := s.startJobWith(target, true, jobs.TriggerSchedule)
		s.logger.Info("scheduled job started", "target", target.Name, "job", jobID)
	}
}