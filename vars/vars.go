// Package vars handles template variable expansion in stepi input files.
//
// Before the input text is passed to the LLM, variables in the form {VAR}
// are replaced with values derived from the current filename (or --name flag).
//
// # Filename anatomy
//
// A stepi filename is dissected as:
//
//	sometask03.md   →  TASK=sometask  STEP=03  NAME=sometask03
//	sometask03.out.md is the output file
//	sometask03.log  is the log file
//
// The STEP is always the trailing two-digit (zero-padded) number before the
// extension cluster. TASK is everything before that number.
//
// # Supported variables
//
// Simple replacements (single value → string):
//
//	{NAME}      task prefix without step, e.g. "sometask" — use as {NAME}01.md → "sometask01.md"
//	{TASK}      same as {NAME}
//	{FULLNAME}  full base name including step, e.g. "sometask03"
//	{STEP}      step portion zero-padded,  e.g. "03"
//
// Relative file references (replaced with the filename string):
//
//	{IN-1}      input file one step back,  e.g. "sometask02.md"
//	{IN-2}      input file two steps back, e.g. "sometask01.md"
//	{OUT-1}     output file one step back, e.g. "sometask02.out.md"
//	{OUT-2}     output file two steps back
//	{LOG-1}     log file one step back,    e.g. "sometask02.log"
//	{LOG-2}     log file two steps back
//
// The N in {IN-N}, {OUT-N}, {LOG-N} can be any positive integer.
//
// Range references (replaced with a newline-separated list of filenames):
//
//	{IN01:03}   input files for steps 01, 02, 03 → "sometask01.md\nsometask02.md\nsometask03.md"
//	{OUT02:04}  output files for steps 02, 03, 04
//	{LOG03:04}  log files for steps 03, 04
//
// The type prefix is case-insensitive: {in01:03} works the same as {IN01:03}.
// If a referenced file does not exist on disk, the variable is kept as-is so
// the LLM can still see that a reference was intended.
package vars

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FileInfo holds the parsed components of a stepi filename.
type FileInfo struct {
	// Dir is the directory part (may be empty for files in the current dir).
	Dir string
	// Task is the non-numeric prefix, e.g. "sometask" from "sometask03".
	// This is what {NAME} and {TASK} expand to.
	// Example: "sometask03.md" → Task = "sometask"
	// So {NAME}01.md → "sometask01.md"  (user appends their own step number)
	Task string
	// Step is the zero-padded two-digit step number, e.g. "03".
	Step string
	// StepN is the numeric value of Step, e.g. 3.
	StepN int
	// FullName is Task+Step, e.g. "sometask03". Expanded by {FULLNAME}.
	FullName string
}

// ParseFilename dissects a filename into its stepi components.
// It accepts both the raw input path (e.g. ".stepi/sometask03.md") and a
// --name flag value (e.g. "sometask03" or ".stepi/sometask03").
//
// Returns nil if the filename does not match the expected pattern
// (i.e. does not end with a two-digit number before the extension).
func ParseFilename(path string) *FileInfo {
	// Normalise: strip everything after the first dot after the base name so
	// "sometask03.out.md", "sometask03.md", "sometask03.log" all yield the
	// same base.
	dir := filepath.Dir(path)
	if dir == "." {
		dir = ""
	}
	base := filepath.Base(path)

	// Strip extensions: everything from the first '.' onward.
	dotIdx := strings.Index(base, ".")
	if dotIdx >= 0 {
		base = base[:dotIdx]
	}

	// The base must end with one or more digits.
	// We want specifically the trailing digit run that represents the step.
	re := regexp.MustCompile(`^(.*?)(\d+)$`)
	m := re.FindStringSubmatch(base)
	if m == nil {
		return nil
	}

	task := m[1]
	stepRaw := m[2]

	// Enforce two-digit zero-padding: "3" → "03", "03" stays "03".
	stepN, err := strconv.Atoi(stepRaw)
	if err != nil {
		return nil
	}
	step := fmt.Sprintf("%02d", stepN)

	fullName := task + step

	return &FileInfo{
		Dir:      dir,
		Task:     task,
		Step:     step,
		StepN:    stepN,
		FullName: fullName,
	}
}

