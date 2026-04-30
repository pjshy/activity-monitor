package main

import "testing"

func TestParseProcessList(t *testing.T) {
	output := `
  101     1 root        2.5  1.1  40960 S /usr/bin/windowserver -daemon
  202   101 alice       0.3  0.5  20480 R /Applications/App.app/Contents/MacOS/App With Spaces --flag value
`

	processes, err := parseProcessList(output)
	if err != nil {
		t.Fatalf("parse process list: %v", err)
	}
	if len(processes) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(processes))
	}

	first := processes[0]
	if first.PID != 101 || first.PPID != 1 {
		t.Fatalf("unexpected pid values: %+v", first)
	}
	if first.User != "root" || first.CPU != 2.5 || first.Memory != 1.1 || first.RSSKB != 40960 {
		t.Fatalf("unexpected parsed metrics: %+v", first)
	}
	if first.Command != "/usr/bin/windowserver" {
		t.Fatalf("unexpected command: %q", first.Command)
	}
	if first.Args != "/usr/bin/windowserver -daemon" {
		t.Fatalf("unexpected args: %q", first.Args)
	}

	second := processes[1]
	if second.Command != "/Applications/App.app/Contents/MacOS/App" {
		t.Fatalf("unexpected spaced command: %q", second.Command)
	}
	if second.Args != "/Applications/App.app/Contents/MacOS/App With Spaces --flag value" {
		t.Fatalf("unexpected spaced args: %q", second.Args)
	}
}

func TestParseProcessListEmpty(t *testing.T) {
	if _, err := parseProcessList("\n"); err == nil {
		t.Fatal("expected error for empty process list")
	}
}
