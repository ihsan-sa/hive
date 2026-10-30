package work

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The judge sits on a model other than the change's author's where the
// config has one: its route, then the reviewer's, then a tribunal seat's.
func TestJudgePickAvoidsTheAuthor(t *testing.T) {
	cfg := core.Config{Harness: "claude", Model: "opus"}
	if p, off := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); off || p.Model != "opus" {
		t.Fatalf("only the author's model: %v %v", p, off)
	}
	cfg.TribunalModels = []core.TribunalSeat{{Model: "opus"}, {Harness: "codex", Model: "gpt"}}
	if p, off := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); !off || p.Model != "gpt" || p.Harness != "codex" {
		t.Fatalf("a tribunal seat off the author: %v %v", p, off)
	}
	cfg.ReviewerModel = "sonnet"
	if p, _ := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); p.Model != "sonnet" {
		t.Fatalf("the reviewer's model first: %v", p)
	}
	if p, _ := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "sonnet"}); p.Model != "opus" {
		t.Fatalf("a reviewer on the author's model is passed over: %v", p)
	}
	cfg.Routes = map[string][]string{core.JudgeRole: {"claude:haiku"}}
	if p, _ := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); p.Model != "haiku" {
		t.Fatalf("the judge route first: %v", p)
	}
}
