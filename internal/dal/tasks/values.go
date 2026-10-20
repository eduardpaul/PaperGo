package tasks

// Status is where a task is in its lifecycle.
type Status string

const (
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusCompleted, StatusCancelled, StatusExpired:
		return true
	}
	return false
}

// Kind is what a task asks for; an approval answers approved or rejected.
type Kind string

const (
	KindTask     Kind = "task"
	KindApproval Kind = "approval"
)

func (k Kind) Valid() bool { return k == KindTask || k == KindApproval }

// Completion is how many assignee entries must answer: any completes on the
// first answer, all once every entry has answered.
type Completion string

const (
	CompletionAny Completion = "any"
	CompletionAll Completion = "all"
)

func (c Completion) Valid() bool { return c == CompletionAny || c == CompletionAll }

type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh:
		return true
	}
	return false
}

// Source is who created a task.
type Source string

const (
	SourceWorkflow Source = "workflow"
	SourceUser     Source = "user"
	SourceSystem   Source = "system"
)

func (s Source) Valid() bool {
	switch s {
	case SourceWorkflow, SourceUser, SourceSystem:
		return true
	}
	return false
}
