package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/update"
)

type UpdateRequested struct{}

func (UpdateRequested) Error() string { return "update requested" }

type ProxyToggleRequested struct {
	Enable        bool
	RequireConfig bool
}

func (ProxyToggleRequested) Error() string { return "proxy toggle requested" }

var errQuit = errors.New("quit")

type Selection struct {
	Project  claudehistory.Project
	Session  claudehistory.Session
	Cwd      string
	UseProxy bool
}

type Options struct {
	LoadProjects    func(context.Context) ([]claudehistory.Project, error)
	Version         string
	CheckUpdate     func(context.Context) update.Status
	PreviewMessages int
	ProxyEnabled    bool
	ProxyConfigured bool
	DefaultCwd      string
}

type uiEvent struct {
	when time.Time
	kind string
}

func (e *uiEvent) When() time.Time { return e.when }

type previewEvent struct {
	sessionID string
	text      string
	err       error
}

type updateEvent struct {
	checking bool
	status   *update.Status
}

type rect struct {
	y int
	x int
	h int
	w int
}

type layout struct {
	projects rect
	sessions rect
	preview  rect
	mode     string
}

type listState struct {
	selected int
	scroll   int
}

type previewState struct {
	scroll int
}

type projectItem struct {
	label         string
	project       claudehistory.Project
	isCurrent     bool
	alwaysVisible bool
}

type sessionItem struct {
	label         string
	session       claudehistory.Session
	isNew         bool
	alwaysVisible bool
}

type uiState struct {
	projects       []claudehistory.Project
	loadError      error
	focus          string
	lastListFocus  string
	inputMode      string
	inputBuffer    string
	projectFilter  string
	sessionFilter  string
	projectState   listState
	sessionState   listState
	previewState   previewState
	updateStatus   *update.Status
	updateChecking bool

	proxyEnabled    bool
	proxyConfigured bool

	previewCache     map[string]string
	previewError     map[string]string
	previewLoading   map[string]bool
	previewSearch    string
	previewSearchBuf string
	previewMatches   []int
	previewMatchIdx  int
	previewSearchKey string
}

