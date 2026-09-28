package notification

import (
	"context"

	"github.com/google/uuid"
)

// AgentScope says who an in-app notice was addressed to. The outbound
// notification agents (internal/notifyagents) use it to pick ONE copy of an
// event: a download failure, for instance, sends the requester a user notice
// and every admin an admin notice, and a household group chat should hear it
// once.
type AgentScope string

const (
	// AgentScopeUser: Notify — a single recipient (for request_* notices, the
	// requester).
	AgentScopeUser AgentScope = "user"
	// AgentScopeAdmins: NotifyAdmins — every admin account.
	AgentScopeAdmins AgentScope = "admins"
	// AgentScopeAll: NotifyAllUsers — every top-level account.
	AgentScopeAll AgentScope = "all"
)

// AgentEvent is one notification event as handed to the outbound agents:
// once per Notify / NotifyAdmins / NotifyAllUsers call, never once per
// recipient.
type AgentEvent struct {
	// Type is the notification type (types.go).
	Type  string
	Title string
	Body  string
	// ItemID is the linked media item, when the notice has one.
	ItemID *uuid.UUID
	// UserID is the single recipient for AgentScopeUser (the requester of a
	// request_* notice); nil for the broadcast scopes.
	UserID *uuid.UUID
	Scope  AgentScope
}

// AgentDispatcher receives every notification event for the outbound agents.
// DispatchAgentEvent must not block: it is called on the request path.
type AgentDispatcher interface {
	DispatchAgentEvent(ctx context.Context, ev AgentEvent)
}

type agentHolder struct{ d AgentDispatcher }

// SetAgentDispatcher attaches the outbound notification agents (safe to call
// while the service is in use); nil detaches.
func (s *Service) SetAgentDispatcher(d AgentDispatcher) {
	if d == nil {
		s.agents.Store(nil)
		return
	}
	s.agents.Store(&agentHolder{d: d})
}

// forwardToAgents hands an event to the outbound agents, if attached.
func (s *Service) forwardToAgents(ctx context.Context, scope AgentScope, userID *uuid.UUID, typ, title, body string, itemID *uuid.UUID) {
	h := s.agents.Load()
	if h == nil {
		return
	}
	ev := AgentEvent{Type: typ, Title: title, Body: body, Scope: scope}
	if itemID != nil {
		id := *itemID
		ev.ItemID = &id
	}
	if userID != nil {
		id := *userID
		ev.UserID = &id
	}
	h.d.DispatchAgentEvent(ctx, ev)
}
