package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	required = append(required, toStrings(schema["required"])...)

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

	// null is a value like any other, and the document has to allow it. A
	// checker that skips nil here cannot tell a property the document marks
	// nullable from one the service forgot to fill in.
	if value == nil {
		if nullable, _ := resolved["nullable"].(bool); nullable {
			return nil
		}
		return []string{fmt.Sprintf("%s: the service sent null, which the document does not allow", where)}
	}

	switch {
	case kind == "array" || resolved["items"] != nil:
		items, ok := value.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: the document says array, the service sent %T", where, value)}
		}

		problems := boundsOnCount(where, "items", len(items), resolved["minItems"], resolved["maxItems"])

		itemSchema, _ := resolved["items"].(map[string]any)
		if itemSchema == nil {
			return problems
		}
		for i, item := range items {
			problems = append(problems, s.validate(t, fmt.Sprintf("%s[%d]", where, i), itemSchema, item)...)
		}
		return problems

	case kind == "object" || len(props) > 0:
		object, ok := value.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: the document says object, the service sent %T", where, value)}
		}

		problems := boundsOnCount(where, "properties", len(object), resolved["minProperties"], resolved["maxProperties"])

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
	if kind == "" {
		return nil
	}

	switch kind {
	case "string":
		text, ok := value.(string)
		if !ok {
			return []string{fmt.Sprintf("%s: the document says string, the service sent %T", where, value)}
		}
		return validateString(where, schema, text)

	case "integer", "number":
		number, ok := value.(float64)
		if !ok {
			return []string{fmt.Sprintf("%s: the document says %s, the service sent %T", where, kind, value)}
		}
		// JSON has one numeric type; the document has two. A client that
		// reads an id into an int is why the difference matters.
		if kind == "integer" && number != math.Trunc(number) {
			return []string{fmt.Sprintf("%s: the document says integer, the service sent %v", where, number)}
		}
		return validateNumber(where, schema, number)

	case "boolean":
		if _, ok := value.(bool); !ok {
			return []string{fmt.Sprintf("%s: the document says boolean, the service sent %T", where, value)}
		}
	}

	return nil
}

func validateString(where string, schema map[string]any, text string) []string {
	if allowed := toStrings(schema["enum"]); len(allowed) > 0 && !slices.Contains(allowed, text) {
		return []string{fmt.Sprintf("%s: %q is not one of the documented values %v", where, text, allowed)}
	}

	problems := boundsOnCount(where, "characters", utf8.RuneCountInString(text),
		schema["minLength"], schema["maxLength"])

	if expression, ok := schema["pattern"].(string); ok {
		matched, err := regexp.MatchString(expression, text)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: the document's pattern %q does not compile: %v", where, expression, err))
		case !matched:
			problems = append(problems, fmt.Sprintf("%s: %q does not match the documented pattern %q", where, text, expression))
		}
	}

	// A format the document declares is a promise to whoever parses the
	// value. An unknown format is not an error - OpenAPI allows any string -
	// so only the ones this document actually uses are judged.
	switch format, _ := schema["format"].(string); format {
	case "date-time":
		if _, err := time.Parse(time.RFC3339, text); err != nil {
			problems = append(problems, fmt.Sprintf("%s: the document says date-time, %q is not RFC 3339", where, text))
		}
	case "date":
		if _, err := time.Parse(time.DateOnly, text); err != nil {
			problems = append(problems, fmt.Sprintf("%s: the document says date, %q is not YYYY-MM-DD", where, text))
		}
	case "email":
		if parsed, err := mail.ParseAddress(text); err != nil || parsed.Address != text {
			problems = append(problems, fmt.Sprintf("%s: the document says email, %q is not a bare address", where, text))
		}
	case "uri":
		if parsed, err := url.Parse(text); err != nil || !parsed.IsAbs() {
			problems = append(problems, fmt.Sprintf("%s: the document says uri, %q is not an absolute URI", where, text))
		}
	}

	return problems
}

