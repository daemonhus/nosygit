package cmd

import (
	"errors"
	"io"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type erringCommitIter struct {
	commits  []*object.Commit
	errAtPos int
	err      error
	pos      int
}

func (e *erringCommitIter) Next() (*object.Commit, error) {
	if e.pos == e.errAtPos {
		return nil, e.err
	}
	if e.pos >= len(e.commits) {
		return nil, io.EOF
	}
	c := e.commits[e.pos]
	e.pos++
	return c, nil
}

func (e *erringCommitIter) ForEach(cb func(*object.Commit) error) error {
	for {
		commit, err := e.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := cb(commit); err != nil {
			return err
		}
	}
}

func (e *erringCommitIter) Close() {}

func mkCommit(hash string) *object.Commit {
	return &object.Commit{Hash: plumbing.NewHash(hash)}
}

func TestCommitIterToChain_EmptyIter(t *testing.T) {
	iter := &erringCommitIter{
		errAtPos: 0,
		err:      io.EOF,
	}

	chain := CommitIterToChain(iter)
	if chain.Curr != nil {
		t.Fatalf("expected empty chain head, got %s", chain.Curr.Hash.String())
	}
	if chain.Prev != nil || chain.Next != nil {
		t.Fatalf("expected empty chain links, got prev=%v next=%v", chain.Prev != nil, chain.Next != nil)
	}
}

func TestCommitIterToChain_MidStreamErrorReturnsBuiltPrefix(t *testing.T) {
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	iter := &erringCommitIter{
		commits: []*object.Commit{
			mkCommit(head),
		},
		errAtPos: 1,
		err:      errors.New("iterator failed"),
	}

	chain := CommitIterToChain(iter)
	if chain.Curr == nil {
		t.Fatalf("expected head commit in chain")
	}
	if got := chain.Curr.Hash.String(); got != head {
		t.Fatalf("expected head %s, got %s", head, got)
	}
	if chain.Prev != nil {
		t.Fatalf("expected no previous node when iterator fails after first commit")
	}
}
