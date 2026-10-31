package tui

import (
	"strings"
	"testing"

	"relay/internal/proto"
)

func approvalModel() model {
	m := testModel()
	m.approval = &proto.Event{Type: "approval", Tool: "run_shell", CallID: "c1", Desc: "run: go test ./... && go vet ./..."}
	return m
}

func TestApprovalHotkeys(t *testing.T) {
	for key, want := range map[string]string{
		"y": "allow", "Y": "allow", "н": "allow",
		"a": "always", "A": "always", "ф": "always",
		"n": "deny", "N": "deny", "т": "deny", "esc": "deny",
		// other keys must do nothing; in particular letters of the word "esc"
		"e": "", "s": "", "c": "", "x": "", "ctrl+a": "", "tab": "", " ": "",
	} {
		m := approvalModel()
		if got := m.approvalKey(key); got != want {
			t.Errorf("key %q -> %q, want %q", key, got, want)
		}
	}
}

func TestApprovalListNavigation(t *testing.T) {
	m := approvalModel()
	if m.appSel != 0 {
		t.Fatal("starts on Allow once")
	}
	m.approvalKey("down")
	m.approvalKey("down")
	if got := m.approvalKey("enter"); got != "deny" {
		t.Errorf("enter on the third line: %q", got)
	}
	m.approvalKey("down") // wraps to the first
	if got := m.approvalKey("enter"); got != "allow" {
		t.Errorf("after wrapping: %q", got)
	}
	m.approvalKey("up") // wraps to the last
	if got := m.approvalKey("enter"); got != "deny" {
		t.Errorf("up from the first line: %q", got)
	}
	m.approvalKey("up")
	if got := m.approvalKey("enter"); got != "always" {
		t.Errorf("second line: %q", got)
	}
}

func TestApprovalListIsDrawnAboveInput(t *testing.T) {
	m := approvalModel()
	m.input.SetValue("half typed message")
	out := m.View()
	for _, want := range []string{"approval needed", "run_shell", "run: go test ./... && go vet ./...",
		"❯ Allow once", "Always allow run_shell in this session", "Deny", "Choose an action in the list above"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(out, "[y] once") {
		t.Error("the old one-line prompt must be gone")
	}
	if strings.Contains(out, "half typed message") {
		t.Error("the input line is not editable while waiting for a decision")
	}
	if n := len(strings.Split(out, "\n")); n != m.height {
		t.Errorf("height %d, want %d", n, m.height)
	}
	// the typed text comes back after the decision
	m.approval = nil
	if !strings.Contains(m.View(), "half typed message") {
		t.Error("typed text lost")
	}
}

func TestApprovalLongDescriptionIsCut(t *testing.T) {
	m := approvalModel()
	m.approval.Desc = strings.Repeat("very long command ", 80)
	lines := m.approvalLines(60, 20, newStyles(themeFor("small", 0)))
	if len(lines) > 1+4+3 {
		t.Errorf("list too tall: %d lines", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "…") {
		t.Error("a cut description must end with …")
	}
	small := m.approvalLines(60, 6, newStyles(themeFor("small", 0)))
	if len(small) > 6 {
		t.Errorf("must fit in 6 lines, got %d", len(small))
	}
}

func TestSidebarListsRunningServers(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	m.status = &proto.StatusResp{Servers: []proto.ServerInfo{{Name: "ollama", Models: 6}, {Name: "lmstudio", Models: 1}}}
	out := m.View()
	for _, want := range []string{"processes", "● ollama 6 models", "● lmstudio 1 model"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(out, "Nothing running") {
		t.Error("unexpected empty text")
	}

	m.status = &proto.StatusResp{Servers: []proto.ServerInfo{}}
	out = m.View()
	if !strings.Contains(out, "processes") || !strings.Contains(out, "Nothing running :)") {
		t.Errorf("empty block:\n%s", out)
	}
	m.status = nil
	if strings.Contains(m.View(), "processes") {
		t.Error("nothing to show before the first status")
	}
}
