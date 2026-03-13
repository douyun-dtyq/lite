package splitter

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
)

type state struct {
	config       *Config
	origin       string
	repo         *git.Repository
	cache        *cache
	logger       *log.Logger
	simplePrefix string
	result       *Result
}

func newState(config *Config, result *Result) (*state, error) {
	var err error

	// validate config
	if err = config.Validate(); err != nil {
		return nil, err
	}

	state := &state{
		config: config,
		result: result,
		repo:   config.Repo,
		logger: config.Logger,
	}

	if state.repo == nil {
		if state.repo, err = git.PlainOpen(config.Path); err != nil {
			return nil, err
		}
	}

	if state.logger == nil {
		state.logger = log.New(os.Stderr, "", log.LstdFlags)
	}

	if state.origin, err = normalizeOrigin(state.repo, config.Origin); err != nil {
		return nil, err
	}

	if state.cache, err = newCache(state.origin, config); err != nil {
		return nil, err
	}

	if config.Debug {
		state.logger.Printf("Splitting %s", state.origin)
		for _, v := range config.Prefixes {
			to := v.To
			if to == "" {
				to = "ROOT"
			}
			state.logger.Printf(`  From "%s" to "%s"`, v.From, to)
			if (len(v.Excludes)) == 0 {
			} else {
				state.logger.Printf(`  Excluding "%s"`, strings.Join(v.Excludes, `", "`))
			}
		}
	}

	if config.Scratch {
		if err := state.flush(); err != nil {
			return nil, err
		}
	}

	// simplePrefix contains the prefix when there is only one
	// with an empty value (target)
	if len(config.Prefixes) == 1 && config.Prefixes[0].To == "" && len(config.Prefixes[0].Excludes) == 0 {
		state.simplePrefix = config.Prefixes[0].From
	}

	return state, nil
}

func (s *state) close() error {
	err := s.cache.close()
	if err != nil {
		return err
	}
	return nil
}

func (s *state) flush() error {
	if err := s.cache.flush(); err != nil {
		return err
	}

	if s.config.Target != "" {
		targetRef := plumbing.ReferenceName(s.config.Target)
		err := s.repo.Storer.RemoveReference(targetRef)
		if err != nil && err != plumbing.ErrReferenceNotFound {
			return err
		}

		if targetRef.IsBranch() {
			_ = s.repo.DeleteBranch(targetRef.Short())
		}
	}

	return nil
}

type gitHashMap[T any] map[string]T

// splitRange determines the commit range to process, equivalent to
// git rev-list's "A..B" range semantics.
//
// Returns (from, excludeSet, error) where:
//   - from: the origin commit hash to start walking from
//   - excludeSet: commits reachable from a prior split point to skip,
//     nil means walk all ancestors of from
//
// Three cases depending on prior state:
//  1. Cache has a head → incremental split (head..origin)
//  2. Config specifies a commit → resume from that point (commit^..origin)
//  3. Neither → full split from origin
func (s *state) splitRange() (*plumbing.Hash, gitHashMap[bool], error) {
	originRef, err := s.repo.Reference(plumbing.ReferenceName(s.origin), true)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot resolve origin %s: %s", s.origin, err)
	}
	originHash, err := peelTag(s.repo, originRef.Hash())
	if err != nil {
		return nil, nil, err
	}

	// Case 1: incremental — only process commits since last cached head
	if head := s.cache.getHead(); head != nil {
		s.result.moveHead(s.cache.get(head))
		excludeSet, err := s.reachableFrom(head)
		return originHash, excludeSet, err
	}

	// Case 2: resume from a specific commit — exclude its first parent and ancestors
	if s.config.Commit != "" {
		commitHash := plumbing.NewHash(s.config.Commit)
		s.result.moveHead(s.cache.get(&commitHash))
		return &commitHash, nil, nil
	}

	// Case 3: full split — no exclusions
	return originHash, nil, nil
}

