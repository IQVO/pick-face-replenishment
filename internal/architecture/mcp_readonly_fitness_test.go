package architecture

// MCP read-only sensor (docs/adr/0007-mcp-server-adoption.md).
//
// The MCP server is a second front door to the data, never to the outbox: a
// tool that wrote would bypass the version guard (If-Match), the idempotency
// key and the CloudEvents/outbox pipeline every other context consumes. The
// sensor pins read-only structurally, on top of the tool-name governance test
// in the adapter package:
//
//   - the MCP adapter and cmd/mcp use no write use case (every use case of
//     internal/application/usecases is classified read or write, so a new one
//     cannot slip in unclassified) and none of the consumer-side Apply*/Intake;
//   - neither imports anything that can write or publish: the outbox, the
//     event sink, Kafka (either direction), the in-memory doubles, or the
//     ports package (which declares EventSink and UnitOfWork);
//   - cmd/mcp builds no outbox repository, unit of work, idempotency store or
//     processed-event repository, and gives its Evaluator no event sink, id
//     generator or clock.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mcpReadUseCases are the use cases the MCP surface exposes.
var mcpReadUseCases = map[string]bool{
	"ListPolicies": true, "GetPolicy": true, "ListPickFaces": true, "ListRequests": true, "GetRequest": true,
}

// mcpWriteUseCases are the use cases that change state or raise events. The
// MCP surface must never reference them or their inputs.
var mcpWriteUseCases = map[string]bool{
	"DeclarePolicy": true, "RemovePolicy": true, "RaiseManualRequest": true, "CancelRequest": true, "EvaluateSite": true,
	"ApplyBinLevel": true, "ApplyForwardAssignmentMap": true, "ApplyZone": true, "ApplyLocationRegistered": true,
	"ApplyLocationDecommissioned": true, "ApplyRelocation": true,
	"ApplyPackLevelDeclared": true, "ApplyPackLevelRetired": true,
	"DeclarePolicyInput": true, "RequestReplenishmentInput": true, "Intake": true,
}

// mcpForbiddenImportSuffixes are import-path tails nothing in the MCP surface
// may import: every way to write, publish, consume or fake.
var mcpForbiddenImportSuffixes = []string{
	"/internal/application/outbox",
	"/internal/application/ports",
	"/internal/adapters/outbound/events",
	"/internal/adapters/outbound/outbox",
	"/internal/adapters/outbound/kafka",
	"/internal/adapters/outbound/memory",
	"/internal/adapters/inbound/kafka",
	"/internal/adapters/inbound/http",
	"/internal/adapters/kafka/cloudevents",
	"github.com/segmentio/kafka-go",
}

// mcpForbiddenConstructors are the postgres constructors of the write side.
var mcpForbiddenConstructors = map[string]bool{
	"NewOutboxRepo": true, "NewUnitOfWork": true, "NewIdempotencyStore": true, "NewProcessedEventRepo": true,
}

// mcpEvaluatorWriteFields are the Evaluator fields only a writer needs.
var mcpEvaluatorWriteFields = map[string]bool{"Events": true, "IDs": true, "Clock": true}

// mcpReadOnlyFindings parses src and returns one finding per way the file
// leaves the read-only surface. cmd is true for the composition root, which
// is additionally held to the constructor and Evaluator-field rules.
func mcpReadOnlyFindings(filename string, src any, cmd bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out, usecasesName, err := mcpImportFindings(fset, f)
	if err != nil {
		return nil, err
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			out = append(out, mcpSelectorFindings(fset, x, usecasesName, cmd)...)
		case *ast.CompositeLit:
			if cmd && isUsecasesEvaluator(x.Type, usecasesName) {
				out = append(out, mcpEvaluatorFindings(fset, x)...)
			}
		}
		return true
	})
	return out, nil
}

// mcpImportFindings flags the forbidden imports and returns the local name
// the usecases package is imported under ("" when it is not).
func mcpImportFindings(fset *token.FileSet, f *ast.File) (findings []string, usecasesName string, err error) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, "", err
		}
		if strings.HasSuffix(path, "/internal/application/usecases") {
			usecasesName = "usecases"
			if imp.Name != nil {
				usecasesName = imp.Name.Name
			}
		}
		for _, bad := range mcpForbiddenImportSuffixes {
			if strings.HasSuffix(path, bad) || strings.Contains(path, bad+"/") {
				findings = append(findings, fmt.Sprintf("%s imports %s", fset.Position(imp.Pos()), path))
			}
		}
	}
	return findings, usecasesName, nil
}

func mcpSelectorFindings(fset *token.FileSet, x *ast.SelectorExpr, usecasesName string, cmd bool) []string {
	id, ok := x.X.(*ast.Ident)
	if !ok {
		return nil
	}
	var out []string
	if usecasesName != "" && id.Name == usecasesName && mcpWriteUseCases[x.Sel.Name] {
		out = append(out, fmt.Sprintf("%s uses usecases.%s, which is a write use case", fset.Position(x.Pos()), x.Sel.Name))
	}
	if cmd && mcpForbiddenConstructors[x.Sel.Name] {
		out = append(out, fmt.Sprintf("%s uses %s.%s: the MCP binary builds no write-side adapter", fset.Position(x.Pos()), id.Name, x.Sel.Name))
	}
	return out
}

func mcpEvaluatorFindings(fset *token.FileSet, lit *ast.CompositeLit) []string {
	var out []string
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && mcpEvaluatorWriteFields[k.Name] {
			out = append(out, fmt.Sprintf("%s sets Evaluator.%s: the MCP detector is read-only", fset.Position(kv.Pos()), k.Name))
		}
	}
	return out
}

