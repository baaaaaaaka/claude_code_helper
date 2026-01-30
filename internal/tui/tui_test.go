package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/update"
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

type sizedScreen struct {
	tcell.Screen
}

func (s *sizedScreen) Init() error {
	if err := s.Screen.Init(); err != nil {
		return err
	}
	s.Screen.SetSize(80, 24)
	return nil
}

func newTestState(projects []claudehistory.Project) *uiState {
	return &uiState{
		projects:         projects,
		focus:            "projects",
		lastListFocus:    "projects",
		expandedSessions: map[string]bool{},
		previewCache:     map[string]string{},
		previewError:     map[string]string{},
		previewLoading:   map[string]bool{},
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
	state.sessionState.selected = 1

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
	state.sessionState.selected = 1

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

func TestBuildProjectItemsPinsCurrent(t *testing.T) {
	cwd := t.TempDir()
	projects := []claudehistory.Project{{Path: "/tmp/other"}}
	items := buildProjectItems(projects, cwd)
	if len(items) == 0 || !items[0].isCurrent {
		t.Fatalf("expected current project first, got %#v", items)
	}
	if items[0].project.Path != cwd {
		t.Fatalf("expected current path %s, got %s", cwd, items[0].project.Path)
	}
	if !strings.Contains(items[0].label, "[current]") {
		t.Fatalf("expected current label, got %q", items[0].label)
	}
}

func TestBuildProjectItemsMarksExistingCurrent(t *testing.T) {
	cwd := t.TempDir()
	projects := []claudehistory.Project{{Path: cwd}, {Path: "/tmp/other"}}
	items := buildProjectItems(projects, cwd)
	if len(items) == 0 || !items[0].isCurrent {
		t.Fatalf("expected current project first, got %#v", items)
	}
}

func TestFilterProjectsKeepsCurrentVisible(t *testing.T) {
	cwd := t.TempDir()
	items := buildProjectItems([]claudehistory.Project{{Path: "/tmp/other"}}, cwd)
	filtered := filterProjects(items, "nomatch")
	found := false
	for _, it := range filtered {
		if it.isCurrent {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected current project to remain visible")
	}
}

func TestBuildSessionItemsIncludesNewAgent(t *testing.T) {
	project := claudehistory.Project{Sessions: []claudehistory.Session{{SessionID: "sess-1"}}}
	items := buildSessionItems(project, nil)
	if len(items) == 0 || items[0].kind != sessionItemNew {
		t.Fatalf("expected new agent item first, got %#v", items)
	}
}

func TestFilterSessionsKeepsNewAgent(t *testing.T) {
	project := claudehistory.Project{Sessions: []claudehistory.Session{{SessionID: "sess-1"}}}
	items := buildSessionItems(project, nil)
	filtered := filterSessions(items, "nomatch")
	if len(filtered) == 0 || filtered[0].kind != sessionItemNew {
		t.Fatalf("expected new agent item to remain visible")
	}
}

func TestBuildSessionItemsShowsSubagentMarkers(t *testing.T) {
	now := time.Now()
	project := claudehistory.Project{
		Sessions: []claudehistory.Session{{
			SessionID:  "sess-1",
			ModifiedAt: now,
			Subagents:  []claudehistory.SubagentSession{{AgentID: "agent-1", ModifiedAt: now}},
		}},
	}

	collapsed := buildSessionItems(project, map[string]bool{})
	if len(collapsed) < 2 {
		t.Fatalf("expected main session row, got %#v", collapsed)
	}
	if !strings.HasPrefix(collapsed[1].label, "[+] ") {
		t.Fatalf("expected collapsed marker, got %q", collapsed[1].label)
	}

	expanded := buildSessionItems(project, map[string]bool{"sess-1": true})
	if len(expanded) < 3 {
		t.Fatalf("expected subagent row when expanded, got %#v", expanded)
	}
	if !strings.HasPrefix(expanded[1].label, "[-] ") {
		t.Fatalf("expected expanded marker, got %q", expanded[1].label)
	}
	if expanded[2].kind != sessionItemSubagent {
		t.Fatalf("expected subagent row, got %#v", expanded[2])
	}
}

func TestBuildSessionItemsNoMarkerWithoutSubagents(t *testing.T) {
	project := claudehistory.Project{
		Sessions: []claudehistory.Session{{SessionID: "sess-1"}},
	}
	items := buildSessionItems(project, map[string]bool{})
	if len(items) < 2 {
		t.Fatalf("expected main session row, got %#v", items)
	}
	if !strings.HasPrefix(items[1].label, "   ") {
		t.Fatalf("expected empty marker, got %q", items[1].label)
	}
}

func TestBuildStatusLinesKeepsGroups(t *testing.T) {
	segments := []statusSegment{{
		text:  "A: one  B: two  C: three",
		style: tcell.StyleDefault,
	}}
	lines := buildStatusLines(12, segments, "", false)
	got := flattenStatusGroups(lines)
	want := []string{"A: one", "B: two", "C: three"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected groups: %#v", got)
	}
}

func TestHandleKeyCtrlOTogglesSubagents(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	now := time.Now()
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{{
			SessionID:  "sess-1",
			ModifiedAt: now,
			Subagents:  []claudehistory.SubagentSession{{AgentID: "agent-1", ModifiedAt: now}},
		}},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "sessions"
	state.lastListFocus = "sessions"
	state.sessionState.selected = 1

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlO, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if !state.expandedSessions["sess-1"] {
		t.Fatalf("expected session to be expanded")
	}
	if state.sessionState.selected != 1 {
		t.Fatalf("expected selection to stay on parent, got %d", state.sessionState.selected)
	}

	_, err = handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlO, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if state.expandedSessions["sess-1"] {
		t.Fatalf("expected session to be collapsed")
	}
}

func TestHandleKeyCtrlOFromSubagentSelectsParent(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	now := time.Now()
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{{
			SessionID:  "sess-1",
			ModifiedAt: now,
			Subagents:  []claudehistory.SubagentSession{{AgentID: "agent-1", ModifiedAt: now}},
		}},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "sessions"
	state.lastListFocus = "sessions"
	state.expandedSessions["sess-1"] = true
	state.sessionState.selected = 2

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlO, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if state.expandedSessions["sess-1"] {
		t.Fatalf("expected session to be collapsed")
	}
	if state.sessionState.selected != 1 {
		t.Fatalf("expected selection to move to parent, got %d", state.sessionState.selected)
	}
}

func TestHandleKeyCtrlOIgnoredWhenNotSessions(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{{
			SessionID: "sess-1",
			Subagents: []claudehistory.SubagentSession{{AgentID: "agent-1"}},
		}},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "projects"
	state.lastListFocus = "projects"

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlO, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if len(state.expandedSessions) != 0 {
		t.Fatalf("expected no expansion when not in sessions")
	}
}

func TestHandleKeyCtrlOIgnoredOnNewAgent(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{{
			SessionID: "sess-1",
			Subagents: []claudehistory.SubagentSession{{AgentID: "agent-1"}},
		}},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "sessions"
	state.lastListFocus = "sessions"
	state.sessionState.selected = 0

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlO, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if len(state.expandedSessions) != 0 {
		t.Fatalf("expected no expansion from new agent row")
	}
}

func TestBuildStatusLinesReservesRightLabel(t *testing.T) {
	segments := []statusSegment{{
		text:  "A: one  B: two  C: three  D: four",
		style: tcell.StyleDefault,
	}}
	width := 20
	right := "v0.0.18"
	lines := buildStatusLines(width, segments, right, false)
	if len(lines) == 0 {
		t.Fatalf("expected status lines")
	}
	last := lines[len(lines)-1]
	if last.right != right {
		t.Fatalf("expected right label %q, got %q", right, last.right)
	}
	maxLeft := width - displayWidth(right)
	if maxLeft < 0 {
		maxLeft = 0
	}
	if lineWidthGroups(last.groups) > maxLeft {
		t.Fatalf("expected last line width <= %d, got %d", maxLeft, lineWidthGroups(last.groups))
	}
}

func flattenStatusGroups(lines []statusLine) []string {
	var out []string
	for _, line := range lines {
		for _, group := range line.groups {
			out = append(out, group.text)
		}
	}
	return out
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

func TestRefreshStateUpdatesOrPreserves(t *testing.T) {
	t.Run("preserves projects on error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		state := newTestState([]claudehistory.Project{{Key: "one"}})
		opts := Options{
			LoadProjects: func(ctx context.Context) ([]claudehistory.Project, error) {
				return nil, ctx.Err()
			},
		}
		refreshState(ctx, state, opts)
		if !errors.Is(state.loadError, context.Canceled) {
			t.Fatalf("expected canceled error, got %v", state.loadError)
		}
		if len(state.projects) != 1 || state.projects[0].Key != "one" {
			t.Fatalf("expected projects to remain unchanged, got %#v", state.projects)
		}
	})

	t.Run("resets selection state on success", func(t *testing.T) {
		state := newTestState([]claudehistory.Project{{Key: "old"}})
		state.projectState.selected = 3
		state.sessionState.selected = 2
		state.previewState.scroll = 5
		opts := Options{
			LoadProjects: func(ctx context.Context) ([]claudehistory.Project, error) {
				return []claudehistory.Project{{Key: "new"}}, nil
			},
		}
		refreshState(context.Background(), state, opts)
		if state.loadError != nil {
			t.Fatalf("expected no error, got %v", state.loadError)
		}
		if len(state.projects) != 1 || state.projects[0].Key != "new" {
			t.Fatalf("expected projects to be updated, got %#v", state.projects)
		}
		if state.projectState.selected != 0 || state.sessionState.selected != 0 || state.previewState.scroll != 0 {
			t.Fatalf("expected state to be reset, got %#v %#v %#v", state.projectState, state.sessionState, state.previewState)
		}
	})
}

func TestTextHelpers(t *testing.T) {
	t.Run("truncate enforces width", func(t *testing.T) {
		if got := truncate("hello", 0); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
		if got := truncate("hello", 3); got != "hel" {
			t.Fatalf("expected truncation, got %q", got)
		}
		if got := truncate("hi", 5); got != "hi" {
			t.Fatalf("expected original string, got %q", got)
		}
	})

	t.Run("padRight pads to width", func(t *testing.T) {
		if got := padRight("hi", 4); got != "hi  " {
			t.Fatalf("expected padded string, got %q", got)
		}
		if got := padRight("hello", 3); got != "hello" {
			t.Fatalf("expected no padding, got %q", got)
		}
	})

	t.Run("versionLabel prefixes and normalizes", func(t *testing.T) {
		if got := versionLabel(""); got != "dev" {
			t.Fatalf("expected dev for empty, got %q", got)
		}
		if got := versionLabel("dev"); got != "dev" {
			t.Fatalf("expected dev to stay, got %q", got)
		}
		if got := versionLabel("1.2.3"); got != "v1.2.3" {
			t.Fatalf("expected v prefix, got %q", got)
		}
		if got := versionLabel("v2.0.0"); got != "v2.0.0" {
			t.Fatalf("expected existing prefix to remain, got %q", got)
		}
	})
}

func TestSelectSessionRequiresLoadProjects(t *testing.T) {
	if _, err := SelectSession(context.Background(), Options{}); err == nil {
		t.Fatalf("expected error when LoadProjects is nil")
	}
}

func TestErrorTypesAndUIEvent(t *testing.T) {
	if (UpdateRequested{}).Error() != "update requested" {
		t.Fatalf("unexpected UpdateRequested error")
	}
	if (ProxyToggleRequested{}).Error() != "proxy toggle requested" {
		t.Fatalf("unexpected ProxyToggleRequested error")
	}
	now := time.Now()
	ev := &uiEvent{when: now}
	if ev.When() != now {
		t.Fatalf("expected uiEvent time to match")
	}
}

func TestPreviewTextHelpers(t *testing.T) {
	session := &claudehistory.Session{FilePath: "/tmp/session.jsonl"}
	subagent := &claudehistory.SubagentSession{FilePath: "/tmp/sub.jsonl"}

	if got := previewFilePath(session, subagent); got != "/tmp/sub.jsonl" {
		t.Fatalf("expected subagent file path, got %q", got)
	}
	if got := previewFilePath(session, nil); got != "/tmp/session.jsonl" {
		t.Fatalf("expected session file path, got %q", got)
	}
	if got := previewFilePath(nil, nil); got != "" {
		t.Fatalf("expected empty file path, got %q", got)
	}

	state := newTestState(nil)
	cacheKey := previewCacheKey(session, nil)
	state.previewError[cacheKey] = "boom"
	if got := previewTextForItem(state, session, nil); got != "Preview failed: boom" {
		t.Fatalf("unexpected preview error text: %q", got)
	}
	delete(state.previewError, cacheKey)
	state.previewCache[cacheKey] = "hello"
	if got := previewTextForItem(state, session, nil); got != "hello" {
		t.Fatalf("unexpected preview text: %q", got)
	}
	delete(state.previewCache, cacheKey)
	state.previewLoading[cacheKey] = true
	if got := previewTextForItem(state, session, nil); got != "Loading preview…" {
		t.Fatalf("unexpected preview loading text: %q", got)
	}
	if got := previewTextForItem(state, nil, nil); got != "" {
		t.Fatalf("expected empty preview text, got %q", got)
	}
}

func TestIsPreviewNavKey(t *testing.T) {
	if !isPreviewNavKey(tcell.NewEventKey(tcell.KeyUp, 0, 0)) {
		t.Fatalf("expected KeyUp to be preview nav key")
	}
	if !isPreviewNavKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0)) {
		t.Fatalf("expected rune j to be preview nav key")
	}
	if isPreviewNavKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0)) {
		t.Fatalf("expected rune x to be non-nav key")
	}
}

