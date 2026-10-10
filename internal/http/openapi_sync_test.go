package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/storage"
	"github.com/shalarasgar/carpool-backend/internal/store"
	"go.yaml.in/yaml/v3"
)

func TestOpenAPICoversRoutes(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t)
	documented := specRoutes(t, spec)
	registered := registeredRoutes(t)

	var missing, extra []string
	for route := range registered {
		if _, ok := documented[route]; !ok {
			missing = append(missing, route)
		}
	}
	for route := range documented {
		if _, ok := registered[route]; !ok {
			extra = append(extra, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("openapi.yaml is out of sync with the router\nmissing from spec:\n%s\nnot registered:\n%s",
			strings.Join(missing, "\n"), strings.Join(extra, "\n"))
	}
}

func TestOpenAPIErrorCodes(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t)
	schemas := specSchemas(t, spec)
	got, err := nestedEnum(schemas["Error"], "error", "code")
	if err != nil {
		t.Fatalf("Error schema: %v", err)
	}
	assertSameSet(t, "error codes", i18n.ErrorCodes(), got)
}

func TestOpenAPITripStatusAndNotificationTypes(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t)
	schemas := specSchemas(t, spec)

	status := []string{
		string(store.TripStatusActive),
		string(store.TripStatusFull),
		string(store.TripStatusCancelled),
		string(store.TripStatusCompleted),
		string(store.TripStatusExpired),
	}
	gotStatus, err := nestedEnum(schemas["Trip"], "status")
	if err != nil {
		t.Fatalf("Trip.status: %v", err)
	}
	assertSameSet(t, "trip status", status, gotStatus)

	queryStatus, ok := queryEnum(t, spec, "/v1/me/trips", "get", "status")
	if !ok {
		t.Fatal("GET /v1/me/trips is missing the status query enum")
	}
	assertSameSet(t, "GET /v1/me/trips status", status, queryStatus)

	var kinds []string
	for _, kind := range notify.AllKinds() {
		kinds = append(kinds, string(kind))
	}
	gotKinds, err := nestedEnum(schemas["Notification"], "type")
	if err != nil {
		t.Fatalf("Notification.type: %v", err)
	}
	assertSameSet(t, "notification types", kinds, gotKinds)
}

func registeredRoutes(t *testing.T) map[string]struct{} {
	t.Helper()
	photos, err := storage.NewLocal(t.TempDir(), "/v1/media/photos")
	if err != nil {
		t.Fatalf("photo dir: %v", err)
	}
	// Zero services are enough: NewRouter only checks that the pointer is set.
	handler := NewRouter(Deps{
		Auth:          &service.Auth{},
		Gateway:       &service.Gateway{},
		Users:         &service.Users{},
		Vehicles:      &service.Vehicles{},
		Locations:     &service.Locations{},
		Trips:         &service.Trips{},
		Bookings:      &service.Bookings{},
		Reviews:       &service.Reviews{},
		Blocks:        &service.Blocks{},
		Reports:       &service.Reports{},
		Notifications: &service.Notifications{},
		Admin:         &service.Admin{},
		Photos:        photos,
		AdminToken:    "openapi-sync-test-token",
	})
	routes, ok := handler.(chi.Routes)
	if !ok {
		t.Fatalf("router type %T does not expose chi routes", handler)
	}
	out := map[string]struct{}{}
	err = chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == "" || strings.Contains(route, "*") {
			return nil
		}
		out[method+" "+normPath(route)] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	return out
}

func normPath(path string) string {
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func specRoutes(t *testing.T, spec map[string]any) map[string]struct{} {
	t.Helper()
	paths, ok := asMap(spec["paths"])
	if !ok {
		t.Fatal("openapi.yaml has no paths")
	}
	methods := map[string]struct{}{
		"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}, "head": {}, "options": {},
	}
	out := map[string]struct{}{}
	for path, item := range paths {
		ops, ok := asMap(item)
		if !ok {
			t.Fatalf("path %s is not an object", path)
		}
		for method := range ops {
			if _, ok := methods[strings.ToLower(method)]; !ok {
				continue
			}
			out[strings.ToUpper(method)+" "+normPath(path)] = struct{}{}
		}
	}
	return out
}

func queryEnum(t *testing.T, spec map[string]any, path, method, name string) ([]string, bool) {
	t.Helper()
	paths, ok := asMap(spec["paths"])
	if !ok {
		t.Fatal("openapi.yaml has no paths")
	}
	item, ok := asMap(paths[path])
	if !ok {
		return nil, false
	}
	op, ok := asMap(item[method])
	if !ok {
		return nil, false
	}
	params, ok := op["parameters"].([]any)
	if !ok {
		return nil, false
	}
	for _, raw := range params {
		param, ok := asMap(raw)
		if !ok || param["name"] != name {
			continue
		}
		schema, ok := asMap(param["schema"])
		if !ok {
			return nil, false
		}
		values, err := stringList(schema["enum"])
		if err != nil {
			t.Fatalf("%s %s query %s: %v", method, path, name, err)
		}
		return values, true
	}
	return nil, false
}

func specSchemas(t *testing.T, spec map[string]any) map[string]map[string]any {
	t.Helper()
	components, ok := asMap(spec["components"])
	if !ok {
		t.Fatal("openapi.yaml has no components")
	}
	raw, ok := asMap(components["schemas"])
	if !ok {
		t.Fatal("openapi.yaml has no schemas")
	}
	out := make(map[string]map[string]any, len(raw))
	for name, item := range raw {
		schema, ok := asMap(item)
		if !ok {
			t.Fatalf("schema %s is not an object", name)
		}
		out[name] = schema
	}
	return out
}

func nestedEnum(schema map[string]any, props ...string) ([]string, error) {
	cur := schema
	for i, prop := range props {
		properties, ok := asMap(cur["properties"])
		if !ok {
			return nil, fmt.Errorf("missing properties before %s", prop)
		}
		next, ok := asMap(properties[prop])
		if !ok {
			return nil, fmt.Errorf("missing property %s", prop)
		}
		if i == len(props)-1 {
			return stringList(next["enum"])
		}
		cur = next
	}
	return nil, fmt.Errorf("empty property path")
}

func stringList(raw any) ([]string, error) {
	switch values := raw.(type) {
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("enum value %T is not a string", value)
			}
			out = append(out, text)
		}
		return out, nil
	case []string:
		return append([]string(nil), values...), nil
	default:
		return nil, fmt.Errorf("enum has type %T", raw)
	}
}

func assertSameSet(t *testing.T, what string, want, got []string) {
	t.Helper()
	missing, extra := diffSets(want, got)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("%s mismatch\nmissing from spec: %v\nextra in spec: %v", what, missing, extra)
	}
}

func diffSets(want, got []string) (missing, extra []string) {
	have := map[string]struct{}{}
	for _, value := range got {
		have[value] = struct{}{}
	}
	need := map[string]struct{}{}
	for _, value := range want {
		need[value] = struct{}{}
		if _, ok := have[value]; !ok {
			missing = append(missing, value)
		}
	}
	for _, value := range got {
		if _, ok := need[value]; !ok {
			extra = append(extra, value)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(body, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	return spec
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	default:
		return nil, false
	}
}
