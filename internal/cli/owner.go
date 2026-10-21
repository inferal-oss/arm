package cli

import "fmt"

// Account names are captured by the backend. Display never resolves old UIDs
// using today's account database or changes the wire identity.
func accountLabel(record map[string]any, key string) string {
	uid := terminalSafe(fmt.Sprint(record[key]))
	if name, ok := record[key+"_username"].(string); ok && name != "" {
		return terminalSafe(name) + " (UID " + uid + ")"
	}
	return "UID " + uid
}