func isUsecasesEvaluator(expr ast.Expr, usecasesName string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || usecasesName == "" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == usecasesName && sel.Sel.Name == "Evaluator"
}

func mcpReadOnlyViolation(finding string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    the MCP surface (internal/adapters/inbound/mcp and cmd/mcp) is read-only\n"+
		"  WHAT:    %s\n"+
		"  WHY:     MCP is additive and read-only (docs/adr/0007): a write tool would be a second front door to the "+
		"outbox that skips the If-Match version guard, the Idempotency-Key and the REST contract.\n"+
		"  FIX:     expose only the read use cases; writes stay on REST. Do NOT loosen this test.\n", finding)
}

func TestMCPSurfaceIsReadOnly(t *testing.T) {
	root := filepath.Join("..", "..")
	var files []string
	files = append(files, goFilesUnder(t, filepath.Join(root, "internal", "adapters", "inbound", "mcp"), false)...)
	cmdFiles := goFilesUnder(t, filepath.Join(root, "cmd", "mcp"), false)
	if len(files) == 0 || len(cmdFiles) == 0 {
		t.Fatal("no non-test Go file under internal/adapters/inbound/mcp or cmd/mcp: the sensor would pass vacuously")
	}
	sort.Strings(files)
	sort.Strings(cmdFiles)
	check := func(paths []string, cmd bool) {
		for _, path := range paths {
			findings, err := mcpReadOnlyFindings(path, nil, cmd)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, finding := range findings {
				t.Errorf("%s", mcpReadOnlyViolation(finding))
			}
		}
	}
	check(files, false)
	check(cmdFiles, true)
}

func TestMCPReadOnlySensorBites(t *testing.T) {
	const pre = "package x\nimport (\n\t\"github.com/claudioed/pick-face-replenishment/internal/application/usecases\"\n"
	cases := []struct {
		name string
		src  string
		cmd  bool
		want int
	}{
		{"a write use case is flagged", pre + ")\ntype Deps struct{ D *usecases.DeclarePolicy }", false, 1},
		{"every write use case is flagged", pre + ")\ntype D struct{ A *usecases.RemovePolicy; B *usecases.RaiseManualRequest; C *usecases.CancelRequest; E *usecases.EvaluateSite }", false, 4},
		{"the read use cases pass", pre + ")\ntype Deps struct{ A *usecases.ListPolicies; B *usecases.GetPolicy; C *usecases.ListPickFaces; D *usecases.ListRequests; E *usecases.GetRequest }", false, 0},
		{"the outbox import is flagged", "package x\nimport _ \"example.com/svc/internal/application/outbox\"", false, 1},
		{"the ports import is flagged", "package x\nimport _ \"example.com/svc/internal/application/ports\"", false, 1},
		{"the event sink import is flagged", "package x\nimport _ \"example.com/svc/internal/adapters/outbound/events\"", false, 1},
		{"kafka-go is flagged", "package x\nimport _ \"github.com/segmentio/kafka-go\"", false, 1},
		{"the memory doubles are flagged", "package x\nimport _ \"example.com/svc/internal/adapters/outbound/memory\"", false, 1},
		{"the REST adapter is flagged", "package x\nimport _ \"example.com/svc/internal/adapters/inbound/http\"", false, 1},
		{"an outbox repo in cmd is flagged", "package main\nvar _ = postgres.NewOutboxRepo", true, 1},
		{"a unit of work in cmd is flagged", "package main\nvar _ = postgres.NewUnitOfWork", true, 1},
		{"a unit of work outside cmd is not this rule", "package x\nvar _ = postgres.NewUnitOfWork", false, 0},
		{"an evaluator with an event sink is flagged", pre + ")\nvar _ = usecases.Evaluator{Events: nil, IDs: nil, Clock: nil}", true, 3},
		{"a read-only evaluator passes", pre + ")\nvar _ = usecases.Evaluator{Policies: nil, Requests: nil, Read: nil}", true, 0},
		{"an aliased usecases import is still checked", "package x\nimport uc \"example.com/svc/internal/application/usecases\"\nvar _ = uc.DeclarePolicy{}", false, 1},
		{"a comment that names a write use case passes", "package x\n// usecases.DeclarePolicy is REST only\nvar _ = 1", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpReadOnlyFindings("src.go", tc.src, tc.cmd)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("findings = %v, want %d", got, tc.want)
			}
		})
	}
}

// useCaseTypes returns the receiver type names of every Handle method in the
// non-test files of dir: the application's use cases.
func useCaseTypes(t *testing.T, dir string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, path := range goFilesUnder(t, dir, false) {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Handle" && fn.Recv != nil {
				found[receiverName(fn.Recv.List[0].Type)] = true
			}
		}
	}
	return found
}

func receiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// TestEveryUseCaseIsClassifiedReadOrWrite keeps the write list from going
// stale: each use case (a type with a Handle method) of the application layer
// is in exactly one of the two lists above, so adding one forces a decision.
func TestEveryUseCaseIsClassifiedReadOrWrite(t *testing.T) {
	found := useCaseTypes(t, filepath.Join("..", "application", "usecases"))
	if len(found) == 0 {
		t.Fatal("no use case found: the sensor would pass vacuously")
	}
	for name := range found {
		if mcpReadUseCases[name] == mcpWriteUseCases[name] {
			t.Errorf("use case %s must be listed in exactly one of mcpReadUseCases and mcpWriteUseCases", name)
		}
	}
	for name := range mcpReadUseCases {
		if !found[name] {
			t.Errorf("mcpReadUseCases lists %s, which is not a use case", name)
		}
	}
}
