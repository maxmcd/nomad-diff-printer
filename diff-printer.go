package nomaddiffprinter

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/nomad/api"
	"github.com/mitchellh/colorstring"
	"github.com/ryanuber/columnize"
	"golang.org/x/term"
)

// UpdateTypes denote the type of update to occur against the task group.
const (
	schedulerUpdateTypeIgnore            = "ignore"
	schedulerUpdateTypeCreate            = "create"
	schedulerUpdateTypeDestroy           = "destroy"
	schedulerUpdateTypeMigrate           = "migrate"
	schedulerUpdateTypeCanary            = "canary"
	schedulerUpdateTypeInplaceUpdate     = "in-place update"
	schedulerUpdateTypeDestructiveUpdate = "create/destroy update"
)

const (
	// preemptionDisplayThreshold is an upper bound used to limit and summarize
	// the details of preempted jobs in the output
	preemptionDisplayThreshold = 10
)

func PlanAndPrintDiff(client *api.Client, job *api.Job, output io.Writer) (resp *api.JobPlanResponse, err error) {
	// Force the region to be that of the job.
	if r := job.Region; r != nil {
		client.SetRegion(*r)
	}

	// Force the namespace to be that of the job.
	if n := job.Namespace; n != nil {
		client.SetNamespace(*n)
	}

	// // Setup the options
	opts := &api.PlanOptions{Diff: true}
	// if diff {
	// 	opts.Diff = true
	// }
	// if policyOverride {
	// 	opts.PolicyOverride = true
	// }

	// if job.IsMultiregion() {
	// 	return c.multiregionPlan(client, job, opts, diff, verbose)
	// }

	// Submit the job
	resp, _, err = client.Jobs().PlanOpts(job, opts, nil)
	if err != nil {
		return nil, err
	}
	print := func(s string) {
		fmt.Fprintln(output, s)
	}
	outputPlannedJob(job, resp, print, opts.Diff, true)

	return resp, err
}

// TODO: add multiregion support
// func multiregionPlan(client *api.Client, job *api.Job, opts *api.PlanOptions, diff, verbose bool) int {

// 	var exitCode int
// 	plans := map[string]*api.JobPlanResponse{}

// 	// collect all the plans first so that we can report all errors
// 	for _, region := range job.Multiregion.Regions {
// 		regionName := region.Name
// 		client.SetRegion(regionName)

// 		// Submit the job for this region
// 		resp, _, err := client.Jobs().PlanOpts(job, opts, nil)
// 		if err != nil {
// 			c.Ui.Error(fmt.Sprintf("Error during plan for region %q: %s", regionName, err))
// 			exitCode = 255
// 		}
// 		plans[regionName] = resp
// 	}

// 	if exitCode > 0 {
// 		return exitCode
// 	}

// 	for regionName, resp := range plans {
// 		print(colorize().Color(fmt.Sprintf("[bold]Region: %q[reset]", regionName)))
// 		regionExitCode := c.outputPlannedJob(job, resp, diff, verbose)
// 		if regionExitCode > exitCode {
// 			exitCode = regionExitCode
// 		}
// 	}
// 	return exitCode
// }

func colorize() *colorstring.Colorize {
	return &colorstring.Colorize{
		Colors:  colorstring.DefaultColors,
		Disable: !term.IsTerminal(int(os.Stdout.Fd())),
		Reset:   true,
	}
}

func outputPlannedJob(job *api.Job, resp *api.JobPlanResponse, print func(string), diff, verbose bool) int {
	// Print the diff if not disabled
	if diff {
		print(fmt.Sprintf("%s\n",
			colorize().Color(strings.TrimSpace(formatJobDiff(resp.Diff, verbose)))))
	}

	// Print the scheduler dry-run output
	print(colorize().Color("[bold]Scheduler dry-run:[reset]"))
	print(colorize().Color(formatDryRun(resp, job)))
	print("")

	// Print any warnings if there are any
	if resp.Warnings != "" {
		print(
			colorize().Color(fmt.Sprintf("[bold][yellow]Job Warnings:\n%s[reset]\n", resp.Warnings)))
	}

	// Print preemptions if there are any
	if resp.Annotations != nil && len(resp.Annotations.PreemptedAllocs) > 0 {
		addPreemptions(resp, print)
	}

	return getExitCode(resp)
}

