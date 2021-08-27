package nomaddiffprinter

import (
	"sort"

	"github.com/hashicorp/nomad/api"
)

func sortedTaskGroupFromMetrics(groups map[string]*api.AllocationMetric) []string {
	tgs := make([]string, 0, len(groups))
	for tg := range groups {
		tgs = append(tgs, tg)
	}
	sort.Strings(tgs)
	return tgs
}
