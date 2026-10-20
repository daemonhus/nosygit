package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"nosygit/internal/config"
)

// isolatedHome keeps tests away from the developer's own gitconfig and ignores.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func openClone(t *testing.T, dir string) *git.Repository {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// fetchOnly mirrors what polling does before the fast-forward step.
func fetchOnly(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := runFetch(openClone(t, dir), nil, args...); err != nil {
		t.Fatalf("fetch: %v", err)
	}
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	ref, err := openClone(t, dir).Head()
	if err != nil {
		t.Fatal(err)
	}
	return ref.Hash().String()
}

func TestFastForwardIgnoresUnrelatedUntrackedFiles(t *testing.T) {
	isolatedHome(t)
	src := newSourceRepo(t)
	clone, oldHead, _ := cloneForPolling(t, src)
	stray := filepath.Join(clone, ".DS_Store")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	newHead := commitFile(t, src, "second.txt", "two", "second")
	fetchOnly(t, clone)

	res, err := fastForwardIfClean(openClone(t, clone), oldHead)
	if err != nil || !res.Advanced {
		t.Fatalf("an unrelated untracked file must not block: %+v err=%v", res, err)
	}
	if headOf(t, clone) != newHead {
		t.Fatal("branch should have moved to origin")
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("untracked file must be preserved: %v", err)
	}
}

