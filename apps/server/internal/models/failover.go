package models

import (
	"context"
	"errors"
	"net"
	"regexp"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/settings"
)

// Model failover per role (PLAN 5.2). settings.modelRoles[role] is an ordered
// chain of {connectorId, model}. A failover model tries each running link in
// order and moves on when a call fails with 5xx, 429, a quota error or a
// network error. Errors after a stream has started are not retried here.

// ReasoningEfforts is models/effort.ts (the same list settings validates).
var ReasoningEfforts = settings.ReasoningEfforts

// ModelLink is one link of a resolved chain.
type ModelLink struct {
	ConnectorID string
	Model       string
	Effort      string
	LM          LanguageModel
}

// ModelHost is what chain resolution needs from the supervisor: the model a
// running connector builds for a model id and role. ok is false when the
// connector isn't running or serves no models; an error means it couldn't
// build this one (skipped like a paused link).
type ModelHost interface {
	ConnectorModel(connectorID, modelID, role string) (LanguageModel, bool, error)
}

// ResolveChain is resolveChain: the running links of a role's global chain
// (the default chain when the role's own is empty).
func ResolveChain(q db.Q, host ModelHost, role string) ([]ModelLink, error) {
	return ResolveAgentChain(q, host, db.AgentModels{}, role)
}

// ResolveAgentChain is ResolveChain for one agent: its own chain for the role
// when it set one, else the global chain. A role with neither uses the agent's
// default chain, then the global one.
func ResolveAgentChain(q db.Q, host ModelHost, own db.AgentModels, role string) ([]ModelLink, error) {
	roles, err := settings.Get[settings.ModelRoles](q, "modelRoles")
	if err != nil {
		return nil, err
	}
	chain := EffectiveChain(roles, own, role)
	var out []ModelLink
	for _, link := range chain {
		lm, ok, err := safeModel(host, link.ConnectorID, link.Model, role)
		if !ok || err != nil || lm == nil {
			continue
		}
		out = append(out, ModelLink{ConnectorID: link.ConnectorID, Model: link.Model, Effort: link.Effort, LM: lm})
	}
	return out, nil
}

// EffectiveChain is the chain a role uses, before checking what's running.
func EffectiveChain(roles settings.ModelRoles, own db.AgentModels, role string) []settings.ModelChainLink {
	for _, chain := range [][]settings.ModelChainLink{own.Role(role), roles.Role(role), own.Default, roles.Default} {
		if len(chain) > 0 {
			return chain
		}
	}
	return nil
}

func safeModel(host ModelHost, id, model, role string) (lm LanguageModel, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			lm, ok, err = nil, false, errors.New("model builder panicked")
		}
	}()
	return host.ConnectorModel(id, model, role)
}

var failoverText = regexp.MustCompile(`(?i)quota|rate.?limit|overloaded|too many requests|insufficient|ECONNREFUSED|ECONNRESET|ENOTFOUND|EAI_AGAIN|fetch failed|socket hang up|connection refused|connection reset|no such host`)
var quotaText = regexp.MustCompile(`(?i)quota|credit|billing|limit|exhaust`)

// IsFailoverError says whether the next link should be tried.
func IsFailoverError(err error) bool {
	if err == nil {
		return false
	}
	var api *APICallError
	if errors.As(err, &api) {
		s := api.StatusCode
		switch {
		case s == 429 || s >= 500:
			return true
		case s == 402 || s == 403:
			return quotaText.MatchString(api.Message + " " + api.ResponseBody)
		case s == 0:
			return true // no response: network failure
		}
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && !ne.Timeout() {
		return true
	}
	if failoverText.MatchString(err.Error()) {
		return true
	}
	if cause := errors.Unwrap(err); cause != nil && cause != err {
		return IsFailoverError(cause)
	}
	return false
}

// WithEffort applies a link's effort unless the caller already chose one.
func WithEffort(opts CallOptions, link ModelLink) CallOptions {
	if link.Effort == "" || opts.Reasoning != "" {
		return opts
	}
	opts.Reasoning = link.Effort
	return opts
}

// ForLink is what one link is sent: its effort, and cache breakpoints in its
// own protocol. The prompt is standardized first, like streamText does before
// the model sees it.
func ForLink(opts CallOptions, link ModelLink) (CallOptions, error) {
	std, err := Standardize(opts.Prompt)
	if err != nil {
		return opts, err
	}
	opts.Prompt = std
	return WithPromptCache(WithEffort(opts, link), link.LM.Provider()), nil
}

// FailoverModel delegates to the first link that answers.
type FailoverModel struct {
	Links      []ModelLink
	onFailover func(from ModelLink, err error)
	mu         sync.Mutex
	last       *ModelLink
}

// ErrNoModel is thrown when a chain has no running link.
var ErrNoModel = errors.New("No model connector is running")

// NewFailoverModel is failoverModel(links, onFailover).
func NewFailoverModel(links []ModelLink, onFailover func(from ModelLink, err error)) (*FailoverModel, error) {
	if len(links) == 0 {
		return nil, ErrNoModel
	}
	return &FailoverModel{Links: links, onFailover: onFailover}, nil
}

func (f *FailoverModel) current() ModelLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last != nil {
		return *f.last
	}
	return f.Links[0]
}

func (f *FailoverModel) Provider() string { return f.current().LM.Provider() }
func (f *FailoverModel) ModelID() string  { return f.current().Model }

// Served is the link that served the last successful call.
func (f *FailoverModel) Served() *ModelLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		return nil
	}
	l := *f.last
	return &l
}

func (f *FailoverModel) Stream(ctx context.Context, opts CallOptions) (Stream, error) {
	var lastErr error
	for i, link := range f.Links {
		o, err := ForLink(opts, link)
		var s Stream
		if err == nil {
			s, err = link.LM.Stream(ctx, o)
		}
		if err == nil {
			f.mu.Lock()
			l := link
			f.last = &l
			f.mu.Unlock()
			return s, nil
		}
		lastErr = err
		if i == len(f.Links)-1 || !IsFailoverError(err) {
			return nil, err
		}
		if f.onFailover != nil {
			f.onFailover(link, err)
		}
	}
	return nil, lastErr
}
