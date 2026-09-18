package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/yaml.v3"

	"to-do-list/internal/apperr"
	"to-do-list/internal/buildinfo"
	"to-do-list/internal/domain"
	"to-do-list/internal/logger"
	httpapi "to-do-list/internal/transport/http"
)

const specPath = "../../../docs/openapi.yaml"

type openAPI struct {
	Components struct {
		Schemas   map[string]any `yaml:"schemas"`
		Responses map[string]any `yaml:"responses"`
	} `yaml:"components"`
	Paths map[string]map[string]any `yaml:"paths"`
}

func loadSpec(t *testing.T) *openAPI {
	t.Helper()

	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	var spec openAPI
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}

	return &spec
}

func (s *openAPI) schemaFor(t *testing.T, path, method, status string) map[string]any {
	t.Helper()

	pathItem, ok := s.Paths[path]
	if !ok {
		t.Fatalf("the specification describes no path %q", path)
	}

	operation, ok := pathItem[strings.ToLower(method)].(map[string]any)
	if !ok {
		t.Fatalf("the specification describes no %s on %q", method, path)
	}

	responses, ok := operation["responses"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s declares no responses", method, path)
	}

	response, ok := responses[status].(map[string]any)
	if !ok {
		t.Fatalf("the specification describes no %s response for %s %s", status, method, path)
	}

	if ref, isRef := response["$ref"].(string); isRef {
		response = s.resolveResponse(t, ref)
	}

	content, ok := response["content"].(map[string]any)
	if !ok {
		t.Fatalf("the %s response for %s %s declares no body", status, method, path)
	}

	body, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("the %s response for %s %s declares no JSON body", status, method, path)
	}

	schema, ok := body["schema"].(map[string]any)
	if !ok {
		t.Fatalf("the %s response for %s %s declares no schema", status, method, path)
	}

	return schema
}

func (s *openAPI) resolveResponse(t *testing.T, ref string) map[string]any {
	t.Helper()

	name := strings.TrimPrefix(ref, "#/components/responses/")
	response, ok := s.Components.Responses[name].(map[string]any)
	if !ok {
		t.Fatalf("the specification has no response %q", name)
	}
	return response
}

func (s *openAPI) resolve(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()

	seen := 0
	for {
		ref, isRef := schema["$ref"].(string)
		if !isRef {
			return schema
		}
		if seen++; seen > 10 {
			t.Fatalf("$ref chain too deep at %q", ref)
		}

		name := strings.TrimPrefix(ref, "#/components/schemas/")
		target, ok := s.Components.Schemas[name].(map[string]any)
		if !ok {
			t.Fatalf("the specification has no schema %q", name)
		}
		schema = target
	}
}

func (s *openAPI) flatten(t *testing.T, schema map[string]any) (props map[string]any, required []string, kind string) {
	t.Helper()

	schema = s.resolve(t, schema)
	props = map[string]any{}

	if parts, isComposite := schema["allOf"].([]any); isComposite {
		for _, part := range parts {
			partMap, ok := part.(map[string]any)
			if !ok {
				t.Fatalf("allOf member is not a schema: %#v", part)
			}
			p, r, k := s.flatten(t, partMap)
			for name, sub := range p {
				props[name] = sub
			}
			required = append(required, r...)
			if kind == "" {
				kind = k
			}
		}
		return props, required, kind
	}

	if declared, ok := schema["type"].(string); ok {
		kind = declared
	}
	if declared, ok := schema["properties"].(map[string]any); ok {
		for name, sub := range declared {
			props[name] = sub
		}
	}
	for _, name := range toStrings(schema["required"]) {
		required = append(required, name)
	}

	return props, required, kind
}

func toStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (s *openAPI) validate(t *testing.T, where string, schema map[string]any, value any) []string {
	t.Helper()

	resolved := s.resolve(t, schema)
	props, required, kind := s.flatten(t, resolved)

	switch {
	case kind == "array" || resolved["items"] != nil:
		items, ok := value.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: the document says array, the service sent %T", where, value)}
		}
		itemSchema, _ := resolved["items"].(map[string]any)
		if itemSchema == nil {
			return nil
		}
		var problems []string
		for i, item := range items {
			problems = append(problems, s.validate(t, fmt.Sprintf("%s[%d]", where, i), itemSchema, item)...)
		}
		return problems

	case kind == "object" || len(props) > 0:
		object, ok := value.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: the document says object, the service sent %T", where, value)}
		}

		var problems []string

		for _, name := range required {
			if _, present := object[name]; !present {
				problems = append(problems, fmt.Sprintf("%s: required property %q is missing from the response", where, name))
			}
		}

		open, _ := resolved["additionalProperties"].(map[string]any)

		for _, name := range sortedKeys(object) {
			sub, documented := props[name]
			if !documented {
				if open != nil {
					problems = append(problems, s.validate(t, where+"."+name, open, object[name])...)
					continue
				}
				problems = append(problems, fmt.Sprintf("%s: the service sent %q, which the document does not describe", where, name))
				continue
			}
			subSchema, ok := sub.(map[string]any)
			if !ok {
				continue
			}
			problems = append(problems, s.validate(t, where+"."+name, subSchema, object[name])...)
		}

		return problems
	}

	return s.validateScalar(where, resolved, value)
}

