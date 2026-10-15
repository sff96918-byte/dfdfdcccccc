package browser

import "testing"

func TestShowModeSendConfirmation(t *testing.T) {
	before := DOMSnapshot{Controls: DOMControls{Conversation: "one", Phase: "chatting", Partner: "partner", OwnName: "self"},
		Messages: []DOMMessage{{ID: "old", Author: "self", Text: "hello"}}}
	for _, tc := range []struct {
		name         string
		row          DOMMessage
		conversation string
		want         bool
	}{
		{"old identical row", DOMMessage{ID: "old", Author: "self", Text: "hello"}, "one", false},
		{"partner identical text", DOMMessage{ID: "new", Author: "partner", Text: "hello"}, "one", false},
		{"unknown author", DOMMessage{ID: "new", Text: "hello"}, "one", false},
		{"missing id", DOMMessage{Author: "self", Text: "hello"}, "one", false},
		{"different text", DOMMessage{ID: "new", Author: "self", Text: "other"}, "one", false},
		{"new own row", DOMMessage{ID: "new", Author: "SELF", Text: "hello"}, "one", true},
		{"next conversation", DOMMessage{ID: "new", Author: "self", Text: "hello"}, "two", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := DOMSnapshot{Controls: before.Controls, Messages: []DOMMessage{tc.row}}
			after.Controls.Conversation = tc.conversation
			if got := confirmedDOMSend(before, after, "hello"); got != tc.want {
				t.Fatalf("confirmation=%v, want %v", got, tc.want)
			}
		})
	}
}
