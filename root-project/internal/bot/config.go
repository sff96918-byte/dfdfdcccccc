package bot

import "time"

func DefaultConfig(dataDir string, profile string, backend string, headless bool) Config {
	return Config{
		AppURL:              "https://app.chitchat.gg/",
		APIBaseURL:          "https://api.chitchat.gg",
		DataDir:             dataDir,
		Profile:             profile,
		Backend:             backend,
		Headless:            headless,
		MatchWhenIdle:       true,
		PollMin:             20 * time.Second,
		PollMax:             45 * time.Second,
		ActionMin:           20 * time.Second,
		ActionMax:            45 * time.Second,
		MatchReplyTimeoutMin: 60 * time.Second,
		MatchReplyTimeoutMax: 60 * time.Second,
		MatchCloseWaitMin:    0 * time.Second,
		MatchCloseWaitMax:    0 * time.Second,
		ReplyDelayMin:        0 * time.Second,
		ReplyDelayMax:        0 * time.Second,
		DMLimit:              100,
		DMOffset:            1,
		MessageFetchLimit:   100,
		MessageSequence:     []string{"hello how are you", "ok i am checking. wait a bit."},
		TransportKey:        "33FsVurAchunk-84DSi8nlzE",
		NonceAlphabet:       "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict",
		StatusFriend:        1,
		StatusPendingOut:    2,
		StatusPendingIn:     3,
		StatusBlocked:       5,
		ActionAcceptFriend:  1,
	}
}
