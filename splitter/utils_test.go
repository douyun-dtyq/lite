package splitter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/libgit2/git2go/v34"
	"github.com/stretchr/testify/assert"
)

func TestGitDirectory(t *testing.T) {
	const worktreePath = "worktree"
	const bareRepoPath = "bare-repo"

	var err error
	pwd, err := os.Getwd()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer os.Chdir(pwd)

	tempDir := t.TempDir()
	defer os.RemoveAll(tempDir)
	os.Chdir(tempDir)

	// create a bare repo using command
	createRepoCmd := exec.Command("git", "init", "--bare", bareRepoPath)
	createRepoCmd.Dir = tempDir
	err = createRepoCmd.Run()
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	// create a worktree repo using command
	err = os.Mkdir(filepath.Join(tempDir, worktreePath), 0755)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	createWorktreeCmd := exec.Command("git", "init", worktreePath)
	createWorktreeCmd.Dir = tempDir
	err = createWorktreeCmd.Run()
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	t.Run("BareRepo", func(t *testing.T) {
		os.Chdir(filepath.Join(tempDir, bareRepoPath))
		assert.Equal(t, GitDirectory(bareRepoPath), bareRepoPath)
	})
	t.Run("WorktreeRepo", func(t *testing.T) {
		os.Chdir(filepath.Join(tempDir, worktreePath))
		assert.Equal(t, GitDirectory(worktreePath), worktreePath)
	})
}

func TestSplitMessage(t *testing.T) {
	t.Skip("TODO")
}

func execCmd(t *testing.T, args ...string) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	if !assert.NoError(t, err) {
		t.Fatalf("err when executing command %s: %s", cmd.String(), err)
		t.FailNow()
	}
}
func execCmdOutput(t *testing.T, args ...string) string {
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.Output()
	if !assert.NoError(t, err) {
		t.Fatalf("err when executing command %s: %s", cmd.String(), err)
		t.FailNow()
	}
	return string(output)
}

func makeTestRepo(t *testing.T, tempDir string) ([]string, []string, string) {
	var err error
	// create a repo using command
	err = os.Mkdir(filepath.Join(tempDir, "test-repo"), 0755)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	err = os.Chdir(filepath.Join(tempDir, "test-repo"))
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	execCmd(t, "git", "init", "--initial-branch", "a")

	// create some commits on test branch
	const aCommits = 10
	aCommitHashes := make([]string, aCommits)
	for i := 0; i < aCommits; i++ {
		os.WriteFile("somea.code", []byte(fmt.Sprintf("test %d", i)), 0644)
		execCmd(t, "git", "add", "somea.code")
		execCmd(t, "git", "commit", "--message", fmt.Sprintf("test %d", i))
		aCommitHashes[i] = strings.TrimSpace(execCmdOutput(t, "git", "rev-parse", "HEAD"))
	}

	// create a new branch
	execCmd(t, "git", "checkout", "-b", "b")
	execCmd(t, "git", "reset", "--hard", aCommitHashes[5])

	// create some commits on b branch
	const bCommits = 10
	bCommitHashes := make([]string, bCommits)
	for i := 0; i < bCommits; i++ {
		os.WriteFile("someb.code", []byte(fmt.Sprintf("test %d", i)), 0644)
		execCmd(t, "git", "add", "someb.code")
		execCmd(t, "git", "commit", "--message", fmt.Sprintf("test %d", i))
		bCommitHashes[i] = strings.TrimSpace(execCmdOutput(t, "git", "rev-parse", "HEAD"))
	}

	// merge b branch into a
	execCmd(t, "git", "merge", "b")

	// get HEAD commit hash
	headCommitHash := strings.TrimSpace(execCmdOutput(t, "git", "rev-parse", "HEAD"))

	// make some tags
	execCmd(t, "git", "tag", "lightweight")
	execCmd(t, "git", "tag", "annotated", "-m", "annotated tag")

	return aCommitHashes, bCommitHashes, headCommitHash
}

func TestNormalizeOrigin(t *testing.T) {

	var err error
	pwd, err := os.Getwd()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer os.Chdir(pwd)

	tempDir := t.TempDir()
	defer os.RemoveAll(tempDir)
	os.Chdir(tempDir)

	_, _, headCommitHash := makeTestRepo(t, tempDir)

	repo, err := git.OpenRepository(filepath.Join(tempDir, "test-repo"))
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer repo.Free()

	t.Run("HEAD", func(t *testing.T) {
		ref, err := normalizeOrigin(repo, "HEAD")
		assert.NoError(t, err)
		assert.Equal(t, "refs/heads/b", ref)

		// not on a branch, this will faild
		ref, err = normalizeOrigin(repo, "HEAD^")
		assert.Error(t, err)

		ref, err = normalizeOrigin(repo, "HEAD^2")
		assert.Error(t, err)

		ref, err = normalizeOrigin(repo, "HEAD-1")
		assert.Error(t, err)
	})

	t.Run("Tags", func(t *testing.T) {
		ref, err := normalizeOrigin(repo, "lightweight")
		assert.NoError(t, err)
		assert.Equal(t, "refs/tags/lightweight", ref)

		ref, err = normalizeOrigin(repo, "annotated")
		assert.NoError(t, err)
		assert.Equal(t, "refs/tags/annotated", ref)
	})

	t.Run("Commits", func(t *testing.T) {
		_, err := normalizeOrigin(repo, headCommitHash)
		assert.Error(t, err)
	})

	t.Run("Ref", func(t *testing.T) {
		ref, err := normalizeOrigin(repo, "refs/heads/b")
		assert.NoError(t, err)
		assert.Equal(t, "refs/heads/b", ref)

		ref, err = normalizeOrigin(repo, "refs/tags/lightweight")
		assert.NoError(t, err)
		assert.Equal(t, "refs/tags/lightweight", ref)
	})
}
