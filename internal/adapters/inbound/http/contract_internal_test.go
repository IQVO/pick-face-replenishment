package http

import (
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

const openapiPath = "../../../../apis/openapi.yaml"

func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(openapiPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// specOperations lists "METHOD /path" for every operation of the contract.
func specOperations(t *testing.T) []string {
	t.Helper()
	paths, _ := loadSpec(t)["paths"].(map[string]any)
	var ops []string
	for path, item := range paths {
		methods, _ := item.(map[string]any)
		for method := range methods {
			if slices.Contains([]string{"get", "put", "post", "delete", "patch"}, method) {
				ops = append(ops, strings.ToUpper(method)+" "+path)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

func routerOperations(t *testing.T) []string {
	t.Helper()
	routes, ok := NewRouter(&Server{}).(chi.Routes)
	if !ok {
		t.Fatal("NewRouter does not return a chi router")
	}
	var ops []string
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		ops = append(ops, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ops)
	return ops
}

// TestRouterServesExactlyTheDocumentedOperations is the contract check of a
// repo without a schemathesis job: every operation of apis/openapi.yaml is
// routed and the router serves nothing the contract does not document.
func TestRouterServesExactlyTheDocumentedOperations(t *testing.T) {
	spec, router := specOperations(t), routerOperations(t)
	if len(spec) != 12 {
		t.Fatalf("the contract documents %d operations, expected 12: %v", len(spec), spec)
	}
	if !slices.Equal(spec, router) {
		t.Fatalf("router and contract differ\ncontract: %v\nrouter:   %v", spec, router)
	}
}

// exampleSlugs collects every problem `type` slug the contract documents.
func exampleSlugs(node any, out map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		if typ, ok := n["type"].(string); ok && strings.HasPrefix(typ, problemBaseURI) {
			out[strings.TrimPrefix(typ, problemBaseURI)] = true
		}
		for _, v := range n {
			exampleSlugs(v, out)
		}
	case []any:
		for _, v := range n {
			exampleSlugs(v, out)
		}
	}
}

// TestProblemSlugsMatchTheContract: every slug the adapter can answer has its
// example in apis/openapi.yaml and every documented slug is produced.
func TestProblemSlugsMatchTheContract(t *testing.T) {
	documented := map[string]bool{}
	exampleSlugs(loadSpec(t), documented)

	produced := map[string]bool{
		internalProblem.slug: true, idempotencyKeyRequired.slug: true, idempotencyKeyReused.slug: true,
		dependencyUnavailable.slug: true,
	}
	for _, entry := range problemCatalogue {
		produced[entry.p.slug] = true
	}
	for slug := range produced {
		if !documented[slug] {
			t.Errorf("slug %q is answered by the adapter but has no example in apis/openapi.yaml", slug)
		}
	}
	for slug := range documented {
		if !produced[slug] {
			t.Errorf("slug %q is documented in apis/openapi.yaml but the adapter never answers it", slug)
		}
	}
}

// TestProblemStatusesMatchTheirResponseGroup: a slug documented under a
// response component keeps the status of that component's code.
func TestProblemStatusesMatchTheContract(t *testing.T) {
	spec := loadSpec(t)
	components, _ := spec["components"].(map[string]any)
	responses, _ := components["responses"].(map[string]any)
	statusBySlug := map[string]int{}
	var collect func(node any)
	collect = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			typ, hasType := n["type"].(string)
			status, hasStatus := n["status"].(int)
			if hasType && hasStatus && strings.HasPrefix(typ, problemBaseURI) {
				statusBySlug[strings.TrimPrefix(typ, problemBaseURI)] = status
			}
			for _, v := range n {
				collect(v)
			}
		case []any:
			for _, v := range n {
				collect(v)
			}
		}
	}
	collect(responses)
	for _, entry := range problemCatalogue {
		if want, ok := statusBySlug[entry.p.slug]; ok && want != entry.p.status {
			t.Errorf("slug %q: adapter status %d, contract example status %d", entry.p.slug, entry.p.status, want)
		}
	}
}