// addPreemptions shows details about preempted allocations
func addPreemptions(resp *api.JobPlanResponse, print func(string)) {
	print(colorize().Color("[bold][yellow]Preemptions:\n[reset]"))
	if len(resp.Annotations.PreemptedAllocs) < preemptionDisplayThreshold {
		var allocs []string
		allocs = append(allocs, fmt.Sprintf("Alloc ID|Job ID|Task Group"))
		for _, alloc := range resp.Annotations.PreemptedAllocs {
			allocs = append(allocs, fmt.Sprintf("%s|%s|%s", alloc.ID, alloc.JobID, alloc.TaskGroup))
		}
		print(formatList(allocs))
		return
	}
	// Display in a summary format if the list is too large
	// Group by job type and job ids
	allocDetails := make(map[string]map[namespaceIdPair]int)
	numJobs := 0
	for _, alloc := range resp.Annotations.PreemptedAllocs {
		id := namespaceIdPair{alloc.JobID, alloc.Namespace}
		countMap := allocDetails[alloc.JobType]
		if countMap == nil {
			countMap = make(map[namespaceIdPair]int)
		}
		cnt, ok := countMap[id]
		if !ok {
			// First time we are seeing this job, increment counter
			numJobs++
		}
		countMap[id] = cnt + 1
		allocDetails[alloc.JobType] = countMap
	}

	// Show counts grouped by job ID if its less than a threshold
	var outputs []string
	if numJobs < preemptionDisplayThreshold {
		outputs = append(outputs, fmt.Sprintf("Job ID|Namespace|Job Type|Preemptions"))
		for jobType, jobCounts := range allocDetails {
			for jobId, count := range jobCounts {
				outputs = append(outputs, fmt.Sprintf("%s|%s|%s|%d", jobId.id, jobId.namespace, jobType, count))
			}
		}
	} else {
		// Show counts grouped by job type
		outputs = append(outputs, fmt.Sprintf("Job Type|Preemptions"))
		for jobType, jobCounts := range allocDetails {
			total := 0
			for _, count := range jobCounts {
				total += count
			}
			outputs = append(outputs, fmt.Sprintf("%s|%d", jobType, total))
		}
	}
	print(formatList(outputs))

}

func formatList(in []string) string {
	columnConf := columnize.DefaultConfig()
	columnConf.Empty = "<none>"
	return columnize.Format(in, columnConf)
}

type namespaceIdPair struct {
	id        string
	namespace string
}

// getExitCode returns 0:
// * 0: No allocations created or destroyed.
// * 1: Allocations created or destroyed.
func getExitCode(resp *api.JobPlanResponse) int {
	// Check for changes
	for _, d := range resp.Annotations.DesiredTGUpdates {
		if d.Stop+d.Place+d.Migrate+d.DestructiveUpdate+d.Canary > 0 {
			return 1
		}
	}

	return 0
}

