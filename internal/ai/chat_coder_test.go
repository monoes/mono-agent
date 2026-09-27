package ai

import "testing"

func TestCoderConversationModeAndWorkspaces(t *testing.T) {
	s, _ := newChatEventStore(t)

	plain, err := s.CreateConversation("default", "agent", "general", "claude", "", "")
	if err != nil || plain.Mode != ModeAssistant || plain.Cwd != "" {
		t.Fatalf("assistant conversation = %+v, %v", plain, err)
	}
	if _, err := s.CreateConversationMode("default", "agent", "general", "claude", "", "", ModeCoder, ""); err == nil {
		t.Error("coder conversation without a folder was created")
	}
	if _, err := s.CreateConversationMode("default", "agent", "general", "claude", "", "", "wizard", "/x"); err == nil {
		t.Error("unknown mode was accepted")
	}
	a, _ := s.CreateConversationMode("default", "agent", "general", "claude", "", "", ModeCoder, "/w/a")
	s.CreateConversationMode("default", "agent", "general", "claude", "", "", ModeCoder, "/w/a")
	s.CreateConversationMode("other", "agent", "general", "claude", "", "", ModeCoder, "/w/other-profile")

	got, err := s.GetConversation(a.ID, "default")
	if err != nil || got.Mode != ModeCoder || got.Cwd != "/w/a" {
		t.Fatalf("GetConversation = %+v, %v", got, err)
	}
	if rt := got.Record().Conversation(); rt.Mode != ModeCoder || rt.Cwd != "/w/a" {
		t.Errorf("record round trip lost mode/cwd: %+v", rt)
	}
	list, _, _ := s.ListConversations("default", "", 10)
	if len(list) != 3 {
		t.Fatalf("ListConversations = %d", len(list))
	}
	ws, err := s.ListCoderWorkspaces("default", 10)
	if err != nil || len(ws) != 1 || ws[0].Path != "/w/a" || ws[0].Conversations != 2 {
		t.Errorf("ListCoderWorkspaces = %+v, %v", ws, err)
	}
}
