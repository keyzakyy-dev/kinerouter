package rtk

// Output filters. Each is a straight port of open-sse/rtk/filters/*.js; the JS
// in turn ports rtk/src/cmds/*.rs. They take raw tool output and return a
// shorter rendering. A filter may return the input unchanged — compressText
// rejects anything that is empty or not shorter.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	gitDiffHunkMaxLines  = 100
	gitLogMaxLines       = 200
	dedupLineMax         = 2000
	grepPerFileMax       = 10
	findPerDirMax        = 10
	findTotalDirMax      = 20
	statusMaxFiles       = 10
	statusMaxUntracked   = 10
	lsExtSummaryTop      = 5
	treeMaxLines         = 200
	searchListPerDirMax  = 10
	searchListTotalDirMx = 20
	smartTruncateHead    = 120
	smartTruncateTail    = 60
	smartTruncateMinLine = 250
	readNumberedMinRatio = 0.7
)

// ------------------------------------------------------------------ git-diff

func gitDiff(diff string) string {
	var result []string
	var currentFile string
	added, removed, hunkShown, hunkSkipped := 0, 0, 0, 0
	inHunk, wasTruncated := false, false
	flushSkipped := func() {
		if hunkSkipped > 0 {
			result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
			wasTruncated = true
			hunkSkipped = 0
		}
	}
	flushCounts := func() {
		if currentFile != "" && (added > 0 || removed > 0) {
			result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
		}
	}

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			flushSkipped()
			flushCounts()
			currentFile = "unknown"
			if i := strings.Index(line, " b/"); i >= 0 {
				currentFile = line[i+3:]
			}
			result = append(result, "\n"+currentFile)
			added, removed, inHunk, hunkShown = 0, 0, false, 0
		case strings.HasPrefix(line, "@@"):
			flushSkipped()
			inHunk, hunkShown = true, 0
			result = append(result, "  "+line)
		case inHunk:
			switch {
			case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
				added++
				if hunkShown < gitDiffHunkMaxLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
				removed++
				if hunkShown < gitDiffHunkMaxLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			case hunkShown < gitDiffHunkMaxLines && !strings.HasPrefix(line, "\\"):
				if hunkShown > 0 {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}
		if len(result) >= 500 {
			result = append(result, "\n... (more changes truncated)")
			wasTruncated = true
			break
		}
	}
	flushSkipped()
	flushCounts()
	if wasTruncated {
		result = append(result, "[full diff: rtk git diff --no-compact]")
	}
	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------- git-status

var (
	reOnBranch   = regexp.MustCompile(`^On branch (\S+)`)
	rePorcelain  = regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] `)
	reLongStatus = regexp.MustCompile(`^\s*(modified|new file|deleted|renamed|both modified):\s+(.+)$`)
)

func gitStatus(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return "Clean working tree"
	}
	var branch string
	var stagedFiles, modifiedFiles, untrackedFiles []string
	staged, modified, untracked, conflicts := 0, 0, 0, 0

	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if m := reOnBranch.FindStringSubmatch(raw); m != nil {
			branch = m[1]
			continue
		}
		if strings.HasPrefix(raw, "##") {
			branch = strings.TrimSpace(strings.TrimPrefix(raw, "##"))
			continue
		}
		if len(raw) >= 3 && rePorcelain.MatchString(raw) {
			x, y, file := raw[0], raw[1], raw[3:]
			if raw[:2] == "??" {
				untracked++
				untrackedFiles = append(untrackedFiles, file)
				continue
			}
			if strings.IndexByte("MADRC", x) >= 0 {
				staged++
				stagedFiles = append(stagedFiles, file)
			} else if x == 'U' {
				conflicts++
			}
			if y == 'M' || y == 'D' {
				modified++
				modifiedFiles = append(modifiedFiles, file)
			}
			continue
		}
		if m := reLongStatus.FindStringSubmatch(raw); m != nil {
			kind, path := m[1], strings.TrimSpace(m[2])
			switch kind {
			case "both modified":
				conflicts++
			case "modified", "deleted":
				modified++
				modifiedFiles = append(modifiedFiles, path)
			default:
				staged++
				stagedFiles = append(stagedFiles, path)
			}
		}
	}

	var b strings.Builder
	if branch != "" {
		b.WriteString("* " + branch + "\n")
	}
	section := func(head string, files []string, n, max int) {
		if n == 0 {
			return
		}
		b.WriteString(fmt.Sprintf("%s: %d files\n", head, n))
		shown := files
		if len(shown) > max {
			shown = shown[:max]
		}
		for _, f := range shown {
			b.WriteString("   " + f + "\n")
		}
		if len(files) > max {
			b.WriteString(fmt.Sprintf("   ... +%d more\n", len(files)-max))
		}
	}
	section("+ Staged", stagedFiles, staged, statusMaxFiles)
	section("~ Modified", modifiedFiles, modified, statusMaxFiles)
	section("? Untracked", untrackedFiles, untracked, statusMaxUntracked)
	if conflicts > 0 {
		b.WriteString(fmt.Sprintf("conflicts: %d files\n", conflicts))
	}
	if staged == 0 && modified == 0 && untracked == 0 && conflicts == 0 {
		b.WriteString("clean — nothing to commit\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ------------------------------------------------------------------- git-log

var (
	reCommitHeader  = regexp.MustCompile(`(?i)^(?:[*|/\\ ]+)?commit [0-9a-f]{7,40}$`)
	reAuthorDate    = regexp.MustCompile(`(?i)^[*|/\\ ]*(Author|Date):`)
	reIndentedSubj  = regexp.MustCompile(`^[*|/\\ ]*    \S`)
	reFileChanged   = regexp.MustCompile(`^\d+ file\w* changed`)
	reGraphShaSubj  = regexp.MustCompile(`(?i)^[*|/\\ ]+([0-9a-f]{7,40}\s+.+)`)
	rePlainOneline  = regexp.MustCompile(`(?i)^[0-9a-f]{7,40}\s+`)
	rePureGraphLine = regexp.MustCompile(`^[*|/\\ ]+$`)
)

func gitLog(text string) string {
	if text == "" {
		return ""
	}
	var out []string
	skipped := 0
	inCommit, subjectSeen := false, false
	pushLine := func(l string) {
		if len(out) < gitLogMaxLines {
			out = append(out, l)
		} else {
			skipped++
		}
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case reCommitHeader.MatchString(trimmed):
			inCommit, subjectSeen = true, false
			pushLine(line)
			continue
		}
		if inCommit {
			switch {
			case reAuthorDate.MatchString(trimmed):
				pushLine(trimmed)
			case trimmed == "":
			case !subjectSeen && reIndentedSubj.MatchString(line):
				pushLine("  Subject: " + trimmed)
				subjectSeen = true
			case reFileChanged.MatchString(trimmed):
				pushLine("  " + trimmed)
			case strings.HasPrefix(trimmed, "diff --git "):
				pushLine("  ... diff body omitted")
			}
			continue
		}
		if m := reGraphShaSubj.FindStringSubmatch(trimmed); m != nil {
			pushLine(m[1])
			continue
		}
		if rePlainOneline.MatchString(trimmed) {
			pushLine(trimmed)
			continue
		}
		if rePureGraphLine.MatchString(trimmed) {
			continue
		}
		pushLine(trimmed)
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("... (%d more lines)", skipped))
	}
	result := strings.Join(out, "\n")
	if result == "" || len(result) > len(text) {
		return text
	}
	return result
}

// -------------------------------------------------------------- build-output

var (
	reCargoErrCont  = regexp.MustCompile(`^\s*(-->|\||\d+\s*\||=)`)
	reNpmErr        = regexp.MustCompile(`(?i)^npm (ERR!|error)`)
	reYarnErr       = regexp.MustCompile(`^yarn error`)
	reNpmDeprecated = regexp.MustCompile(`^npm warn deprecated`)
	reNpmWarn       = regexp.MustCompile(`^npm warn`)
	reYarnWarn      = regexp.MustCompile(`^yarn warn`)
	reRustErr       = regexp.MustCompile(`(?i)^error(\[|:)`)
	reRustWarn      = regexp.MustCompile(`(?i)^warning(\[|:)`)
	reUppercaseErr  = regexp.MustCompile(`^ERROR:`)
	reBracketErr    = regexp.MustCompile(`^\[ERROR\]`)
	reBuildFailed   = regexp.MustCompile(`^BUILD FAILED`)
	reBracketWarn   = regexp.MustCompile(`^\[WARNING\]`)
	reCompiling     = regexp.MustCompile(`(?i)^\s*Compiling\s+\S+`)
	reDownloading   = regexp.MustCompile(`(?i)^\s*Downloading\s+\S+`)
	reFetching      = regexp.MustCompile(`^Fetching\s+`)
	reSummary       = regexp.MustCompile(`(?i)^(added|removed|changed|audited|installed)\s+\d+\s+package`)
	reFinished      = regexp.MustCompile(`^\s*Finished\s+`)
	reBuildOK       = regexp.MustCompile(`^BUILD SUCCESS`)
	reCountLine     = regexp.MustCompile(`^\d+\s+(vulnerabilities|packages?|warnings?|errors?)`)
	reSuccessfully  = regexp.MustCompile(`^Successfully (installed|built)`)
	reToAddress     = regexp.MustCompile(`^To address .* issues`)
	reNpmRun        = regexp.MustCompile("^Run `npm (audit|fund)`")
	reFunding       = regexp.MustCompile(`packages are looking for funding`)
)

func buildOutput(input string) string {
	const deprecationKeep = 3
	var errors, warnings, deprecations []string
	var summary []string
	compiling, downloading := 0, 0
	inCargoError := false

	for _, line := range strings.Split(input, "\n") {
		trimmed := strings.TrimSpace(line)
		if inCargoError {
			if trimmed == "" {
				inCargoError = false
				continue
			}
			if reCargoErrCont.MatchString(line) {
				errors = append(errors, line)
				continue
			}
			inCargoError = false
		}
		if trimmed == "" {
			continue
		}
		switch {
		case reNpmErr.MatchString(trimmed) || reYarnErr.MatchString(trimmed):
			errors = append(errors, line)
		case reNpmDeprecated.MatchString(trimmed):
			deprecations = append(deprecations, line)
		case reNpmWarn.MatchString(trimmed) || reYarnWarn.MatchString(trimmed):
			warnings = append(warnings, line)
		case reRustErr.MatchString(trimmed) || strings.HasPrefix(trimmed, "error -->"):
			errors = append(errors, line)
			inCargoError = true
		case reRustWarn.MatchString(trimmed) || strings.HasPrefix(trimmed, "warning -->"):
			warnings = append(warnings, line)
			inCargoError = true
		case reUppercaseErr.MatchString(trimmed), reBracketErr.MatchString(trimmed), reBuildFailed.MatchString(trimmed):
			errors = append(errors, line)
		case reBracketWarn.MatchString(trimmed):
			warnings = append(warnings, line)
		case reCompiling.MatchString(trimmed):
			compiling++
		case reDownloading.MatchString(trimmed) || reFetching.MatchString(trimmed):
			downloading++
		case reSummary.MatchString(trimmed), reFinished.MatchString(trimmed), reBuildOK.MatchString(trimmed),
			reCountLine.MatchString(trimmed), reSuccessfully.MatchString(trimmed),
			reToAddress.MatchString(trimmed), reNpmRun.MatchString(trimmed), reFunding.MatchString(trimmed):
			summary = append(summary, line)
		}
	}

	var b strings.Builder
	keepDep := deprecations
	if len(keepDep) > deprecationKeep {
		keepDep = keepDep[:deprecationKeep]
	}
	for _, d := range keepDep {
		b.WriteString(d + "\n")
	}
	if len(deprecations) > deprecationKeep {
		b.WriteString(fmt.Sprintf("... +%d more deprecated packages\n", len(deprecations)-deprecationKeep))
	}
	if compiling > 0 {
		b.WriteString(fmt.Sprintf("Compiled %d packages\n", compiling))
	}
	if downloading > 0 {
		b.WriteString(fmt.Sprintf("Downloaded %d packages\n", downloading))
	}
	for _, e := range errors {
		b.WriteString(e + "\n")
	}
	keepWarn := warnings
	if len(keepWarn) > 5 {
		keepWarn = keepWarn[:5]
	}
	for _, w := range keepWarn {
		b.WriteString(w + "\n")
	}
	if len(warnings) > 5 {
		b.WriteString(fmt.Sprintf("... +%d more warnings\n", len(warnings)-5))
	}
	for _, s := range summary {
		b.WriteString(s + "\n")
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return input
	}
	return out
}

// ---------------------------------------------------------------------- grep

func grepFilter(input string) string {
	type match struct{ num, content string }
	byFile := map[string][]match{}
	var order []string
	total := 0
	for _, line := range strings.Split(input, "\n") {
		first := strings.IndexByte(line, ':')
		if first < 0 {
			continue
		}
		second := strings.IndexByte(line[first+1:], ':')
		if second < 0 {
			continue
		}
		second += first + 1
		file, numStr, content := line[:first], line[first+1:second], line[second+1:]
		if numStr == "" || !allDigits(numStr) {
			continue
		}
		total++
		if _, seen := byFile[file]; !seen {
			order = append(order, file)
		}
		byFile[file] = append(byFile[file], match{numStr, content})
	}
	if total == 0 {
		return input
	}
	sort.Strings(order)
	var b strings.Builder
	fmt.Fprintf(&b, "%d matches in %dF:\n\n", total, len(order))
	for _, file := range order {
		ms := byFile[file]
		fmt.Fprintf(&b, "[file] %s (%d):\n", file, len(ms))
		show := ms
		if len(show) > grepPerFileMax {
			show = show[:grepPerFileMax]
		}
		for _, m := range show {
			fmt.Fprintf(&b, "  %4s: %s\n", m.num, strings.TrimSpace(m.content))
		}
		if len(ms) > grepPerFileMax {
			fmt.Fprintf(&b, "  +%d\n", len(ms)-grepPerFileMax)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ---------------------------------------------------------------------- find

func splitPath(p string) (dir, base string) {
	sep := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			sep = i
			break
		}
	}
	if sep < 0 {
		return ".", p
	}
	dir = p[:sep]
	if dir == "" {
		dir = "/"
	}
	return dir, p[sep+1:]
}

func findFilter(input string) string {
	var lines []string
	for _, l := range strings.Split(input, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return input
	}
	byDir := map[string][]string{}
	var order []string
	for _, p := range lines {
		dir, base := splitPath(p)
		if _, seen := byDir[dir]; !seen {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], base)
	}
	sort.Strings(order)
	var b strings.Builder
	fmt.Fprintf(&b, "%d files in %d dirs:\n\n", len(lines), len(order))
	dirs := order
	if len(dirs) > findTotalDirMax {
		dirs = dirs[:findTotalDirMax]
	}
	for _, dir := range dirs {
		files := byDir[dir]
		fmt.Fprintf(&b, "%s/  (%d)\n", strings.ReplaceAll(dir, "\\", "/"), len(files))
		show := files
		if len(show) > findPerDirMax {
			show = show[:findPerDirMax]
		}
		for _, f := range show {
			b.WriteString("  " + f + "\n")
		}
		if len(files) > findPerDirMax {
			fmt.Fprintf(&b, "  +%d\n", len(files)-findPerDirMax)
		}
	}
	if len(order) > findTotalDirMax {
		fmt.Fprintf(&b, "\n+%d more dirs\n", len(order)-findTotalDirMax)
	}
	return b.String()
}

// ------------------------------------------------------------------------ ls

var (
	lsDateRe = regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(\d{4}|\d{2}:\d{2})\s+`)
	lsNoise  = map[string]bool{
		"node_modules": true, ".git": true, "target": true, "__pycache__": true,
		".next": true, "dist": true, "build": true, ".cache": true, ".turbo": true,
		".vercel": true, ".pytest_cache": true, ".mypy_cache": true, ".tox": true,
		".venv": true, "venv": true, "env": true,
		"coverage": true, ".nyc_output": true, ".DS_Store": true, "Thumbs.db": true,
		".idea": true, ".vscode": true, ".vs": true, "*.egg-info": true, ".eggs": true,
	}
)

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1048576:
		return fmt.Sprintf("%.1fM", float64(bytes)/1048576)
	case bytes >= 1024:
		return fmt.Sprintf("%.1fK", float64(bytes)/1024)
	}
	return strconv.FormatInt(bytes, 10) + "B"
}

