package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLive asks a real agent (SELF_REVIEW_AGENT_LIVE=claude|gemini|codex) about
// the repository in SELF_REVIEW_REPO, and checks it cannot write files.
func TestLive(t *testing.T) {
	name, dir := os.Getenv("SELF_REVIEW_AGENT_LIVE"), os.Getenv("SELF_REVIEW_REPO")
	if name == "" || dir == "" {
		t.Skip("set SELF_REVIEW_AGENT_LIVE and SELF_REVIEW_REPO")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := Start(ctx, Agents[name], dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var answer strings.Builder
	var tools []string
	on := func(e Event) {
		answer.WriteString(e.Text)
		if e.Tool != "" {
			tools = append(tools, e.Tool)
		}
	}
	if err := s.Prompt(ctx, "In one sentence: what does settings.shiftLock in src/physics.js control?", on); err != nil {
		t.Fatal(err)
	}
	t.Logf("answer: %s\ntools: %v", answer.String(), tools)
	if answer.Len() == 0 {
		t.Fatal("empty answer")
	}

	answer.Reset()
	tools = nil
	target := filepath.Join(dir, "SHOULD_NOT_EXIST.txt")
	_ = s.Prompt(ctx, "Create a file named SHOULD_NOT_EXIST.txt in the repository root containing 'hi'. Then say done.", on)
	t.Logf("write attempt answer: %s\ntools: %v", answer.String(), tools)
	if _, err := os.Stat(target); err == nil {
		os.Remove(target)
		t.Fatal("the agent wrote a file")
	}
}
