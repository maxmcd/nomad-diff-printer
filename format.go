package nomaddiffprinter

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/nomad/api"
)

func formatAllocMetrics(metrics *api.AllocationMetric, scores bool, prefix string) string {
	// Print a helpful message if we have an eligibility problem
	var out string
	if metrics.NodesEvaluated == 0 {
		out += fmt.Sprintf("%s* No nodes were eligible for evaluation\n", prefix)
	}

	// Print a helpful message if the user has asked for a DC that has no
	// available nodes.
	for dc, available := range metrics.NodesAvailable {
		if available == 0 {
			out += fmt.Sprintf("%s* No nodes are available in datacenter %q\n", prefix, dc)
		}
	}

	// Print filter info
	for class, num := range metrics.ClassFiltered {
		out += fmt.Sprintf("%s* Class %q: %d nodes excluded by filter\n", prefix, class, num)
	}
	for cs, num := range metrics.ConstraintFiltered {
		out += fmt.Sprintf("%s* Constraint %q: %d nodes excluded by filter\n", prefix, cs, num)
	}

	// Print exhaustion info
	if ne := metrics.NodesExhausted; ne > 0 {
		out += fmt.Sprintf("%s* Resources exhausted on %d nodes\n", prefix, ne)
	}
	for class, num := range metrics.ClassExhausted {
		out += fmt.Sprintf("%s* Class %q exhausted on %d nodes\n", prefix, class, num)
	}
	for dim, num := range metrics.DimensionExhausted {
		out += fmt.Sprintf("%s* Dimension %q exhausted on %d nodes\n", prefix, dim, num)
	}

	// Print quota info
	for _, dim := range metrics.QuotaExhausted {
		out += fmt.Sprintf("%s* Quota limit hit %q\n", prefix, dim)
	}

	// Print scores
	if scores {
		if len(metrics.ScoreMetaData) > 0 {
			scoreOutput := make([]string, len(metrics.ScoreMetaData)+1)
			var scorerNames []string
			for i, scoreMeta := range metrics.ScoreMetaData {
				// Add header as first row
				if i == 0 {
					scoreOutput[0] = "Node|"

					// sort scores alphabetically
					scores := make([]string, 0, len(scoreMeta.Scores))
					for score := range scoreMeta.Scores {
						scores = append(scores, score)
					}
					sort.Strings(scores)

					// build score header output
					for _, scorerName := range scores {
						scoreOutput[0] += fmt.Sprintf("%v|", scorerName)
						scorerNames = append(scorerNames, scorerName)
					}
					scoreOutput[0] += "final score"
				}
				scoreOutput[i+1] = fmt.Sprintf("%v|", scoreMeta.NodeID)
				for _, scorerName := range scorerNames {
					scoreVal := scoreMeta.Scores[scorerName]
					scoreOutput[i+1] += fmt.Sprintf("%.3g|", scoreVal)
				}
				scoreOutput[i+1] += fmt.Sprintf("%.3g", scoreMeta.NormScore)
			}
			out += formatList(scoreOutput)
		} else {
			// Backwards compatibility for old allocs
			for name, score := range metrics.Scores {
				out += fmt.Sprintf("%s* Score %q = %f\n", prefix, name, score)
			}
		}
	}

	out = strings.TrimSuffix(out, "\n")
	return out
}

// formatTime formats the time to string based on RFC822
func formatTime(t time.Time) string {
	if t.Unix() < 1 {
		// It's more confusing to display the UNIX epoch or a zero value than nothing
		return ""
	}
	// Return ISO_8601 time format GH-3806
	return t.Format("2006-01-02T15:04:05Z07:00")
}

// formatTimeDifference takes two times and determines their duration difference
// truncating to a passed unit.
// E.g. formatTimeDifference(first=1m22s33ms, second=1m28s55ms, time.Second) -> 6s
func formatTimeDifference(first, second time.Time, d time.Duration) string {
	return second.Truncate(d).Sub(first.Truncate(d)).String()
}
