package application

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

var workStageOrder = []struct{ key, label string }{
	{"discovery", "Discovery"}, {"spec", "SPEC"}, {"plan", "Plan"}, {"code", "Code"}, {"eval", "QA"}, {"prs", "PRs"},
}

func workStageLabel(stage string) string {
	for _, item := range workStageOrder {
		if item.key == stage {
			return item.label
		}
	}
	return stage
}

// workStatusReport says, in plain text, every phase of a work, where it stopped and what the person can do next. It is
// what the work's chat is told, so it can answer "where are we" without exploring the project to find out.
func (s *Service) workStatusReport(pipelineID string) (string, error) {
	run, err := s.GetPipeline(pipelineID)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	title := strings.TrimSpace(run.Title)
	if title == "" || !utf8.ValidString(title) {
		title = "untitled work"
	}
	fmt.Fprintf(&out, "Work: %s\n", title)
	if objective := strings.TrimSpace(run.Objective); objective != "" && utf8.ValidString(objective) {
		fmt.Fprintf(&out, "Objective: %s\n", clipUTF8(objective, 600))
	}
	out.WriteString("Phases:\n")
	for _, item := range workStageOrder {
		status := run.StageStatus[item.key]
		if status == "" {
			status = "pending"
		}
		line := fmt.Sprintf("- %s: %s", item.label, workStatusPhrase(status))
		if artifact, ok := run.Artifacts[item.key]; ok && artifact.Version > 0 {
			line += fmt.Sprintf("; document version %d", artifact.Version)
			if artifact.ReviewStatus != "" {
				line += ", review " + artifact.ReviewStatus
			}
		}
		if item.key == run.CurrentStage {
			line += "  <- current phase"
		}
		out.WriteString(line + "\n")
	}

	stopped := "every phase is finished"
	if run.CurrentStage != "" {
		stopped = fmt.Sprintf("it stopped at %s (%s)", workStageLabel(run.CurrentStage), workStatusPhrase(run.StageStatus[run.CurrentStage]))
	}
	fmt.Fprintf(&out, "Where it stands: %s.\n", stopped)

	switch run.CurrentStage {
	case "discovery", "spec", "plan":
		if run.StageStatus[run.CurrentStage] == "waiting_user" {
			fmt.Fprintf(&out, "Next: the %s document waits for the person to review and approve it on the Pipelines screen.\n", workStageLabel(run.CurrentStage))
		} else {
			fmt.Fprintf(&out, "Next: the %s document is being prepared; the person approves it on the Pipelines screen.\n", workStageLabel(run.CurrentStage))
		}
	}
	if activity, err := s.GetPipelineStageActivity(pipelineID, "code"); err == nil && activity.SessionID != "" && (run.CurrentStage == "code" || run.StageStatus["code"] == "active") {
		fmt.Fprintf(&out, "Code: the Coder's last run is %s.\n", activity.Status)
	}
	if run.StageStatus["code"] == "completed" || run.CodeAppliedAt != nil || run.CodePatchPending {
		switch {
		case run.CodeAppliedAt != nil:
			out.WriteString("Code: the approved patch is applied to the project folder.\n")
		case run.CodePatchPending:
			out.WriteString("Code: the approved patch is NOT yet applied to the project folder; it is applied when the person approves opening the PRs.\n")
		}
	}
	if qa, err := s.GetPipelineQALoop(pipelineID); err == nil && qa.Phase != "" {
		state := qa.Phase
		if qa.Running {
			state += ", running"
		}
		fmt.Fprintf(&out, "QA: %s, round %d", state, qa.Round)
		if msg := strings.TrimSpace(qa.Message); msg != "" && utf8.ValidString(msg) {
			fmt.Fprintf(&out, " - %s", clipUTF8(msg, 300))
		}
		out.WriteString(".\n")
	}
	if prs, err := s.ListPipelinePullRequests(pipelineID); err == nil && len(prs) > 0 {
		out.WriteString("Pull requests:\n")
		for _, pr := range prs {
			watch := ""
			if pr.Watch {
				watch = ", watched for review comments"
			}
			fmt.Fprintf(&out, "- %s (%s%s) %s\n", clipUTF8(strings.TrimSpace(pr.Title), 120), pr.State, watch, pr.URL)
		}
	}
	return out.String(), nil
}

func workStatusPhrase(status string) string {
	switch status {
	case "completed":
		return "done"
	case "active":
		return "in progress"
	case "waiting_user":
		return "waiting for the person"
	case "skipped":
		return "skipped"
	case "failed":
		return "failed"
	}
	return "not started"
}
