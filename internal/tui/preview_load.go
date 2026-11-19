package tui

import (
	"context"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/gdamore/tcell/v2"
)

const previewCacheLimit = 64

type previewRequest struct {
	ctx   context.Context
	key   string
	path  string
	count int
}

type subagentLoadEvent struct {
	generation int
	parent     sessionIdentity
	summaries  map[string]claudehistory.SubagentSession
}

func ensurePreview(
	ctx context.Context, done <-chan struct{}, screen tcell.Screen, state *uiState, opts Options,
	session *claudehistory.Session, subagent *claudehistory.SubagentSession, results chan<- previewEvent,
) {
	key := previewCacheKey(session, subagent)
	if state.previewActive != key && state.cancelPreview != nil {
		state.cancelPreview()
		delete(state.previewLoading, state.previewActive)
		state.cancelPreview = nil
	}
	state.previewActive = key
	path := previewFilePath(session, subagent)
	if key == "" || path == "" || state.previewLoading[key] {
		return
	}
	if _, ok := state.previewCache[key]; ok {
		return
	}
	if _, ok := state.previewError[key]; ok {
		return
	}

	if state.previewRequests == nil {
		state.previewRequests = make(chan previewRequest, 1)
		go loadPreviews(ctx, done, screen, state.previewRequests, results)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	state.cancelPreview = cancel
	state.previewLoading[key] = true
	count := opts.PreviewMessages
	if count <= 0 {
		count = 20
	}
	// Replace queued work. A request already reading will observe cancellation.
	select {
	case <-state.previewRequests:
	default:
	}
	state.previewRequests <- previewRequest{ctx: requestCtx, key: key, path: path, count: count}
}

func loadPreviews(ctx context.Context, done <-chan struct{}, screen tcell.Screen, requests <-chan previewRequest, results chan<- previewEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case req := <-requests:
			messages, limited, err := claudehistory.ReadSessionPreview(req.ctx, req.path, req.count)
			if req.ctx.Err() != nil {
				continue
			}
			text := ""
			if err == nil {
				text = claudehistory.FormatMessages(messages, 400)
				if limited {
					if text != "" {
						text += "\n\n"
					}
					text += "Preview limited to the end of this file."
				}
			}
			select {
			case <-req.ctx.Done():
				continue
			case <-done:
				return
			case results <- previewEvent{cacheKey: req.key, ctx: req.ctx, text: text, err: err}:
			}
			postUIEventWithRetry(ctx, done, screen, &uiEvent{when: time.Now(), kind: "preview"})
		}
	}
}

func (s *uiState) applyPreview(ev previewEvent) {
	if ev.ctx.Err() != nil {
		return
	}
	delete(s.previewLoading, ev.cacheKey)
	s.previewOrder = append(s.previewOrder, ev.cacheKey)
	if len(s.previewOrder) > previewCacheLimit {
		delete(s.previewCache, s.previewOrder[0])
		delete(s.previewError, s.previewOrder[0])
		s.previewOrder = s.previewOrder[1:]
	}
	if ev.err != nil {
		s.previewError[ev.cacheKey] = ev.err.Error()
	} else {
		s.previewCache[ev.cacheKey] = ev.text
	}
}

func (s *uiState) loadSubagentDetails(ctx context.Context, done <-chan struct{}, screen tcell.Screen, session claudehistory.Session, results chan<- subagentLoadEvent) {
	parent := sessionIdentity{}
	if s.expandedSessions[session.SessionID] && len(session.Subagents) > 0 {
		parent = subagentParentIdentity(session)
	}
	if s.subagentParent == parent {
		return
	}
	s.subagentParent = parent
	s.stopSubagentLoad()
	if parent == (sessionIdentity{}) {
		return
	}
	cached := s.subagentDetails[parent]
	var toLoad []claudehistory.SubagentSession
	summaries := make(map[string]claudehistory.SubagentSession)
	for _, sub := range session.Subagents {
		if sub.SourceVersion == "" || sub.FirstPrompt != "" {
			continue
		}
		key := previewCacheKey(nil, &sub)
		if summary, ok := cached[key]; ok {
			summaries[key] = summary
		} else {
			toLoad = append(toLoad, sub)
		}
	}
	if len(toLoad) == 0 {
		return
	}
	if s.subagentSlot == nil {
		s.subagentSlot = make(chan struct{}, 1)
	}
	jobCtx, cancel := context.WithCancel(ctx)
	s.cancelSubagents = cancel
	generation, slot := s.subagentGeneration, s.subagentSlot
	go func() {
		select {
		case <-jobCtx.Done():
			return
		case slot <- struct{}{}:
		}
		defer func() { <-slot }()
		for _, sub := range toLoad {
			if jobCtx.Err() != nil {
				return
			}
			detail, err := claudehistory.LoadSubagentSummary(jobCtx, sub)
			if err == nil {
				summaries[previewCacheKey(nil, &sub)] = detail
			}
		}
		select {
		case <-jobCtx.Done():
			return
		case <-done:
			return
		case results <- subagentLoadEvent{generation: generation, parent: parent, summaries: summaries}:
		}
		postUIEventWithRetry(ctx, done, screen, &uiEvent{when: time.Now(), kind: "subagents"})
	}()
}

func (s *uiState) stopSubagentLoad() {
	if s.cancelSubagents != nil {
		s.cancelSubagents()
		s.cancelSubagents = nil
	}
	s.subagentGeneration++
}
