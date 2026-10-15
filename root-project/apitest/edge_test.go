package main

import "testing"

func TestSignals(t *testing.T) {
	cases := []struct {
		text               string
		bye, insult, money bool
		hardQ, nonEng      bool
		name, place        string
	}{
		{"maybe later", false, false, false, false, false, "", ""},
		{"brb", false, false, false, false, false, "", ""},
		{"ok bye", true, false, false, false, false, "", ""},
		{"gtg talk later", true, false, false, false, false, "", ""},
		{"this app is stupid lol", false, false, false, false, false, "", ""},
		{"ur ugly af", false, true, false, false, false, "", ""},
		{"stfu bot", false, true, false, false, false, "", ""},
		{"wanna buy u a drink", false, false, false, false, false, "", ""},
		{"ill pay u for pics", false, false, true, false, false, "", ""},
		{"do u have onlyfans", false, false, true, false, false, "", ""},
		{"explain urself lol", false, false, false, false, false, "", ""},
		{"what is 47 times 12", false, false, false, true, false, "", ""},
		{"its cold here", false, false, false, false, false, "", ""},
		{"im jake btw", false, false, false, false, false, "jake", ""},
		{"im bored af", false, false, false, false, false, "", ""},
		{"m 21 from texas", false, false, false, false, false, "", "texas"},
		{"hola guapa", false, false, false, false, true, "", ""},
		{"sup bro", false, false, false, false, false, "", ""},
		{"nothing much wbu", false, false, false, false, false, "", ""},
	}
	for _, c := range cases {
		d := newDirector(newRng())
		d.observe(c.text)
		if d.userBye != c.bye || d.userInsult != c.insult || d.userMoney != c.money || d.userHardQ != c.hardQ || d.userNonEng != c.nonEng || d.userName != c.name || d.userPlace != c.place {
			t.Errorf("%q: bye=%v insult=%v money=%v hardQ=%v nonEng=%v name=%q place=%q", c.text, d.userBye, d.userInsult, d.userMoney, d.userHardQ, d.userNonEng, d.userName, d.userPlace)
		}
	}
}

func TestOnlyQuestion(t *testing.T) {
	if onlyQuestion("nothing much, u?") {
		t.Error("answer+question flagged")
	}
	if !onlyQuestion("what about u?") {
		t.Error("pure question not flagged")
	}
	if onlyQuestion("f 21, wyd?") {
		t.Error("asl answer flagged")
	}
}
