package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/update"
)

func newTestScreen(t *testing.T, w, h int) tcell.Screen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("init screen: %v", err)
	}
	screen.SetSize(w, h)
	t.Cleanup(func() { screen.Fini() })
	return screen
}

func newTestState(projects []claudehistory.Project) *uiState {
	return &uiState{
		projects:       projects,
		focus:          "projects",
		lastListFocus:  "projects",
		previewCache:   map[string]string{},
		previewError:   map[string]string{},
		previewLoading: map[string]bool{},
	}
}

func TestHandleKeyQuit(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if !errors.Is(err, errQuit) {
		t.Fatalf("expected quit error, got %v", err)
	}
}

func TestHandleKeyJKNavigation(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	state := newTestState([]claudehistory.Project{
		{Key: "one", Path: "/tmp/one"},
		{Key: "two", Path: "/tmp/two"},
	})

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if state.projectState.selected != 1 {
		t.Fatalf("expected selection=1, got %d", state.projectState.selected)
	}

	_, err = handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if state.projectState.selected != 0 {
		t.Fatalf("expected selection=0, got %d", state.projectState.selected)
	}
}

func TestHandleKeyEnterSelectsSession(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	now := time.Now()
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{
			{SessionID: "sess-1", Summary: "hello", ModifiedAt: now},
		},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "sessions"
	state.lastListFocus = "sessions"

	selection, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if selection == nil || selection.Session.SessionID != "sess-1" {
		t.Fatalf("expected session sess-1, got %#v", selection)
	}
	if selection.UseProxy {
		t.Fatalf("expected proxy to be disabled by default")
	}
}

func TestHandleKeyCtrlJSelectsSession(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{
			{SessionID: "sess-2", Summary: "hello"},
		},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "sessions"
	state.lastListFocus = "sessions"

	selection, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlJ, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if selection == nil || selection.Session.SessionID != "sess-2" {
		t.Fatalf("expected session sess-2, got %#v", selection)
	}
	if selection.UseProxy {
		t.Fatalf("expected proxy to be disabled by default")
	}
}

func TestNewSessionCwdPrefersProjectPath(t *testing.T) {
	project := claudehistory.Project{Path: "/tmp/project"}
	if got := newSessionCwd(project, "/tmp/default"); got != "/tmp/project" {
		t.Fatalf("expected project path, got %q", got)
	}
}

func TestNewSessionCwdUsesDefaultWhenNoProjectPath(t *testing.T) {
	project := claudehistory.Project{}
	if got := newSessionCwd(project, "/tmp/default"); got != "/tmp/default" {
		t.Fatalf("expected default path, got %q", got)
	}
}

func TestNewSessionCwdEmptyWhenNoPaths(t *testing.T) {
	project := claudehistory.Project{}
	if got := newSessionCwd(project, ""); got != "" {
		t.Fatalf("expected empty path, got %q", got)
	}
}

func TestHandleKeyEnterStartsNewSessionWhenNoHistory(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	dir := t.TempDir()
	state := newTestState(nil)

	selection, err := handleKey(context.Background(), screen, state, Options{DefaultCwd: dir}, tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if selection == nil || selection.Cwd != dir || selection.Session.SessionID != "" {
		t.Fatalf("expected new session in %s, got %#v", dir, selection)
	}
}

func TestHandleKeyCtrlNStartsNewSessionInProject(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	dir := t.TempDir()
	project := claudehistory.Project{
		Key:  "one",
		Path: dir,
		Sessions: []claudehistory.Session{
			{SessionID: "sess-5", Summary: "hello"},
		},
	}
	state := newTestState([]claudehistory.Project{project})
	state.proxyEnabled = true

	selection, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlN, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if selection == nil || selection.Cwd != dir || selection.Session.SessionID != "" {
		t.Fatalf("expected new session in %s, got %#v", dir, selection)
	}
	if !selection.UseProxy {
		t.Fatalf("expected proxy enabled")
	}
}

func TestHandleKeyCtrlNIgnoredWithoutCwd(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState(nil)

	selection, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlN, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if selection != nil {
		t.Fatalf("expected no selection, got %#v", selection)
	}
}

func TestHandleKeyProxyToggleRequiresConfig(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.proxyEnabled = false
	state.proxyConfigured = false

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlP, 0, 0))
	var toggle ProxyToggleRequested
	if !errors.As(err, &toggle) {
		t.Fatalf("expected proxy toggle error, got %v", err)
	}
	if !toggle.Enable || !toggle.RequireConfig {
		t.Fatalf("expected enable=true requireConfig=true, got %+v", toggle)
	}
}

