# ADR 0012: Agent authority levels and complete MCP surface

## Status

Accepted (2026-11-02). Follows [ADR 0007](./0007-mcp-server-adoption.md) (the read-only MCP charter, kept unchanged)
and [ADR 0010](./0010-a2a-inbound-adapter.md) (the A2A skills that get the same level as the tools they front).
Mirrors the fleet's reference rollout (workforce-management ADR 0041, PR #235) and the harness template's portable
sensor (`internal/architecture/mcp_authority_fitness_test.go`, template v4).

## Context

The fleet is adopting the Agentic Standard v2 so a model host — or a delegating agent — knows from the advertisement
itself what a tool or skill may do, before calling it. A declaration is one level:

| Level | Meaning |
| --- | --- |
| L1 | pure read of this context's own state |
| L2 | computed recommendation or proposal, changes nothing |
| L3 | a state change; the caller is accountable |
| L4 | a state change without human approval — needs an ADR in the owning context; forbidden here |

The standard also completes the surface: an MCP server should offer a resource (scoped read model) and a prompt
(operator SOP) beside its tools, and the Streamable HTTP transport should be stateless so the 2026-07-28 protocol
revision's per-request handshake (`server/discover`) works without a session while legacy `initialize` clients keep
working.

pick-face-replenishment's whole agent surface is a read: five MCP tools over the existing read use cases (ADR 0007)
and two A2A skills over the same use cases (ADR 0010). Every tool and skill here is therefore **L1**; no L2
recommendation and no L3 write exists on this surface, and ADR 0007's charter stays exactly as it was.

## Decision

1. **Every `mcp.Tool` declares its level** in `Meta` under the pinned key `com.warehouse/authority`, through the
   fleet's pinned helper shape defined once in the adapter package:
   `const authorityMetaKey = "com.warehouse/authority"`, the constants `authorityL1`..`authorityL3`
   (`authorityL2`/`authorityL3` carry explained nolints: nothing uses them while the surface is read-only), and
   `func authorityMeta(level string) mcp.Meta { return mcp.Meta{authorityMetaKey: level} }`.
   All five tools register `Meta: authorityMeta(authorityL1)`.
2. **Every A2A skill carries the tag `authority:L1`** (keeping its `read` tag): a skill gets the same level as the
   tool it fronts.
3. **The Streamable HTTP handler is stateless** (AS-4): `&mcp.StreamableHTTPOptions{Stateless: true}`. No
   `Mcp-Session-Id` is issued; `server/discover` advertises the 2026-07-28 revision and a legacy `initialize`
   still answers 200. Raw-POST tests (no SDK client) pin these wire facts, and reverting the option to `nil`
   provably fails them with `-32022 protocol version not supported`.
4. **Exactly one resource**: the template
   `warehouse://pick-face-replenishment/policies/{siteId}/{sku}` — the min/max policy of ONE site and SKU from the
   existing `GetPolicy` use case, one bounded JSON object, never a dump of a table. Model-supplied URI segments are
   validated (wrong scheme, wrong path and empty segments are rejected); an unknown policy is an error, not an
   empty object.
5. **Exactly one prompt**: `cover_below_min_pick_faces` — the operator SOP for covering below-min forward pick
   faces, with one typed optional argument (`site_id`). It names tools, calls nothing and writes nothing: raising
   or cancelling a replenishment stays a REST operation.
6. **The golden registry carries the level**: `testdata/tool_registry.golden.json` includes the `_meta`
   authority value on every advertised tool, so any level change is a reviewed golden diff. Runtime-wired tests
   (`TestEveryAgentToolDeclaresAuthority`, in both the MCP and the A2A adapter) assert the live surface.
7. **The template's static sensor ships here**: `internal/architecture/mcp_authority_fitness_test.go`, copied
   verbatim from the harness template (an unmanaged copy; `mcpAdapterDir` already exists in the package with the
   same value). It fails the build on a tool without a Meta, a write tool below L3, a skill without an
   `authority:Ln` tag, or an MCP server with no resource or prompt. The tool literals carry their
   `mcp.ToolAnnotations` inline (not a shared variable) because the sensor reads `ReadOnlyHint` statically.

## Consequences

- A host gating on authority sees L1 everywhere and needs no approval flow for this context; when a write tool is
  ever added, its level must be L3 (or L4 behind a new ADR) in the same PR, or the sensor fails.
- No behaviour changes: the same five tools with the same names, arguments and results, the same two skills, no
  auth anywhere, CloudEvents untouched, no chart or replica change.
- Clients that relied on `initialize` keep working; nothing in this server ever relied on cross-call session
  state.
- The `.agent-surface-pending` marker is NOT used: the resource and prompt landed in this ADR's PR.
