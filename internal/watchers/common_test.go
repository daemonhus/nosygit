package watchers

import (
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"testing"
)

func getTestChain() *CommitChain {
	thirdCommit := generateCommit("de37fd6a883ff6852ed33cee33119f6273200a09")
	thirdChain := &CommitChain{
		Curr: thirdCommit,
		Prev: nil,
		Next: nil,
	}
	secondCommit := generateCommit("de27fd6a883ff6852ed33cee33119f6273200a09")
	secondChain := &CommitChain{
		Curr: secondCommit,
		Prev: nil,
		Next: nil,
	}
	firstCommit := generateCommit("de17fd6a883ff6852ed33cee33119f6273200a09")
	firstChain := &CommitChain{
		Curr: firstCommit,
		Prev: nil,
		Next: nil,
	}

	thirdChain.Prev = secondChain
	secondChain.Next = thirdChain
	secondChain.Prev = firstChain
	firstChain.Next = secondChain

	return thirdChain
}

func generateCommit(s string) *object.Commit {
	return &object.Commit{
		Hash: plumbing.NewHash(s),
	}
}

func TestChainFromCommitSelf(t *testing.T) {
	chain := getTestChain()
	testChain := chain.ChainFromCommit("de37fd6a883ff6852ed33cee33119f6273200a09")

	// When cutting at HEAD commit, should have no previous commits
	if testChain.Prev != nil {
		t.Errorf("Expected no previous commits when cutting at HEAD, but found previous commit")
	}

	// Should still be the HEAD commit
	if testChain.Curr.Hash.String() != "de37fd6a883ff6852ed33cee33119f6273200a09" {
		t.Errorf("Expected HEAD commit hash, got %s", testChain.Curr.Hash.String())
	}
}

func TestChainFromCommitBack(t *testing.T) {
	chain := getTestChain()
	testChain := chain.ChainFromCommit("de17fd6a883ff6852ed33cee33119f6273200a09")

	// Count commits by traversing forward through Next pointers
	cnt := 1
	current := &testChain
	for current.Next != nil {
		cnt += 1
		current = current.Next
	}

	if cnt != 3 {
		t.Errorf("Expected 3 commits when cutting from first commit, got %d", cnt)
	}

	// Verify we start at the first commit
	if testChain.Curr.Hash.String() != "de17fd6a883ff6852ed33cee33119f6273200a09" {
		t.Errorf("Expected first commit hash, got %s", testChain.Curr.Hash.String())
	}

	// Verify we have no previous commit (chain was cut)
	if testChain.Prev != nil {
		t.Errorf("Expected no previous commits when cutting at first commit, but found one")
	}
}
