package splitter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

var messageNormalizer = regexp.MustCompile(`\s*\r?\n`)

// GitDirectory returns the .git directory for a given directory
func GitDirectory(path string) string {
	gitPath := filepath.Join(path, ".git")
	if _, err := os.Stat(gitPath); os.IsNotExist(err) {
		// this might be a bare repo
		return path
	}

	return gitPath
}

// SplitMessage splits a git message
func SplitMessage(message string) (string, string) {
	// we split the message at \n\n or \r\n\r\n
	var subject, body string
	found := false
	for i := 0; i+4 <= len(message); i++ {
		if message[i] == '\n' && message[i+1] == '\n' {
			subject = message[0:i]
			body = message[i+2:]
			found = true
			break
		} else if message[i] == '\r' && message[i+1] == '\n' && message[i+2] == '\r' && message[i+3] == '\n' {
			subject = message[0:i]
			body = message[i+4:]
			found = true
			break
		}
	}

	if !found {
		subject = message
		body = ""
	}

	// normalize \r\n and whitespaces
	subject = messageNormalizer.ReplaceAllLiteralString(subject, " ")

	// remove spaces at the end of the subject
	subject = strings.TrimRight(subject, " ")
	body = strings.TrimLeft(body, "\r\n")
	return subject, body
}

func normalizeOrigin(repo *git.Repository, origin string) (string, error) {
	if origin == "" {
		origin = "HEAD"
	}

	// try as a tagReference
	if tagRef, err := repo.Tag(origin); err == nil {
		return tagRef.Name().String(), nil
	}

	if ref, err := repo.Reference(plumbing.ReferenceName(origin), true); err == nil {
		return ref.Name().String(), nil
	}

	return "", fmt.Errorf("bad revision for origin")
}

func peelTag(repo *git.Repository, maybeTagHash plumbing.Hash) (*plumbing.Hash, error) {
	tagObj, err := repo.TagObject(maybeTagHash)
	switch err {
	case nil:
		// annotated tag
		return &tagObj.Target, nil
	case plumbing.ErrObjectNotFound:
		// lightweight tag
		return &maybeTagHash, nil
	default:
		// real error
		return nil, err
	}
}
