package forecast

// confidence returns a confidence level string based on the amount of
// telemetry history available and the number of commits in the branch.
//
//   - high:   >= 7 days of telemetry AND >= 5 commits
//   - medium: >= 3 days of telemetry
//   - low:    less than 3 days of telemetry (using defaults)
func confidence(telemetryDays int, commits int) string {
	switch {
	case telemetryDays >= 7 && commits >= 5:
		return "high"
	case telemetryDays >= 3:
		return "medium"
	default:
		return "low"
	}
}
