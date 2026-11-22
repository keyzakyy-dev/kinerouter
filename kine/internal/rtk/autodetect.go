package rtk

// Filter autodetection, ported from open-sse/rtk/autodetect.js (itself a port of
// rtk/src/cmds/system/pipe_cmd.rs auto_detect_filter plus JS extras).
//
// Order matters: build-output is checked before the porcelain heuristic so a
// cargo "Compiling" log is not mistaken for `git status` output.

import (
	"regexp"
	"strings"
)

const detectWindow = 1024

var (
	reGitDiff       = regexp.MustCompile(`(?m)^diff --git `)
	reGitDiffHunk   = regexp.MustCompile(`(?m)^@@ `)
	reGitStatusHead = regexp.MustCompile(`(?m)^On branch |^nothing to commit|^Changes (not |to be )|^Untracked files:`)
	reGitLogHead    = regexp.MustCompile(`(?m)^[*|/\\ ]*commit [0-9a-f]{7,40}$`)
	rePorcelainLn   = regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] \S`)
	reBuildOutput   = regexp.MustCompile(`(?im)^(npm (warn|error|ERR!)|yarn (warn|error)|\s*Compiling\s+\S+|\s*Downloading\s+\S+|added \d+ package|\[ERROR\]|BUILD (SUCCESS|FAILED)|\s*Finished\s+|Successfully (installed|built)|ERROR:)`)
	reTreeGlyph     = regexp.MustCompile(`[├└]──|│  `)
	reLsRow         = regexp.MustCompile(`(?m)^[-dlbcps][rwx-]{9}`)
	reLsTotal       = regexp.MustCompile(`(?m)^total \d+$`)
	reDrivePath     = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// filter pairs an output filter with its log name.
type filter struct {
	apply func(string) string
	name  string
}

// autoDetect returns the filter that recognises text, or a zero filter.
func autoDetect(text string) filter {
	head := text
	if len(head) > detectWindow {
		head = head[:detectWindow]
	}
	// Trimming to a byte window can split a multi-byte rune; only affects the
	// final grapheme of the sniff window, which no pattern matches on.
	switch {
	case reGitLogHead.MatchString(head):
		return filter{gitLog, "git-log"}
	case reGitDiff.MatchString(head) || reGitDiffHunk.MatchString(head):
		return filter{gitDiff, "git-diff"}
	case reGitStatusHead.MatchString(head):
		return filter{gitStatus, "git-status"}
	case reBuildOutput.MatchString(head):
		return filter{buildOutput, "build-output"}
	case isMostlyPorcelain(head):
		return filter{gitStatus, "git-status"}
	}

	lines := strings.Split(head, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	first5 := nonEmpty
	if len(first5) > 5 {
		first5 = first5[:5]
	}
	for _, l := range first5 {
		if isGrepLine(l) {
			return filter{grepFilter, "grep"}
		}
	}
	if len(nonEmpty) >= 3 {
		allPath := true
		for _, l := range nonEmpty {
			if !isPathLike(l) {
				allPath = false
				break
			}
		}
		if allPath {
			return filter{findFilter, "find"}
		}
	}
	if reTreeGlyph.MatchString(head) {
		return filter{treeFilter, "tree"}
	}
	if reLsTotal.MatchString(head) || len(reLsRow.FindAllString(head, -1)) >= 3 {
		return filter{lsFilter, "ls"}
	}
	if searchListHeaderRe.MatchString(head) {
		return filter{searchList, "search-list"}
	}
	if len(lines) >= smartTruncateMinLine && isLineNumbered(lines) {
		return filter{readNumbered, "read-numbered"}
	}
	if len(nonEmpty) >= 5 {
		return filter{dedupLog, "dedup-log"}
	}
	if len(strings.Split(text, "\n")) >= smartTruncateMinLine {
		return filter{smartTruncate, "smart-truncate"}
	}
	return filter{}
}

func isGrepLine(line string) bool {
	first := strings.IndexByte(line, ':')
	if first < 0 {
		return false
	}
	second := strings.IndexByte(line[first+1:], ':')
	if second < 0 {
		return false
	}
	return allDigits(line[first+1 : first+1+second])
}

func isPathLike(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if reDrivePath.MatchString(t) {
		return true
	}
	if strings.Contains(t, ":") {
		return false
	}
	return strings.HasPrefix(t, ".") || strings.HasPrefix(t, "/") || strings.Contains(t, "/")
}

func isMostlyPorcelain(head string) bool {
	var lines []string
	for _, l := range strings.Split(head, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 3 {
		return false
	}
	hits := 0
	for _, l := range lines {
		if rePorcelainLn.MatchString(l) {
			hits++
		}
	}
	return float64(hits)/float64(len(lines)) >= 0.6
}

func isLineNumbered(lines []string) bool {
	hits, nonEmpty := 0, 0
	sample := lines
	if len(sample) > 100 {
		sample = sample[:100]
	}
	for _, l := range sample {
		if l == "" {
			continue
		}
		nonEmpty++
		if readNumberedLineRe.MatchString(l) {
			hits++
		}
	}
	if nonEmpty < 5 {
		return false
	}
	return float64(hits)/float64(nonEmpty) >= readNumberedMinRatio
}