// formatDryRun produces a string explaining the results of the dry run.
func formatDryRun(resp *api.JobPlanResponse, job *api.Job) string {
	var rolling *api.Evaluation
	for _, eval := range resp.CreatedEvals {
		if eval.TriggeredBy == "rolling-update" {
			rolling = eval
		}
	}

	var out string
	if len(resp.FailedTGAllocs) == 0 {
		out = "[bold][green]- All tasks successfully allocated.[reset]\n"
	} else {
		// Change the output depending on if we are a system job or not
		if job.Type != nil && *job.Type == "system" {
			out = "[bold][yellow]- WARNING: Failed to place allocations on all nodes.[reset]\n"
		} else {
			out = "[bold][yellow]- WARNING: Failed to place all allocations.[reset]\n"
		}
		sorted := sortedTaskGroupFromMetrics(resp.FailedTGAllocs)
		for _, tg := range sorted {
			metrics := resp.FailedTGAllocs[tg]

			noun := "allocation"
			if metrics.CoalescedFailures > 0 {
				noun += "s"
			}
			out += fmt.Sprintf("%s[yellow]Task Group %q (failed to place %d %s):\n[reset]", strings.Repeat(" ", 2), tg, metrics.CoalescedFailures+1, noun)
			out += fmt.Sprintf("[yellow]%s[reset]\n\n", formatAllocMetrics(metrics, false, strings.Repeat(" ", 4)))
		}
		if rolling == nil {
			out = strings.TrimSuffix(out, "\n")
		}
	}

	if rolling != nil {
		out += fmt.Sprintf("[green]- Rolling update, next evaluation will be in %s.\n", rolling.Wait)
	}

	if next := resp.NextPeriodicLaunch; !next.IsZero() && !job.IsParameterized() {
		loc, err := job.Periodic.GetLocation()
		if err != nil {
			out += fmt.Sprintf("[yellow]- Invalid time zone: %v", err)
		} else {
			now := time.Now().In(loc)
			out += fmt.Sprintf("[green]- If submitted now, next periodic launch would be at %s (%s from now).\n",
				formatTime(next), formatTimeDifference(now, next, time.Second))
		}
	}

	out = strings.TrimSuffix(out, "\n")
	return out
}

// formatJobDiff produces an annotated diff of the job. If verbose mode is
// set, added or deleted task groups and tasks are expanded.
func formatJobDiff(job *api.JobDiff, verbose bool) string {
	marker, _ := getDiffString(job.Type)
	out := fmt.Sprintf("%s[bold]Job: %q\n", marker, job.ID)

	// Determine the longest markers and fields so that the output can be
	// properly aligned.
	longestField, longestMarker := getLongestPrefixes(job.Fields, job.Objects)
	for _, tg := range job.TaskGroups {
		if _, l := getDiffString(tg.Type); l > longestMarker {
			longestMarker = l
		}
	}

	// Only show the job's field and object diffs if the job is edited or
	// verbose mode is set.
	if job.Type == "Edited" || verbose {
		fo := alignedFieldAndObjects(job.Fields, job.Objects, 0, longestField, longestMarker)
		out += fo
		if len(fo) > 0 {
			out += "\n"
		}
	}

	// Print the task groups
	for _, tg := range job.TaskGroups {
		_, mLength := getDiffString(tg.Type)
		kPrefix := longestMarker - mLength
		out += fmt.Sprintf("%s\n", formatTaskGroupDiff(tg, kPrefix, verbose))
	}

	return out
}

