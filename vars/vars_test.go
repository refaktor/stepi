package vars_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/user/stepi/vars"
)

// ─── ParseFilename ────────────────────────────────────────────────────────────

func TestParseFilename_Basic(t *testing.T) {
	tests := []struct {
		input    string
		wantTask string
		wantStep string
		wantN    int
		wantFullName string
		wantDir  string
	}{
		{"sometask03.md", "sometask", "03", 3, "sometask03", ""},
		{"sometask03.out.md", "sometask", "03", 3, "sometask03", ""},
		{"sometask03.log", "sometask", "03", 3, "sometask03", ""},
		{"google01.md", "google", "01", 1, "google01", ""},
		{"stepi_server_04.md", "stepi_server_", "04", 4, "stepi_server_04", ""},
		// single-digit step gets zero-padded
		{"task3.md", "task", "03", 3, "task03", ""},
		// directory prefix
		{".stepi/google03.md", "google", "03", 3, "google03", ".stepi"},
		{".stepi/stepi_brainstorm-mobile_02.md", "stepi_brainstorm-mobile_", "02", 2, "stepi_brainstorm-mobile_02", ".stepi"},
		// --name flag values (no extension)
		{"sometask03", "sometask", "03", 3, "sometask03", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			fi := vars.ParseFilename(tc.input)
			if fi == nil {
				t.Fatalf("ParseFilename(%q) = nil, want non-nil", tc.input)
			}
			if fi.Task != tc.wantTask {
				t.Errorf("Task = %q, want %q", fi.Task, tc.wantTask)
			}
			if fi.Step != tc.wantStep {
				t.Errorf("Step = %q, want %q", fi.Step, tc.wantStep)
			}
			if fi.StepN != tc.wantN {
				t.Errorf("StepN = %d, want %d", fi.StepN, tc.wantN)
			}
			if fi.FullName != tc.wantFullName {
				t.Errorf("FullName = %q, want %q", fi.FullName, tc.wantFullName)
			}
			if fi.Dir != tc.wantDir {
				t.Errorf("Dir = %q, want %q", fi.Dir, tc.wantDir)
			}
		})
	}
}

func TestParseFilename_NoMatch(t *testing.T) {
	noMatch := []string{
		"README.md",
		"sometask.md",
		"",
		"nodots",
	}
	for _, s := range noMatch {
		if fi := vars.ParseFilename(s); fi != nil {
			t.Errorf("ParseFilename(%q) = %+v, want nil", s, fi)
		}
	}
}

// ─── Expand – simple variables ────────────────────────────────────────────────