// collectCommits walks commit history from the given hash and returns
// commits in topological order (parents before children) for replay.
//
// go-git's LogOrderDFSPost is not a true topological sort for DAGs
// with merge commits, so we do our own BFS + Kahn's algorithm.
func (s *state) collectCommits(from *plumbing.Hash, excludeSet gitHashMap[bool]) ([]*object.Commit, error) {
	commitMap := make(map[string]*object.Commit)
	queue := []plumbing.Hash{*from}
	for len(queue) > 0 {
		hash := queue[0]
		queue = queue[1:]
		key := hash.String()
		if commitMap[key] != nil {
			continue
		}
		if excludeSet != nil && excludeSet[key] {
			continue
		}
		c, err := s.repo.CommitObject(hash)
		if err != nil {
			return nil, fmt.Errorf("impossible to read commit %s: %s", hash, err)
		}
		commitMap[key] = c
		for _, ph := range c.ParentHashes {
			queue = append(queue, ph)
		}
	}
	if len(commitMap) == 0 {
		return nil, nil
	}
	children := make(map[string][]string)
	inDegree := make(map[string]int)
	for key, c := range commitMap {
		deg := 0
		for _, ph := range c.ParentHashes {
			pk := ph.String()
			if commitMap[pk] != nil {
				deg++
				children[pk] = append(children[pk], key)
			}
		}
		inDegree[key] = deg
	}
	var ready []string
	for key, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, key)
		}
	}
	var result []*object.Commit
	for len(ready) > 0 {
		key := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		result = append(result, commitMap[key])
		for _, childKey := range children[key] {
			inDegree[childKey]--
			if inDegree[childKey] == 0 {
				ready = append(ready, childKey)
			}
		}
	}
	return result, nil
}

func (s *state) split() error {
	startTime := time.Now()
	defer func() {
		s.result.end(startTime)
	}()
	from, excludeSet, err := s.splitRange()
	if err != nil {
		return err
	}
	commits, err := s.collectCommits(from, excludeSet)
	if err != nil {
		return err
	}
	var lastHash plumbing.Hash
	for _, rev := range commits {
		lastHash = rev.Hash
		if s.config.Debug {
			s.logger.Printf("Processing commit: %s\n", rev.Hash)
		}
		newrev, err := s.splitRev(rev)
		if err != nil {
			return err
		}
		if newrev != nil {
			s.result.moveHead(newrev)
		}
	}
	// Record the last processed original commit as cache head
	// so future runs can skip already-split commits.
	if lastHash != plumbing.ZeroHash {
		s.cache.setHead(&lastHash)
	}
	return s.updateTarget()
}