func SelectSession(ctx context.Context, opts Options) (*Selection, error) {
	if opts.LoadProjects == nil {
		return nil, errors.New("LoadProjects is required")
	}

	projects, err := opts.LoadProjects(ctx)
	state := &uiState{
		projects:        projects,
		loadError:       err,
		focus:           "projects",
		lastListFocus:   "projects",
		proxyEnabled:    opts.ProxyEnabled,
		proxyConfigured: opts.ProxyConfigured,
		previewCache:    map[string]string{},
		previewError:    map[string]string{},
		previewLoading:  map[string]bool{},
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := screen.Init(); err != nil {
		return nil, err
	}
	defer screen.Fini()

	done := make(chan struct{})
	defer close(done)

	updateCh := make(chan updateEvent, 2)
	if opts.CheckUpdate != nil {
		state.updateChecking = true
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case updateCh <- updateEvent{checking: true}:
				default:
				}
				screen.PostEvent(&uiEvent{when: time.Now(), kind: "update"})

				st := opts.CheckUpdate(ctx)
				ev := updateEvent{checking: false, status: &st}
				select {
				case updateCh <- ev:
				default:
					<-updateCh
					updateCh <- ev
				}
				screen.PostEvent(&uiEvent{when: time.Now(), kind: "update"})

				select {
				case <-ticker.C:
				case <-done:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	previewCh := make(chan previewEvent, 8)

	go func() {
		<-ctx.Done()
		screen.PostEvent(&uiEvent{when: time.Now(), kind: "quit"})
	}()

	for {
		if err := draw(screen, state, opts, previewCh); err != nil {
			return nil, err
		}
		ev := screen.PollEvent()

		switch tev := ev.(type) {
		case *uiEvent:
			switch tev.kind {
			case "quit":
				return nil, ctx.Err()
			case "update":
				for {
					select {
					case ev := <-updateCh:
						if ev.checking {
							state.updateChecking = true
						}
						if ev.status != nil {
							state.updateStatus = ev.status
							state.updateChecking = false
						}
					default:
						goto nextEvent
					}
				}
			case "preview":
				for {
					select {
					case ev := <-previewCh:
						if ev.sessionID != "" {
							if ev.err != nil {
								state.previewError[ev.sessionID] = ev.err.Error()
							} else {
								state.previewCache[ev.sessionID] = ev.text
								delete(state.previewError, ev.sessionID)
							}
							state.previewLoading[ev.sessionID] = false
						}
					default:
						goto nextEvent
					}
				}
			}
		nextEvent:
			continue
		case *tcell.EventResize:
			screen.Sync()
			continue
		case *tcell.EventKey:
			selection, err := handleKey(ctx, screen, state, opts, tev)
			if err != nil {
				if errors.Is(err, errQuit) {
					return nil, nil
				}
				return nil, err
			}
			if selection != nil {
				return selection, nil
			}
		}
	}
}

func handleKey(
	ctx context.Context,
	screen tcell.Screen,
	state *uiState,
	opts Options,
	ev *tcell.EventKey,
) (*Selection, error) {
	if state.inputMode != "" {
		switch ev.Key() {
		case tcell.KeyESC:
			if state.inputMode == "preview" {
				state.previewSearchBuf = state.previewSearch
			}
			state.inputMode = ""
			state.inputBuffer = ""
			return nil, nil
		case tcell.KeyEnter:
			if state.inputMode == "projects" {
				state.projectFilter = strings.TrimSpace(state.inputBuffer)
			}
			if state.inputMode == "sessions" {
				state.sessionFilter = strings.TrimSpace(state.inputBuffer)
			}
			if state.inputMode == "preview" {
				state.previewSearch = strings.TrimSpace(state.previewSearchBuf)
				state.previewSearchBuf = state.previewSearch
				state.previewMatchIdx = 0
				state.previewSearchKey = ""
			}
			state.inputMode = ""
			state.inputBuffer = ""
			return nil, nil
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(state.inputBuffer) > 0 {
				state.inputBuffer = state.inputBuffer[:len(state.inputBuffer)-1]
			}
			if state.inputMode == "preview" && len(state.previewSearchBuf) > 0 {
				state.previewSearchBuf = state.previewSearchBuf[:len(state.previewSearchBuf)-1]
			}
			return nil, nil
		case tcell.KeyRune:
			ch := ev.Rune()
			if ch >= 32 && ch <= 126 {
				state.inputBuffer += string(ch)
				if state.inputMode == "preview" {
					state.previewSearchBuf += string(ch)
				}
			}
			return nil, nil
		default:
			return nil, nil
		}
	}

	switch ev.Key() {
	case tcell.KeyCtrlU:
		if state.updateStatus != nil && state.updateStatus.Supported && state.updateStatus.UpdateAvailable {
			return nil, UpdateRequested{}
		}
		return nil, nil
	case tcell.KeyCtrlP:
		enable := !state.proxyEnabled
		if enable && !state.proxyConfigured {
			return nil, ProxyToggleRequested{Enable: true, RequireConfig: true}
		}
		return nil, ProxyToggleRequested{Enable: enable}
	case tcell.KeyCtrlR:
		refreshState(ctx, state, opts)
		return nil, nil
	case tcell.KeyCtrlC:
		return nil, errQuit
	case tcell.KeyESC:
		return nil, errQuit
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', 'Q':
			return nil, errQuit
		case 'r', 'R':
			refreshState(ctx, state, opts)
			return nil, nil
		case '/':
			if state.focus == "projects" {
				state.inputMode = "projects"
				state.inputBuffer = state.projectFilter
			} else if state.focus == "sessions" {
				state.inputMode = "sessions"
				state.inputBuffer = state.sessionFilter
			} else if state.focus == "preview" {
				state.inputMode = "preview"
				state.previewSearchBuf = state.previewSearch
			}
			return nil, nil
		case 'h', 'H':
			if state.focus == "preview" {
				state.focus = state.lastListFocus
			} else {
				state.focus = "projects"
				state.lastListFocus = "projects"
			}
			return nil, nil
		case 'l', 'L':
			if state.focus == "projects" {
				state.focus = "sessions"
				state.lastListFocus = "sessions"
			} else if state.focus == "sessions" {
				state.focus = "preview"
			} else {
				state.focus = state.lastListFocus
			}
			return nil, nil
		case 'n', 'N':
			if state.focus == "preview" && len(state.previewMatches) > 0 {
				layoutMode := computeLayout(screen)
				if ev.Rune() == 'n' {
					state.previewMatchIdx = (state.previewMatchIdx + 1) % len(state.previewMatches)
				} else {
					state.previewMatchIdx = (state.previewMatchIdx - 1 + len(state.previewMatches)) % len(state.previewMatches)
				}
				matchLine := state.previewMatches[state.previewMatchIdx]
				state.previewState.scroll = previewScrollToMatch(matchLine, max(0, layoutMode.preview.h-2))
				return nil, nil
			}
		}
	case tcell.KeyTab:
		if state.focus == "projects" {
			state.focus = "sessions"
			state.lastListFocus = "sessions"
		} else if state.focus == "sessions" {
			state.focus = "preview"
		} else {
			state.focus = "projects"
			state.lastListFocus = "projects"
		}
		return nil, nil
	case tcell.KeyLeft:
		if state.focus == "preview" {
			state.focus = state.lastListFocus
		} else {
			state.focus = "projects"
			state.lastListFocus = "projects"
		}
		return nil, nil
	case tcell.KeyRight:
		if state.focus == "projects" {
			state.focus = "sessions"
			state.lastListFocus = "sessions"
		} else if state.focus == "sessions" {
			state.focus = "preview"
		} else {
			state.focus = state.lastListFocus
		}
		return nil, nil
	}

	layoutMode := computeLayout(screen)
	listFocus := state.focus
	if layoutMode.mode == "1col" && state.focus == "preview" {
		listFocus = state.lastListFocus
	}

	projects := buildProjectItems(state.projects, opts.DefaultCwd)
	filteredProjects := filterProjects(projects, state.projectFilter)
	state.projectState.clamp(len(filteredProjects))
	selectedProject := selectedProject(filteredProjects, state.projectState.selected)

	sessions := buildSessionItems(selectedProject)
	filteredSessions := filterSessions(sessions, state.sessionFilter)
	state.sessionState.clamp(len(filteredSessions))
	selectedSession, selectedIsNew := selectedSessionItem(filteredSessions, state.sessionState.selected)

	if ev.Key() == tcell.KeyCtrlN {
		if cwd := newSessionCwd(selectedProject, opts.DefaultCwd); cwd != "" {
			return &Selection{Project: selectedProject, Cwd: cwd, UseProxy: state.proxyEnabled}, nil
		}
		return nil, nil
	}

	enterPressed := ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyCtrlJ || ev.Key() == tcell.KeyCtrlM
	if ev.Key() == tcell.KeyRune {
		if ev.Rune() == '\n' || ev.Rune() == '\r' {
			enterPressed = true
		}
	}
	if enterPressed {
		if selectedSession != nil {
			return &Selection{Project: selectedProject, Session: *selectedSession, UseProxy: state.proxyEnabled}, nil
		}
		if selectedIsNew {
			if cwd := newSessionCwd(selectedProject, opts.DefaultCwd); cwd != "" {
				return &Selection{Project: selectedProject, Cwd: cwd, UseProxy: state.proxyEnabled}, nil
			}
		}
	}

	if state.focus == "preview" && isPreviewNavKey(ev) {
		previewText := previewTextForSession(state, selectedSession)
		lines := buildPreviewLines(selectedProject, selectedSession, selectedIsNew, state, previewText, opts)
		lines = buildWrappedLines(lines, max(0, layoutMode.preview.w-2))
		applyPreviewNavigation(&state.previewState, len(lines), max(0, layoutMode.preview.h-2), ev)
		return nil, nil
	}

	if listFocus == "projects" {
		prev := state.projectState.selected
		applyListNavigation(&state.projectState, len(filteredProjects), layoutMode.projects.h-2, ev)
		if state.projectState.selected != prev {
			state.sessionState.selected = 0
			state.sessionState.scroll = 0
			state.previewState.scroll = 0
		}
		return nil, nil
	}

	if listFocus == "sessions" {
		prev := state.sessionState.selected
		applyListNavigation(&state.sessionState, len(filteredSessions), layoutMode.sessions.h-2, ev)
		if state.sessionState.selected != prev {
			state.previewState.scroll = 0
		}
		return nil, nil
	}

	if state.focus == "preview" {
		previewText := previewTextForSession(state, selectedSession)
		lines := buildPreviewLines(selectedProject, selectedSession, selectedIsNew, state, previewText, opts)
		lines = buildWrappedLines(lines, max(0, layoutMode.preview.w-2))
		applyPreviewNavigation(&state.previewState, len(lines), max(0, layoutMode.preview.h-2), ev)
		return nil, nil
	}

	return nil, nil
}

func refreshState(ctx context.Context, state *uiState, opts Options) {
	projects, err := opts.LoadProjects(ctx)
	if err != nil {
		state.loadError = err
		return
	}
	state.loadError = nil
	state.projects = projects
	state.projectState = listState{}
	state.sessionState = listState{}
	state.previewState = previewState{}
}

func computeLayout(screen tcell.Screen) layout {
	maxX, maxY := screen.Size()
	usableH := max(1, maxY-1)

	if maxX >= 120 && usableH >= 10 {
		leftW := min(40, max(24, maxX/4))
		midW := min(60, max(32, maxX/3))
		rightW := max(20, maxX-leftW-midW)
		return layout{
			projects: rect{y: 0, x: 0, h: usableH, w: leftW},
			sessions: rect{y: 0, x: leftW, h: usableH, w: midW},
			preview:  rect{y: 0, x: leftW + midW, h: usableH, w: rightW},
			mode:     "3col",
		}
	}

	if maxX >= 80 && usableH >= 10 {
		leftW := min(40, max(24, maxX/3))
		rightW := maxX - leftW
		convH := max(6, int(float64(usableH)*0.6))
		prevH := max(3, usableH-convH)
		return layout{
			projects: rect{y: 0, x: 0, h: usableH, w: leftW},
			sessions: rect{y: 0, x: leftW, h: convH, w: rightW},
			preview:  rect{y: convH, x: leftW, h: prevH, w: rightW},
			mode:     "2col",
		}
	}

	listH := max(1, int(float64(usableH)*0.6))
	if usableH > 1 {
		listH = clamp(listH, 1, usableH-1)
	}
	return layout{
		projects: rect{y: 0, x: 0, h: listH, w: maxX},
		sessions: rect{y: 0, x: 0, h: listH, w: maxX},
		preview:  rect{y: listH, x: 0, h: usableH - listH, w: maxX},
		mode:     "1col",
	}
}

func draw(screen tcell.Screen, state *uiState, opts Options, previewCh chan<- previewEvent) error {
	screen.Clear()

	layoutMode := computeLayout(screen)

	projects := buildProjectItems(state.projects, opts.DefaultCwd)
	filteredProjects := filterProjects(projects, state.projectFilter)
	state.projectState.clamp(len(filteredProjects))
	state.projectState.ensureVisible(layoutMode.projects.h-2, len(filteredProjects))

	selectedProject := selectedProject(filteredProjects, state.projectState.selected)

	sessions := buildSessionItems(selectedProject)
	filteredSessions := filterSessions(sessions, state.sessionFilter)
	state.sessionState.clamp(len(filteredSessions))
	state.sessionState.ensureVisible(layoutMode.sessions.h-2, len(filteredSessions))

	selectedSession, selectedIsNew := selectedSessionItem(filteredSessions, state.sessionState.selected)

	listFocus := state.focus
	if layoutMode.mode == "1col" && state.focus == "preview" {
		listFocus = state.lastListFocus
	}

	projectFilter := state.projectFilter
	sessionFilter := state.sessionFilter
	if state.inputMode == "projects" {
		projectFilter = state.inputBuffer
	}
	if state.inputMode == "sessions" {
		sessionFilter = state.inputBuffer
	}

	if layoutMode.mode == "1col" {
		title := "Projects"
		listFilter := projectFilter
		if listFocus == "sessions" {
			title = "Sessions"
			listFilter = sessionFilter
		}
		drawBox(screen, layoutMode.projects, title, listFocus != "preview", listFilter)
		drawList(
			screen,
			layoutMode.projects,
			renderProjectRows(filteredProjects, listFocus == "projects", state.projectState, layoutMode.projects.h-2),
		)
		if listFocus == "sessions" {
			drawList(
				screen,
				layoutMode.projects,
				renderSessionRows(filteredSessions, listFocus == "sessions", state.sessionState, layoutMode.projects.h-2),
			)
		}
	} else {
		drawBox(screen, layoutMode.projects, "Projects", state.focus == "projects", projectFilter)
		drawList(
			screen,
			layoutMode.projects,
			renderProjectRows(filteredProjects, state.focus == "projects", state.projectState, layoutMode.projects.h-2),
		)

		drawBox(screen, layoutMode.sessions, "Sessions", state.focus == "sessions", sessionFilter)
		drawList(
			screen,
			layoutMode.sessions,
			renderSessionRows(filteredSessions, state.focus == "sessions", state.sessionState, layoutMode.sessions.h-2),
		)
	}

	if selectedSession != nil {
		ensurePreview(screen, state, opts, *selectedSession, previewCh)
	}

	previewText := previewTextForSession(state, selectedSession)

	previewFilter := ""
	if state.inputMode == "preview" {
		previewFilter = state.previewSearchBuf
	} else if state.previewSearch != "" {
		previewFilter = state.previewSearch
	}
	drawBox(screen, layoutMode.preview, "Preview", state.focus == "preview", previewFilter)
	lines := buildPreviewLines(selectedProject, selectedSession, selectedIsNew, state, previewText, opts)
	lines = buildWrappedLines(lines, max(0, layoutMode.preview.w-2))
	viewH := max(0, layoutMode.preview.h-2)
	state.previewState.scroll = clamp(state.previewState.scroll, 0, max(0, len(lines)-viewH))

	previewKey := fmt.Sprintf("%s|%d|%s|%s", sessionID(selectedSession), layoutMode.preview.w, previewText, state.previewSearch)
	if previewKey != state.previewSearchKey {
		state.previewSearchKey = previewKey
		if state.previewSearch != "" {
			state.previewMatches = previewFindMatches(lines, state.previewSearch)
			state.previewMatchIdx = clamp(state.previewMatchIdx, 0, max(0, len(state.previewMatches)-1))
			if len(state.previewMatches) > 0 {
				state.previewMatchIdx = 0
				state.previewState.scroll = previewScrollToMatch(state.previewMatches[0], viewH)
			}
		} else {
			state.previewMatches = nil
			state.previewMatchIdx = 0
		}
	}

	lineAttrs := map[int]tcell.Style{}
	if len(state.previewMatches) > 0 {
		matchLine := state.previewMatches[state.previewMatchIdx]
		lineAttrs[matchLine] = tcell.StyleDefault.Reverse(true)
	}

	drawPreview(screen, layoutMode.preview, lines, state.previewState.scroll, lineAttrs)

	proxyLabel := "Proxy: off"
	if state.proxyEnabled {
		proxyLabel = "Proxy: on"
	}
	newSessionPath := newSessionCwd(selectedProject, opts.DefaultCwd)
	openLabel := "Enter: open"
	newHint := ""
	if newSessionPath != "" {
		newHint = "  Ctrl+N: new"
		if selectedIsNew {
			openLabel = "Enter: new"
		}
	}
	status := "Tab/Left/Right: switch  /: search  " + openLabel + "  r: refresh" + newHint + "  " + proxyLabel + "  Ctrl+P: toggle  q: quit"
	if state.inputMode != "" {
		status = "Type to search. Enter: apply  Esc: cancel  " + proxyLabel + "  Ctrl+P: toggle"
	} else if state.focus == "preview" {
		status = "Up/Down PgUp/PgDn: scroll  /: search  " + openLabel + "  Tab/Left/Right: switch" + newHint + "  " + proxyLabel + "  Ctrl+P: toggle  q: quit"
		if state.previewSearch != "" && len(state.previewMatches) > 0 {
			status = status + "  n/N: next/prev"
		}
	}
	if state.loadError != nil {
		if len(state.projects) == 0 && newSessionPath != "" {
			status = "No history found. " + openLabel + "  " + proxyLabel + "  Ctrl+P: toggle  q: quit"
		} else {
			status = fmt.Sprintf("Load error: %v", state.loadError)
		}
	}
	if state.loadError == nil && state.updateStatus != nil && !state.updateStatus.Supported && state.updateStatus.Error != "" && state.inputMode == "" {
		status = fmt.Sprintf("Update check failed: %s", state.updateStatus.Error)
	}

	updateRight := versionLabel(opts.Version)
	updateBold := false
	if state.updateStatus == nil && state.updateChecking {
		updateRight = updateRight + " checking"
	} else if state.updateStatus != nil {
		if state.updateStatus.Supported {
			if state.updateStatus.UpdateAvailable {
				updateRight = updateRight + "  Ctrl+U upgrade"
				updateBold = true
			} else {
				updateRight = updateRight + " latest"
			}
		} else if state.updateStatus.Error != "" {
			updateRight = updateRight + " update failed"
			updateBold = true
		}
	}

	drawStatus(screen, status, updateRight, updateBold)
	screen.Show()
	return nil
}

func ensurePreview(screen tcell.Screen, state *uiState, opts Options, session claudehistory.Session, previewCh chan<- previewEvent) {
	if session.SessionID == "" || session.FilePath == "" {
		return
	}
	if _, ok := state.previewCache[session.SessionID]; ok {
		return
	}
	if state.previewLoading[session.SessionID] {
		return
	}
	state.previewLoading[session.SessionID] = true

	maxMessages := opts.PreviewMessages
	if maxMessages <= 0 {
		maxMessages = 20
	}

	go func(sess claudehistory.Session, maxMsgs int) {
		msgs, err := claudehistory.ReadSessionMessages(sess.FilePath, maxMsgs)
		text := ""
		if err == nil {
			text = claudehistory.FormatMessages(msgs, 400)
		}
		previewCh <- previewEvent{sessionID: sess.SessionID, text: text, err: err}
		screen.PostEvent(&uiEvent{when: time.Now(), kind: "preview"})
	}(session, maxMessages)
}

func buildProjectItems(projects []claudehistory.Project, defaultCwd string) []projectItem {
	items := make([]projectItem, 0, len(projects)+1)
	currentPath := strings.TrimSpace(defaultCwd)
	currentResolved := normalizePathForCompare(currentPath)
	currentIdx := -1

	for _, project := range projects {
		label := project.Path
		if label == "" {
			label = project.Key
		}
		if label == "" {
			label = "Unknown project"
		}
		if len(project.Sessions) > 0 {
			label = fmt.Sprintf("%s  (%d)", label, len(project.Sessions))
		}
		isCurrent := currentResolved != "" && isSamePath(project.Path, currentResolved)
		if isCurrent {
			label = "[current] " + label
		}
		items = append(items, projectItem{
			label:         label,
			project:       project,
			isCurrent:     isCurrent,
			alwaysVisible: isCurrent,
		})
		if isCurrent {
			currentIdx = len(items) - 1
		}
	}

	if currentResolved != "" {
		if currentIdx == -1 {
			project := claudehistory.Project{Path: currentPath}
			label := "[current] " + currentPath
			items = append([]projectItem{{
				label:         label,
				project:       project,
				isCurrent:     true,
				alwaysVisible: true,
			}}, items...)
		} else if currentIdx != 0 {
			cur := items[currentIdx]
			items = append([]projectItem{cur}, append(items[:currentIdx], items[currentIdx+1:]...)...)
		}
	}

	return items
}

func buildSessionItems(project claudehistory.Project) []sessionItem {
	items := []sessionItem{{
		label:         "(New Agent)",
		isNew:         true,
		alwaysVisible: true,
	}}
	for _, session := range project.Sessions {
		title := session.DisplayTitle()
		ts := "unknown"
		if !session.ModifiedAt.IsZero() {
			ts = session.ModifiedAt.Format("2006-01-02 15:04")
		}
		label := fmt.Sprintf("%s  (%s)", title, ts)
		items = append(items, sessionItem{label: label, session: session})
	}
	return items
}

func selectedProject(items []projectItem, idx int) claudehistory.Project {
	if idx < 0 || idx >= len(items) {
		return claudehistory.Project{}
	}
	return items[idx].project
}

func selectedSessionItem(items []sessionItem, idx int) (*claudehistory.Session, bool) {
	if idx < 0 || idx >= len(items) {
		return nil, false
	}
	item := items[idx]
	if item.isNew {
		return nil, true
	}
	return &item.session, false
}

func normalizePathForCompare(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func isSamePath(path string, currentResolved string) bool {
	if strings.TrimSpace(path) == "" || currentResolved == "" {
		return false
	}
	return normalizePathForCompare(path) == currentResolved
}

func newSessionCwd(project claudehistory.Project, defaultCwd string) string {
	if strings.TrimSpace(project.Path) != "" {
		return strings.TrimSpace(project.Path)
	}
	if strings.TrimSpace(defaultCwd) != "" {
		return strings.TrimSpace(defaultCwd)
	}
	return ""
}

func buildPreviewLines(
	project claudehistory.Project,
	session *claudehistory.Session,
	selectedIsNew bool,
	state *uiState,
	previewText string,
	opts Options,
) []string {
	if state.loadError != nil {
		return []string{fmt.Sprintf("Load error: %v", state.loadError)}
	}
	if project.Path == "" && len(state.projects) == 0 {
		return []string{"No Claude Code sessions found.", "Run Claude Code to create a session first."}
	}

	lines := []string{}
	if project.Path != "" {
		lines = append(lines, "Project:")
		lines = append(lines, "  "+project.Path)
	}
	if selectedIsNew {
		cwd := newSessionCwd(project, opts.DefaultCwd)
		if cwd != "" {
			lines = append(lines, "")
			lines = append(lines, "Start a new Claude session in:")
			lines = append(lines, "  "+cwd)
		} else {
			lines = append(lines, "")
			lines = append(lines, "Start a new Claude session in the current directory.")
		}
		return lines
	}
	if session == nil {
		lines = append(lines, "")
		lines = append(lines, "Select a session to preview.")
		return lines
	}

	lines = append(lines, "")
	lines = append(lines, "Session:")
	lines = append(lines, "  ID: "+session.SessionID)
	if session.Summary != "" {
		lines = append(lines, "  Summary: "+session.Summary)
	}
	if session.FirstPrompt != "" {
		lines = append(lines, "  First prompt: "+session.FirstPrompt)
	}
	if session.MessageCount > 0 {
		lines = append(lines, fmt.Sprintf("  Messages: %d", session.MessageCount))
	}
	if !session.CreatedAt.IsZero() {
		lines = append(lines, "  Created: "+session.CreatedAt.Format(time.RFC3339))
	}
	if !session.ModifiedAt.IsZero() {
		lines = append(lines, "  Modified: "+session.ModifiedAt.Format(time.RFC3339))
	}

	if previewText != "" {
		lines = append(lines, "")
		lines = append(lines, "Preview:")
		lines = append(lines, previewText)
	}
	return lines
}

func renderProjectRows(items []projectItem, focused bool, state listState, viewH int) []row {
	rows := make([]row, 0, min(len(items), viewH))
	start := clamp(state.scroll, 0, max(0, len(items)))
	end := min(len(items), start+max(0, viewH))
	for i := start; i < end; i++ {
		rows = append(rows, row{label: items[i].label, bold: items[i].isCurrent})
	}
	return applySelection(rows, focused, listState{selected: state.selected - start})
}

func renderSessionRows(items []sessionItem, focused bool, state listState, viewH int) []row {
	rows := make([]row, 0, min(len(items), viewH))
	start := clamp(state.scroll, 0, max(0, len(items)))
	end := min(len(items), start+max(0, viewH))
	for i := start; i < end; i++ {
		rows = append(rows, row{label: items[i].label})
	}
	return applySelection(rows, focused, listState{selected: state.selected - start})
}

type row struct {
	label    string
	dim      bool
	bold     bool
	selected bool
	focused  bool
}

func previewTextForSession(state *uiState, session *claudehistory.Session) string {
	if session == nil {
		return ""
	}
	if errMsg, ok := state.previewError[session.SessionID]; ok && errMsg != "" {
		return "Preview failed: " + errMsg
	}
	if text, ok := state.previewCache[session.SessionID]; ok && text != "" {
		return text
	}
	if state.previewLoading[session.SessionID] {
		return "Loading preview…"
	}
	return ""
}

func sessionID(session *claudehistory.Session) string {
	if session == nil {
		return ""
	}
	return session.SessionID
}

func applySelection(rows []row, focused bool, state listState) []row {
	if len(rows) == 0 {
		return rows
	}
	state.clamp(len(rows))
	rows[state.selected].selected = true
	rows[state.selected].focused = focused
	rows[state.selected].dim = false
	return rows
}

func filterProjects(items []projectItem, needle string) []projectItem {
	if strings.TrimSpace(needle) == "" {
		return items
	}
	n := strings.ToLower(needle)
	out := make([]projectItem, 0, len(items))
	for _, it := range items {
		if it.alwaysVisible {
			out = append(out, it)
			continue
		}
		if strings.Contains(strings.ToLower(it.label), n) {
			out = append(out, it)
		}
	}
	return out
}

func previewFindMatches(lines []string, needle string) []int {
	n := strings.ToLower(strings.TrimSpace(needle))
	if n == "" {
		return nil
	}
	out := make([]int, 0, len(lines))
	for i, ln := range lines {
		if strings.Contains(strings.ToLower(ln), n) {
			out = append(out, i)
		}
	}
	return out
}

func previewScrollToMatch(matchLine int, viewH int) int {
	vh := max(1, viewH)
	return max(0, matchLine-(vh/2))
}

func isPreviewNavKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j', 'J', 'k', 'K', 'g', 'G':
			return true
		}
	}
	return false
}

