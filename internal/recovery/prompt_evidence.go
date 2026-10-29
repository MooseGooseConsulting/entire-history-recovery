package recovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
)

// PromptEvidence reports overlap, not attribution: identical text can occur in
// multiple places. It deliberately retains no prompt text or digest.
type PromptEvidence struct {
	ImporterUnits           int
	NativeMessages          int
	TextMatches             int
	UnmatchedUnits          int
	UnmatchedNativeMessages int
}

// CompareNativePrompts compares Entire's candidate prompts with completed
// native UserMessage items. It does not filter or change imported data.
func CompareNativePrompts(full []byte, turns []agentimport.Turn) PromptEvidence {
	result := PromptEvidence{ImporterUnits: len(turns)}
	candidates := make(map[[32]byte]int)
	for _, turn := range turns {
		candidates[sha256.Sum256([]byte(strings.TrimSpace(turn.Prompt)))]++
	}
	for _, line := range bytes.Split(full, []byte{'\n'}) {
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Item struct {
					Type    string `json:"type"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "event_msg" || event.Payload.Type != "item_completed" || event.Payload.Item.Type != "UserMessage" {
			continue
		}
		result.NativeMessages++
		var text []string
		for _, part := range event.Payload.Item.Content {
			if trimmed := strings.TrimSpace(part.Text); trimmed != "" {
				text = append(text, trimmed)
			}
		}
		digest := sha256.Sum256([]byte(strings.Join(text, "\n\n")))
		if candidates[digest] > 0 {
			result.TextMatches++
			candidates[digest]--
		} else {
			result.UnmatchedNativeMessages++
		}
	}
	result.UnmatchedUnits = result.ImporterUnits - result.TextMatches
	return result
}