func validateNumber(where string, schema map[string]any, number float64) []string {
	var problems []string

	// OpenAPI 3.0 spells the exclusive bounds as booleans beside minimum and
	// maximum; 3.1 spells them as numbers of their own. Both are read, so
	// the checker does not quietly ignore a bound written the other way.
	if limit, ok := asFloat(schema["minimum"]); ok {
		exclusive, _ := schema["exclusiveMinimum"].(bool)
		if (exclusive && number <= limit) || (!exclusive && number < limit) {
			problems = append(problems, fmt.Sprintf("%s: %v is below the documented minimum %v", where, number, limit))
		}
	}
	if limit, ok := asFloat(schema["exclusiveMinimum"]); ok && number <= limit {
		problems = append(problems, fmt.Sprintf("%s: %v is not above the documented exclusive minimum %v", where, number, limit))
	}
	if limit, ok := asFloat(schema["maximum"]); ok {
		exclusive, _ := schema["exclusiveMaximum"].(bool)
		if (exclusive && number >= limit) || (!exclusive && number > limit) {
			problems = append(problems, fmt.Sprintf("%s: %v is above the documented maximum %v", where, number, limit))
		}
	}
	if limit, ok := asFloat(schema["exclusiveMaximum"]); ok && number >= limit {
		problems = append(problems, fmt.Sprintf("%s: %v is not below the documented exclusive maximum %v", where, number, limit))
	}

	return problems
}

// boundsOnCount covers every min/max pair that counts something: characters
// in a string, items in an array, properties in an object.
func boundsOnCount(where, unit string, count int, min, max any) []string {
	var problems []string
	if limit, ok := asFloat(min); ok && float64(count) < limit {
		problems = append(problems, fmt.Sprintf("%s: %d %s, the document requires at least %v", where, count, unit, limit))
	}
	if limit, ok := asFloat(max); ok && float64(count) > limit {
		problems = append(problems, fmt.Sprintf("%s: %d %s, the document allows at most %v", where, count, unit, limit))
	}
	return problems
}