func TestApplyListNavigation(t *testing.T) {
	state := &listState{selected: 1}
	applyListNavigation(state, 0, 2, tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if state.selected != 0 || state.scroll != 0 {
		t.Fatalf("expected reset on empty list")
	}

	cases := []struct {
		ev   *tcell.EventKey
		want int
	}{
		{tcell.NewEventKey(tcell.KeyUp, 0, 0), 0},
		{tcell.NewEventKey(tcell.KeyDown, 0, 0), 2},
		{tcell.NewEventKey(tcell.KeyPgUp, 0, 0), 0},
		{tcell.NewEventKey(tcell.KeyPgDn, 0, 0), 3},
		{tcell.NewEventKey(tcell.KeyHome, 0, 0), 0},
		{tcell.NewEventKey(tcell.KeyEnd, 0, 0), 4},
		{tcell.NewEventKey(tcell.KeyRune, 'k', 0), 0},
		{tcell.NewEventKey(tcell.KeyRune, 'j', 0), 2},
		{tcell.NewEventKey(tcell.KeyRune, 'g', 0), 0},
		{tcell.NewEventKey(tcell.KeyRune, 'G', 0), 4},
	}
	for _, tc := range cases {
		state = &listState{selected: 1}
		applyListNavigation(state, 5, 2, tc.ev)
		if state.selected != tc.want {
			t.Fatalf("expected selected %d, got %d", tc.want, state.selected)
		}
	}

	state = &listState{selected: 1}
	applyListNavigation(state, 5, 2, tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	if state.selected != 1 {
		t.Fatalf("expected selection to remain unchanged")
	}
}

func TestApplyPreviewNavigation(t *testing.T) {
	state := &previewState{scroll: 5}
	applyPreviewNavigation(state, 0, 2, tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if state.scroll != 0 {
		t.Fatalf("expected reset when no lines")
	}

	cases := []struct {
		ev   *tcell.EventKey
		want int
	}{
		{tcell.NewEventKey(tcell.KeyUp, 0, 0), 0},
		{tcell.NewEventKey(tcell.KeyDown, 0, 0), 2},
		{tcell.NewEventKey(tcell.KeyPgUp, 0, 0), 0},
		{tcell.NewEventKey(tcell.KeyPgDn, 0, 0), 3},
		{tcell.NewEventKey(tcell.KeyHome, 0, 0), 0},
		{tcell.NewEventKey(tcell.KeyEnd, 0, 0), 8},
		{tcell.NewEventKey(tcell.KeyRune, 'k', 0), 0},
		{tcell.NewEventKey(tcell.KeyRune, 'j', 0), 2},
		{tcell.NewEventKey(tcell.KeyRune, 'g', 0), 0},
		{tcell.NewEventKey(tcell.KeyRune, 'G', 0), 8},
	}
	for _, tc := range cases {
		state = &previewState{scroll: 1}
		applyPreviewNavigation(state, 10, 2, tc.ev)
		if state.scroll != tc.want {
			t.Fatalf("expected scroll %d, got %d", tc.want, state.scroll)
		}
	}

	state = &previewState{scroll: 1}
	applyPreviewNavigation(state, 10, 2, tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	if state.scroll != 1 {
		t.Fatalf("expected scroll to remain unchanged")
	}
}

func TestEnsurePreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"Hello"},"timestamp":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	screen := newTestScreen(t, 80, 24)
	state := newTestState(nil)
	session := &claudehistory.Session{FilePath: path}
	previewCh := make(chan previewEvent, 1)

	ensurePreview(screen, state, Options{PreviewMessages: 1}, session, nil, previewCh)
	cacheKey := previewCacheKey(session, nil)
	if !state.previewLoading[cacheKey] {
		t.Fatalf("expected preview loading to be set")
	}
	select {
	case ev := <-previewCh:
		if ev.cacheKey != cacheKey {
			t.Fatalf("expected cache key %q, got %q", cacheKey, ev.cacheKey)
		}
		if ev.err != nil {
			t.Fatalf("unexpected preview error: %v", ev.err)
		}
		if !strings.Contains(ev.text, "User:") {
			t.Fatalf("unexpected preview text: %q", ev.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for preview event")
	}

	state.previewCache[cacheKey] = "cached"
	ensurePreview(screen, state, Options{}, session, nil, previewCh)
	if !state.previewLoading[cacheKey] {
		t.Fatalf("expected preview loading to remain set")
	}
}

func TestSelectSessionReturnsSelectionOnEnter(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	prevNewScreen := newScreen
	newScreen = func() (tcell.Screen, error) {
		return &sizedScreen{Screen: screen}, nil
	}
	t.Cleanup(func() { newScreen = prevNewScreen })

	projectPath := t.TempDir()
	projects := []claudehistory.Project{{
		Key:  "proj-1",
		Path: projectPath,
		Sessions: []claudehistory.Session{{
			SessionID:   "sess-1",
			ProjectPath: projectPath,
			FilePath:    filepath.Join(projectPath, "sess-1.jsonl"),
		}},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		screen.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'l', 0))
		screen.PostEvent(tcell.NewEventKey(tcell.KeyDown, 0, 0))
		screen.PostEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	}()

	selection, err := SelectSession(ctx, Options{
		LoadProjects: func(context.Context) ([]claudehistory.Project, error) {
			return projects, nil
		},
	})
	if err != nil {
		t.Fatalf("SelectSession error: %v", err)
	}
	if selection == nil || selection.Session.SessionID != "sess-1" {
		t.Fatalf("unexpected selection: %#v", selection)
	}
}

func TestSelectSessionQuit(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	prevNewScreen := newScreen
	newScreen = func() (tcell.Screen, error) {
		return &sizedScreen{Screen: screen}, nil
	}
	t.Cleanup(func() { newScreen = prevNewScreen })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		screen.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	}()

	selection, err := SelectSession(ctx, Options{
		LoadProjects: func(context.Context) ([]claudehistory.Project, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("SelectSession error: %v", err)
	}
	if selection != nil {
		t.Fatalf("expected nil selection on quit")
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
	state.yoloEnabled = true

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
	if !selection.UseYolo {
		t.Fatalf("expected yolo enabled")
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

func TestHandleKeyCtrlYTogglesYoloOn(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.yoloEnabled = false

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlY, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if !state.yoloEnabled {
		t.Fatalf("expected yolo enabled")
	}
}

func TestHandleKeyCtrlYTogglesYoloOff(t *testing.T) {
	screen := newTestScreen(t, 80, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.yoloEnabled = true

	_, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyCtrlY, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if state.yoloEnabled {
		t.Fatalf("expected yolo disabled")
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
	state.sessionState.selected = 1
	state.previewCache[previewCacheKey(&project.Sessions[0], nil)] = strings.Repeat("line ", 80)

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
	state.sessionState.selected = 1
	state.previewCache[previewCacheKey(&project.Sessions[0], nil)] = "alpha\nbeta\nalpha"

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
	state.updateErrorUntil = time.Now().Add(updateErrorDisplayDuration)

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

func TestDrawHidesUpdateErrorAfterTimeout(t *testing.T) {
	screen := newTestScreen(t, 160, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})
	state.updateStatus = &update.Status{Supported: false, Error: "network timeout"}
	state.updateErrorUntil = time.Now().Add(-time.Second)

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{Version: "1.0.0"}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}

	_, h := screen.Size()
	line := readScreenLine(screen, h-1)
	if strings.Contains(line, "Update check failed") {
		t.Fatalf("expected update error to be hidden, got %q", strings.TrimSpace(line))
	}
	if strings.Contains(line, "update failed") {
		t.Fatalf("expected update failed hint to be hidden, got %q", strings.TrimSpace(line))
	}
}

func TestDrawShowsYoloStatus(t *testing.T) {
	screen := newTestScreen(t, 160, 20)
	state := newTestState([]claudehistory.Project{})

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}

	_, h := screen.Size()
	line := readScreenLine(screen, h-1)
	if !strings.Contains(line, "YOLO mode (Ctrl+Y): off") {
		t.Fatalf("expected yolo off hint in status line, got %q", strings.TrimSpace(line))
	}
}

func TestDrawShowsYoloWarningWhenEnabled(t *testing.T) {
	screen := newTestScreen(t, 160, 20)
	state := newTestState([]claudehistory.Project{})
	state.yoloEnabled = true

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}

	_, h := screen.Size()
	line := readScreenLine(screen, h-1)
	if !strings.Contains(line, "[!] YOLO mode (Ctrl+Y): on") {
		t.Fatalf("expected yolo warning in status line, got %q", strings.TrimSpace(line))
	}
}

func TestDrawShowsSubagentToggleHint(t *testing.T) {
	screen := newTestScreen(t, 160, 20)
	state := newTestState([]claudehistory.Project{{Key: "one", Path: "/tmp"}})

	previewCh := make(chan previewEvent, 1)
	if err := draw(screen, state, Options{}, previewCh); err != nil {
		t.Fatalf("draw error: %v", err)
	}

	_, h := screen.Size()
	line := readScreenLine(screen, h-1)
	if !strings.Contains(line, "Ctrl+O: subagents") {
		t.Fatalf("expected subagent hint in status line, got %q", strings.TrimSpace(line))
	}
}

func TestPreviewCacheKeySeparatesSessionAndSubagent(t *testing.T) {
	session := claudehistory.Session{SessionID: "sess-1", FilePath: "/tmp/sess-1.jsonl"}
	subagent := claudehistory.SubagentSession{AgentID: "agent-1", FilePath: "/tmp/agent-1.jsonl"}

	sessionKey := previewCacheKey(&session, nil)
	subagentKey := previewCacheKey(&session, &subagent)

	if sessionKey == "" || subagentKey == "" {
		t.Fatalf("expected non-empty cache keys")
	}
	if sessionKey == subagentKey {
		t.Fatalf("expected different cache keys, got %q", sessionKey)
	}
	if !strings.HasPrefix(subagentKey, "subagent:") {
		t.Fatalf("expected subagent cache key, got %q", subagentKey)
	}
}

func TestPreviewCacheKeyFallsBackToFilePath(t *testing.T) {
	session := claudehistory.Session{FilePath: "/tmp/sess-1.jsonl"}
	key := previewCacheKey(&session, nil)
	if key == "" || !strings.HasPrefix(key, "session:") {
		t.Fatalf("expected session cache key from file path, got %q", key)
	}

	subagent := claudehistory.SubagentSession{FilePath: "/tmp/agent-1.jsonl"}
	subKey := previewCacheKey(&session, &subagent)
	if subKey == "" || !strings.HasPrefix(subKey, "subagent:") {
		t.Fatalf("expected subagent cache key from file path, got %q", subKey)
	}
}

func TestHandleKeyEnterOpensParentForSubagent(t *testing.T) {
	screen := newTestScreen(t, 120, 40)
	now := time.Now()
	project := claudehistory.Project{
		Key:  "one",
		Path: "/tmp/one",
		Sessions: []claudehistory.Session{{
			SessionID:  "sess-1",
			ModifiedAt: now,
			Subagents:  []claudehistory.SubagentSession{{AgentID: "agent-1", ModifiedAt: now}},
		}},
	}
	state := newTestState([]claudehistory.Project{project})
	state.focus = "sessions"
	state.lastListFocus = "sessions"
	state.expandedSessions["sess-1"] = true
	state.sessionState.selected = 2

	selection, err := handleKey(context.Background(), screen, state, Options{}, tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if err != nil {
		t.Fatalf("handleKey error: %v", err)
	}
	if selection == nil || selection.Session.SessionID != "sess-1" {
		t.Fatalf("expected parent session sess-1, got %#v", selection)
	}
}

func TestBuildPreviewLinesForSubagent(t *testing.T) {
	state := newTestState(nil)
	project := claudehistory.Project{Path: "/tmp/project"}
	session := &claudehistory.Session{SessionID: "sess-1"}
	subagent := &claudehistory.SubagentSession{AgentID: "agent-1", ParentSessionID: "sess-1"}

	lines := buildPreviewLines(project, session, subagent, false, state, "preview", Options{})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Subagent:") {
		t.Fatalf("expected subagent header, got %q", joined)
	}
	if !strings.Contains(joined, "Parent: sess-1") {
		t.Fatalf("expected parent id, got %q", joined)
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
