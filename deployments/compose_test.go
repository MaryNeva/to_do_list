package deployments

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	composePath              = "../docker-compose.yml"
	observabilityComposePath = "../docker-compose.observability.yml"
	prometheusPath           = "prometheus/prometheus.yml"
)

type compose struct {
	Services map[string]service `yaml:"services"`
}

type service struct {
	Ports       []string          `yaml:"ports"`
	Environment map[string]string `yaml:"environment"`
	Command     []string          `yaml:"command"`
}

// loadCompose merges the base stack with the observability one, the way
// "docker compose -f ... -f ..." does, so the checks below cover both files.
func loadCompose(t *testing.T, paths ...string) compose {
	t.Helper()

	if len(paths) == 0 {
		paths = []string{composePath, observabilityComposePath}
	}

	merged := compose{Services: map[string]service{}}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		var file compose
		if err := yaml.Unmarshal(raw, &file); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for name, svc := range file.Services {
			merged.Services[name] = svc
		}
	}
	return merged
}

func TestCompose_StartingTheAPIDoesNotRequireMonitoringSecrets(t *testing.T) {
	base := loadCompose(t, composePath)

	for name := range base.Services {
		if name == "prometheus" || name == "grafana" {
			t.Errorf("%s is in %s; a plain \"docker compose up\" would then demand its variables", name, composePath)
		}
	}

	raw, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read %s: %v", composePath, err)
	}
	// Comments are not interpolated; a "${GRAFANA...}" reference would be.
	for i, line := range strings.Split(string(raw), "\n") {
		if code, _, _ := strings.Cut(line, "#"); strings.Contains(code, "${GRAFANA") {
			t.Errorf("%s:%d references a Grafana variable, which would block starting the API alone: %s",
				composePath, i+1, strings.TrimSpace(line))
		}
	}
}

// A published port with no interface in front of it is published on every
// interface, so a laptop on café wifi serves its database to the room.
func TestCompose_PublishesNothingBeyondThisMachineByDefault(t *testing.T) {
	file := loadCompose(t)

	const wantPrefix = "${BIND_HOST:-127.0.0.1}:"

	for name, service := range file.Services {
		for _, port := range service.Ports {
			if !strings.HasPrefix(port, wantPrefix) {
				t.Errorf("%s publishes %q on every interface; it should start with %q",
					name, port, wantPrefix)
			}
		}
	}
}

// /metrics is unauthenticated, so it lives on a listener this file never
// publishes and Prometheus reaches it over the internal network.
func TestCompose_MetricsAreNotOnThePublishedPort(t *testing.T) {
	file := loadCompose(t)

	app, ok := file.Services["app"]
	if !ok {
		t.Fatal("the compose file has no app service")
	}

	address := app.Environment["METRICS_ADDRESS"]
	if address == "" {
		t.Fatal("app does not set METRICS_ADDRESS, so /metrics is served on the published API port")
	}

	port := strings.TrimPrefix(address, ":")
	for _, published := range app.Ports {
		if strings.Contains(published, ":"+port) {
			t.Errorf("app publishes %q, which exposes the metrics listener %q", published, address)
		}
	}

	raw, err := os.ReadFile(prometheusPath)
	if err != nil {
		t.Fatalf("read %s: %v", prometheusPath, err)
	}
	if !strings.Contains(string(raw), "app:"+port) {
		t.Errorf("%s does not scrape app:%s, so the separate listener is never read", prometheusPath, port)
	}
}

// A password that ships in a repository is not a password.
func TestCompose_GrafanaHasNoDefaultCredentialsAndNoAnonymousAccess(t *testing.T) {
	file := loadCompose(t)

	grafana, ok := file.Services["grafana"]
	if !ok {
		t.Fatal("the compose file has no grafana service")
	}

	admin := grafana.Environment["GF_SECURITY_ADMIN_PASSWORD"]
	if !strings.Contains(admin, ":?") {
		t.Errorf("GF_SECURITY_ADMIN_PASSWORD is %q; it must fail the stack when unset, not fall back to a value", admin)
	}
	if strings.Contains(admin, ":-") {
		t.Errorf("GF_SECURITY_ADMIN_PASSWORD is %q, which carries a default password", admin)
	}

	anonymous := grafana.Environment["GF_AUTH_ANONYMOUS_ENABLED"]
	if anonymous != "${GRAFANA_ANONYMOUS:-false}" {
		t.Errorf("GF_AUTH_ANONYMOUS_ENABLED is %q, want it off unless a person opts in", anonymous)
	}
}

// --web.enable-lifecycle lets anyone who can reach Prometheus reload or stop
// it, and it has no authentication of its own.
func TestCompose_PrometheusHasNoRemoteControl(t *testing.T) {
	file := loadCompose(t)

	prometheus, ok := file.Services["prometheus"]
	if !ok {
		t.Fatal("the compose file has no prometheus service")
	}

	for _, flag := range prometheus.Command {
		if strings.Contains(flag, "enable-lifecycle") || strings.Contains(flag, "enable-admin-api") {
			t.Errorf("prometheus is started with %q", flag)
		}
	}
}
