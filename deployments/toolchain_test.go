package deployments

import (
	"os"
	"regexp"
	"testing"
)

const (
	dockerfilePath = "Dockerfile"
	workflowPath   = "../.github/workflows/ci.yml"
)

func TestBuildImageUsesTheGoLineCIVerifies(t *testing.T) {
	image := dockerfileGoLine(t)
	verified := workflowGoLine(t)

	if image != verified {
		t.Errorf("the image is built with Go %s but CI verifies Go %s;\n"+
			"whatever govulncheck reported was never said about the binary that ships",
			image, verified)
	}
}

var dockerfileGo = regexp.MustCompile(`(?m)^FROM golang:(\d+\.\d+)[.\w-]*\s+AS build`)

func dockerfileGoLine(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfilePath, err)
	}

	match := dockerfileGo.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s has no 'FROM golang:<version> AS build' stage to read a Go version from", dockerfilePath)
	}
	return string(match[1])
}

var workflowGo = regexp.MustCompile(`(?m)^\s*GO_VERSION:\s*"?(\d+\.\d+)[.\w-]*"?\s*$`)

func workflowGoLine(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		// Nothing to compare in a copy without the workflow.
		t.Skipf("no workflow at %s: %v", workflowPath, err)
	}

	match := workflowGo.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s declares no GO_VERSION", workflowPath)
	}
	return string(match[1])
}

func TestNeitherPlacePinsAPatchRelease(t *testing.T) {
	pinned := regexp.MustCompile(`(?m)^FROM golang:\d+\.\d+\.\d+`)

	raw, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfilePath, err)
	}
	if pinned.Match(raw) {
		t.Error("the Dockerfile pins an exact Go patch release; it will stop picking up standard-library fixes and nothing will say so")
	}
}
