// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0007-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads replenishment data (list_policies, get_policy, list_pick_faces,
// list_replenishments, get_replenishment); declaring or removing a policy,
// raising and cancelling a request stay on REST, and governance_test.go fails
// the build on a write-verb tool name. This package depends inward on the
// application layer and the domain only -- never on an outbound adapter or the
// REST adapter -- and nothing else may depend on it (internal/architecture's
// TestMCPAdapterDependencyRule). The composition root (cmd/mcp) wires concrete
// repositories into the use cases. There is no auth of any kind (fleet-wide
// revert 2026-09-11; TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "pick-face-replenishment-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Forward pick face replenishment of the warehouse (read-only): see the min/max policies with " +
	"list_policies and get_policy, the forward pick faces of a site with their stock level and status " +
	"(BELOW_MIN, OK, NO_SOURCE, NO_POLICY) with list_pick_faces, and the replenishment requests that move stock from " +
	"a reserve bin to a forward bin (state Requested, Completed or Cancelled) with list_replenishments and " +
	"get_replenishment. Stock levels are event-fed local copies, so they may lag the inventory ledger by a few " +
	"seconds. Nothing here changes data: declaring a policy, requesting or cancelling a replenishment are REST " +
	"operations of pick-face-replenishment."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
