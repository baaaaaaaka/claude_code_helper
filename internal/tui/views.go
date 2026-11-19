package tui

import (
	"slices"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
)

type projectIdentity struct {
	key  string
	path string
}

func identityOf(project claudehistory.Project) projectIdentity {
	return projectIdentity{key: project.Key, path: project.Path}
}

type listViews struct {
	builtProjects    bool
	projectCwd       string
	projectItems     []projectItem
	projectFilter    string
	filteredProjects []projectItem
	builtSessions    bool
	sessionProject   projectIdentity
	sessionItems     []sessionItem
	sessionFilter    string
	filteredSessions []sessionItem
}

type previewLayout struct {
	valid    bool
	project  projectIdentity
	session  string
	isNew    bool
	text     string
	width    int
	lines    []string
	revision int
}

func (s *uiState) previewLines(project claudehistory.Project, session *claudehistory.Session, subagent *claudehistory.SubagentSession, isNew bool, opts Options, width int) []string {
	v := &s.previewLayout
	projectID := identityOf(project)
	key := previewCacheKey(session, subagent)
	text := previewTextForItem(s, session, subagent)
	if !v.valid || v.project != projectID || v.session != key || v.isNew != isNew || v.text != text || v.width != width {
		lines := buildPreviewLines(project, session, subagent, isNew, s, text, opts)
		wrapped := buildWrappedLines(lines, max(0, width))
		revision := v.revision
		if !slices.Equal(wrapped, v.lines) {
			revision++
		}
		*v = previewLayout{
			valid: true, project: projectID, session: key, isNew: isNew, text: text, width: width,
			lines: wrapped, revision: revision,
		}
	}
	return v.lines
}

func (s *uiState) projectItems(cwd string) []projectItem {
	v := &s.views
	if !v.builtProjects || v.projectCwd != cwd {
		v.builtProjects = true
		v.projectCwd = cwd
		v.projectItems = buildProjectItems(s.projects, cwd)
		v.filteredProjects = nil
		v.builtSessions = false
	}
	if v.filteredProjects == nil || v.projectFilter != s.projectFilter {
		v.projectFilter = s.projectFilter
		v.filteredProjects = filterProjects(v.projectItems, s.projectFilter)
	}
	return v.filteredProjects
}

func (s *uiState) sessionItems(project claudehistory.Project) []sessionItem {
	v := &s.views
	if identity := identityOf(project); !v.builtSessions || v.sessionProject != identity {
		v.builtSessions = true
		v.sessionProject = identity
		v.sessionItems = buildSessionItems(project, s.expandedSessions)
		for i, item := range v.sessionItems {
			if item.kind == sessionItemSubagent {
				key := previewCacheKey(nil, &item.subagent)
				if summary, ok := s.subagentDetails[subagentParentIdentity(item.parentSession)][key]; ok {
					v.sessionItems[i].subagent = summary
					v.sessionItems[i].label = subagentLabel(summary)
				}
			}
		}
		v.filteredSessions = nil
	}
	if v.filteredSessions == nil || v.sessionFilter != s.sessionFilter {
		v.sessionFilter = s.sessionFilter
		v.filteredSessions = filterSessions(v.sessionItems, s.sessionFilter)
	}
	return v.filteredSessions
}

type sessionIdentity struct {
	kind sessionItemKind
	id   string
	path string
}

func identityOfSession(item sessionItem) sessionIdentity {
	if item.kind == sessionItemSubagent {
		return sessionIdentity{kind: item.kind, id: item.subagent.AgentID, path: item.subagent.FilePath}
	}
	return sessionIdentity{kind: item.kind, id: item.session.SessionID}
}

func subagentParentIdentity(session claudehistory.Session) sessionIdentity {
	return sessionIdentity{kind: sessionItemMain, id: session.SessionID, path: session.FilePath}
}

func (s *uiState) applySubagentDetails(ev subagentLoadEvent, cwd string) {
	project := selectedProject(s.projectItems(cwd), s.projectState.selected)
	item, selected := selectedSessionItem(s.sessionItems(project), s.sessionState.selected)
	if s.subagentDetails == nil {
		s.subagentDetails = make(map[sessionIdentity]map[string]claudehistory.SubagentSession)
	}
	if _, ok := s.subagentDetails[ev.parent]; !ok {
		const cachedParents = 8
		if len(s.subagentOrder) == cachedParents {
			delete(s.subagentDetails, s.subagentOrder[0])
			s.subagentOrder = s.subagentOrder[1:]
		}
		s.subagentOrder = append(s.subagentOrder, ev.parent)
	}
	s.subagentDetails[ev.parent] = ev.summaries
	s.views.builtSessions = false
	s.previewLayout.valid = false
	if selected {
		s.keepSessionSelection(s.sessionItems(project), identityOfSession(item))
	}
}

func (s *uiState) keepSessionSelection(items []sessionItem, id sessionIdentity) bool {
	for i, item := range items {
		if identityOfSession(item) == id {
			s.sessionState.selected = i
			return true
		}
	}
	return false
}

func (s *uiState) applyProjects(ev projectLoadEvent, cwd string) {
	initial := s.loadingProjects
	s.loadingProjects = false
	s.refreshingProjects = false
	if len(s.projects) == 0 && (s.loadError != nil || ev.err != nil) {
		s.previewLayout.valid = false
	}
	s.loadError = ev.err
	if initial {
		s.setProjects(ev, cwd)
		return
	}
	if ev.err != nil || !ev.changed {
		return
	}

	projects := s.projectItems(cwd)
	oldProject := selectedProject(projects, s.projectState.selected)
	session, _ := selectedSessionItem(s.sessionItems(oldProject), s.sessionState.selected)
	oldProjectID := identityOf(oldProject)
	oldSessionID := identityOfSession(session)

	s.setProjects(ev, cwd)
	s.stopSubagentLoad()
	s.subagentParent = sessionIdentity{}
	projects = s.projectItems(cwd)
	s.projectState = listState{}
	for i, item := range projects {
		if identityOf(item.project) == oldProjectID {
			s.projectState.selected = i
			break
		}
	}
	project := selectedProject(projects, s.projectState.selected)
	s.sessionState = listState{}
	if identityOf(project) == oldProjectID && s.keepSessionSelection(s.sessionItems(project), oldSessionID) {
		return
	}
	s.previewState = previewState{}
}

func (s *uiState) setProjects(ev projectLoadEvent, cwd string) {
	s.projects = ev.projects
	s.views = listViews{builtProjects: ev.items != nil, projectCwd: cwd, projectItems: ev.items}
	s.previewLayout.valid = false
}
