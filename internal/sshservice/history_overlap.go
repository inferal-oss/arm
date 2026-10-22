package sshservice

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// An interrupted unlink can legitimately retain identical hot/pending copies.
// Contradictory bytes remain unresolved evidence, never a successful readonly
// settlement response or permission to discard either representation.
func checkSourceHistoryOverlap(s *State, cold SourceHistoryLookup, raw []byte) error {
	if !cold.Archived {
		return nil
	}
	var intent SourceIntent
	if cold.Kind == "completion" && cold.Completion != nil {
		intent = cold.Completion.Intent
	} else if cold.Kind == "seal" && cold.Seal != nil {
		intent = cold.Seal.Intent
	} else {
		return fmt.Errorf("cold source family missing original intention")
	}
	for _, item := range []struct{ name, kind string }{{completionName(intent), "completion"}, {sealCompletionName(intent), "seal"}} {
		if hot, ok := s.contents[item.name]; ok && (item.kind != cold.Kind || !bytes.Equal(hot, raw)) {
			return fmt.Errorf("hot/cold source settlement evidence contradicts")
		}
	}
	if pending, ok := s.contents[intentName(intent)]; ok {
		original, e := json.Marshal(intent)
		if e != nil || !bytes.Equal(pending, original) {
			return fmt.Errorf("cold source receipt/pending original contradicts")
		}
	}
	return nil
}
