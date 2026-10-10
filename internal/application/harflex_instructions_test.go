package application

import (
	"strings"
	"testing"
)

// The frontend draws the choice from this exact block; the agent is told to write it and nothing else about the options.
func TestChatInstructionsAskForTheChoiceBlockBeforeCompleteWork(t *testing.T) {
	for _, want := range []string{"```harflex-choice\n{\"kind\":\"pipeline\"}\n```", "do not start working and do not call harflex_create_pipeline yet", "never write those options yourself"} {
		if !strings.Contains(harflexChatInstructions, want) {
			t.Fatalf("the chat instructions lack %q", want)
		}
	}
}
