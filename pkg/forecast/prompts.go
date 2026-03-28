package forecast

// estimatePrompts uses a simple heuristic to estimate the number of AI prompts
// needed to produce the given commits.
//
// Heuristic:
//   - small commit (<=2 files changed):  1 prompt
//   - medium commit (3-5 files changed): 2 prompts
//   - large commit (>5 files changed):   3 prompts
func estimatePrompts(commits []Commit) int {
	total := 0
	for _, c := range commits {
		switch {
		case c.FilesChanged <= 2:
			total += 1
		case c.FilesChanged <= 5:
			total += 2
		default:
			total += 3
		}
	}
	return total
}
