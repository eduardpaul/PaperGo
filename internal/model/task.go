package model

// TaskOutcome is an answer a task offers, such as approved or rejected.
type TaskOutcome struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Style is primary, danger or default: how the answer's button looks.
	Style string `json:"style,omitempty"`
	// Comment is none, optional (the default) or required.
	Comment string `json:"comment,omitempty"`
	// SkipForm answers without the task's form, such as a rejection that
	// needs no data.
	SkipForm bool `json:"skip_form,omitempty"`
	// AllowStale allows the answer when the task's review shows a staged
	// file made from files that are no longer current, as a rejection may.
	AllowStale bool `json:"allow_stale,omitempty"`
	// Decisive completes an all-mode task at once with this answer.
	Decisive bool `json:"decisive,omitempty"`
}
