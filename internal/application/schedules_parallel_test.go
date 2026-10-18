package application

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

// blockingScheduleService gives every prompt its own provider that blocks until cancelled, and reports each start.
func blockingScheduleService(t *testing.T) (*Service, string, chan struct{}) {
	t.Helper()
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "parallel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	started := make(chan struct{}, 8)
	factory := func(openai.Config) (agentcore.Provider, error) {
		provider := &fakeProvider{block: true, started: make(chan struct{})}
		go func() { <-provider.started; started <- struct{}{} }()
		return provider, nil
	}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: &memorySecrets{values: map[secrets.Reference]string{}}, External: map[string]ExternalBackend{}, ProviderFactory: factory})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	return s, workspace.ID, started
}

func runNow(t *testing.T, s *Service, workspaceID, name string) ScheduleJobDTO {
	t.Helper()
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: name, TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestTwoDueSchedulesRunAtTheSameTime(t *testing.T) {
	s, workspaceID, started := blockingScheduleService(t)
	scheduler := StartScheduler(s)
	defer scheduler.Stop()
	runNow(t, s, workspaceID, "First")
	runNow(t, s, workspaceID, "Second")
	for index := range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of two schedules started while the other ran", index)
		}
	}
}

func TestStoppingTheSchedulerCancelsAndJoinsEveryJob(t *testing.T) {
	s, workspaceID, started := blockingScheduleService(t)
	scheduler := StartScheduler(s)
	jobs := []ScheduleJobDTO{runNow(t, s, workspaceID, "First"), runNow(t, s, workspaceID, "Second")}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("a schedule did not start")
		}
	}
	done := make(chan struct{})
	go func() { scheduler.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduler did not join its jobs")
	}
	for _, job := range jobs {
		got, err := s.GetScheduleJob(job.ID)
		if err != nil || (got.Status != "cancelled" && got.Status != "interrupted") {
			t.Fatalf("job after stop: %+v %v", got, err)
		}
	}
}

func TestPullRequestWatchTicksWhileAJobRuns(t *testing.T) {
	s, workspaceID, started := blockingScheduleService(t)
	var ticks atomic.Int32
	var once sync.Once
	blocked := make(chan struct{})
	s.pullRequestWatchTick = func(time.Time) {
		select {
		case <-blocked:
			ticks.Add(1)
		default:
		}
	}
	scheduler := StartScheduler(s)
	defer scheduler.Stop()
	runNow(t, s, workspaceID, "Long")
	select {
	case <-started:
		once.Do(func() { close(blocked) })
	case <-time.After(3 * time.Second):
		t.Fatal("the schedule did not start")
	}
	time.Sleep(2500 * time.Millisecond)
	if ticks.Load() < 2 {
		t.Fatalf("pull request watching waited for the job: %d ticks", ticks.Load())
	}
}