func lsFilter(input string) string {
	var dirs []string
	var files [][2]string // name, size
	byExt := map[string]int{}

	for _, line := range strings.Split(input, "\n") {
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		m := lsDateRe.FindStringIndex(line)
		if m == nil {
			continue
		}
		name := line[m[1]:]
		before := strings.Fields(line[:m[0]])
		if len(before) < 4 {
			continue
		}
		perms := before[0]
		var size int64
		for i := len(before) - 1; i >= 0; i-- {
			if n, err := strconv.ParseInt(before[i], 10, 64); err == nil && strconv.FormatInt(n, 10) == before[i] {
				size = n
				break
			}
		}
		if name == "." || name == ".." || lsNoise[name] {
			continue
		}
		switch perms[0] {
		case 'd':
			dirs = append(dirs, name)
		case '-', 'l':
			ext := "no ext"
			if dot := strings.LastIndexByte(name, '.'); dot > 0 {
				ext = name[dot:]
			}
			byExt[ext]++
			files = append(files, [2]string{name, humanSize(size)})
		}
	}
	if len(dirs) == 0 && len(files) == 0 {
		return input
	}

	var b strings.Builder
	for _, d := range dirs {
		b.WriteString(d + "/\n")
	}
	for _, f := range files {
		b.WriteString(f[0] + "  " + f[1] + "\n")
	}
	fmt.Fprintf(&b, "\nSummary: %d files, %d dirs", len(files), len(dirs))
	if len(byExt) > 0 {
		type ec struct {
			ext string
			n   int
		}
		var all []ec
		for e, n := range byExt {
			all = append(all, ec{e, n})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
		top := all
		if len(top) > lsExtSummaryTop {
			top = top[:lsExtSummaryTop]
		}
		parts := make([]string, len(top))
		for i, e := range top {
			parts[i] = fmt.Sprintf("%d %s", e.n, e.ext)
		}
		b.WriteString(" (" + strings.Join(parts, ", "))
		if len(all) > lsExtSummaryTop {
			fmt.Fprintf(&b, ", +%d more", len(all)-lsExtSummaryTop)
		}
		b.WriteString(")")
	}
	return b.String()
}

// ---------------------------------------------------------------------- tree

func treeFilter(input string) string {
	var filtered []string
	for _, line := range strings.Split(input, "\n") {
		if strings.Contains(line, "director") && strings.Contains(line, "file") {
			continue
		}
		if strings.TrimSpace(line) == "" && len(filtered) == 0 {
			continue
		}
		filtered = append(filtered, line)
	}
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}
	if len(filtered) > treeMaxLines {
		cut := len(filtered) - treeMaxLines
		return strings.Join(filtered[:treeMaxLines], "\n") + fmt.Sprintf("\n... +%d more lines", cut)
	}
	return strings.Join(filtered, "\n")
}

