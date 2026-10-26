//go:build !arm_forward_review

package meshsshrelay

// ForwardReviewEnabled fences TCP and generic transit authority until acceptance gates pass.
// Ordinary builds preserve only the existing class-specific SSH/sync grants.
const ForwardReviewEnabled = false
