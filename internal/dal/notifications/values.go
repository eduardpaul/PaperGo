package notifications

// Type is what a notification tells.
type Type string

const (
	// TypeMessage is a message a person or a workflow sends.
	TypeMessage Type = "message"
	// TypeTaskAssigned tells people a task waits for them.
	TypeTaskAssigned Type = "task.assigned"
	// TypeTaskCompleted tells the creator of a person's task its answer.
	TypeTaskCompleted Type = "task.completed"
	// TypeWorkflowFailed tells who is responsible for a workflow that a run
	// failed.
	TypeWorkflowFailed Type = "workflow.failed"
	// TypeCommentMentioned tells people a comment mentions them.
	TypeCommentMentioned Type = "comment.mentioned"
	// TypeCommentReplied tells a thread's participants of new replies.
	TypeCommentReplied Type = "comment.replied"
)

func (t Type) Valid() bool {
	switch t {
	case TypeMessage, TypeTaskAssigned, TypeTaskCompleted, TypeWorkflowFailed, TypeCommentMentioned, TypeCommentReplied:
		return true
	}
	return false
}

// Source is who sent a notification.
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
