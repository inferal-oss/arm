package meshops

import (
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

type StopDraft struct {
	Host   string                `json:"host"`
	Reason string                `json:"reason"`
	Policy model.ShutdownRequest `json:"policy"`
}

type StopReview = nodestop.SourceReview
type StopCompletionCheck = nodestop.CompletionCheck
type StopResult = nodestop.Result
