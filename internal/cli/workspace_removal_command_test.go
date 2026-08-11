package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "herdr" {
		os.Exit(runFakeHerdrCommand(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func TestRunRemoveReturnsToPrimaryWhenClosedHerdrWorkspaceDiffersFromCaller(t *testing.T) {
	repo, worktrees := setupRemovalCommandRepo(t, "target")
	closeLog := installFakeHerdr(t)
	setRemovalCommandState(t, worktrees["target"], true, false)
	t.Setenv("HERDR_WORKSPACE_ID", "caller-workspace")
	t.Setenv("HERDR_LIST_RESPONSE", workspaceListResponse(map[string]string{
		worktrees["target"]: "other-workspace",
	}))

	if err := runRemove(nil, []string{"target"}); err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
	assertCurrentDirectory(t, repo)
	if got := readCloseLog(t, closeLog); !reflect.DeepEqual(got, []string{"other-workspace"}) {
		t.Fatalf("closed workspace IDs = %q, want [other-workspace]", got)
	}
}

func TestRunCleanProcessesCurrentWorktreeLastAndContinuesAfterCloseFailure(t *testing.T) {
	currentName, laterName := "aaa-current", "zzz-later"
	_, worktrees := setupRemovalCommandRepo(t, currentName, laterName)
	closeLog := installFakeHerdr(t)
	setRemovalCommandState(t, worktrees[currentName], false, true)
	t.Setenv("HERDR_WORKSPACE_ID", "caller-workspace")
	t.Setenv("HERDR_FAIL_CLOSE_ID", "later-workspace")
	t.Setenv("HERDR_LIST_RESPONSE", workspaceListResponse(map[string]string{
		worktrees[currentName]: "caller-workspace",
		worktrees[laterName]:   "later-workspace",
	}))

	if err := runClean(nil, nil); err != nil {
		t.Fatalf("runClean() error = %v", err)
	}
	assertPathAbsent(t, worktrees[currentName])
	assertPathAbsent(t, worktrees[laterName])
	if got := readCloseLog(t, closeLog); !reflect.DeepEqual(got, []string{"later-workspace", "caller-workspace"}) {
		t.Fatalf("closed workspace IDs = %q, want later worktree before current worktree", got)
	}
}

func TestRunCleanReturnsToPrimaryWhenCurrentWorktreeHasNoWorkspaceMatch(t *testing.T) {
	repo, worktrees := setupRemovalCommandRepo(t, "current")
	installFakeHerdr(t)
	setRemovalCommandState(t, worktrees["current"], false, true)
	t.Setenv("HERDR_WORKSPACE_ID", "caller-workspace")
	t.Setenv("HERDR_LIST_RESPONSE", `{"result":{"type":"workspace_list","workspaces":[]}}`)

	if err := runClean(nil, nil); err != nil {
		t.Fatalf("runClean() error = %v", err)
	}
	assertCurrentDirectory(t, repo)
}

func TestRunCleanReturnsToPrimaryWhenCurrentWorkspaceCloseFails(t *testing.T) {
	repo, worktrees := setupRemovalCommandRepo(t, "current")
	installFakeHerdr(t)
	setRemovalCommandState(t, worktrees["current"], false, true)
	t.Setenv("HERDR_WORKSPACE_ID", "caller-workspace")
	t.Setenv("HERDR_FAIL_CLOSE_ID", "caller-workspace")
	t.Setenv("HERDR_LIST_RESPONSE", workspaceListResponse(map[string]string{
		worktrees["current"]: "caller-workspace",
	}))

	if err := runClean(nil, nil); err != nil {
		t.Fatalf("runClean() error = %v", err)
	}
	assertCurrentDirectory(t, repo)
}

func setupRemovalCommandRepo(t *testing.T, names ...string) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	runRemovalGit(t, root, "init", "-q", "--initial-branch=main", repo)
	runRemovalGit(t, repo, "config", "user.email", "test@example.invalid")
	runRemovalGit(t, repo, "config", "user.name", "Test")
	runRemovalGit(t, repo, "commit", "-q", "--allow-empty", "-m", "initial")

	worktrees := make(map[string]string, len(names))
	for _, name := range names {
		path := filepath.Join(repo, ".worktrees", name)
		runRemovalGit(t, repo, "worktree", "add", "-q", "-b", name, path, "main")
		worktrees[name] = path
	}
	return repo, worktrees
}

func runRemovalGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func installFakeHerdr(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	closeLog := filepath.Join(t.TempDir(), "close.log")
	path := filepath.Join(binDir, "herdr")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	if err := os.Symlink(testBinary, path); err != nil {
		t.Fatalf("install fake herdr: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_CLOSE_LOG", closeLog)
	t.Setenv("HERDR_FAIL_CLOSE_ID", "")
	return closeLog
}

func runFakeHerdrCommand(args []string) int {
	if reflect.DeepEqual(args, []string{"workspace", "list"}) {
		fmt.Println(os.Getenv("HERDR_LIST_RESPONSE"))
		return 0
	}
	if len(args) == 3 && args[0] == "workspace" && args[1] == "close" {
		file, err := os.OpenFile(os.Getenv("HERDR_CLOSE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		_, writeErr := fmt.Fprintln(file, args[2])
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			fmt.Fprintln(os.Stderr, "failed to record workspace close")
			return 2
		}
		if failID := os.Getenv("HERDR_FAIL_CLOSE_ID"); failID != "" && args[2] == failID {
			fmt.Println("close denied")
			return 1
		}
		output, _ := json.Marshal(map[string]any{"result": map[string]string{"type": "ok"}})
		fmt.Println(string(output))
		return 0
	}
	fmt.Fprintln(os.Stderr, "unexpected herdr invocation")
	return 2
}

func setRemovalCommandState(t *testing.T, cwd string, removeForce, forceClean bool) {
	t.Helper()
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get original working directory: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir %s: %v", cwd, err)
	}
	originalForceRemove, originalCleanForce, originalCleanDry := forceRemove, cleanForce, cleanDry
	forceRemove, cleanForce, cleanDry = removeForce, forceClean, false
	t.Setenv("YGG_SHELL", "1")
	t.Cleanup(func() {
		forceRemove, cleanForce, cleanDry = originalForceRemove, originalCleanForce, originalCleanDry
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func workspaceListResponse(workspaces map[string]string) string {
	entries := make([]string, 0, len(workspaces))
	for path, id := range workspaces {
		entries = append(entries, fmt.Sprintf(`{"workspace_id":%q,"worktree":{"checkout_path":%q}}`, id, path))
	}
	return fmt.Sprintf(`{"result":{"type":"workspace_list","workspaces":[%s]}}`, strings.Join(entries, ","))
}

func readCloseLog(t *testing.T, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read close log: %v", err)
	}
	return strings.Fields(string(contents))
}

func assertCurrentDirectory(t *testing.T, want string) {
	t.Helper()
	got, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() after command: %v", err)
	}
	if got != want {
		t.Fatalf("working directory after command = %q, want %q", got, want)
	}
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path %q still exists (stat error %v)", path, err)
	}
}