func filterSessions(items []sessionItem, needle string) []sessionItem {
	if strings.TrimSpace(needle) == "" {
		return items
	}
	n := strings.ToLower(needle)
	out := make([]sessionItem, 0, len(items))
	for _, it := range items {
		if it.alwaysVisible {
			out = append(out, it)
			continue
		}
		if strings.Contains(strings.ToLower(it.label), n) {
			out = append(out, it)
		}
	}
	return out
}

func applyListNavigation(state *listState, nItems int, viewH int, ev *tcell.EventKey) {
	if nItems <= 0 {
		state.selected = 0
		state.scroll = 0
		return
	}
	switch ev.Key() {
	case tcell.KeyUp:
		state.selected = clamp(state.selected-1, 0, nItems-1)
	case tcell.KeyDown:
		state.selected = clamp(state.selected+1, 0, nItems-1)
	case tcell.KeyPgUp:
		state.selected = clamp(state.selected-max(1, viewH), 0, nItems-1)
	case tcell.KeyPgDn:
		state.selected = clamp(state.selected+max(1, viewH), 0, nItems-1)
	case tcell.KeyHome:
		state.selected = 0
	case tcell.KeyEnd:
		state.selected = nItems - 1
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k', 'K':
			state.selected = clamp(state.selected-1, 0, nItems-1)
		case 'j', 'J':
			state.selected = clamp(state.selected+1, 0, nItems-1)
		case 'g':
			state.selected = 0
		case 'G':
			state.selected = nItems - 1
		default:
			return
		}
	default:
		return
	}
	state.ensureVisible(viewH, nItems)
}