func TestHandleKeyProxyToggleDisablesProxy(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.proxyEnabled = true
	state.proxyConfigured = true

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlP, 0, 0))
	var toggle ProxyToggleRequested
	if !errors.As(err, &toggle) {
		t.Fatalf("expected proxy toggle error, got %v", err)
	}
	if toggle.Enable || toggle.RequireConfig {
		t.Fatalf("expected enable=false requireConfig=false, got %+v", toggle)
	}
}

func TestPreviewArrowScrollsWhenFocused(t *testing.T) {
	screen := newTestScreen(t, 60, 12)
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{
			{SessionID: "sess-3", Summary: "long summary to force wrapping and scrolling"},
		},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "preview"
	state.lastListFocus = "sessions"
	state.previewCache["sess-3"] = strings.Repeat("line ", 80)

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if state.previewState.scroll == 0 {
		t.Fatalf("expected preview scroll to move")
	}
}

func TestDisplayWidthHelpers(t *testing.T) {
	txt := "中文ABC"
	if got := displayWidth(txt); got != 7 {
		t.Fatalf("expected display width 7, got %d", got)
	}
	if got := truncate(txt, 4); got != "中文" {
		t.Fatalf("expected truncate to 中文, got %q", got)
	}
	padded := padRight("中文", 6)
	if got := displayWidth(padded); got != 6 {
		t.Fatalf("expected padded width 6, got %d (%q)", got, padded)
	}
}

func TestPreviewSearchMatches(t *testing.T) {
	screen := newTestScreen(t, 80, 12)
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{
			{SessionID: "sess-4", Summary: "preview search"},
		},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "preview"
	state.lastListFocus = "sessions"
	state.previewCache["sess-4"] = "alpha\nbeta\nalpha"

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyRune, '/', 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	for _, ch := range []rune("alpha") {
		_, err = handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyRune, ch, 0))
		if err != nil {
			t.Fatalf("handleKey error: %v", err)
		}
	}
	_, err = handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}
	if len(state.previewMatches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(state.previewMatches))
	}
}

func TestHandleKeyCtrlURequestsUpdateWhenAvailable(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.updateStatus = &update.Status{Supported: true, UpdateAvailable: true}

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlU, 0, 0))
	if !errors.As(err, &UpdateRequested{}) {
		t.Fatalf("expected update requested error, got %v", err)
	}
}

func TestHandleKeyCtrlUIgnoredWhenNoUpdate(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.updateStatus = &update.Status{Supported: true, UpdateAvailable: false}

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlU, 0, 0))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDrawShowsUpdateHintWhenAvailable(t *testing.T) {
	screen := newTestScreen(t, 120, 20)
	state := newTestState([]claudehistory.Project{})
	state.updateStatus = &update.Status{Supported: true, UpdateAvailable: true}

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{Version: "1.0.0"}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}

	_, h := screen.Size()
	line := readScreenLine(screen, h-1)
	if !strings.Contains(line, "Ctrl+U upgrade") {
		t.Fatalf("expected update hint in status line, got %q", strings.TrimSpace(line))
	}
}

func TestDrawShowsUpdateErrorWhenCheckFails(t *testing.T) {
	screen := newTestScreen(t, 160, 20)
	state := newTestState([]claudehistory.Project{})
	state.updateStatus = &update.Status{Supported: false, Error: "network timeout"}

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{Version: "1.0.0"}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}

	_, h := screen.Size()
	line := readScreenLine(screen, h-1)
	if !strings.Contains(line, "Update check failed: network timeout") {
		t.Fatalf("expected update error in status line, got %q", strings.TrimSpace(line))
	}
	if !strings.Contains(line, "update failed") {
		t.Fatalf("expected update failed hint in status line, got %q", strings.TrimSpace(line))
	}
}

func readScreenLine(screen tcell.Screen, y int) string {
	w, _ := screen.Size()
	var buf strings.Builder
	for x := 0; x < w; x++ {
		ch, _, _, _ := screen.GetContent(x, y)
		if ch == 0 {
			ch = ' '
		}
		buf.WriteRune(ch)
	}
	return buf.String()
}
