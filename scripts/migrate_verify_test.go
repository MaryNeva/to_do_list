package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeTools(t *testing.T, createSucceeds bool) (binDir, logPath string) {
	t.Helper()

	binDir = t.TempDir()
	logPath = filepath.Join(binDir, "psql.log")

	createExit := "1"
	if createSucceeds {
		createExit = "0"
	}

	psql := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$*" in
  *"CREATE DATABASE"*) exit ` + createExit + ` ;;
  *"FROM pg_database"*) echo 0; exit 0 ;;
esac
exit 0
`
	// The script asks the service for its own DSN before anything else.
	goStub := `#!/bin/sh
case "$*" in
  "run ./cmd/dsn") echo "postgres://postgres@127.0.0.1:5432/to_do?sslmode=disable" ;;
esac
exit 0
`
	for name, body := range map[string]string{"psql": psql, "go": goStub} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return binDir, logPath
}

func runScript(t *testing.T, binDir, verifyDB, migrateBin string) string {
	t.Helper()

	cmd := exec.Command("bash", "migrate-verify.sh")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERIFY_DB="+verifyDB,
		"MIGRATE_BIN="+migrateBin,
		"MIGRATE_VERIFY_ADMIN_URL=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable",
	)

	out, _ := cmd.CombinedOutput() // a non-zero exit is the point of these cases
	return string(out)
}

func readLog(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func TestMigrateVerify_DoesNotDropADatabaseItFailedToCreate(t *testing.T) {
	binDir, logPath := fakeTools(t, false)

	output := runScript(t, binDir, "someone_elses_database", "/bin/true")
	log := readLog(t, logPath)

	if !strings.Contains(log, "CREATE DATABASE") {
		t.Fatalf("the script never tried to create a database:\n%s\n%s", log, output)
	}
	if strings.Contains(log, "DROP DATABASE") {
		t.Errorf("the script dropped a database it did not create:\n%s", log)
	}
}

// The other half of the same contract: what this run did create, it removes.
func TestMigrateVerify_DropsTheDatabaseItCreated(t *testing.T) {
	binDir, logPath := fakeTools(t, true)

	output := runScript(t, binDir, "to_do_migrate_verify_selftest", "/bin/false")
	log := readLog(t, logPath)

	if !strings.Contains(log, "CREATE DATABASE") {
		t.Fatalf("the script never created its database:\n%s\n%s", log, output)
	}
	if !strings.Contains(log, "DROP DATABASE") {
		t.Errorf("the script left its own throwaway database behind:\n%s", log)
	}
}

func TestMigrateVerify_RefusesANameThatIsNotAnIdentifier(t *testing.T) {
	binDir, logPath := fakeTools(t, true)

	output := runScript(t, binDir, `x"; DROP DATABASE postgres; --`, "/bin/true")

	if !strings.Contains(output, "not a plain SQL identifier") {
		t.Errorf("output does not name the reason:\n%s", output)
	}
	if log := readLog(t, logPath); log != "" {
		t.Errorf("the script reached psql with a name it should have refused:\n%s", log)
	}
}
