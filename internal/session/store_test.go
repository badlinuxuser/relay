package session

import (
	"testing"

	"relay/internal/provider"
)

func TestTotalsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	s := st.Create("small", "/w")
	s.TokensIn, s.TokensOut, s.Cost = 120, 30, 0.004
	s.Messages = append(s.Messages, providerMsg("hi"))
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	again := NewStore(dir) // a fresh process reads it from disk
	got, err := again.Get(s.ID)
	if err != nil || got.TokensIn != 120 || got.TokensOut != 30 || got.Cost != 0.004 {
		t.Fatalf("reload: %+v %v", got, err)
	}
	list := again.List()
	if len(list) != 1 || list[0].TokensIn != 120 || list[0].Messages != 1 {
		t.Errorf("list: %+v", list)
	}
	if err := again.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	if len(NewStore(dir).List()) != 0 {
		t.Error("deleted session is gone from disk")
	}
	if again.Delete("../x") == nil || again.Delete(s.ID) == nil {
		t.Error("bad or missing ids are errors")
	}
}

func providerMsg(text string) provider.Message { return provider.Message{Role: "user", Content: text} }
