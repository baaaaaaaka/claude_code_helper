package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRunTargetOutputIgnoresMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.log")
	if err := archiveRunTargetOutput(path, 1); err != nil {
		t.Fatalf("archiveRunTargetOutput error: %v", err)
	}
}

func TestArchiveRunTargetOutputReservesUniqueArchivePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.log")
	if err := os.WriteFile(path, []byte("latest"), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}
	if err := os.WriteFile(path+".attempt-1", []byte("older"), 0o600); err != nil {
		t.Fatalf("write existing archive: %v", err)
	}

	if err := archiveRunTargetOutput(path, 1); err != nil {
		t.Fatalf("archiveRunTargetOutput error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected original output to be moved, stat err=%v", err)
	}

	archived, err := os.ReadFile(path + ".attempt-1.1")
	if err != nil {
		t.Fatalf("read reserved archive: %v", err)
	}
	if string(archived) != "latest" {
		t.Fatalf("unexpected reserved archive content %q", string(archived))
	}

	existing, err := os.ReadFile(path + ".attempt-1")
	if err != nil {
		t.Fatalf("read existing archive: %v", err)
	}
	if string(existing) != "older" {
		t.Fatalf("unexpected existing archive content %q", string(existing))
	}
}

func TestNewFileRunTargetIOWithOptionsRejectsCollidingPaths(t *testing.T) {
	dir := t.TempDir()
	sharedPath := filepath.Join(dir, "shared.log")
	prepare := newFileRunTargetIOWithOptions("", sharedPath, sharedPath, fileRunTargetIOOptions{})

	if _, err := prepare(); err == nil || !strings.Contains(err.Error(), "stdoutPath and stderrPath") {
		t.Fatalf("expected colliding path error, got %v", err)
	}
}

func TestNewFileRunTargetIOWithOptionsArchivesRetriesAndHeadlessDefaults(t *testing.T) {
	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "logs", "stdout.log")
	stderrPath := filepath.Join(dir, "logs", "stderr.log")
	prepare := newFileRunTargetIOWithOptions(
		"",
		stdoutPath,
		stderrPath,
		fileRunTargetIOOptions{
			Headless:            true,
			ArchiveRetryOutputs: true,
		},
	)
	if prepare == nil {
		t.Fatalf("expected headless prepare IO function")
	}

	first, err := prepare()
	if err != nil {
		t.Fatalf("first prepare error: %v", err)
	}
	if first.Stdin == nil || first.Stdout == nil || first.Stderr == nil {
		t.Fatalf("expected headless prepare to populate all stdio handles")
	}
	if _, err := io.WriteString(first.Stdout, "first-out"); err != nil {
		t.Fatalf("write first stdout: %v", err)
	}
	if _, err := io.WriteString(first.Stderr, "first-err"); err != nil {
		t.Fatalf("write first stderr: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first IO: %v", err)
	}

	second, err := prepare()
	if err != nil {
		t.Fatalf("second prepare error: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second IO: %v", err)
	}

	archivedStdout, err := os.ReadFile(stdoutPath + ".attempt-1")
	if err != nil {
		t.Fatalf("read archived stdout: %v", err)
	}
	if string(archivedStdout) != "first-out" {
		t.Fatalf("unexpected archived stdout %q", string(archivedStdout))
	}

	archivedStderr, err := os.ReadFile(stderrPath + ".attempt-1")
	if err != nil {
		t.Fatalf("read archived stderr: %v", err)
	}
	if string(archivedStderr) != "first-err" {
		t.Fatalf("unexpected archived stderr %q", string(archivedStderr))
	}
}

func TestAppendFileWriter(t *testing.T) {
	if got := newAppendFileWriter("   "); got != nil {
		t.Fatalf("expected nil writer for blank path")
	}

	path := filepath.Join(t.TempDir(), "nested", "events.log")
	writer, ok := newAppendFileWriter(path).(*appendFileWriter)
	if !ok {
		t.Fatalf("expected appendFileWriter")
	}

	if n, err := writer.Write([]byte("first")); err != nil || n != len("first") {
		t.Fatalf("first write n=%d err=%v", n, err)
	}
	if n, err := writer.Write(nil); err != nil || n != 0 {
		t.Fatalf("empty write n=%d err=%v", n, err)
	}
	if n, err := writer.Write([]byte(" second")); err != nil || n != len(" second") {
		t.Fatalf("second write n=%d err=%v", n, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read appended file: %v", err)
	}
	if string(data) != "first second" {
		t.Fatalf("unexpected appended content %q", string(data))
	}
}
