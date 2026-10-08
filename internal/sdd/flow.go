package sdd

import "errors"

type Stage string

const (
	Discovery Stage = "discovery"
	Spec      Stage = "spec"
	Plan      Stage = "plan"
	Code      Stage = "code"
	// Eval is the QA stage; the identifier stays "eval" because catalogs already hold it.
	Eval Stage = "eval"
	// PRs hands the approved, applied change to the agent so it opens the pull requests. Pipelines saved before it
	// existed have no status for it and finished at Eval.
	PRs Stage = "prs"
)

var Stages = [...]Stage{Discovery, Spec, Plan, Code, Eval, PRs}

type Status string

const (
	Pending     Status = "pending"
	Active      Status = "active"
	Completed   Status = "completed"
	Skipped     Status = "skipped"
	Failed      Status = "failed"
	WaitingUser Status = "waiting_user"
)

var ErrEvidenceRequired = errors.New("phase evidence required")
var ErrInvalidTransition = errors.New("invalid phase transition")
var ErrBrainstormBudgetExceeded = errors.New("brainstorm budget exceeded")

type Flow struct {
	Current Stage            `json:"current"`
	Status  map[Stage]Status `json:"status"`
}

func NewFlow() Flow {
	status := make(map[Stage]Status, len(Stages))
	for _, stage := range Stages {
		status[stage] = Pending
	}
	status[Discovery] = Active
	return Flow{Current: Discovery, Status: status}
}

func next(flow Flow, status Status) (Flow, error) {
	index := -1
	for i, stage := range Stages {
		if stage == flow.Current {
			index = i
			break
		}
	}
	if index < 0 || flow.Status[flow.Current] != Active {
		return Flow{}, ErrInvalidTransition
	}
	copyStatus := make(map[Stage]Status, len(flow.Status))
	for stage, value := range flow.Status {
		copyStatus[stage] = value
	}
	copyStatus[flow.Current] = status
	if index+1 == len(Stages) {
		return Flow{Current: "", Status: copyStatus}, nil
	}
	current := Stages[index+1]
	copyStatus[current] = Active
	return Flow{Current: current, Status: copyStatus}, nil
}

func Advance(flow Flow, evidence bool) (Flow, error) {
	if !evidence {
		return Flow{}, ErrEvidenceRequired
	}
	return next(flow, Completed)
}

// ApproveDiscovery is the authoring gate; legacy Advance retains its contract.
func ApproveDiscovery(flow Flow) (Flow, error) {
	if flow.Current != Discovery || flow.Status[Discovery] != WaitingUser {
		return Flow{}, ErrInvalidTransition
	}
	status := make(map[Stage]Status, len(flow.Status))
	for stage, value := range flow.Status {
		status[stage] = value
	}
	status[Discovery] = Completed
	status[Spec] = Active
	return Flow{Current: Spec, Status: status}, nil
}

func Skip(flow Flow) (Flow, error) {
	if flow.Current == Code || flow.Current == Eval {
		return Flow{}, ErrEvidenceRequired
	}
	return next(flow, Skipped)
}

func Revise(flow Flow) (Flow, error) {
	if flow.Current != Eval || flow.Status[Eval] != Active {
		return Flow{}, ErrInvalidTransition
	}
	copyStatus := make(map[Stage]Status, len(flow.Status))
	for stage, value := range flow.Status {
		copyStatus[stage] = value
	}
	copyStatus[Eval] = Failed
	copyStatus[Code] = Active
	return Flow{Current: Code, Status: copyStatus}, nil
}
