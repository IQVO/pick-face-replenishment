package a2a

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const testAgentURL = "http://pick-face-replenishment-a2a.warehouse-systems.svc.cluster.local:8093/"

// TestCardShape pins the identity, interface and capability surface of the
// Agent Card: the discovery contract other contexts program against.
func TestCardShape(t *testing.T) {
	card := Card(Config{AgentURL: testAgentURL, Version: "1.0.0"})

	if card.Name != "pick-face-replenishment" {
		t.Errorf("card name = %q, want pick-face-replenishment", card.Name)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("card has %d interfaces, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.URL != testAgentURL || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("interface = %+v, want JSONRPC at the configured agent URL", iface)
	}
	if card.Capabilities.Streaming {
		t.Error("streaming must not be advertised: this is a message-only agent")
	}
	if card.SecuritySchemes != nil {
		t.Error("card must declare no security schemes: the surface is unauthenticated fleet-wide")
	}
	if card.Provider == nil || card.Provider.Org != "IQVO" {
		t.Errorf("provider = %+v, want IQVO", card.Provider)
	}
}

// TestCardSkillsAreCuratedReadOnlyAndTruthful pins the two-skill, read-only
// surface (the write gate: MCP offers no write, so neither does A2A) and
// proves the card never lies about its own contract: every advertised example
// must parse under the SAME strict decoder the executor uses, name its own
// skill, and carry arguments that skill accepts.
func TestCardSkillsAreCuratedReadOnlyAndTruthful(t *testing.T) {
	card := Card(Config{AgentURL: testAgentURL, Version: "1.0.0"})

	want := map[string]bool{skillPickFaceStatus: true, skillReplenishmentLookup: true}
	if len(card.Skills) != len(want) {
		t.Fatalf("card has %d skills, want exactly %d (curated, goal-shaped -- not one per endpoint)", len(card.Skills), len(want))
	}
	for _, s := range card.Skills {
		if !want[s.ID] {
			t.Errorf("unexpected skill %q on the card", s.ID)
			continue
		}
		if s.Name == "" || s.Description == "" || len(s.Examples) == 0 {
			t.Errorf("skill %q lacks a name, description or example", s.ID)
		}
		if !containsTag(s.Tags, "read") || containsTag(s.Tags, "write") {
			t.Errorf("skill %q must be tagged read (and never write): this agent is read-only, tags = %v", s.ID, s.Tags)
		}
		for _, ex := range s.Examples {
			var req skillRequest
			if err := decodeStrict([]byte(ex), &req); err != nil {
				t.Errorf("skill %q example is not valid under the strict decoder: %v", s.ID, err)
				continue
			}
			if req.Skill != s.ID {
				t.Errorf("skill %q example names skill %q", s.ID, req.Skill)
			}
			assertArgsAccepted(t, s.ID, req.Arguments)
		}
	}
}

func assertArgsAccepted(t *testing.T, skill string, raw json.RawMessage) {
	t.Helper()
	var err error
	switch skill {
	case skillPickFaceStatus:
		err = decodeStrict(raw, &pickFaceStatusArgs{})
	case skillReplenishmentLookup:
		err = decodeStrict(raw, &replenishmentLookupArgs{})
	}
	if err != nil {
		t.Errorf("skill %q example arguments are rejected by its own strict decoder: %v", skill, err)
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// TestCardClaimsNoWriteCapability keeps the card honest about the write gate:
// the description says the agent changes nothing, and no skill text promises
// an outbox publication or a state change.
func TestCardClaimsNoWriteCapability(t *testing.T) {
	card := Card(Config{AgentURL: testAgentURL, Version: "1.0.0"})
	if !strings.Contains(card.Description, "read-only") || !strings.Contains(card.Description, "It changes nothing") {
		t.Errorf("the card description must state the agent is read-only: %q", card.Description)
	}
	for _, s := range card.Skills {
		for _, banned := range []string{"outbox", "publishes", "declares a policy", "cancels"} {
			if strings.Contains(strings.ToLower(s.Description), banned) {
				t.Errorf("skill %q description mentions %q: a read skill must not promise a write", s.ID, banned)
			}
		}
	}
}

// TestCardSerializable proves the card marshals to JSON a client can parse
// back, with the discovery fields intact.
func TestCardSerializable(t *testing.T) {
	raw, err := json.Marshal(Card(Config{AgentURL: testAgentURL, Version: "1.0.0"}))
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var back a2a.AgentCard
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("card JSON does not round-trip: %v", err)
	}
	if back.Name != "pick-face-replenishment" || len(back.Skills) != 2 {
		t.Errorf("round-tripped card = %q with %d skills", back.Name, len(back.Skills))
	}
}