// formatTaskGroupDiff produces an annotated diff of a task group. If the
// verbose field is set, the task groups fields and objects are expanded even if
// the full object is an addition or removal. tgPrefix is the number of spaces to prefix
// the output of the task group.
func formatTaskGroupDiff(tg *api.TaskGroupDiff, tgPrefix int, verbose bool) string {
	marker, _ := getDiffString(tg.Type)
	out := fmt.Sprintf("%s%s[bold]Task Group: %q[reset]", marker, strings.Repeat(" ", tgPrefix), tg.Name)

	// Append the updates and colorize them
	if l := len(tg.Updates); l > 0 {
		order := make([]string, 0, l)
		for updateType := range tg.Updates {
			order = append(order, updateType)
		}

		sort.Strings(order)
		updates := make([]string, 0, l)
		for _, updateType := range order {
			count := tg.Updates[updateType]
			var color string
			switch updateType {
			case schedulerUpdateTypeIgnore:
			case schedulerUpdateTypeCreate:
				color = "[green]"
			case schedulerUpdateTypeDestroy:
				color = "[red]"
			case schedulerUpdateTypeMigrate:
				color = "[blue]"
			case schedulerUpdateTypeInplaceUpdate:
				color = "[cyan]"
			case schedulerUpdateTypeDestructiveUpdate:
				color = "[yellow]"
			case schedulerUpdateTypeCanary:
				color = "[light_yellow]"
			}
			updates = append(updates, fmt.Sprintf("[reset]%s%d %s", color, count, updateType))
		}
		out += fmt.Sprintf(" (%s[reset])\n", strings.Join(updates, ", "))
	} else {
		out += "[reset]\n"
	}

	// Determine the longest field and markers so the output is properly
	// aligned
	longestField, longestMarker := getLongestPrefixes(tg.Fields, tg.Objects)
	for _, task := range tg.Tasks {
		if _, l := getDiffString(task.Type); l > longestMarker {
			longestMarker = l
		}
	}

	// Only show the task groups's field and object diffs if the group is edited or
	// verbose mode is set.
	subStartPrefix := tgPrefix + 2
	if tg.Type == "Edited" || verbose {
		fo := alignedFieldAndObjects(tg.Fields, tg.Objects, subStartPrefix, longestField, longestMarker)
		out += fo
		if len(fo) > 0 {
			out += "\n"
		}
	}

	// Output the tasks
	for _, task := range tg.Tasks {
		_, mLength := getDiffString(task.Type)
		prefix := longestMarker - mLength
		out += fmt.Sprintf("%s\n", formatTaskDiff(task, subStartPrefix, prefix, verbose))
	}

	return out
}

// formatTaskDiff produces an annotated diff of a task. If the verbose field is
// set, the tasks fields and objects are expanded even if the full object is an
// addition or removal. startPrefix is the number of spaces to prefix the output of
// the task and taskPrefix is the number of spaces to put between the marker and
// task name output.
func formatTaskDiff(task *api.TaskDiff, startPrefix, taskPrefix int, verbose bool) string {
	marker, _ := getDiffString(task.Type)
	out := fmt.Sprintf("%s%s%s[bold]Task: %q",
		strings.Repeat(" ", startPrefix), marker, strings.Repeat(" ", taskPrefix), task.Name)
	if len(task.Annotations) != 0 {
		out += fmt.Sprintf(" [reset](%s)", colorAnnotations(task.Annotations))
	}

	if task.Type == "None" {
		return out
	} else if (task.Type == "Deleted" || task.Type == "Added") && !verbose {
		// Exit early if the job was not edited and it isn't verbose output
		return out
	} else {
		out += "\n"
	}

	subStartPrefix := startPrefix + 2
	longestField, longestMarker := getLongestPrefixes(task.Fields, task.Objects)
	out += alignedFieldAndObjects(task.Fields, task.Objects, subStartPrefix, longestField, longestMarker)
	return out
}

// formatObjectDiff produces an annotated diff of an object. startPrefix is the
// number of spaces to prefix the output of the object and keyPrefix is the number
// of spaces to put between the marker and object name output.
func formatObjectDiff(diff *api.ObjectDiff, startPrefix, keyPrefix int) string {
	start := strings.Repeat(" ", startPrefix)
	marker, markerLen := getDiffString(diff.Type)
	out := fmt.Sprintf("%s%s%s%s {\n", start, marker, strings.Repeat(" ", keyPrefix), diff.Name)

	// Determine the length of the longest name and longest diff marker to
	// properly align names and values
	longestField, longestMarker := getLongestPrefixes(diff.Fields, diff.Objects)
	subStartPrefix := startPrefix + keyPrefix + 2
	out += alignedFieldAndObjects(diff.Fields, diff.Objects, subStartPrefix, longestField, longestMarker)

	endprefix := strings.Repeat(" ", startPrefix+markerLen+keyPrefix)
	return fmt.Sprintf("%s\n%s}", out, endprefix)
}

