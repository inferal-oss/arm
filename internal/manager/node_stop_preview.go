package manager

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/nodestop"
)

func nodeStopReadScope(actor nodestop.Actor, views []nodestop.LeaseReadScope) nodestop.LeaseReadScope {
	if len(views) != 1 || views[0].Source != actor.Host {
		return nodestop.LeaseReadScope{}
	}
	return views[0]
}

// Every obligation survives. Only optional previews are shortened; the private
// census digest continues to cover the complete original recorded lease. Size
// is measured after JSON escaping, not by counting displayed characters.
func boundNodeStopPreviews(items []nodestop.Obligation) {
	count := 0
	for _, item := range items {
		if item.Lease != nil {
			count++
		}
	}
	if count == 0 {
		return
	}
	share := 8192 / count
	for n := range items {
		v := items[n].Lease
		if v == nil {
			continue
		}
		for {
			raw, err := json.Marshal(v)
			if err != nil || len(raw) <= share {
				break
			}
			v.Truncated = true
			// Prefer the recorded account and purpose over extra attribution fields.
			switch {
			case v.Project != "":
				v.Project = ""
			case v.Agent != "":
				v.Agent = ""
			case len(v.Reason) > 40:
				v.Reason = trimPreview(v.Reason)
			case v.Username != "":
				v.Username = ""
			case v.Reason != "":
				v.Reason = trimPreview(v.Reason)
			default:
				break
			}
			if v.Project == "" && v.Agent == "" && v.Username == "" && v.Reason == "" {
				break
			}
		}
	}
}

func trimPreview(value string) string {
	// Remove at least one rune and usually half; never produce invalid UTF-8.
	end := len(value) / 2
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
