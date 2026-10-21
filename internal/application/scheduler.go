package application

import (
	"context"
	"sync"
	"time"
)

// Scheduler is owned by the desktop lifecycle. Its worker claims due jobs and runs each one in its own goroutine;
// Stop cancels active work and joins every job before SQLite closes.
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
	// Pull requests are watched on their own beat, so a running job never holds them up.
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		watch := time.NewTicker(time.Second)
		defer watch.Stop()
		for {
			scheduler.service.pullRequestWatch(time.Now().UTC())
			select {
			case <-ctx.Done():
				return
			case <-watch.C:
			}
		}
	}()
	defer func() { <-watching }()
	// Chat watchers too: their checks can take up to a minute, so they never hold up the schedules.
	watchers := make(chan struct{})
	go func() {
		defer close(watchers)
		beat := time.NewTicker(5 * time.Second)
		defer beat.Stop()
		for {
			scheduler.service.tickChatWatchers(time.Now().UTC())
			select {
			case <-ctx.Done():
				return
			case <-beat.C:
			}
		}
	}()
	defer func() { <-watchers }()
	for {
		if ctx.Err() != nil {
			return
		}
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
		for _, active := range scheduler.service.activeScheduleJobs() {
			_, _ = scheduler.service.CancelScheduleJob(active)
		}
		<-scheduler.done
		scheduler.service.scheduleJobs.Wait()
	})
}

func (s *Service) pullRequestWatch(now time.Time) {
	if s.pullRequestWatchTick != nil {
		s.pullRequestWatchTick(now)
		return
	}
	s.tickPullRequestWatch(now)
}