// fileForStep returns the filename for a given step and extension type.
// extType is one of "in", "out", "log".
func (fi *FileInfo) fileForStep(stepN int, extType string) string {
	step := fmt.Sprintf("%02d", stepN)
	base := fi.Task + step

	var ext string
	switch strings.ToLower(extType) {
	case "in":
		ext = ".md"
	case "out":
		ext = ".out.md"
	case "log":
		ext = ".log"
	default:
		ext = "." + extType
	}

	if fi.Dir != "" {
		return filepath.Join(fi.Dir, base+ext)
	}
	return base + ext
}

// Expand replaces all template variables in text with their resolved values,
// based on the parsed file info fi.
//
// Variables that refer to non-existent files are left unchanged so the LLM
// can still observe the intended reference.
func Expand(text string, fi *FileInfo) string {
	if fi == nil {
		return text
	}

	// ── Simple variables ───────────────────────────────────────────────────
	//
	// {NAME} and {TASK} both expand to the TASK prefix (without the step
	// number), so the user can write {NAME}01.md to reference step 01.
	// {FULLNAME} expands to the full base name including the step number.
	// {STEP} expands to the zero-padded step number string.

	text = replaceCI(text, "{FULLNAME}", fi.FullName)
	text = replaceCI(text, "{NAME}", fi.Task)
	text = replaceCI(text, "{TASK}", fi.Task)
	text = replaceCI(text, "{STEP}", fi.Step)

	// ── Relative references: {IN-N}, {OUT-N}, {LOG-N} ─────────────────────
	// Pattern: {(IN|OUT|LOG)-N} where N is a positive integer.
	relRe := regexp.MustCompile(`(?i)\{(IN|OUT|LOG)-(\d+)\}`)
	text = relRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := relRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		extType := strings.ToLower(sub[1])
		offset, _ := strconv.Atoi(sub[2])
		targetStep := fi.StepN - offset
		if targetStep < 1 {
			return match // step doesn't exist, keep as-is
		}
		filename := fi.fileForStep(targetStep, extType)
		if !fileExists(filename) {
			return match
		}
		return filename
	})

	// ── Range references: {IN01:03}, {OUT02:04}, {LOG03:04} ───────────────
	// Pattern: {(IN|OUT|LOG)SS:EE} where SS and EE are two-digit step numbers.
	rangeRe := regexp.MustCompile(`(?i)\{(IN|OUT|LOG)(\d{2}):(\d{2})\}`)
	text = rangeRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := rangeRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		extType := strings.ToLower(sub[1])
		startStep, _ := strconv.Atoi(sub[2])
		endStep, _ := strconv.Atoi(sub[3])

		if startStep > endStep {
			return match // invalid range, keep as-is
		}

		var files []string
		for s := startStep; s <= endStep; s++ {
			filename := fi.fileForStep(s, extType)
			if fileExists(filename) {
				files = append(files, filename)
			}
			// If the file doesn't exist we silently skip it in range mode,
			// so the user gets whatever actually exists.
		}

		if len(files) == 0 {
			return match // nothing exists, keep variable as-is
		}
		return strings.Join(files, "\n")
	})

	return text
}

// ExpandFromPath is a convenience wrapper that parses the path first and then
// calls Expand.  If path cannot be parsed (no step number found) the text is
// returned unchanged.
func ExpandFromPath(text, path string) string {
	fi := ParseFilename(path)
	return Expand(text, fi)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// replaceCI replaces all case-insensitive occurrences of variable (a string
// like "{NAME}") with value.  Uses a compiled regexp so all case variants are
// caught in a single pass.
func replaceCI(text, variable, value string) string {
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(variable))
	return re.ReplaceAllString(text, value)
}

// fileExists returns true if the given path exists on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
