package recovery

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	git "github.com/go-git/go-git/v6"
)

func importedPromptFixture(t *testing.T, body string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	source, anchor := syntheticCodexSource(t, root)
	contents := `{"timestamp":"2020-01-01T00:00:00Z","type":"session_meta","payload":{"id":"session-prompt-normalization","cwd":"/deleted/checkout"}}` + "\n" + body
	if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return root, source, anchor
}

func readImportedPrompt(t *testing.T, root string) (*checkpoint.SessionContent, int) {
	t.Helper()
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := settings.WithWorktreeRoot(context.Background(), root)
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := stores.Persistent.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("checkpoint count = %d, want one evidence checkpoint", len(items))
	}
	content, err := stores.Persistent.ReadSessionContent(ctx, items[0].CheckpointID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return content, len(items)
}

func TestImportFiltersInjectedPromptButKeepsFullTranscript(t *testing.T) {
	root, source, anchor := importedPromptFixture(t,
		`{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>runtime context</environment_context>"}]}}`+"\n"+
			`{"timestamp":"2020-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"setup work"}]}}`+"\n"+
			`{"timestamp":"2020-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the actual request"}]}}`+"\n"+
			`{"timestamp":"2020-01-01T00:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the answer"}]}}`+"\n")
	result, err := ImportCodex(context.Background(), root, source, anchor, false)
	if err != nil || result.TurnsImported != 1 {
		t.Fatalf("import result: %+v, %v", result, err)
	}
	content, _ := readImportedPrompt(t, root)
	if content.Prompts != "the actual request" {
		t.Fatalf("stored prompt = %q, want only the human request", content.Prompts)
	}
	for _, expected := range []string{"<environment_context>runtime context</environment_context>", "setup work", "the actual request", "the answer"} {
		if !strings.Contains(string(content.Transcript), expected) {
			t.Fatalf("full transcript is missing %q", expected)
		}
	}
}

func TestImportKeepsContextOnlyWorkAsEmptyPromptEvidence(t *testing.T) {
	root, source, anchor := importedPromptFixture(t,
		`{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>runtime context</environment_context>"}]}}`+"\n"+
			`{"timestamp":"2020-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"context-only work"}]}}`+"\n"+
			`{"timestamp":"2020-01-01T00:00:03Z","type":"response_item","payload":{"type":"function_call","name":"shell","input":"pwd"}}`+"\n"+
			`{"timestamp":"2020-01-01T00:00:04Z","type":"response_item","payload":{"type":"function_call_output","output":"/repo"}}`+"\n")
	result, err := ImportCodex(context.Background(), root, source, anchor, false)
	if err != nil || result.TurnsImported != 1 {
		t.Fatalf("context evidence import result: %+v, %v", result, err)
	}
	content, count := readImportedPrompt(t, root)
	if count != 1 || content.Prompts != "" {
		t.Fatalf("context-only import must create one evidence checkpoint with empty prompt, count=%d prompt=%q", count, content.Prompts)
	}
	if !strings.Contains(string(content.Transcript), "context-only work") || !strings.Contains(string(content.Transcript), `"type":"function_call"`) {
		t.Fatal("context-only evidence checkpoint lost assistant/tool transcript")
	}
}

func TestInjectedOnlySessionWithoutAssistantOrToolWorkHasNoEvidenceTurn(t *testing.T) {
	_, source, _ := importedPromptFixture(t,
		`{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>runtime context</environment_context>"}]}}`+"\n")
	full, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	file := agentimport.SessionFile{Path: source, SessionID: "session-prompt-normalization"}
	turns, err := (fixedSource{Importer: codexImporter(t), file: file}).SplitTurns(file, full)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Fatalf("injected-only session created %d turn(s), want none", len(turns))
	}
}