func TestFastForwardBlockedWhenUntrackedFileWouldBeOverwritten(t *testing.T) {
	isolatedHome(t)
	src := newSourceRepo(t)
	clone, oldHead, _ := cloneForPolling(t, src)
	mine := filepath.Join(clone, "second.txt")
	if err := os.WriteFile(mine, []byte("my version"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFile(t, src, "second.txt", "upstream version", "second")
	fetchOnly(t, clone)

	res, err := fastForwardIfClean(openClone(t, clone), oldHead)
	if err != nil || res.Advanced || !strings.Contains(res.Reason, "second.txt") {
		t.Fatalf("expected a block naming the file, got %+v err=%v", res, err)
	}
	if data, _ := os.ReadFile(mine); string(data) != "my version" {
		t.Fatalf("untracked file was overwritten: %q", data)
	}
	if headOf(t, clone) != oldHead {
		t.Fatal("branch must not move when blocked")
	}
}

func TestFastForwardHonoursGlobalIgnoreFile(t *testing.T) {
	home := isolatedHome(t)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\texcludesFile = ~/.gitignore_global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitignore_global"), []byte("# local tooling\n**/.claude/settings.local.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := newSourceRepo(t)
	clone, oldHead, _ := cloneForPolling(t, src)
	local := filepath.Join(clone, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An untracked file that would collide, to prove the ignore is what lets it through.
	commitFile(t, src, "second.txt", "two", "second")
	fetchOnly(t, clone)

	res, err := fastForwardIfClean(openClone(t, clone), oldHead)
	if err != nil || !res.Advanced {
		t.Fatalf("a globally ignored file must not block: %+v err=%v", res, err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("ignored file must be preserved: %v", err)
	}
}

func TestFastForwardReportsReasons(t *testing.T) {
	isolatedHome(t)

	t.Run("modified tracked file", func(t *testing.T) {
		src := newSourceRepo(t)
		clone, oldHead, _ := cloneForPolling(t, src)
		if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("edited"), 0o644); err != nil {
			t.Fatal(err)
		}
		commitFile(t, src, "second.txt", "two", "second")
		fetchOnly(t, clone)

		res, _ := fastForwardIfClean(openClone(t, clone), oldHead)
		if res.Advanced || !strings.Contains(res.Reason, "tracked files") {
			t.Fatalf("got %+v", res)
		}
	})

	t.Run("local commits", func(t *testing.T) {
		src := newSourceRepo(t)
		clone, oldHead, _ := cloneForPolling(t, src)
		localHead := commitFile(t, clone, "local.txt", "mine", "local work")
		commitFile(t, src, "second.txt", "two", "second")
		fetchOnly(t, clone)

		res, _ := fastForwardIfClean(openClone(t, clone), oldHead)
		if res.Advanced || !strings.Contains(res.Reason, "local commits") {
			t.Fatalf("got %+v", res)
		}
		if headOf(t, clone) != localHead {
			t.Fatal("local commit must be kept")
		}
	})
}

// A shallow clone that falls further behind than the fetch depth can no longer
// see its own tip in origin's history. Its tip equalling the recorded head
// proves there are no local commits, so it must still catch up.
func TestFastForwardCatchesUpShallowCloneBeyondDepth(t *testing.T) {
	isolatedHome(t)
	src := newSourceRepo(t)
	shallow := config.ShallowConfig{Enabled: true, Depth: 5}
	clonePath := t.TempDir()
	ctx := safetyCtxWithShallow(shallow)
	oldHead, err := cloneIfNeeded(ctx, "acme/repo", src, clonePath)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	clone := filepath.Join(clonePath, "acme", "repo")

	var newHead string
	for i := 0; i < 12; i++ {
		newHead = commitFile(t, src, fmt.Sprintf("f%02d.txt", i), "x", fmt.Sprintf("c%02d", i))
	}
	fetchOnly(t, clone, "--depth", "5", "origin")

	// Without the recorded head the clone cannot be proven free of local commits.
	res, _ := fastForwardIfClean(openClone(t, clone), "")
	if res.Advanced || !strings.Contains(res.Reason, "shallow") {
		t.Fatalf("expected a shallow-history reason without a recorded head, got %+v", res)
	}

	res, err = fastForwardIfClean(openClone(t, clone), oldHead)
	if err != nil || !res.Advanced {
		t.Fatalf("a tip equal to the recorded head must fast-forward: %+v err=%v", res, err)
	}
	if headOf(t, clone) != newHead {
		t.Fatal("branch should have caught up")
	}
}

func TestFastForwardAppliesModifyDeleteAndNestedChanges(t *testing.T) {
	isolatedHome(t)
	src := newSourceRepo(t)
	commitFile(t, src, "docs/old.txt", "old", "add docs")
	clone, oldHead, _ := cloneForPolling(t, src)

	// One commit that modifies, deletes and adds nested, executable files.
	repo := openClone(t, src)
	wt, _ := repo.Worktree()
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Remove("docs/old.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "bin", "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "tools", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("bin/tools/run.sh"); err != nil {
		t.Fatal(err)
	}
	newHead := commitStaged(t, wt, "mixed changes")
	fetchOnly(t, clone)

	res, err := fastForwardIfClean(openClone(t, clone), oldHead)
	if err != nil || !res.Advanced {
		t.Fatalf("fast-forward: %+v err=%v", res, err)
	}
	if headOf(t, clone) != newHead {
		t.Fatal("branch should be at origin")
	}
	if data, _ := os.ReadFile(filepath.Join(clone, "README.md")); string(data) != "changed" {
		t.Fatalf("modified file not updated: %q", data)
	}
	if _, err := os.Stat(filepath.Join(clone, "docs")); !os.IsNotExist(err) {
		t.Fatalf("deleted file and its empty directory should be gone: %v", err)
	}
	info, err := os.Stat(filepath.Join(clone, "bin", "tools", "run.sh"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("nested executable missing or not executable: %v %v", info, err)
	}
	cw, _ := openClone(t, clone).Worktree()
	if status, err := cw.Status(); err != nil || !status.IsClean() {
		t.Fatalf("clone should be clean afterwards: %v (err %v)", status, err)
	}
}

func TestHasGitComponent(t *testing.T) {
	for path, want := range map[string]bool{
		".git/config":         true,
		"a/.GIT/hooks/x":      true,
		"a/b.git/c":           false,
		".github/workflows/x": false,
		"gitignore":           false,
	} {
		if got := hasGitComponent(path); got != want {
			t.Fatalf("hasGitComponent(%q) = %v, want %v", path, got, want)
		}
	}
}
