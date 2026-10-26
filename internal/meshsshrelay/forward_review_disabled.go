//go:build !arm_forward_review

package meshsshrelay

// ForwardReviewEnabled fences unapproved TCP and generic transit authority.
// Ordinary builds preserve only the existing class-specific SSH/sync grants.
const ForwardReviewEnabled = false
