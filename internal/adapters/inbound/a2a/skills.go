package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/a2a"

// maxIDLen is the OpenAPI maxLength of a site id and a SKU (REST adapter).
const maxIDLen = 64

// Skill ids advertised on the Agent Card (see Card).
const (
	skillPickFaceStatus      = "pick_face_status"
	skillReplenishmentLookup = "replenishment_lookup"
)

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the
// skill id and its arguments. Strict on purpose -- an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored: a
// misspelled name like "stat" must not silently drop the status filter. (Go's
// encoding/json matches field names case-insensitively, so "SITEID" still
// reaches siteId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// pickFaceStatusArgs and replenishmentLookupArgs are the per-skill argument
// shapes. RequestID is a pointer so "requestId": "" (a blank id, answered as
// invalid-request-id) differs from an omitted one (the list form).
type pickFaceStatusArgs struct {
	SiteID string `json:"siteId"`
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

type replenishmentLookupArgs struct {
	RequestID *string `json:"requestId"`
	SiteID    string  `json:"siteId"`
	SKU       string  `json:"sku"`
	State     string  `json:"state"`
	Limit     int     `json:"limit"`
	Cursor    string  `json:"cursor"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer.
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillPickFaceStatus:
		return deps.pickFaceStatus(ctx, req.Arguments)
	case skillReplenishmentLookup:
		return deps.replenishmentLookup(ctx, req.Arguments)
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part.
// A Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered -- not raised -- because
// a malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// idLength applies the REST adapter's 1..64 character bound to an id that was
// given; an empty id is left to the use case's own blank-id error.
func idLength(id string, bad error) *problem {
	if utf8.RuneCountInString(id) > maxIDLen {
		p, _ := lookupProblem(bad)
		return p
	}
	return nil
}

// pickFaceStatus is the site read: the forward pick faces of one site with
// their stock level and replenishment status, from this context's own event-
// fed local copies, through the same ListPickFaces use case as GET /pick-faces
// and the list_pick_faces MCP tool.
func (d Deps) pickFaceStatus(ctx context.Context, rawArgs json.RawMessage) (skillDTO, *problem) {
	var args pickFaceStatusArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if p := idLength(args.SiteID, errSiteIDLength); p != nil {
		return skillDTO{}, p
	}
	page, err := d.ListPickFaces.Handle(ctx, usecases.PickFaceQuery{SiteID: args.SiteID, Status: args.Status, Limit: args.Limit, Cursor: args.Cursor})
	if err != nil {
		return skillDTO{}, problemFromUseCase(err)
	}
	out := toPickFacePage(page)
	return skillDTO{PickFaceStatus: &out, summary: pickFaceSummary(args.SiteID, out)}, nil
}

// replenishmentLookup is the request read: one request by id, or a filtered
// newest-first list, through the same GetRequest / ListRequests use cases as
// GET /replenishments[/{requestId}] and the get_replenishment /
// list_replenishments MCP tools. The id form excludes filters and paging so a
// caller cannot believe a filter narrowed a by-id answer.
func (d Deps) replenishmentLookup(ctx context.Context, rawArgs json.RawMessage) (skillDTO, *problem) {
	var args replenishmentLookupArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if args.RequestID != nil {
		return d.lookupByID(ctx, args)
	}
	if p := firstProblem(idLength(args.SiteID, errSiteIDLength), idLength(args.SKU, errSKULength)); p != nil {
		return skillDTO{}, p
	}
	page, err := d.ListRequests.Handle(ctx, usecases.ListRequestsQuery{Limit: args.Limit, Cursor: args.Cursor, SiteID: args.SiteID, SKU: args.SKU, State: args.State})
	if err != nil {
		return skillDTO{}, problemFromUseCase(err)
	}
	out := toReplenishmentPage(page)
	return skillDTO{Replenishments: &out, summary: replenishmentPageSummary(out)}, nil
}

func (d Deps) lookupByID(ctx context.Context, args replenishmentLookupArgs) (skillDTO, *problem) {
	if args.SiteID != "" || args.SKU != "" || args.State != "" || args.Limit != 0 || args.Cursor != "" {
		return skillDTO{}, problemInvalidArgument(errors.New("requestId cannot be combined with siteId, sku, state, limit or cursor"))
	}
	req, err := d.GetRequest.Handle(ctx, *args.RequestID)
	if err != nil {
		return skillDTO{}, problemFromUseCase(err)
	}
	out := toReplenishment(req)
	return skillDTO{Replenishment: &out, summary: replenishmentSummary(out)}, nil
}

func firstProblem(ps ...*problem) *problem {
	for _, p := range ps {
		if p != nil {
			return p
		}
	}
	return nil
}

// problemFromUseCase maps a use case error to a problem answer, using the
// SAME classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) so a delegating agent sees the outcome a REST caller
// would. Domain rejections are EXPECTED business outcomes and are answered as
// RFC 7807-shaped problems; anything unrecognised is an internal failure,
// still answered as a problem -- status 500, with the cause logged, never
// sent -- so the calling agent always receives a structured result.
func problemFromUseCase(err error) *problem {
	if p, ok := lookupProblem(err); ok {
		return p
	}
	slog.Error("a2a skill failed", "error", err)
	return newProblem(500, "internal-error", "Internal server error", "the skill failed; see server logs")
}