func applyPreviewNavigation(state *previewState, nLines int, viewH int, ev *tcell.EventKey) {
	if nLines <= 0 || viewH <= 0 {
		state.scroll = 0
		return
	}
	switch ev.Key() {
	case tcell.KeyUp:
		state.scroll = clamp(state.scroll-1, 0, max(0, nLines-viewH))
	case tcell.KeyDown:
		state.scroll = clamp(state.scroll+1, 0, max(0, nLines-viewH))
	case tcell.KeyPgUp:
		state.scroll = clamp(state.scroll-max(1, viewH), 0, max(0, nLines-viewH))
	case tcell.KeyPgDn:
		state.scroll = clamp(state.scroll+max(1, viewH), 0, max(0, nLines-viewH))
	case tcell.KeyHome:
		state.scroll = 0
	case tcell.KeyEnd:
		state.scroll = max(0, nLines-viewH)
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k', 'K':
			state.scroll = clamp(state.scroll-1, 0, max(0, nLines-viewH))
		case 'j', 'J':
			state.scroll = clamp(state.scroll+1, 0, max(0, nLines-viewH))
		case 'g':
			state.scroll = 0
		case 'G':
			state.scroll = max(0, nLines-viewH)
		default:
			return
		}
	}
}

func (s *listState) clamp(nItems int) {
	if nItems <= 0 {
		s.selected = 0
		s.scroll = 0
		return
	}
	s.selected = clamp(s.selected, 0, nItems-1)
	s.scroll = clamp(s.scroll, 0, max(0, nItems-1))
}