func (s *openAPI) validateScalar(where string, schema map[string]any, value any) []string {
	kind, _ := schema["type"].(string)
	if kind == "" || value == nil {
		return nil
	}

	ok := true
	switch kind {
	case "string":
		_, ok = value.(string)
	case "integer", "number":
		_, ok = value.(float64)
	case "boolean":
		_, ok = value.(bool)
	}
	if !ok {
		return []string{fmt.Sprintf("%s: the document says %s, the service sent %T", where, kind, value)}
	}

	if allowed := toStrings(schema["enum"]); len(allowed) > 0 {
		got, _ := value.(string)
		for _, candidate := range allowed {
			if candidate == got {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s: %q is not one of the documented values %v", where, got, allowed)}
	}

	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// A service that answers every endpoint with a realistic payload.
// ---------------------------------------------------------------------------

func sampleUser() domain.User {
	return domain.User{
		ID: 7, Username: "mary", Email: "mary@example.com",
		PasswordHash: "never-serialised", CredentialsVersion: 3,
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	}
}

func sampleTask() domain.Task {
	return domain.Task{
		ID: 42, Title: "buy milk", Description: "2 litres",
		Status: domain.StatusCreated, CreatorID: 7,
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	}
}

type contractAuth struct{}

func (contractAuth) Register(context.Context, string, string, string) (domain.User, error) {
	return sampleUser(), nil
}

func (contractAuth) Login(context.Context, string, string) (domain.Tokens, domain.User, error) {
	return domain.Tokens{
		AccessToken: "access-token", AccessExpiresAt: time.Now().Add(time.Hour),
		RefreshToken: "refresh-token", RefreshExpiresAt: time.Now().Add(720 * time.Hour),
	}, sampleUser(), nil
}

func (contractAuth) Refresh(context.Context, string) (domain.Tokens, error) {
	return domain.Tokens{
		AccessToken: "access-token", AccessExpiresAt: time.Now().Add(time.Hour),
		RefreshToken: "refresh-token", RefreshExpiresAt: time.Now().Add(720 * time.Hour),
	}, nil
}

func (contractAuth) Logout(context.Context, string) error { return nil }

func (contractAuth) ValidateToken(context.Context, string) (domain.Claims, error) {
	return domain.Claims{UserID: 7, Username: "mary", IsAdmin: true}, nil
}

type contractTasks struct{}

func (contractTasks) Create(context.Context, int64, string, string) (domain.Task, error) {
	return sampleTask(), nil
}
func (contractTasks) Get(context.Context, int64, int64) (domain.Task, error) {
	return sampleTask(), nil
}
func (contractTasks) List(context.Context, int64, domain.TaskFilter) (domain.Page[domain.Task], error) {
	return domain.NewPage([]domain.Task{sampleTask()}, 1, domain.PageRequest{Limit: 20, Offset: 0}), nil
}
func (contractTasks) Update(context.Context, int64, int64, string, string) (domain.Task, error) {
	return sampleTask(), nil
}
func (contractTasks) Delete(context.Context, int64, int64) error { return nil }
func (contractTasks) ToggleStatus(context.Context, int64, int64) (domain.Task, error) {
	task := sampleTask()
	task.Status = domain.StatusInProgress
	return task, nil
}

type contractUsers struct{}

func (contractUsers) Get(context.Context, int64) (domain.User, error) { return sampleUser(), nil }
func (contractUsers) List(context.Context, domain.PageRequest) (domain.Page[domain.User], error) {
	return domain.NewPage([]domain.User{sampleUser()}, 1, domain.PageRequest{Limit: 20, Offset: 0}), nil
}
func (contractUsers) Update(context.Context, int64, string, string, string) (domain.User, error) {
	return sampleUser(), nil
}
func (contractUsers) Delete(context.Context, int64) error { return nil }

func contractApp(t *testing.T) *fiber.App {
	t.Helper()

	return New(
		Config{
			AppName:                  "to-do-list-contract",
			ReadTimeout:              time.Second,
			WriteTimeout:             time.Second,
			CORSAllowOrigins:         "*",
			CORSAllowMethods:         "GET, POST, PATCH, DELETE, OPTIONS",
			CORSAllowHeaders:         "Content-Type, Authorization",
			RateLimitAuthMaxRequests: 1000,
			RateLimitAuthWindow:      time.Minute,
			HealthReadyTimeout:       time.Second,
			MetricsEnabled:           false,
			MetricsPath:              "/metrics",
		},
		logger.New(io.Discard, "error", "json"),
		Observability{Build: buildinfo.Info{Version: "1.0.0", Commit: "abc1234", BuiltAt: "2026-01-01T00:00:00Z", GoVersion: "go1.24.0"}},
		contractAuth{},
		contractTasks{},
		contractUsers{},
		httpapi.Check{Name: "postgres", Probe: func(context.Context) error { return nil }},
	)
}

type contractCase struct {
	name     string
	method   string
	url      string
	body     string
	specPath string
	status   string
}

func TestOpenAPI_ResponsesMatchTheDocument(t *testing.T) {
	spec := loadSpec(t)
	app := contractApp(t)

	cases := []contractCase{
		{"register", fiber.MethodPost, "/api/v1/auth/register",
			`{"username":"mary","email":"mary@example.com","password":"password123"}`,
			"/auth/register", "201"},
		{"login", fiber.MethodPost, "/api/v1/auth/login",
			`{"username":"mary","password":"password123"}`, "/auth/login", "200"},
		{"refresh", fiber.MethodPost, "/api/v1/auth/refresh",
			`{"refresh_token":"whatever"}`, "/auth/refresh", "200"},
		{"me", fiber.MethodGet, "/api/v1/auth/me", "", "/auth/me", "200"},

		{"create task", fiber.MethodPost, "/api/v1/tasks",
			`{"title":"buy milk","description":"2 litres"}`, "/tasks", "201"},
		{"list tasks", fiber.MethodGet, "/api/v1/tasks?limit=20&offset=0", "", "/tasks", "200"},
		{"get task", fiber.MethodGet, "/api/v1/tasks/42", "", "/tasks/{id}", "200"},
		{"update task", fiber.MethodPatch, "/api/v1/tasks/42",
			`{"title":"buy oat milk"}`, "/tasks/{id}", "200"},
		{"toggle task", fiber.MethodPost, "/api/v1/tasks/42/toggle-status", "",
			"/tasks/{id}/toggle-status", "200"},

		{"list users", fiber.MethodGet, "/api/v1/users?limit=20&offset=0", "", "/users", "200"},
		{"get user", fiber.MethodGet, "/api/v1/users/7", "", "/users/{id}", "200"},
		{"update user", fiber.MethodPatch, "/api/v1/users/7",
			`{"email":"new@example.com"}`, "/users/{id}", "200"},

		{"liveness", fiber.MethodGet, "/healthz", "", "/healthz", "200"},
		{"readiness", fiber.MethodGet, "/readyz", "", "/readyz", "200"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader
			if tc.body != "" {
				reader = strings.NewReader(tc.body)
			}

			req := httptest.NewRequest(tc.method, tc.url, reader)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer contract-test-token")
			if tc.body != "" {
				req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.url, err)
			}
			defer resp.Body.Close()

			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if fmt.Sprint(resp.StatusCode) != tc.status {
				t.Fatalf("%s %s returned %d, the case expects %s: %s",
					tc.method, tc.url, resp.StatusCode, tc.status, raw)
			}

			var body any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("response is not JSON: %v (%s)", err, raw)
			}

			schema := spec.schemaFor(t, tc.specPath, tc.method, tc.status)
			for _, problem := range spec.validate(t, "response", schema, body) {
				t.Error(problem)
			}
		})
	}
}