// ----------------------------------------------------------------- dedup-log

func dedupLog(input string) string {
	var out []string
	prev := ""
	hasPrev := false
	runCount, blankStreak := 0, 0
	flushRun := func() {
		if hasPrev && runCount > 1 {
			out = append(out, fmt.Sprintf("  ... (%d duplicate lines)", runCount-1))
		}
	}
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) == "" {
			if blankStreak < 1 {
				out = append(out, line)
			}
			blankStreak++
			flushRun()
			hasPrev, runCount = false, 0
			continue
		}
		blankStreak = 0
		if hasPrev && line == prev {
			runCount++
			continue
		}
		flushRun()
		out = append(out, line)
		prev, hasPrev, runCount = line, true, 1
		if len(out) >= dedupLineMax {
			out = append(out, fmt.Sprintf("... (truncated at %d lines)", dedupLineMax))
			return strings.Join(out, "\n")
		}
	}
	flushRun()
	return strings.Join(out, "\n")
}

// ----------------------------------------------------------- smart-truncate

func smartTruncate(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < smartTruncateMinLine {
		return input
	}
	head := lines[:smartTruncateHead]
	tail := lines[len(lines)-smartTruncateTail:]
	cut := len(lines) - len(head) - len(tail)
	return strings.Join(head, "\n") + fmt.Sprintf("\n... +%d lines truncated\n", cut) + strings.Join(tail, "\n")
}