func (s *listState) ensureVisible(viewH int, nItems int) {
	if nItems <= 0 || viewH <= 0 {
		s.scroll = 0
		return
	}
	maxScroll := max(0, nItems-viewH)
	if s.selected < s.scroll {
		s.scroll = s.selected
	} else if s.selected >= s.scroll+viewH {
		s.scroll = s.selected - viewH + 1
	}
	s.scroll = clamp(s.scroll, 0, maxScroll)
}

func drawBox(screen tcell.Screen, r rect, title string, focused bool, filter string) {
	if r.w <= 0 || r.h <= 0 {
		return
	}
	borderStyle := tcell.StyleDefault
	if focused {
		borderStyle = borderStyle.Bold(true)
	} else {
		borderStyle = borderStyle.Dim(true)
	}
	h := tcell.RuneHLine
	v := tcell.RuneVLine
	ul := tcell.RuneULCorner
	ur := tcell.RuneURCorner
	ll := tcell.RuneLLCorner
	lr := tcell.RuneLRCorner
	for x := r.x + 1; x < r.x+r.w-1; x++ {
		screen.SetContent(x, r.y, h, nil, borderStyle)
		screen.SetContent(x, r.y+r.h-1, h, nil, borderStyle)
	}
	for y := r.y + 1; y < r.y+r.h-1; y++ {
		screen.SetContent(r.x, y, v, nil, borderStyle)
		screen.SetContent(r.x+r.w-1, y, v, nil, borderStyle)
	}
	screen.SetContent(r.x, r.y, ul, nil, borderStyle)
	screen.SetContent(r.x+r.w-1, r.y, ur, nil, borderStyle)
	screen.SetContent(r.x, r.y+r.h-1, ll, nil, borderStyle)
	screen.SetContent(r.x+r.w-1, r.y+r.h-1, lr, nil, borderStyle)

	titleStyle := tcell.StyleDefault.Reverse(true)
	if focused {
		titleStyle = titleStyle.Bold(true)
		title = "> " + title + " <"
	} else {
		title = " " + title + " "
	}
	maxTitleWidth := max(0, r.w-2)
	title = truncate(title, maxTitleWidth)
	titleWidth := displayWidth(title)
	titleX := r.x + 1
	if maxTitleWidth > 0 {
		titleX = r.x + 1 + max(0, (maxTitleWidth-titleWidth)/2)
	}
	writeText(screen, titleX, r.y, title, titleStyle)

	if filter != "" && r.h >= 2 {
		hint := "/" + filter
		writeText(screen, r.x+1, r.y+r.h-1, truncate(hint, r.w-2), borderStyle.Dim(true))
	}
}

