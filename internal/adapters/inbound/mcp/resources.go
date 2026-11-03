package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of the resource this server serves.
const mimeTypeJSON = "application/json"

// registerResources adds the scoped read-model resource (Agentic Standard v2,
// AS-3; docs/adr/0012). Per the fleet rule a resource is a bounded-context
// read model tied to a decision, never a dump of a table: the policy
// resource answers "what are the min/max bounds this site and SKU is
// replenished under?" for ONE site and SKU, backed by the same GetPolicy use
// case the get_policy tool and the REST adapter use. Its size is one object.
// The concrete values are read from the request URI at call time.
func (d Deps) registerResources(server *mcp.Server) {
	const policyTemplate = "warehouse://pick-face-replenishment/policies/{siteId}/{sku}"
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: policyTemplate,
		Name:        "replenishment policy",
		Description: "The declared min/max replenishment policy of one site and SKU: min_qty, max_qty, multiple (replenish in multiples of this), version and declared_at. The bounds every below-min detection and raised request of that pick face is judged against.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		siteID, sku, err := parsePolicyURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		rec, err := d.GetPolicy.Handle(ctx, siteID, sku)
		if err != nil {
			return nil, mapError(err)
		}
		body, err := json.Marshal(toPolicy(rec))
		if err != nil {
			return nil, fmt.Errorf("encode policy %s/%s: %w", siteID, sku, err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: mimeTypeJSON,
				Text:     string(body),
			}},
		}, nil
	})
}

// parsePolicyURI extracts siteId and sku from a
// warehouse://pick-face-replenishment/policies/{siteId}/{sku} URI. The two
// segments are model-supplied and therefore untrusted; a malformed URI or an
// empty segment is rejected rather than silently defaulted.
func parsePolicyURI(uri string) (siteID, sku string, err error) {
	const scheme = "warehouse://pick-face-replenishment/"
	rest, ok := strings.CutPrefix(uri, scheme)
	if !ok {
		return "", "", fmt.Errorf("resource uri %q must start with %q", uri, scheme)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] != "policies" {
		return "", "", fmt.Errorf("resource uri %q must be warehouse://pick-face-replenishment/policies/{siteId}/{sku}", uri)
	}
	siteID, sku = parts[1], parts[2]
	if siteID == "" || sku == "" {
		return "", "", fmt.Errorf("resource uri %q has an empty siteId or sku segment", uri)
	}
	return siteID, sku, nil
}
