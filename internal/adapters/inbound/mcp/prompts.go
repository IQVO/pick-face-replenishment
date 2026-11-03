package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// coverBelowMinSOP is the operational standard-operating-procedure the
// cover_below_min_pick_faces prompt hands to the model (Agentic Standard v2,
// AS-3; docs/adr/0012). Prompts encode how to interpret results, when to
// escalate and what "done" means; they name tools but never call them, and
// they never write: this MCP surface is read-only (docs/adr/0007), so
// raising or cancelling a replenishment stays a REST operation of this
// context.
const coverBelowMinSOP = `You are helping keep the forward pick faces of a warehouse stocked. Use only the MCP tools; never assume state. This context surfaces the pick faces and the requests; it never moves stock itself.

Procedure:
1. Call list_pick_faces(site_id, status="BELOW_MIN") for your site: every returned face is at or below its policy minimum and needs replenishment.
2. For a below-min face, call get_policy(site_id, sku) (or read the resource warehouse://pick-face-replenishment/policies/{siteId}/{sku}) to see the min_qty/max_qty it is replenished under and the multiple quantities arrive in.
3. Call list_replenishments(site_id, state="Requested") to see which below-min faces already have an open request (the pick face view also carries open_request_id): a face with an open request is being handled; do not raise a duplicate.
4. Call get_replenishment(request_id) when you need the detail of one request: the reserve source bin chosen, the quantity, the policy bounds and its state.

Interpretation:
- A face with status NO_POLICY has no declared policy, so nothing guards its level. Declaring a policy is a human planning decision.
- A face with status NO_SOURCE is below its minimum but no reserve bin can supply it: the reserve stock is the blocker, not the policy.
- Stock levels are event-fed local copies; they may lag the inventory ledger by a few seconds.

Escalate to a human when: a below-min face has NO_POLICY (a policy must be declared, over REST, by a planner), NO_SOURCE (reserve stock must be moved or topped up), or its open request stays Requested far longer than the site's replenishment cycle. Raising or cancelling a replenishment is a REST operation of this context, not yours.

Done means: for every below-min face of the site you have reported its stock level against its policy bounds, the open request id or its absence, and surfaced each blocker (no policy, no source, stalled request) with a one-line reason.`

// registerPrompts adds the workflow prompt (the operator SOP).
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "cover_below_min_pick_faces",
		Description: "Standard operating procedure for finding the forward pick faces of a site that are below their policy minimum and judging what, if anything, blocks their replenishment.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "site_id",
			Description: "the site the SOP focuses on, e.g. SITE-1; omitted means the operator names the site in the conversation",
			Required:    false,
		}},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		sop := coverBelowMinSOP
		if siteID := strings.TrimSpace(req.Params.Arguments["site_id"]); siteID != "" {
			sop = fmt.Sprintf("Your site is %s.\n\n%s", siteID, sop)
		}
		return &mcp.GetPromptResult{
			Description: "How to cover below-min forward pick faces: list them, read their policy bounds, check open requests, and know when to escalate.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: sop},
			}},
		}, nil
	})
}
