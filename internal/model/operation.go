package model

type OperationFailure struct {
	ItemID string `json:"item_id"`
	Reason string `json:"reason"`
}