func drawList(screen tcell.Screen, r rect, rows []row) {
	if r.h < 3 || r.w < 4 {
		return
	}
	innerH := r.h - 2
	innerW := r.w - 2
	for i := 0; i < innerH; i++ {
		y := r.y + 1 + i
		if i >= len(rows) {
			writeText(screen, r.x+1, y, padRight("", innerW), tcell.StyleDefault)
			continue
		}
		row := rows[i]
		style := tcell.StyleDefault
		if row.bold {
			style = style.Bold(true)
		}
		if row.selected {
			style = style.Reverse(true)
			if row.focused {
				style = style.Bold(true)
			} else {
				style = style.Dim(true)
			}
		} else if row.dim {
			style = style.Dim(true)
		}
		writeText(screen, r.x+1, y, padRight(truncate(row.label, innerW), innerW), style)
	}
}

func drawPreview(screen tcell.Screen, r rect, lines []string, scroll int, lineAttrs map[int]tcell.Style) {
	if r.h < 3 || r.w < 4 {
		return
	}
	innerH := r.h - 2
	innerW := r.w - 2
	scroll = clamp(scroll, 0, max(0, len(lines)-innerH))
	for i := 0; i < innerH; i++ {
		y := r.y + 1 + i
		idx := scroll + i
		if idx >= len(lines) {
			writeText(screen, r.x+1, y, padRight("", innerW), tcell.StyleDefault)
			continue
		}
		line := padRight(truncate(lines[idx], innerW), innerW)
		style := tcell.StyleDefault
		if attr, ok := lineAttrs[idx]; ok {
			style = attr
		}
		writeText(screen, r.x+1, y, line, style)
	}
}

