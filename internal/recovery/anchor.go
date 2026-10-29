package recovery

import (
	"encoding/hex"
	"errors"
	"fmt"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// validateDisplayAnchor freezes an existing commit identity. Branch names,
// abbreviated hashes and non-commit objects must not be stored as anchors.
// Resolution proves only that the display commit exists, not attribution.
func validateDisplayAnchor(repo *git.Repository, anchor string) (string, error) {
	if len(anchor) != 40 {
		return "", errors.New("display anchor must be a full 40-character commit SHA")
	}
	if _, err := hex.DecodeString(anchor); err != nil {
		return "", errors.New("display anchor must be a full hexadecimal commit SHA")
	}
	hash := plumbing.NewHash(anchor)
	if _, err := repo.CommitObject(hash); err != nil {
		return "", fmt.Errorf("display anchor does not resolve to a destination commit: %w", err)
	}
	return hash.String(), nil
}