func TestExpand_SimpleVars(t *testing.T) {
	fi := vars.ParseFilename("sometask03.md")
	if fi == nil {
		t.Fatal("unexpected nil FileInfo")
	}

	tests := []struct {
		in   string
		want string
	}{
		// {NAME} and {TASK} both expand to the TASK prefix (without the step number)
		{"{NAME}", "sometask"},
		{"{TASK}", "sometask"},
		{"{STEP}", "03"},
		// {FULLNAME} expands to the full base name (TASK + STEP)
		{"{FULLNAME}", "sometask03"},
		// Composing filenames: {NAME} is the prefix so the user adds their own step
		{"file is {NAME}03.md", "file is sometask03.md"},
		{"from {NAME}01.md to {NAME}02.out.md", "from sometask01.md to sometask02.out.md"},
		// case-insensitive
		{"{name}", "sometask"},
		{"{task}", "sometask"},
		{"{step}", "03"},
		{"{Name}", "sometask"},
		{"{fullname}", "sometask03"},
	}

	for _, tc := range tests {
		got := vars.Expand(tc.in, fi)
		if got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─── Expand – relative references (requires real files on disk) ───────────────

func TestExpand_RelativeRefs(t *testing.T) {
	// Create a temporary directory and seed it with some files.
	dir := t.TempDir()

	// Create files: task01.md, task01.out.md, task01.log, task02.md, task02.out.md
	for _, name := range []string{
		"task01.md", "task01.out.md", "task01.log",
		"task02.md", "task02.out.md", "task02.log",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate being in task03.md (step 3).
	fi := vars.ParseFilename(filepath.Join(dir, "task03.md"))
	if fi == nil {
		t.Fatal("unexpected nil FileInfo for task03.md")
	}

	tests := []struct {
		in   string
		want string
	}{
		{"{IN-1}", filepath.Join(dir, "task02.md")},
		{"{IN-2}", filepath.Join(dir, "task01.md")},
		{"{OUT-1}", filepath.Join(dir, "task02.out.md")},
		{"{OUT-2}", filepath.Join(dir, "task01.out.md")},
		{"{LOG-1}", filepath.Join(dir, "task02.log")},
		{"{LOG-2}", filepath.Join(dir, "task01.log")},
		// case-insensitive
		{"{in-1}", filepath.Join(dir, "task02.md")},
		{"{out-1}", filepath.Join(dir, "task02.out.md")},
		// step too far back → kept as-is
		{"{IN-3}", "{IN-3}"},
		// non-existent file → kept as-is
		{"{IN-99}", "{IN-99}"},
	}

	for _, tc := range tests {
		got := vars.Expand(tc.in, fi)
		if got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─── Expand – range references ────────────────────────────────────────────────

func TestExpand_RangeRefs(t *testing.T) {
	dir := t.TempDir()

	// Create: task01.md, task02.md, task03.md, task03.log, task04.log
	for _, name := range []string{
		"task01.md", "task02.md", "task03.md",
		"task01.log", "task02.log", "task03.log", "task04.log",
		"task02.out.md", "task03.out.md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fi := vars.ParseFilename(filepath.Join(dir, "task05.md"))
	if fi == nil {
		t.Fatal("unexpected nil FileInfo for task05.md")
	}

	p := func(name string) string { return filepath.Join(dir, name) }

	tests := []struct {
		in   string
		want string
	}{
		// All three exist
		{"{IN01:03}", p("task01.md") + "\n" + p("task02.md") + "\n" + p("task03.md")},
		// Only 02 and 03 exist for out
		{"{OUT02:04}", p("task02.out.md") + "\n" + p("task03.out.md")},
		// log 03 and 04 exist
		{"{LOG03:04}", p("task03.log") + "\n" + p("task04.log")},
		// case-insensitive
		{"{in01:02}", p("task01.md") + "\n" + p("task02.md")},
		// nothing exists → kept as-is
		{"{OUT10:12}", "{OUT10:12}"},
		// invalid range (start > end) → kept as-is
		{"{IN03:01}", "{IN03:01}"},
	}

	for _, tc := range tests {
		got := vars.Expand(tc.in, fi)
		if got != tc.want {
			t.Errorf("Expand(%q) =\n  %q\nwant\n  %q", tc.in, got, tc.want)
		}
	}
}

// ─── ExpandFromPath convenience wrapper ───────────────────────────────────────

func TestExpandFromPath_NoStepNumber(t *testing.T) {
	// Files without a step number are returned unchanged.
	text := "hello {NAME} world"
	got := vars.ExpandFromPath(text, "README.md")
	if got != text {
		t.Errorf("expected unchanged text, got %q", got)
	}
}

func TestExpandFromPath_WithStep(t *testing.T) {
	text := "task is {TASK}, step is {STEP}"
	got := vars.ExpandFromPath(text, "myjob05.md")
	want := "task is myjob, step is 05"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ─── Integration: mixed variables in one string ───────────────────────────────

func TestExpand_Mixed(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"proj01.md", "proj01.out.md", "proj02.md", "proj02.out.md", "proj02.log"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644)
	}

	fi := vars.ParseFilename(filepath.Join(dir, "proj03.md"))

	text := `Read context from {IN-2} and {IN-1}, also check {OUT-1} and {LOG-1}.
Current task name: {NAME}, task: {TASK}, step: {STEP}.
Previous inputs: {IN01:02}`

	got := vars.Expand(text, fi)

	// Verify key substitutions
	if !contains(got, filepath.Join(dir, "proj01.md")) {
		t.Errorf("missing proj01.md in output:\n%s", got)
	}
	if !contains(got, filepath.Join(dir, "proj02.md")) {
		t.Errorf("missing proj02.md in output:\n%s", got)
	}
	if !contains(got, filepath.Join(dir, "proj02.out.md")) {
		t.Errorf("missing proj02.out.md in output:\n%s", got)
	}
	// {NAME} expands to TASK prefix "proj" (not "proj03")
	if !contains(got, "proj") {
		t.Errorf("missing {NAME}={TASK}=proj in output:\n%s", got)
	}
	// {STEP} expands to "03"
	if !contains(got, "03") {
		t.Errorf("missing {STEP}=03 in output:\n%s", got)
	}
	// {LOG-1} → proj02.log (exists)
	if !contains(got, filepath.Join(dir, "proj02.log")) {
		t.Errorf("missing proj02.log in output:\n%s", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) &&
		(s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