func drawStatus(screen tcell.Screen, left string, right string, rightBold bool) {
	w, h := screen.Size()
	if h <= 0 {
		return
	}
	y := h - 1
	bar := padRight(truncate(left, w), w)
	writeText(screen, 0, y, bar, tcell.StyleDefault.Reverse(true))
	if right == "" {
		return
	}
	r := truncate(right, w)
	x := max(0, w-len(r))
	style := tcell.StyleDefault.Reverse(true)
	if rightBold {
		style = style.Bold(true)
	}
	writeText(screen, x, y, r, style)
}

func writeText(screen tcell.Screen, x, y int, text string, style tcell.Style) {
	offset := 0
	for _, ch := range text {
		width := runewidth.RuneWidth(ch)
		if width == 0 {
			continue
		}
		screen.SetContent(x+offset, y, ch, nil, style)
		offset += width
	}
}

func buildWrappedLines(lines []string, width int) []string {
	if width <= 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		for _, w := range wrapText(ln, width) {
			out = append(out, w)
		}
	}
	return out
}

func wrapText(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	if s == "" {
		return []string{""}
	}
	out := []string{}
	for _, ln := range strings.Split(s, "\n") {
		if ln == "" {
			out = append(out, "")
			continue
		}
		var buf strings.Builder
		curWidth := 0
		for _, ch := range ln {
			chWidth := runewidth.RuneWidth(ch)
			if chWidth == 0 {
				buf.WriteRune(ch)
				continue
			}
			if curWidth+chWidth > width {
				if curWidth == 0 {
					buf.WriteRune(ch)
					out = append(out, buf.String())
					buf.Reset()
					curWidth = 0
					continue
				}
				out = append(out, buf.String())
				buf.Reset()
				curWidth = 0
			}
			buf.WriteRune(ch)
			curWidth += chWidth
		}
		out = append(out, buf.String())
	}
	return out
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(s) <= width {
		return s
	}
	var buf strings.Builder
	curWidth := 0
	for _, ch := range s {
		chWidth := runewidth.RuneWidth(ch)
		if chWidth == 0 {
			buf.WriteRune(ch)
			continue
		}
		if curWidth+chWidth > width {
			break
		}
		buf.WriteRune(ch)
		curWidth += chWidth
	}
	return buf.String()
}

func padRight(s string, width int) string {
	if displayWidth(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-displayWidth(s))
}

func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}

func versionLabel(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		v = "dev"
	}
	if strings.EqualFold(v, "dev") {
		return v
	}
	if strings.HasPrefix(strings.ToLower(v), "v") {
		return v
	}
	return "v" + v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
