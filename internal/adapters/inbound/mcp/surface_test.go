// AS-3 surface tests: the resource template and the workflow prompt, read
// through the in-memory client over the real server (docs/adr/0012).
package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// readResource reads one URI through the connected session.
func readResource(t *testing.T, h *harness, uri string) (*sdk.ReadResourceResult, error) {
	t.Helper()
	return h.session.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
}

// TestServer_ResourceTemplateRead reads the policy resource of a seeded
// site/SKU and gets the same JSON body the get_policy tool returns.
func TestServer_ResourceTemplateRead(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	res, err := readResource(t, h, "warehouse://pick-face-replenishment/policies/SITE-1/SKU-1")
	if err != nil {
		t.Fatalf("read policy resource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(res.Contents))
	}
	c := res.Contents[0]
	if c.MIMEType != "application/json" {
		t.Errorf("mime type = %q, want application/json", c.MIMEType)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
		t.Fatalf("resource text is not JSON: %v (%s)", err, c.Text)
	}
	if body["site_id"] != "SITE-1" || body["sku"] != "SKU-1" {
		t.Errorf("resource body = %v, want the SITE-1/SKU-1 policy", body)
	}
	if body["min_qty"] != float64(10) || body["max_qty"] != float64(60) || body["multiple"] != float64(6) {
		t.Errorf("policy bounds = %v, want min 10 max 60 multiple 6", body)
	}
}

// TestServer_ResourceTemplateRejectsBadURIs proves the model-supplied URI is
// validated, not defaulted: a wrong scheme, a wrong path and an empty
// segment are all refused.
func TestServer_ResourceTemplateRejectsBadURIs(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	for _, uri := range []string{
		"staffing://SITE-1/SKU-1",
		"warehouse://pick-face-replenishment/requests/repl-1",
		"warehouse://pick-face-replenishment/policies/SITE-1/",
		"warehouse://pick-face-replenishment/policies//SKU-1",
	} {
		if _, err := readResource(t, h, uri); err == nil {
			t.Errorf("uri %q was accepted, want a validation error", uri)
		}
	}
}

// TestServer_ResourceUnknownPolicyFails keeps the resource as honest as the
// tool: an undeclared policy is an error, not an empty object.
func TestServer_ResourceUnknownPolicyFails(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if _, err := readResource(t, h, "warehouse://pick-face-replenishment/policies/SITE-1/SKU-404"); err == nil {
		t.Fatal("unknown policy resource read succeeded, want policy-not-found")
	}
}

// TestServer_PromptGet returns the SOP, scoped by the optional typed
// site_id argument when it is supplied.
func TestServer_PromptGet(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "cover_below_min_pick_faces"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Content == nil {
		t.Fatalf("prompt messages = %+v, want one content-bearing message", res.Messages)
	}
	if !strings.Contains(promptText(t, res), "list_pick_faces") {
		t.Error("the SOP does not name list_pick_faces")
	}
	if strings.Contains(promptText(t, res), "SITE-1") {
		t.Error("the unscoped SOP should not name a site")
	}

	scoped, err := h.session.GetPrompt(context.Background(), &sdk.GetPromptParams{
		Name:      "cover_below_min_pick_faces",
		Arguments: map[string]string{"site_id": "SITE-1"},
	})
	if err != nil {
		t.Fatalf("get scoped prompt: %v", err)
	}
	if !strings.Contains(promptText(t, scoped), "Your site is SITE-1.") {
		t.Errorf("the scoped SOP does not carry the site_id argument: %q", promptText(t, scoped))
	}
}

// promptText extracts the text of the first message of a prompt result.
func promptText(t *testing.T, res *sdk.GetPromptResult) string {
	t.Helper()
	if len(res.Messages) == 0 {
		t.Fatal("prompt has no messages")
	}
	text, ok := res.Messages[0].Content.(*sdk.TextContent)
	if !ok {
		t.Fatalf("prompt content = %T, want TextContent", res.Messages[0].Content)
	}
	return text.Text
}