// ------------------------------------------------------------ read-numbered

var readNumberedLineRe = regexp.MustCompile(`^\s*\d+\|`)

func readNumbered(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < smartTruncateMinLine {
		return input
	}
	head := lines[:smartTruncateHead]
	tail := lines[len(lines)-smartTruncateTail:]
	cut := len(lines) - len(head) - len(tail)
	return strings.Join(head, "\n") + fmt.Sprintf("\n... +%d lines truncated (file continues)\n", cut) + strings.Join(tail, "\n")
}

// ------------------------------------------------------------- search-list

var searchListHeaderRe = regexp.MustCompile(`^Result of search in '[^']*' \(total (\d+) files?\):`)

func searchList(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	header := lines[0]
	var paths []string
	for _, raw := range lines[1:] {
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "- ") {
			paths = append(paths, t[2:])
		}
	}
	if len(paths) == 0 {
		return input
	}
	byDir := map[string][]string{}
	var order []string
	for _, p := range paths {
		slash := strings.LastIndexByte(p, '/')
		var dir, name string
		if slash < 0 {
			dir, name = ".", p
		} else {
			dir, name = p[:slash], p[slash+1:]
			if dir == "" {
				dir = "/"
			}
		}
		if _, seen := byDir[dir]; !seen {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], name)
	}
	sort.Strings(order)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%d files in %d dirs:\n\n", header, len(paths), len(order))
	dirs := order
	if len(dirs) > searchListTotalDirMx {
		dirs = dirs[:searchListTotalDirMx]
	}
	for _, dir := range dirs {
		names := byDir[dir]
		fmt.Fprintf(&b, "%s/ (%d):\n", dir, len(names))
		show := names
		if len(show) > searchListPerDirMax {
			show = show[:searchListPerDirMax]
		}
		for _, n := range show {
			b.WriteString("  " + n + "\n")
		}
		if len(names) > searchListPerDirMax {
			fmt.Fprintf(&b, "  +%d\n", len(names)-searchListPerDirMax)
		}
		b.WriteString("\n")
	}
	if len(order) > searchListTotalDirMx {
		fmt.Fprintf(&b, "+%d more dirs\n", len(order)-searchListTotalDirMx)
	}
	return strings.TrimRight(b.String(), "\n")
}