// Errors are part of the contract too: a client that only knows the happy
// path cannot tell a rejected request from a broken server.
func TestOpenAPI_ErrorResponsesMatchTheDocument(t *testing.T) {
	spec := loadSpec(t)

	app := New(
		Config{
			AppName: "to-do-list-contract", ReadTimeout: time.Second, WriteTimeout: time.Second,
			CORSAllowOrigins: "*", CORSAllowMethods: "GET", CORSAllowHeaders: "Content-Type",
			RateLimitAuthMaxRequests: 1000, RateLimitAuthWindow: time.Minute,
			HealthReadyTimeout: time.Second, MetricsPath: "/metrics",
		},
		logger.New(io.Discard, "error", "json"),
		Observability{Build: buildinfo.Info{Version: "1.0.0"}},
		rejectingAuth{},
		contractTasks{},
		contractUsers{},
	)

	req := httptest.NewRequest(fiber.MethodGet, "/api/v1/tasks", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	raw, _ := io.ReadAll(resp.Body)
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error response is not JSON: %v (%s)", err, raw)
	}

	schema := spec.schemaFor(t, "/tasks", fiber.MethodGet, "401")
	for _, problem := range spec.validate(t, "error response", schema, body) {
		t.Error(problem)
	}
}

type rejectingAuth struct{ contractAuth }

func (rejectingAuth) ValidateToken(context.Context, string) (domain.Claims, error) {
	return domain.Claims{}, apperr.ErrUnauthorized
}

func TestOpenAPI_ListEndpointsReturnThePageEnvelope(t *testing.T) {
	app := contractApp(t)

	for _, path := range []string{"/api/v1/tasks", "/api/v1/users"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodGet, path, nil)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer contract-test-token")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			raw, _ := io.ReadAll(resp.Body)

			var envelope struct {
				Items  *[]any `json:"items"`
				Total  *int   `json:"total"`
				Limit  *int   `json:"limit"`
				Offset *int   `json:"offset"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("not a page envelope: %v (%s)", err, raw)
			}
			if envelope.Items == nil || envelope.Total == nil || envelope.Limit == nil || envelope.Offset == nil {
				t.Fatalf("the envelope is missing fields: %s", raw)
			}
			if len(*envelope.Items) != 1 || *envelope.Total != 1 || *envelope.Limit != 20 {
				t.Errorf("envelope = %s, want one item with total 1 and limit 20", raw)
			}
		})
	}
}
