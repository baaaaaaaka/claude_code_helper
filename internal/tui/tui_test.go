package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
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
