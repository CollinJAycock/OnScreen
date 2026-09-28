package v1

import "strings"

// hubRowsAfterContinue are hub rows that shipped after per-user layouts did.
// A layout saved before they existed doesn't mention them, and the clients'
// generic rule (unknown-to-the-layout rows go last) would bury them under
// every library strip. withDefaultHubRows slots them in where the default
// layout has them — straight after the Continue Watching rows — enabled.
// Once the user saves a layout that includes them (hidden or moved), their
// choice is kept as-is.
var hubRowsAfterContinue = []string{"next_up", "plan_to_watch"}

// withDefaultHubRows returns layout with any missing hubRowsAfterContinue key
// inserted, enabled, after the last "continue_*" entry (at the top when the
// layout has none). An empty layout means "never customized" and is returned
// unchanged — clients render their default order for it.
func withDefaultHubRows(layout []hubRowPref) []hubRowPref {
	if len(layout) == 0 {
		return layout
	}
	present := make(map[string]bool, len(layout))
	insertAt := 0
	for i, p := range layout {
		present[p.Key] = true
		if strings.HasPrefix(p.Key, "continue_") {
			insertAt = i + 1
		}
	}
	var missing []hubRowPref
	for _, k := range hubRowsAfterContinue {
		if !present[k] {
			missing = append(missing, hubRowPref{Key: k, Enabled: true})
		}
	}
	if len(missing) == 0 {
		return layout
	}
	out := make([]hubRowPref, 0, len(layout)+len(missing))
	out = append(out, layout[:insertAt]...)
	out = append(out, missing...)
	out = append(out, layout[insertAt:]...)
	return out
}