func (s *state) reachableFrom(hash *plumbing.Hash) (gitHashMap[bool], error) {
	iter, err := s.repo.Log(&git.LogOptions{From: *hash, Order: git.LogOrderDFS})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	set := make(gitHashMap[bool])
	err = iter.ForEach(func(c *object.Commit) error {
		set[c.Hash.String()] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to collect reachable commits: %s", err)
	}
	return set, nil
}

func (s *state) isReachableFrom(target, source *plumbing.Hash) (bool, error) {
	if target.String() == source.String() {
		return true, nil
	}
	iter, err := s.repo.Log(&git.LogOptions{From: *source, Order: git.LogOrderDFS})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	targetStr := target.String()
	found := false
	err = iter.ForEach(func(c *object.Commit) error {
		if c.Hash.String() == targetStr {
			found = true
			return storer.ErrStop
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

func (s *state) splitRev(rev *object.Commit) (*plumbing.Hash, error) {
	s.result.incTraversed()

	v := s.cache.get(&rev.Hash)
	if v != nil {
		if s.config.Debug {
			s.logger.Printf("  prior: %s\n", v.String())
		}
		return v, nil
	}

	var parents []*plumbing.Hash
	for i := range rev.ParentHashes {
		parents = append(parents, &rev.ParentHashes[i])
	}

	if s.config.Debug {
		debugMsg := "  parents:"
		for _, parent := range parents {
			debugMsg += fmt.Sprintf(" %s", parent.String())
		}
		s.logger.Print(debugMsg)
	}

	newParents := s.cache.gets(parents)

	if s.config.Debug {
		debugMsg := "  newparents:"
		for _, parent := range newParents {
			debugMsg += fmt.Sprintf(" %s", parent)
		}
		s.logger.Print(debugMsg)
	}

	tree, err := s.subtreeForCommit(rev)
	if err != nil {
		return nil, err
	}

	if tree == nil {
		return nil, nil
	}

	if s.config.Debug {
		s.logger.Printf("  tree is: %s\n", tree.Hash.String())
	}

	newrev, created, err := s.copyOrSkip(rev, tree, newParents)
	if err != nil {
		return nil, err
	}

	if s.config.Debug {
		s.logger.Printf("  newrev is: %s\n", newrev)
	}

	if created {
		s.result.incCreated()
	}

	s.cache.set(&rev.Hash, newrev)

	return newrev, nil
}

func (s *state) subtreeForCommit(commit *object.Commit) (*object.Tree, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	if s.simplePrefix != "" {
		return s.treeByPath(tree, s.simplePrefix)
	}

	return s.treeByPaths(tree)
}

func (s *state) treeByPath(tree *object.Tree, prefix string) (*object.Tree, error) {
	entry, err := tree.FindEntry(prefix)
	if err != nil {
		return nil, nil
	}

	if entry.Mode != filemode.Dir {
		return nil, nil
	}

	return s.repo.TreeObject(entry.Hash)
}

func (s *state) treeByPaths(tree *object.Tree) (*object.Tree, error) {
	var currentTree *object.Tree
	for _, prefix := range s.config.Prefixes {
		splitTree, err := s.treeByPath(tree, prefix.From)
		if err != nil {
			return nil, err
		}
		if splitTree == nil {
			continue
		}

		if len(prefix.Excludes) > 0 {
			prunedTree, err := s.pruneTree(splitTree, prefix.Excludes)
			if err != nil {
				return nil, err
			}
			splitTree = prunedTree
		}

		var prefixedTree *object.Tree
		if prefix.To != "" {
			prefixedTree, err = s.addPrefixToTree(splitTree, prefix.To)
			if err != nil {
				return nil, err
			}
		} else {
			prefixedTree = splitTree
		}

		if currentTree != nil {
			mergedTree, err := s.mergeTrees(currentTree, prefixedTree)
			if err != nil {
				return nil, err
			}
			currentTree = mergedTree
		} else {
			currentTree = prefixedTree
		}
	}

	return currentTree, nil
}

func (s *state) mergeTrees(t1, t2 *object.Tree) (*object.Tree, error) {
	entryMap := make(map[string]object.TreeEntry)
	for _, e := range t1.Entries {
		entryMap[e.Name] = e
	}

	for _, e := range t2.Entries {
		if existing, ok := entryMap[e.Name]; ok {
			if existing.Hash.Equal(e.Hash) && existing.Mode == e.Mode {
				continue
			}
			if existing.Mode == filemode.Dir && e.Mode == filemode.Dir {
				existingTree, err := s.repo.TreeObject(existing.Hash)
				if err != nil {
					return nil, err
				}
				newTree, err := s.repo.TreeObject(e.Hash)
				if err != nil {
					return nil, err
				}
				merged, err := s.mergeTrees(existingTree, newTree)
				if err != nil {
					return nil, err
				}
				entryMap[e.Name] = object.TreeEntry{
					Name: e.Name,
					Mode: filemode.Dir,
					Hash: merged.Hash,
				}
				continue
			}
			return nil, fmt.Errorf("cannot split as there is a merge conflict between two paths")
		}
		entryMap[e.Name] = e
	}

	entries := make([]object.TreeEntry, 0, len(entryMap))
	for _, e := range entryMap {
		entries = append(entries, e)
	}

	return s.storeTree(entries)
}

func (s *state) addPrefixToTree(tree *object.Tree, prefix string) (*object.Tree, error) {
	treeHash := tree.Hash
	parts := strings.Split(prefix, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		entries := []object.TreeEntry{{
			Name: parts[i],
			Mode: filemode.Dir,
			Hash: treeHash,
		}}
		newTree, err := s.storeTree(entries)
		if err != nil {
			return nil, err
		}
		treeHash = newTree.Hash
	}

	return s.repo.TreeObject(treeHash)
}

func (s *state) pruneTree(tree *object.Tree, excludes []string) (*object.Tree, error) {
	var entries []object.TreeEntry
	for _, entry := range tree.Entries {
		if entry.Mode.IsFile() {
			entries = append(entries, entry)
			continue
		}

		if entry.Mode != filemode.Dir {
			return nil, fmt.Errorf("Unexpected entry %s (type %s)", entry.Name, entry.Mode)
		}

		excluded := false
		for _, exclude := range excludes {
			if entry.Name == exclude {
				excluded = true
				break
			}
		}

		if !excluded {
			entries = append(entries, entry)
		}
	}

	return s.storeTree(entries)
}

func (s *state) copyOrSkip(rev *object.Commit, tree *object.Tree, newParents []*plumbing.Hash) (*plumbing.Hash, bool, error) {
	var identical, nonIdentical *plumbing.Hash
	var parentHashes []plumbing.Hash
	for _, parent := range newParents {
		ptree, err := s.topTreeForCommit(parent)
		if err != nil {
			return nil, false, err
		}
		if ptree == nil {
			continue
		}

		if ptree.Equal(tree.Hash) {
			identical = parent
		} else {
			nonIdentical = parent
		}

		// sometimes both old parents map to the same newparent
		// eliminate duplicates
		isNew := true
		for _, gp := range parentHashes {
			if gp.Equal(*parent) {
				isNew = false
				break
			}
		}

		if isNew {
			parentHashes = append(parentHashes, *parent)
		}
	}

	copyCommit := false
	if s.config.Git > 2 && identical != nil && nonIdentical != nil {
		reachable, err := s.isReachableFrom(nonIdentical, identical)
		if err != nil {
			return nil, false, fmt.Errorf("impossible to walk the repository: %s", err)
		}
		if !reachable {
			copyCommit = true
		}
	}

	if identical != nil && !copyCommit {
		return identical, false, nil
	}

	hash, err := s.copyCommit(rev, tree, parentHashes)
	if err != nil {
		return nil, false, err
	}

	return hash, true, nil
}

func (s *state) topTreeForCommit(sha *plumbing.Hash) (*plumbing.Hash, error) {
	commit, err := s.repo.CommitObject(*sha)
	if err != nil {
		return nil, err
	}

	treeHash := commit.TreeHash
	return &treeHash, nil
}

func (s *state) copyCommit(rev *object.Commit, tree *object.Tree, parents []plumbing.Hash) (*plumbing.Hash, error) {
	// fmt.Printf("copyCommit: %s %s\n", rev.Hash.String(), tree.Hash.String())
	if s.config.Debug {
		parentStrs := make([]string, len(parents))
		for i, parent := range parents {
			parentStrs[i] = parent.String()
		}
		s.logger.Printf("  copy commit \"%s\" \"%s\" \"%s\"\n", rev.Hash.String(), tree.Hash.String(), strings.Join(parentStrs, " "))
	}

	message := rev.Message
	if s.config.Git == 1 {
		message = s.legacyMessage(rev)
	}

	author := rev.Author
	if author.Email == "" {
		author.Email = "nobody@example.com"
	}

	committer := rev.Committer
	if committer.Email == "" {
		committer.Email = "nobody@example.com"
	}

	newCommit := &object.Commit{
		Author:       author,
		Committer:    committer,
		Message:      message,
		TreeHash:     tree.Hash,
		ParentHashes: parents,
	}

	obj := s.repo.Storer.NewEncodedObject()
	if err := newCommit.Encode(obj); err != nil {
		return nil, err
	}
	hash, err := s.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return nil, err
	}

	return &hash, nil
}

func (s *state) updateTarget() error {
	if s.config.Target == "" {
		return nil
	}

	head := s.result.Head()
	if head == nil {
		return fmt.Errorf("unable to create branch %s as it is empty (no commits were split)", s.config.Target)
	}

	ref := plumbing.NewHashReference(plumbing.ReferenceName(s.config.Target), *head)
	return s.repo.Storer.SetReference(ref)
}

func (s *state) legacyMessage(rev *object.Commit) string {
	subject, body := SplitMessage(rev.Message)
	return subject + "\n\n" + body
}

func (s *state) storeTree(entries []object.TreeEntry) (*object.Tree, error) {
	sort.Sort(object.TreeEntrySorter(entries))
	tree := &object.Tree{Entries: entries}
	obj := s.repo.Storer.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return nil, err
	}
	hash, err := s.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return nil, err
	}
	tree.Hash = hash
	return tree, nil
}
