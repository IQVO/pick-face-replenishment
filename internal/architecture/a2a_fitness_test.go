package architecture

// A2A inbound adapter sensors (docs/adr/0010-a2a-inbound-adapter.md).
//
// A2A is the DELEGATION channel (an agent hands this context a goal); facts
// still leave only as CloudEvents through the transactional outbox. This
// context's A2A agent is READ-ONLY because its MCP surface is (the write gate:
// a skill may change state only if MCP already offers that write). The sensors
// pin, structurally:
//
//   - the adapter depends only on application and domain, and nothing depends
//     on it (TestA2AAdapterDependencyRule);
//   - the adapter and cmd/a2a hold no write use case, import nothing that can
//     write, publish or consume, and build no write-side adapter: the very
//     same sensor as the MCP surface (mcpReadOnlyFindings), applied to the
//     A2A directories (TestA2ASurfaceIsReadOnly);
//   - the A2A SDK is imported only by the adapter and cmd/a2a, and nothing
//     imports its OUTBOUND client package (a2aclient) outside tests: this
//     context delegates to nobody (TestA2ASDKStaysInTheInboundAdapter).

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// a2aSDKPrefix is the import-path prefix of the A2A Go SDK (all majors).
const a2aSDKPrefix = "github.com/a2aproject/a2a-go"

func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

func a2aReadOnlyViolation(finding string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    the A2A surface (internal/adapters/inbound/a2a and cmd/a2a) is read-only\n"+
		"  WHAT:    %s\n"+
		"  WHY:     the A2A agent mirrors the MCP charter (docs/adr/0007, docs/adr/0010): a skill may change state only if "+
		"MCP already offers that write, and MCP offers none; a write skill would be a second front door to the outbox that "+
		"skips the If-Match version guard, the Idempotency-Key and the REST contract.\n"+
		"  FIX:     expose only the read use cases; writes stay on REST. Do NOT loosen this test.\n", finding)
}

func TestA2ASurfaceIsReadOnly(t *testing.T) {
	root := filepath.Join("..", "..")
	files := goFilesUnder(t, filepath.Join(root, "internal", "adapters", "inbound", "a2a"), false)
	cmdFiles := goFilesUnder(t, filepath.Join(root, "cmd", "a2a"), false)
	if len(files) == 0 || len(cmdFiles) == 0 {
		t.Fatal("no non-test Go file under internal/adapters/inbound/a2a or cmd/a2a: the sensor would pass vacuously")
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
				t.Errorf("%s", a2aReadOnlyViolation(finding))
			}
		}
	}
	check(files, false)
	check(cmdFiles, true)
}

// a2aSDKFindings returns one finding per import of the A2A SDK in src that
// breaks the containment rule: the outbound client package is imported
// anywhere, or the SDK is imported by a file outside the inbound adapter and
// cmd/a2a (allowed is true for those two places).
func a2aSDKFindings(filename string, src any, allowed bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(path, a2aSDKPrefix) {
			continue
		}
		switch {
		case strings.Contains(path, "/a2aclient"):
			out = append(out, fmt.Sprintf("%s imports %s: this context has no outbound A2A client", fset.Position(imp.Pos()), path))
		case !allowed:
			out = append(out, fmt.Sprintf("%s imports %s outside the inbound A2A adapter and cmd/a2a", fset.Position(imp.Pos()), path))
		}
	}
	return out, nil
}

func TestA2ASDKStaysInTheInboundAdapter(t *testing.T) {
	root := filepath.Join("..", "..")
	adapter := filepath.Join(root, "internal", "adapters", "inbound", "a2a") + string(filepath.Separator)
	cmdA2A := filepath.Join(root, "cmd", "a2a") + string(filepath.Separator)
	var files []string
	files = append(files, goFilesUnder(t, filepath.Join(root, "internal"), false)...)
	files = append(files, goFilesUnder(t, filepath.Join(root, "cmd"), false)...)
	sawAdapter := false
	for _, path := range files {
		allowed := strings.HasPrefix(path, adapter) || strings.HasPrefix(path, cmdA2A)
		sawAdapter = sawAdapter || allowed
		findings, err := a2aSDKFindings(path, nil, allowed)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, finding := range findings {
			t.Errorf("ARCHITECTURE VIOLATION (contents rule)\n  RULE:    A2A is inbound only (docs/adr/0010)\n  WHAT:    %s\n"+
				"  FIX:     keep the SDK inside internal/adapters/inbound/a2a and cmd/a2a; a delegation client is a separate ADR.\n", finding)
		}
	}
	if !sawAdapter {
		t.Fatal("no file under the A2A adapter or cmd/a2a was scanned: the sensor would pass vacuously")
	}
}

func TestA2ASDKSensorBites(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		allowed bool
		want    int
	}{
		{"the server SDK in the adapter passes", `package x; import _ "github.com/a2aproject/a2a-go/v2/a2asrv"`, true, 0},
		{"the outbound client anywhere is flagged", `package x; import _ "github.com/a2aproject/a2a-go/v2/a2aclient"`, true, 1},
		{"the outbound client outside the adapter is flagged once", `package x; import _ "github.com/a2aproject/a2a-go/v2/a2aclient"`, false, 1},
		{"the SDK outside the adapter is flagged", `package x; import _ "github.com/a2aproject/a2a-go/v2/a2a"`, false, 1},
		{"an unrelated import passes", `package x; import _ "net/http"`, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a2aSDKFindings("src.go", tc.src, tc.allowed)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("findings = %v, want %d", got, tc.want)
			}
		})
	}
}
