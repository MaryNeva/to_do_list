package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictYAML(t *testing.T) {
	for _, content := range []string{
		"unknown: true", "server:\n  typo: 1", "server:\n  address: x\n  address: y",
		"server: [", "run_migrations: invalid", "run_migrations: true\n---\nrun_migrations: false",
	} {
		t.Run(content, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(p, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readYAML(p); err == nil {
				t.Fatal("invalid YAML accepted")
			}
		})
	}
}
func TestStrictEnv(t *testing.T) {
	for _, content := range []string{"INVALID", "A=one\nA=two", "A=\"unclosed", "1KEY=value"} {
		p := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(p, []byte(content), 0600)
		if _, err := ReadEnvFile(p); err == nil {
			t.Fatalf("invalid input accepted: %q", content)
		}
	}
}
func TestCommandEnvironmentLiteralValuesAndPrecedence(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	literal := `$2a$12$hash $(touch never) "quotes" and spaces # literal`
	os.WriteFile(p, []byte("SECRET='"+literal+"'\nOVERRIDE=file\nEMPTY=file\n"), 0600)
	entries, err := CommandEnvironment(p, []string{"OVERRIDE=environment", "EMPTY="})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		k, v, _ := strings.Cut(e, "=")
		got[k] = v
	}
	if got["SECRET"] != literal || got["OVERRIDE"] != "environment" || got["EMPTY"] != "file" {
		t.Fatal("literal values or precedence changed")
	}
}