// formatFieldDiff produces an annotated diff of a field. startPrefix is the
// number of spaces to prefix the output of the field, keyPrefix is the number
// of spaces to put between the marker and field name output and valuePrefix is
// the number of spaces to put infront of the value for aligning values.
func formatFieldDiff(diff *api.FieldDiff, startPrefix, keyPrefix, valuePrefix int) string {
	marker, _ := getDiffString(diff.Type)
	out := fmt.Sprintf("%s%s%s%s: %s",
		strings.Repeat(" ", startPrefix),
		marker, strings.Repeat(" ", keyPrefix),
		diff.Name,
		strings.Repeat(" ", valuePrefix))

	switch diff.Type {
	case "Added":
		out += fmt.Sprintf("%q", diff.New)
	case "Deleted":
		out += fmt.Sprintf("%q", diff.Old)
	case "Edited":
		out += fmt.Sprintf("%q => %q", diff.Old, diff.New)
	default:
		out += fmt.Sprintf("%q", diff.New)
	}

	// Color the annotations where possible
	if l := len(diff.Annotations); l != 0 {
		out += fmt.Sprintf(" (%s)", colorAnnotations(diff.Annotations))
	}

	return out
}

// alignedFieldAndObjects is a helper method that prints fields and objects
// properly aligned.
func alignedFieldAndObjects(fields []*api.FieldDiff, objects []*api.ObjectDiff,
	startPrefix, longestField, longestMarker int) string {

	var out string
	numFields := len(fields)
	numObjects := len(objects)
	haveObjects := numObjects != 0
	for i, field := range fields {
		_, mLength := getDiffString(field.Type)
		kPrefix := longestMarker - mLength
		vPrefix := longestField - len(field.Name)
		out += formatFieldDiff(field, startPrefix, kPrefix, vPrefix)

		// Avoid a dangling new line
		if i+1 != numFields || haveObjects {
			out += "\n"
		}
	}

	for i, object := range objects {
		_, mLength := getDiffString(object.Type)
		kPrefix := longestMarker - mLength
		out += formatObjectDiff(object, startPrefix, kPrefix)

		// Avoid a dangling new line
		if i+1 != numObjects {
			out += "\n"
		}
	}

	return out
}

// getLongestPrefixes takes a list  of fields and objects and determines the
// longest field name and the longest marker.
func getLongestPrefixes(fields []*api.FieldDiff, objects []*api.ObjectDiff) (longestField, longestMarker int) {
	for _, field := range fields {
		if l := len(field.Name); l > longestField {
			longestField = l
		}
		if _, l := getDiffString(field.Type); l > longestMarker {
			longestMarker = l
		}
	}
	for _, obj := range objects {
		if _, l := getDiffString(obj.Type); l > longestMarker {
			longestMarker = l
		}
	}
	return longestField, longestMarker
}

// getDiffString returns a colored diff marker and the length of the string
// without color annotations.
func getDiffString(diffType string) (string, int) {
	switch diffType {
	case "Added":
		return "[green]+[reset] ", 2
	case "Deleted":
		return "[red]-[reset] ", 2
	case "Edited":
		return "[light_yellow]+/-[reset] ", 4
	default:
		return "", 0
	}
}

// colorAnnotations returns a comma concatenated list of the annotations where
// the annotations are colored where possible.
func colorAnnotations(annotations []string) string {
	l := len(annotations)
	if l == 0 {
		return ""
	}

	colored := make([]string, l)
	for i, annotation := range annotations {
		switch annotation {
		case "forces create":
			colored[i] = fmt.Sprintf("[green]%s[reset]", annotation)
		case "forces destroy":
			colored[i] = fmt.Sprintf("[red]%s[reset]", annotation)
		case "forces in-place update":
			colored[i] = fmt.Sprintf("[cyan]%s[reset]", annotation)
		case "forces create/destroy update":
			colored[i] = fmt.Sprintf("[yellow]%s[reset]", annotation)
		default:
			colored[i] = annotation
		}
	}

	return strings.Join(colored, ", ")
}
