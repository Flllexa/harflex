package application

import (
	"context"
	"sync"
	"time"
)

// Scheduler is owned by the desktop lifecycle. Its sole worker serializes
// scheduled jobs; Stop cancels active work and joins before SQLite closes.
type Scheduler struct {
	service *Service
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func StartScheduler(service *Service) *Scheduler {
	ctx, cancel := context.WithCancel(service.ctx)
	scheduler := &Scheduler{service: service, cancel: cancel, done: make(chan struct{})}
	go scheduler.run(ctx)
	return scheduler
}

func (scheduler *Scheduler) run(ctx context.Context) {
	defer close(scheduler.done)
	openedAt := time.Now().UTC()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		scheduler.service.tickPullRequestWatch(time.Now().UTC())
		_ = scheduler.service.tickSchedules(ctx, time.Now().UTC(), openedAt)
		select {
		case <-ctx.Done():
			return
		case <-scheduler.service.scheduleWake:
		case <-ticker.C:
		}
	}
}

func (scheduler *Scheduler) Stop() {
	scheduler.once.Do(func() {
		scheduler.cancel()
		scheduler.service.scheduleMu.Lock()
		active := scheduler.service.scheduleRunning
		scheduler.service.scheduleMu.Unlock()
		if active != "" {
			_, _ = scheduler.service.CancelScheduleJob(active)
		}
		<-scheduler.done
	})
}