// YAML numbers arrive as int or float64 depending on how they were written.
func asFloat(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
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

func (contractTasks) Create(context.Context, domain.Claims, string, string) (domain.Task, error) {
	return sampleTask(), nil
}
func (contractTasks) Get(context.Context, domain.Claims, int64) (domain.Task, error) {
	return sampleTask(), nil
}
func (contractTasks) List(context.Context, domain.Claims, domain.TaskFilter) (domain.Page[domain.Task], error) {
	return domain.NewPage([]domain.Task{sampleTask()}, 1, domain.PageRequest{Limit: 20, Offset: 0}), nil
}
func (contractTasks) Update(context.Context, domain.Claims, int64, domain.TaskUpdate) (domain.Task, error) {
	return sampleTask(), nil
}
func (contractTasks) Delete(context.Context, domain.Claims, int64) error { return nil }
func (contractTasks) ToggleStatus(context.Context, domain.Claims, int64) (domain.Task, error) {
	task := sampleTask()
	task.Status = domain.StatusInProgress
	return task, nil
}

type contractUsers struct{}

func (contractUsers) Get(context.Context, domain.Claims, int64) (domain.User, error) {
	return sampleUser(), nil
}
func (contractUsers) List(context.Context, domain.Claims, domain.PageRequest) (domain.Page[domain.User], error) {
	return domain.NewPage([]domain.User{sampleUser()}, 1, domain.PageRequest{Limit: 20, Offset: 0}), nil
}
func (contractUsers) Update(context.Context, domain.Claims, int64, domain.UserEdit) (domain.User, error) {
	return sampleUser(), nil
}
func (contractUsers) Delete(context.Context, domain.Claims, int64) error { return nil }

func contractApp(t *testing.T) *fiber.App {
	t.Helper()

	return New(
		context.Background(),
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
		context.Background(),
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

// The code is what a client branches on, so it is checked against the
// document's own list rather than against whatever the handler happens to
// send today.
func TestOpenAPI_ErrorCodesAreTheDocumentedOnes(t *testing.T) {
	spec := loadSpec(t)
	app := contractApp(t)

	for _, tc := range []struct {
		name       string
		method     string
		url        string
		body       string
		specPath   string
		status     string
		wantCode   string
		wantFields []string
	}{
		{
			name: "missing required field", method: fiber.MethodPost, url: "/api/v1/tasks", body: `{}`,
			specPath: "/tasks", status: "400", wantCode: "validation_error", wantFields: []string{"title"},
		},
		{
			name: "unparseable path parameter", method: fiber.MethodGet, url: "/api/v1/tasks/not-a-number",
			specPath: "/tasks/{id}", status: "400", wantCode: "validation_error",
		},
		{
			name: "unknown route", method: fiber.MethodGet, url: "/api/v1/nothing-here",
			specPath: "/tasks/{id}", status: "404", wantCode: "not_found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			req.Header.Set(fiber.HeaderAuthorization, "Bearer contract-test-token")
			if tc.body != "" {
				req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			raw, _ := io.ReadAll(resp.Body)

			var body any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("error response is not JSON: %v (%s)", err, raw)
			}
			for _, problem := range spec.validate(t, "error response", spec.schemaFor(t, tc.specPath, tc.method, tc.status), body) {
				t.Error(problem)
			}

			var envelope struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Fields  []struct {
					Field string `json:"field"`
				} `json:"fields"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("not an error envelope: %v (%s)", err, raw)
			}
			if envelope.Code != tc.wantCode {
				t.Errorf("code = %q, want %q (%s)", envelope.Code, tc.wantCode, raw)
			}
			if envelope.Message == "" {
				t.Errorf("the envelope carries no message: %s", raw)
			}

			var got []string
			for _, f := range envelope.Fields {
				got = append(got, f.Field)
			}
			if !slices.Equal(got, tc.wantFields) {
				t.Errorf("fields = %v, want %v (%s)", got, tc.wantFields, raw)
			}
		})
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

func TestOpenAPIChecker_CatchesWhatItClaimsTo(t *testing.T) {
	spec := &openAPI{}

	for _, tc := range []struct {
		name   string
		schema map[string]any
		value  any
		want   string // a fragment the complaint must contain; "" means accept
	}{
		{
			name:   "a whole number where the document says integer",
			schema: map[string]any{"type": "integer"},
			value:  float64(7),
		},
		{
			name:   "a fraction where the document says integer",
			schema: map[string]any{"type": "integer"},
			value:  7.5,
			want:   "the document says integer",
		},
		{
			name:   "a fraction where the document says number",
			schema: map[string]any{"type": "number"},
			value:  7.5,
		},
		{
			name:   "null where the document does not allow it",
			schema: map[string]any{"type": "string"},
			value:  nil,
			want:   "the service sent null",
		},
		{
			name:   "null where the document allows it",
			schema: map[string]any{"type": "string", "nullable": true},
			value:  nil,
		},
		{
			name:   "a timestamp that is not RFC 3339",
			schema: map[string]any{"type": "string", "format": "date-time"},
			value:  "2026-09-20 11:00:00",
			want:   "is not RFC 3339",
		},
		{
			name:   "a timestamp that is",
			schema: map[string]any{"type": "string", "format": "date-time"},
			value:  "2026-09-20T11:00:00Z",
		},
		{
			name:   "an address with a display name is not an email",
			schema: map[string]any{"type": "string", "format": "email"},
			value:  "Mary <mary@example.com>",
			want:   "is not a bare address",
		},
		{
			name:   "a string longer than the document allows",
			schema: map[string]any{"type": "string", "maxLength": 3},
			value:  "abcd",
			want:   "the document allows at most 3",
		},
		{
			name:   "length is counted in characters, not bytes",
			schema: map[string]any{"type": "string", "maxLength": 3},
			value:  "ЙЦУ",
		},
		{
			name:   "a number below the documented minimum",
			schema: map[string]any{"type": "integer", "minimum": 1},
			value:  float64(0),
			want:   "below the documented minimum",
		},
		{
			name:   "a number at an exclusive minimum written the 3.1 way",
			schema: map[string]any{"type": "integer", "exclusiveMinimum": 0},
			value:  float64(0),
			want:   "not above the documented exclusive minimum",
		},
		{
			name:   "a string that does not match the documented pattern",
			schema: map[string]any{"type": "string", "pattern": "^v[0-9]+$"},
			value:  "version-2",
			want:   "does not match the documented pattern",
		},
		{
			name:   "fewer items than the document requires",
			schema: map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
			value:  []any{},
			want:   "the document requires at least 1",
		},
		{
			name:   "a value outside the documented enum",
			schema: map[string]any{"type": "string", "enum": []any{"created", "completed"}},
			value:  "archived",
			want:   "is not one of the documented values",
		},
		{
			name: "a property the document does not describe",
			schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "integer"}},
			},
			value: map[string]any{"id": float64(1), "password_hash": "leaked"},
			want:  "which the document does not describe",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := spec.validate(t, "value", tc.schema, tc.value)

			if tc.want == "" {
				if len(problems) > 0 {
					t.Fatalf("the checker rejected a value the document allows: %v", problems)
				}
				return
			}
			if len(problems) == 0 {
				t.Fatalf("the checker accepted %#v, which the document does not allow", tc.value)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("complaint %v does not mention %q", problems, tc.want)
			}
		})
	}
}
