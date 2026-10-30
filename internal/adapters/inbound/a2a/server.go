// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// same application-layer READ use cases the REST and MCP adapters use. It is
// built on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0
// over HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0010-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between bounded contexts: an agent client sends a goal-shaped skill request
// as a structured message part and this agent answers from its own state
// through its own use cases. It is not a data-sync channel -- facts between
// contexts still travel exclusively as CloudEvents on Kafka (ADR 0004).
//
// The agent is READ-ONLY, exactly like the MCP surface (ADR 0007): the write
// gate says an A2A skill may change state only if MCP already offers that
// write, and MCP offers none. Declaring or removing a policy, requesting and
// cancelling a replenishment and the site evaluation stay REST-only (they are
// the operations whose events other contexts consume). Deps holds no write use
// case at all; internal/architecture's TestA2ASurfaceIsReadOnly pins it.
//
// Like the MCP adapter this package depends inward on the application layer
// and the domain only -- never on an outbound adapter, the REST adapter or the
// MCP adapter -- and nothing else may depend on it
// (TestA2AAdapterDependencyRule). It holds no outbound A2A client. The
// composition root (cmd/a2a) wires concrete repositories into the use cases.
// Domain structs never leak across the message boundary. There is no auth of
// any kind (fleet-wide revert 2026-09-11; TestNoAuthMiddlewareReintroduced).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the SAME read use cases the REST and MCP adapters use
// (GET /pick-faces, /replenishments, /replenishments/{requestId}); the adapter
// never constructs an outbound adapter itself and holds no write use case.
type Deps struct {
	ListPickFaces *usecases.ListPickFaces
	ListRequests  *usecases.ListRequests
	GetRequest    *usecases.GetRequest
}

// Card builds this agent's Agent Card (ADR 0010). The surface is two curated,
// goal-shaped READ skills -- not one skill per endpoint, the same curation
// discipline ADR 0007 applied to MCP tools. pick_face_status answers "which
// forward pick faces of this site are low and why"; replenishment_lookup
// answers "what is happening with replenishment requests" (one request by id,
// or a filtered list). The card declares no security schemes on purpose: this
// surface is unauthenticated by the same fleet-wide decision as REST and MCP;
// schemes are added here, not in a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "pick-face-replenishment",
		Description: "Pick-face replenishment agent for the Warehouse Management System (read-only): answers which forward pick faces of a site are below their minimum, have no reserve source or no policy (with on-hand, reserved and available stock and the policy min/max), and what the replenishment requests that move stock from a reserve bin to a forward bin are doing (Requested, Completed or Cancelled). Stock levels are event-fed local copies and may lag the inventory ledger by a few seconds. It changes nothing: declaring a policy, requesting or cancelling a replenishment are REST operations of this context.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/pick-face-replenishment",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillPickFaceStatus,
				Name:        "Pick-face status of a site",
				Description: "Read the forward pick faces of one site ordered by forward bin, a page at a time (limit 1..500, default 100): SKU, onHand, reserved and available quantity, the policy minQty and maxQty (absent without a policy), the openRequestId (absent without an open request) and a status: BELOW_MIN (projected level at or below the minimum), OK, NO_SOURCE (below minimum but no reserve bin can supply it) or NO_POLICY. Optionally only one status. Pass the returned nextCursor for the next page. siteId is required.",
				Tags:        []string{"replenishment", "pick-face", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"pick_face_status","arguments":{"siteId":"SITE-1"}}`,
					`{"skill":"pick_face_status","arguments":{"siteId":"SITE-1","status":"BELOW_MIN","limit":50}}`,
				},
			},
			{
				ID:          skillReplenishmentLookup,
				Name:        "Replenishment request lookup",
				Description: "Read replenishment requests. With requestId (starting with repl-) it answers that one request: forward and reserve source bin, quantity, movedQuantity once completed, priority, state (Requested, Completed or Cancelled), the policy min and max it was raised under, requestedAt, completedAt or cancelledAt with cancelReason, and version. Without requestId it lists requests newest first, a page at a time (limit 1..500, default 100), optionally only those of one siteId, one sku and/or one state; pass the returned nextCursor for the next page. requestId cannot be combined with filters or paging.",
				Tags:        []string{"replenishment", "request", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"replenishment_lookup","arguments":{"requestId":"repl-1b4e28ba"}}`,
					`{"skill":"replenishment_lookup","arguments":{"siteId":"SITE-1","state":"Requested"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track. The response carries the
// caller's contextId (and taskId when the client referenced one) so a
// delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler -- the only transport this
// agent ships (ADR 0010), mirroring ADR 0007's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
