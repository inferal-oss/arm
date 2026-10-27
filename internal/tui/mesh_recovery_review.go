package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"getarm.org/internal/mesh"
)

// Existing recovery reviews are distinct from granting new trust. Their
// producer deliberately omits SSH credentials; never infer absent account/key
// or listener facts from the current host or an unrelated operation.
func meshRecoveryReviewSummary(raw json.RawMessage) (string, bool) {
	var review struct {
		Operation      string         `json:"operation"`
		Original       string         `json:"original_key"`
		Pair           string         `json:"pair"`
		Local          meshReviewNode `json:"local"`
		Remote         meshReviewNode `json:"remote"`
		LocalListen    string         `json:"local_listen"`
		RemoteListen   string         `json:"remote_listen"`
		LocalRevision  int            `json:"local_revision"`
		RemoteRevision int            `json:"remote_revision"`
		Forward        *mesh.Grant    `json:"source_permissions_on_target"`
		Reverse        *mesh.Grant    `json:"target_permissions_on_source"`
		Effect         string         `json:"effect"`
	}
	if json.Unmarshal(raw, &review) != nil {
		return "Recovery review could not be decoded. Inspect the full record; approval is unavailable.", false
	}
	left, right := review.Local.Identity, review.Remote.Identity
	if left == nil || right == nil || left.Host == "" || right.Host == "" || left.Certificate == "" || right.Certificate == "" || review.Pair == "" || review.Original == "" || review.Effect == "" {
		return "Existing-connection recovery lacks exact identities, pins, pair, original receipt or consequences. Inspect the complete record; approval is unavailable.", false
	}
	var out strings.Builder
	label := "VERIFY EXISTING CONNECTION · READ-ONLY PROOF"
	if review.Operation == "restore-existing-connection" {
		label = "RESTORE EXISTING CONNECTION · SAVED TRUST ONLY"
	}
	fmt.Fprintf(&out, "%s\nOriginal receipt: %s\nExact pair: %s\n\n", strong.Render(label), safe(review.Original), safe(review.Pair))
	out.WriteString(meshIdentitySummary("THIS SOURCE NODE · EXISTING IDENTITY", left, meshReviewPrepared{}, review.Local) + "\n")
	out.WriteString(meshIdentitySummary("DESTINATION NODE · EXISTING IDENTITY", right, meshReviewPrepared{}, review.Remote) + "\n")
	var forward, reverse *mesh.Grant
	switch review.Operation {
	case "verify-existing-connection":
		if review.LocalListen == "" || review.RemoteListen == "" || review.LocalRevision <= 0 || review.RemoteRevision <= 0 {
			return "Exact listener endpoints or enrollment revisions are missing from verification review. Inspect the record; approval is unavailable.", false
		}
		fmt.Fprintf(&out, "EXISTING LISTENERS AND ENROLLMENT\nSource listener: %s · revision %d\nDestination listener: %s · revision %d\n\n", safe(review.LocalListen), review.LocalRevision, safe(review.RemoteListen), review.RemoteRevision)
		forward, reverse = review.Forward, review.Reverse
	case "restore-existing-connection":
		if review.Local.Prior == nil || review.Remote.Prior == nil {
			return "Existing enrollment snapshots are missing from restore review. Inspect the record; approval is unavailable.", false
		}
		forward, reverse = review.Remote.Prior.Grant, review.Local.Prior.Grant
	default:
		return "Unrecognized recovery type; approval is unavailable.", false
	}
	if forward == nil || reverse == nil {
		return "Exact existing directional grants are missing. Inspect the complete record; approval is unavailable.", false
	}
	out.WriteString("WE MAY DO THERE · existing destination grant\n" + meshGrantText(grantView(*forward)) + "\n\nPEER MAY DO HERE · existing source grant\n" + meshGrantText(grantView(*reverse)) + "\n\n")
	out.WriteString("SSH account/key are not included in this recovery plan; inspect the linked original durable receipt. No new identity or grant is inferred.\n\nCONSEQUENCES\n" + safe(review.Effect) + "\n")
	return out.String(), true
}
