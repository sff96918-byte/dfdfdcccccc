// apitest — standalone console chat tester for an OpenAI-compatible API
// (Featherless, OpenAI, Groq, OpenRouter, Gemini, DeepSeek ...).
//
// It is deliberately NOT connected to the main ChitChat Bot code: one file,
// no dependencies, its own go.mod. It contains the "director": a small,
// deterministic funnel brain that reads the stranger's interest and tells the
// model what the next line must do (see PLAN.md). The model only writes the
// words; the director decides mood, double lines, when to ask for snap and
// when to share it.
//
//	apitest.exe            interactive chat (you are the stranger)
//	apitest.exe ping       one raw request, prints status + body
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- config ---

type config struct {
	BaseURL      string
	Model        string
	APIKey       string
	Timeout      time.Duration
	MaxTokens    int
	Temperature  float64
	History      int
	AgeMin       int
	AgeMax       int
	Countries    []string
	SnapUsername string
	ShareHot     int // bot reply number at which a HOT user gets the snap
	ShareWarm    int
	ShareCold    int
	ShowPrompt   bool
	ExtraJSON    map[string]any
	Models       []string // COMPARE_MODELS for `apitest compare <scenario>`
	AutoNoThink  bool     // AUTO_NOTHINK (default 1): send thinking-off fields for known families
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// -------------------------------------------------------------- director ---

const (
	tierCold = "COLD"
	tierWarm = "WARM"
	tierHot  = "HOT"
)

var (
	thinkRe     = regexp.MustCompile(`(?s)<think>.*?</think>`)
	forbiddenRe = regexp.MustCompile(`(?i)(@|https?://|www\.|as an ai|language model|i am a bot|i'm a bot)`)
	hornyRe     = regexp.MustCompile(`(?i)\b(h[o0]rny|hrny|s[e3]x|sexy|nudes?|n[o0]des|dick|cock|pussy|boobs?|tits|wet|dtf|f[u*]ck\w*|naked|nsfw|cum|turned on|in the mood|hard rn|send pics?|ur body|strip|hookup|hook up|make ?out|kinky|18\+)\b`)
	flirtyRe    = regexp.MustCompile(`(?i)\b(cute|pretty|beautiful|gorgeous|hot|babe|baby|bae|love|luv|kiss|date|single|gf|bf|boyfriend|girlfriend|miss u|miss you|wanna meet|meet up|ur eyes|ur smile|sweet|crush|flirt\w*|marry|wifey|pics?|photo|selfie)\b`)
	snapAskRe   = regexp.MustCompile(`(?i)(\bsnap\b|\bsc\b|\bsnapchat\b|\bsnp\b|\binsta\b|\big\b|\binstagram\b|\btelegram\b|\btg\b|\bdiscord\b|\bnumber\b|\bwhatsapp\b)`)
	refuseRe    = regexp.MustCompile(`(?i)\b(no|nope|nah|naw|dont have|don't have|do not have|not on|deleted|never|not really)\b`)
	yesRe       = regexp.MustCompile(`(?i)\b(yes+|yea+h*|yep+|yeop|ye|ya|sure|ofc|ok+|okay|k|yup|i do|got it|send|drop|gimme|add me|bet|fr|why not|lets go|let's go)\b`)
	ageRe       = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b([mf])[\s\-_./ ]{0,2}(\d{1,2})\b`),
		regexp.MustCompile(`(?i)\b(\d{1,2})[\s\-_./ ]{0,2}([mf])\b`),
		regexp.MustCompile(`(?i)\b(male|female|guy|girl|boy|man|woman|dude)[\s,]{0,3}(\d{1,2})\b`),
		regexp.MustCompile(`(?i)\b(\d{1,2})[\s,]{0,3}(male|female|guy|girl|boy|man|woman|dude)\b`),
		regexp.MustCompile(`(?i)\bi'?m (\d{2})\b`),
		regexp.MustCompile(`(?i)\b(\d{2}) ?(yo|years? old|yrs)\b`),
	}
	byeRe         = regexp.MustCompile(`(?i)(\b(bye+|goodbye|gtg|g2g|ttyl|cya|gotta go|got to go|have to go|im out|i\'m out|peace out|night night|gn\b|good ?night)\b|^(later|laters|nite|gn)\W*$)`)
	questionRe    = regexp.MustCompile(`(?i)\b(wyd|hbu|wbu|how|what|where|why|when|who|r u|are u|do u|u got|got any|asl|age)\b`)
	botQRe        = regexp.MustCompile(`(?i)(r u (a )?(bot|real|fake|ai|human)|are (u|you) (a )?(bot|real|fake|ai|human)|\bbot\b|\bfake\b|\bscam|\bai\b|\breal\?)`)
	picAskRe      = regexp.MustCompile(`(?i)\b(send|show|got|have|any|ur|your)\b.*\b(pics?|photo|selfie|face)\b|\b(pics?|photo|selfie)\?`)
	laughRe       = regexp.MustCompile(`(?i)\b(lol|lmao|haha+|hehe|rofl|😂|💀)`)
	lonelyRe      = regexp.MustCompile(`(?i)\b(bored|boring|alone|lonely|cant sleep|can't sleep|nothing much|nm|nothin|chilling|chillin)\b`)
	lowEffortRe   = regexp.MustCompile(`(?i)^(k|kk|ok|okay|oh|ohh|lol|lmao|haha+|hmm+|cool|nice|yea+h*|ya|yep|nah|idk|same|true|fr|wow|good|fine|nothing|nm|u\??|wbu\??|hbu\??|and\??|so\??|\W*)$`)
	hesitantRe    = regexp.MustCompile(`(?i)\b(idk|maybe|why|what for|hmm+|depends|not sure|later|dunno|we'?ll see|for what)\b`)
	aslAskRe      = regexp.MustCompile(`(?i)(\basl\b|\bage\b|how old|\bold r u|\bold are (u|you)|\bm or f\b|\bf or m\b|\bgender\b|r u a (girl|guy|boy|female|male)|are (u|you) a (girl|guy|boy|female|male)|\bgirl or\b|^(u|you|wbu|hbu|and u|n u)\s*\??$)`)
	placeRe       = regexp.MustCompile(`(?i)\b(?:from|live in|living in|based in|i\'?m in|im in)\s+(?:the\s+)?([a-z]{3,}(?:\s[a-z]{3,})?)\b`)
	placeStop     = regexp.MustCompile(`(?i)^(the|here|there|bed|home|class|school|work|college|uni|my|a|an|this|that|love|mood|fact|case|general|front|time|real|life|town|it|and|but|lol|rn|now|bored|chat)$`)
	knownPlaces   = regexp.MustCompile(`(?i)\b(usa|us|america|uk|england|london|canada|australia|germany|france|italy|spain|netherlands|sweden|norway|poland|ireland|scotland|mexico|brazil|india|texas|california|cali|florida|new york|nyc|ny|la|chicago|ohio|georgia|arizona|nevada|vegas|seattle|boston|michigan|colorado|utah|oregon|jersey|virginia|carolina|tennessee|kentucky|alabama|louisiana|miami|dallas|houston|austin|atlanta|philly|detroit|toronto|vancouver|sydney|melbourne|berlin|paris|amsterdam|dubai|pakistan|bangladesh|nigeria|philippines|indonesia|turkey|egypt|kenya|south africa|russia|ukraine|romania|greece|portugal|belgium|denmark|finland|austria|switzerland|czech|hungary|serbia|croatia)\b`)
	nameRe        = regexp.MustCompile(`(?i)\b(?:i\'?m|im|my name is|my name\'?s|name is|names|call me)\s+([a-z]{3,12})\b`)
	nameStop      = regexp.MustCompile(`(?i)^(bored|horny|good|fine|home|here|alone|tired|from|just|not|also|single|new|down|kinda|lowkey|sorry|okay|chilling|chillin|lol|male|female|guy|girl|real|sure|done|back|still|only|like|about|gonna|going|trying|looking|cool|busy|lost|sad|happy|high|drunk|sleepy|hungry|the|that|this|what|wondering|curious|asking|serious|joking|kidding|saying|talking|thinking|waiting|lying|laying|sitting|doing|dying|dead|cold|hot|late|nice|weird|funny|crazy|wild|dark|quiet|boring|lit|whatever|nothing|true|fake|over|early|ready|free|stuck|outside|inside|awake|asleep|sick|bad|mad|straight|bi|gay|taken|married|old|young|tall|short|big|small|hard|soft|wet|naked|close|near|far|online|offline|texting|watching|working|studying|driving|walking|eating|drinking|smoking|gaming|listening|scrolling|interested|intrested|bored|lonely|shy|honest|serious|legit|actually|literally|really|pretty|very|super|so|too|already|always|never|kind|sort|type|way|more|less|better|worse|sure|def|probably|maybe|guess|think|feel|mean|means|meant|supposed|about|around|almost|half|full|all|gone|off|out|in|on|up|at|by|for|to|with|and|but|or|if|as|an|a)$`)
	doingRe       = regexp.MustCompile(`(?i)\b(in bed|laying|lying|chilling|chillin|watching|gaming|playing|working|studying|eating|cooking|driving|at work|at home|at school|at the gym|gym|smoking|drinking|netflix|youtube|tiktok|cant sleep|can't sleep|just woke|about to sleep|nothing much|nothin|nm\b)`)
	bareMaleRe    = regexp.MustCompile(`(?i)\b(i'?m|im) (a )?(guy|male|boy|man|dude)\b|^(m|male|guy|boy|dude)\W*$`)
	bareFemaleRe  = regexp.MustCompile(`(?i)\b(i'?m|im) (a )?(girl|female|woman)\b|^(f|female|girl)\W*$`)
	bareAgeRe     = regexp.MustCompile(`(?i)^(?:im |i'm |i am |age )?(\d{2})\W*$`)
	nameAskRe     = regexp.MustCompile(`(?i)(ur name|your name|whats ur name|what's your name|name\?|who r u|who are u|who are you|what do i call u|call u\?|ur name\?|name tho)`)
	insultRe      = regexp.MustCompile(`(?i)(\b(bitch|slut|whore|hoe|stfu|kys|retard\w*|cunt|fuck (you|u|off))\b|\b(u|you|ur|you\'?re|youre|your|she\'?s)\b[^.?!]{0,20}\b(ugly|stupid|dumb|fat|fake|pathetic|loser|idiot|boring af|trash|disgusting|catfish|a bot bitch)\b|\b(ugly|stupid|dumb|fat|fake|pathetic|loser|idiot|trash|catfish)\b[^.?!]{0,12}\b(bitch|girl|hoe|ass|whore)\b)`)
	moneyRe       = regexp.MustCompile(`(?i)(\b(onlyfans|only fans|of link|of page|paypal|cashapp|cash app|venmo|zelle|sugar (daddy|baby|mommy)|gift ?cards?|crypto|bitcoin|btc|usdt|premium (snap|content)|my (rates|prices|menu))\b|\b(pay|paying|paid|send|give|got|need|want|for|make|earn)\b[^.?!]{0,12}\b(money|cash|dollars?|bucks)\b|\$ ?\d|\b(i'?ll|ill|i will|i can|can|will|wanna|would|gonna) pay (u|you|for)\b|\bpay (u|you) (for|to)\b|\b(how much|what'?s? (ur|your) price|do (u|you) charge|u charge|sell(ing)? (pics|nudes|content|vids|videos)|buy (ur|your) (pics|nudes|content))\b)`)
	hardQRe       = regexp.MustCompile(`(?i)\b(calculate|solve|math problem|capital of|president of|prime minister|politic\w*|election|write (me )?(some |a )?(code|script|poem|story|essay|program)|in python|in javascript|programming|explain (how|why|what|the|quantum|relativity|crypto)|meaning of life|define [a-z]+|definition of|history of|religion|philosophy|translate (this|to|into)|what is \d|\d+ ?(times|plus|minus|divided by|x|\+|-|\*|/) ?\d+|who won the|square root|quantum|stock market|gdp|theorem|equation|chemistry|physics)\b`)
	noSnapRe      = regexp.MustCompile(`(?i)(don'?t have (a )?snap|dont have (a )?sc|no snap|not on snap|deleted (my )?snap|snap got banned|don'?t use snap|i don'?t have (it|one|that)|dont have (it|one|that)|no sc\b|got no snap)`)
	englishWords  = map[string]bool{}
	nonLatinRe    = regexp.MustCompile(`[\p{Cyrillic}\p{Arabic}\p{Bengali}\p{Devanagari}\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}\p{Thai}\p{Hebrew}\p{Greek}]`)
	trailingDotRe = regexp.MustCompile(`[.,]+\s*([^\w\s]*)\s*$`)
	foreignRe     = regexp.MustCompile(`(?i)\b(hola|que|qué|como|cómo|estas|estás|guapa|bonita|bonjour|salut|ça va|ciao|bella|hallo|wie geht|kya|kaise|kaisi|haan|nahi|tum|aap|kemon|acho|tumi|apni|bhai|kaha|kahan|koi|merhaba|nasılsın|privet|kak dela|oi|tudo bem|voce|você|olá|habibi|kaif|halik|mabuhay|kumusta|apa kabar|selamat|xin chào|annyeong|konnichiwa|ni hao)\b`)
	aiPhraseRe    = regexp.MustCompile(`(?i)\b(absolutely|sounds like (you|u)|feel free|that'?s (great|awesome|wonderful|fantastic|amazing)|i'?d love to|tell me more|i'?m here to|how can i help|great question|i appreciate|certainly|definitely|wonderful|fantastic|delightful|i understand|let'?s chat|chat with you|get to know (you|u)|what about yourself|interesting!|as an ai|language model|i'?m (just )?an ai|virtual|assistant|i cannot|i can'?t help with|inappropriate|respectful|boundaries|comfortable with|let'?s keep (it|this)|i'?m not able to)\b`)
	addedRe       = regexp.MustCompile(`(?i)\b(added|sent (u|you)? ?(a )?(req|request)|just added|adding|requested|follow(ed)?)\b`)
)

func init() {
	for _, w := range strings.Fields("the a an i im i'm u you ur your we they he she it is are am was be been do did does have has had not no yes yea yeah ok okay k lol lmao haha hey hi hello sup yo wyd hbu wbu what where when why who how which and or but so if then than too also just only like wanna gonna want need can could would should will may might from in on at to for of with about by up down out off over here there this that these those my me mine our us them him her his its one two 18 19 20 21 22 23 24 25 m f male female guy girl boy man woman bored horny cute hot sexy nice cool fine good bad great love hate miss know think feel see look talk chat text snap sc insta ig add send pic pics photo show tell ask say said says go going come came back home bed sleep tired night tonight today now rn later soon old age asl from live living school college uni work working job study studying class single taken bf gf friend friends real fake bot ai nah nope sure maybe idk idc tbh ngl fr lowkey kinda sorta btw omg bruh bro dude babe baby bae damn shit fuck wtf lmfao pls please thanks thx ty np wya hmu dm dms call vc number phone what's whats thats that's it's dont don't cant can't wont won't isnt isn't aint doesnt didnt wasnt werent") {
		englishWords[w] = true
	}
}

var allMoods = []string{"friendly", "playful", "lonely", "flirty", "fun/joke", "curious", "teasing"}
var hotMoods = []string{"flirty", "teasing", "playful"}

var moodHints = map[string]string{
	"friendly": "warm and easy, like texting a new friend",
	"playful":  "light, cheeky, a little sarcastic in a cute way",
	"lonely":   "a bit bored and alone tonight, wants someone to talk to, soft",
	"flirty":   "subtle flirting, compliments back, a little suggestive but never explicit",
	"fun/joke": "make a tiny joke or funny exaggeration about what they said",
	"curious":  "genuinely curious about them, ask one specific thing",
	"teasing":  "tease them playfully about what they said, confident",
}

var lorePool = struct{ Names, Majors, Years, Tonight, Shows, Snacks []string }{
	Names:   []string{"emma", "mia", "ava", "chloe", "lily", "zoe", "ella", "madison", "hailey", "kayla", "brooke", "sophie", "riley", "paige", "lexi", "jenna", "sierra", "kenzie", "tori", "lauren", "sadie", "maddie", "kylie", "abby", "josie", "harper", "quinn", "nina", "tessa", "callie"},
	Majors:  []string{"psych", "nursing", "business", "marketing", "bio", "comm", "graphic design", "english lit", "pre law", "sociology", "fashion", "education", "poli sci", "econ", "art history"},
	Years:   []string{"freshman", "sophomore", "junior", "senior"},
	Tonight: []string{"in bed scrolling tiktok", "half watching netflix", "procrastinating an essay", "eating cereal at midnight", "lying on the floor bored", "doing a face mask", "cant sleep again", "listening to music in the dark", "home alone, roommate is out", "painting my nails badly"},
	Shows:   []string{"euphoria", "the office", "greys anatomy", "friends", "outer banks", "stranger things", "love island", "gilmore girls", "the summer i turned pretty", "new girl"},
	Snacks:  []string{"takis", "cold pizza", "cereal", "oreos", "hot cheetos", "ice cream", "ramen", "goldfish"},
}

var shareLines = []string{
	"ok fine, add me on snap 👻 %username%",
	"my snap is %username%, add me rn",
	"sc: %username% .. come find me there",
	"ok u got me lol, snap %username%",
}

type director struct {
	score      int
	tier       string
	hornyHits  int
	flirtyHits int
	underage   bool
	userAge    int
	userGender string
	askedSnap  bool
	askedTurn  int
	nudges     int
	hesitated  bool
	shared     bool
	sharedTurn int
	ended      bool
	endReason  string
	moods      []string
	lastMood   string
	lastDouble int
	coldStreak int
	// per-message signals
	userAskedSnap  bool
	userRefused    bool
	userHesitant   bool
	userQuestion   bool
	userHorny      bool
	userFlirty     bool
	userBye        bool
	userBotQ       bool
	userPicAsk     bool
	userLaugh      bool
	userLonely     bool
	userLowEffort  bool
	userShared     bool // told something about themselves
	userAdded      bool // after the share: "added u"
	userAslAsk     bool // "asl?", "how old r u", "m or f", "u?"
	userGaveAsl    bool // this message contained their age/gender
	toldAsl        bool // we already sent our own "f 21"
	userNameAsk    bool
	toldName       bool
	userInsult     bool
	userMoney      bool
	moneyHits      int
	userHardQ      bool
	userNoSnap     bool
	userNonEng     bool
	nonEngHits     int
	userRepeat     bool
	lastUserText   string
	hookPlanted    bool
	wantsSnapEarly bool // asked for snap on message 1: share on reply 2 instead
	noQuestionAt   int  // last turn that was a statement-only reply
	rng            *rand.Rand
	userPlace      string
	userName       string
	userDoing      string
}

type plan struct {
	Mood        string
	Instruction string
	Double      bool
	ShareNow    bool // append the username line after the model's lead-in
	EndAfter    bool // conversation ends after this reply
	Fixed       string
	Why         string // short reason shown in the status line
}

func newDirector(rng *rand.Rand) *director {
	moods := append([]string(nil), allMoods...)
	rng.Shuffle(len(moods), func(i, j int) { moods[i], moods[j] = moods[j], moods[i] })
	return &director{tier: tierCold, moods: moods, rng: rng}
}

// looksNonEnglish: non-latin script, or 3+ words with none of them common english.
func looksNonEnglish(low string) bool {
	if nonLatinRe.MatchString(low) || foreignRe.MatchString(low) {
		return true
	}
	words := strings.Fields(regexp.MustCompile(`[^a-z0-9' ]+`).ReplaceAllString(low, " "))
	if len(words) < 2 {
		return false
	}
	hits := 0
	for _, w := range words {
		if englishWords[w] {
			hits++
		}
	}
	return hits == 0
}

// observe scores one stranger message and updates the funnel state.
func (d *director) observe(text string) {
	low := strings.ToLower(strings.TrimSpace(text))
	words := len(strings.Fields(low))
	d.userRepeat = low != "" && low == d.lastUserText && len(low) > 1
	d.lastUserText = low
	d.userInsult = insultRe.MatchString(low)
	d.userMoney = moneyRe.MatchString(low)
	if d.userMoney {
		d.moneyHits++
	}
	d.userHardQ = hardQRe.MatchString(low)
	d.userNameAsk = nameAskRe.MatchString(low)
	d.userNoSnap = noSnapRe.MatchString(low)
	d.userNonEng = looksNonEnglish(low)
	if d.userNonEng {
		d.nonEngHits++
	}
	d.userAskedSnap = snapAskRe.MatchString(low) && !d.userNoSnap
	d.userQuestion = strings.Contains(low, "?") || questionRe.MatchString(low)
	d.userHorny = hornyRe.MatchString(low) && !d.userInsult
	d.userFlirty = flirtyRe.MatchString(low)
	d.userBye = byeRe.MatchString(low)
	d.userBotQ = botQRe.MatchString(low)
	d.userPicAsk = picAskRe.MatchString(low)
	d.userLaugh = laughRe.MatchString(low)
	d.userLonely = lonelyRe.MatchString(low)
	d.userAdded = d.shared && addedRe.MatchString(low)
	d.userAslAsk = aslAskRe.MatchString(low)
	if d.userAslAsk {
		d.userQuestion = true
	}
	_, _, hasAsl := genderAge(low)
	d.userLowEffort = !hasAsl && !d.userAskedSnap && (words <= 2 && lowEffortRe.MatchString(low) || len([]rune(low)) <= 3)
	d.userShared = !d.userQuestion && !d.userLowEffort && words >= 4
	d.userRefused = d.askedSnap && !d.shared && refuseRe.MatchString(low) && !yesRe.MatchString(low)
	d.userHesitant = d.askedSnap && !d.shared && !d.userRefused && hesitantRe.MatchString(low) && !yesRe.MatchString(low)
	d.userGaveAsl = false
	if g, a, ok := genderAge(low); ok {
		d.userGaveAsl = true
		if d.userAge == 0 {
			d.userAge = a
		}
		if d.userGender == "" && g != "" {
			d.userGender = g
		}
		if a < 18 {
			d.underage = true
		}
	}
	if d.userGender == "" {
		switch {
		case bareMaleRe.MatchString(low):
			d.userGender, d.userGaveAsl = "m", true
		case bareFemaleRe.MatchString(low):
			d.userGender, d.userGaveAsl = "f", true
		}
	}
	if d.userAge == 0 {
		if m := bareAgeRe.FindStringSubmatch(low); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 13 && n <= 70 {
				d.userAge, d.userGaveAsl = n, true
				if n < 18 {
					d.underage = true
				}
			}
		}
	}
	if d.userPlace == "" {
		if m := knownPlaces.FindString(low); m != "" {
			d.userPlace = m
		} else if m := placeRe.FindStringSubmatch(low); m != nil {
			cand := m[1]
			first := strings.Fields(cand)
			if len(first) > 0 && !placeStop.MatchString(first[0]) && !d.userHorny && !lowEffortRe.MatchString(first[0]) {
				d.userPlace = cand
			}
		}
	}
	if d.userName == "" {
		if m := nameRe.FindStringSubmatch(low); m != nil && !nameStop.MatchString(m[1]) && !englishWords[m[1]] && !knownPlaces.MatchString(m[1]) && !doingRe.MatchString(m[1]) && !hornyRe.MatchString(m[1]) && !flirtyRe.MatchString(m[1]) {
			if _, _, isAsl := genderAge(m[1]); !isAsl {
				d.userName = m[1]
			}
		}
	}
	if m := doingRe.FindString(low); m != "" {
		d.userDoing = m
	}
	if d.userGaveAsl {
		d.userLowEffort = false // "m" / "21" is an answer, not a cold reply
	}
	if d.userLowEffort {
		d.coldStreak++
	} else {
		d.coldStreak = 0
	}
	switch {
	case d.userHorny:
		d.score += 3
		d.hornyHits++
	case d.userFlirty || d.userPicAsk:
		d.score += 2
		d.flirtyHits++
	}
	if d.userQuestion {
		d.score++
	}
	if len([]rune(text)) > 25 {
		d.score++
	}
	if strings.ContainsAny(text, "😏😘😍🥵😂❤️🔥") {
		d.score++
	}
	switch {
	case d.hornyHits > 0 || d.score >= 6:
		d.tier = tierHot
	case d.score >= 3:
		d.tier = tierWarm
	default:
		d.tier = tierCold
	}
}

// known returns what the stranger already told us, for the director note.
func (d *director) known() (facts string, dontAsk []string) {
	var parts []string
	if d.userGender != "" || d.userAge > 0 {
		parts = append(parts, strings.TrimSpace(fmt.Sprintf("%s %s", d.userGender, nz(d.userAge))))
	}
	if d.userGender != "" {
		dontAsk = append(dontAsk, "gender / m or f")
	}
	if d.userAge > 0 {
		dontAsk = append(dontAsk, "age")
	}
	if d.userPlace != "" {
		parts = append(parts, "from "+d.userPlace)
		dontAsk = append(dontAsk, "where they are from")
	}
	if d.userName != "" {
		parts = append(parts, "name "+d.userName)
		dontAsk = append(dontAsk, "their name")
	}
	if d.userDoing != "" {
		parts = append(parts, "doing: "+d.userDoing)
		dontAsk = append(dontAsk, "what they are doing (wyd)")
	}
	return strings.Join(parts, ", "), dontAsk
}

func nz(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// aslAnswer builds the exact short self-intro the model should use.
func (d *director) aslAnswer(withPlace bool) string {
	if withPlace {
		return "'f <your age> <your country>'"
	}
	return "'f <your age>'"
}

func (d *director) shareTurn(cfg config) int {
	switch d.tier {
	case tierHot:
		return cfg.ShareHot
	case tierWarm:
		return cfg.ShareWarm
	}
	return cfg.ShareCold
}

// mood: context first (react to what they are doing), rotation as fallback.
func (d *director) mood(turn int) string {
	var m string
	switch {
	case d.userHorny:
		if d.lastMood == "teasing" {
			m = "flirty"
		} else {
			m = "teasing"
		}
	case d.userFlirty || d.userPicAsk:
		m = "flirty"
	case d.userLaugh:
		m = "fun/joke"
	case d.userLonely:
		m = "lonely"
	case d.coldStreak >= 2:
		if d.lastMood == "lonely" {
			m = "teasing"
		} else {
			m = "lonely"
		}
	case d.userShared:
		m = "curious"
	case d.tier == tierHot:
		m = hotMoods[(turn/2)%len(hotMoods)]
	default:
		m = d.moods[(turn/2)%len(d.moods)]
		if m == d.lastMood && turn%2 == 1 && turn > 2 {
			m = d.moods[(turn/2+1)%len(d.moods)]
		}
	}
	d.lastMood = m
	return m
}

// plan decides what bot reply number `turn` must do.
func (d *director) plan(cfg config, turn int, userText string) plan {
	p := plan{Mood: d.mood(turn)}
	base := "reply to exactly what they just said"
	switch {
	case d.userHardQ:
		base = "they ask something nerdy / complicated: do NOT answer it, dodge in 2-5 words like a bored college girl ('idk lol', 'google it lmao', 'bro its 1am')"
	case d.userAslAsk:
		base = "they ask your asl / age / gender: answer in the short app style " + d.aslAnswer(strings.Contains(userText, "from") || strings.Contains(userText, "where")) + ", nothing like 'female' or 'i am a girl'"
	case d.userGaveAsl:
		base = "they just told you their age/gender" + map[bool]string{true: " and where they are from", false: ""}[d.userPlace != ""] + ": reply with yours the same short way " + d.aslAnswer(d.userPlace != "") + " — do NOT add 'u?', 'hbu' or 'wbu', you already know theirs"
	case d.userQuestion:
		base = "they asked you something: answer it directly first, in your own words"
	case d.userShared:
		base = "pick up the specific detail they just shared and react to it"
	}

	// hard gates ---------------------------------------------------------
	if d.underage {
		d.ended, d.endReason = true, "underage"
		return plan{Fixed: "oh ur too young for me lol. bye", EndAfter: true, Why: "underage"}
	}
	if d.userBye && !d.shared {
		d.ended, d.endReason = true, "user left"
		return plan{Mood: p.Mood, Instruction: "they are leaving: say bye casually, one short line, no questions", EndAfter: true, Why: "bye"}
	}
	if d.userInsult {
		d.ended, d.endReason = true, "insult"
		return plan{Mood: "teasing", Instruction: "they insulted you: one dry unbothered line like 'ok bye weirdo' or 'lol k', 2-4 words, no question.", EndAfter: true, Why: "insult -> end"}
	}
	if d.userNonEng {
		if d.nonEngHits >= 2 {
			d.ended, d.endReason = true, "not english"
			return plan{Fixed: "english only lol, bye", EndAfter: true, Why: "non-english x2"}
		}
		return plan{Fixed: "english only lol", Why: "non-english"}
	}
	if d.userMoney {
		if d.moneyHits >= 2 {
			d.ended, d.endReason = true, "money talk"
			return plan{Mood: "teasing", Instruction: "they keep talking about money / paying / selling: shut it down and leave, 'lol no im good, bye', no question.", EndAfter: true, Why: "money x2 -> end"}
		}
		return plan{Mood: "playful", Instruction: "they mention money / paying / onlyfans / selling: shut it down casually in one line ('lol no im not selling anything' / 'lol im not that kind of girl'), no question, do NOT mention snap.", Why: "money -> shut down"}
	}
	if d.userNoSnap && (d.askedSnap || d.shared) {
		d.ended, d.endReason = true, "no snap"
		return plan{Mood: "friendly", Instruction: "they don't have snapchat: say 'aw ok' casually, no pressure, no other app, and close it nicely in one short line. no question.", EndAfter: true, Why: "no snap -> skip"}
	}
	if d.userRepeat && !d.userAskedSnap {
		return plan{Mood: "teasing", Instruction: "they sent the exact same message again: react like 'u ok lol' / 'u said that already', 3-6 words, no question.", Why: "repeat"}
	}

	// after the share -----------------------------------------------------
	if d.shared {
		switch {
		case d.userNoSnap:
			d.ended, d.endReason = true, "no snap"
			return plan{Mood: "friendly", Instruction: "they say they don't have snapchat after all: 'aw ok' casually, no pressure, no other app, close it nicely in one short line. no question.", EndAfter: true, Why: "no snap -> skip"}
		case d.userAskedSnap:
			// "whats ur snap again?" -> resend the username line
			d.sharedTurn = turn
			return plan{Mood: p.Mood, Instruction: "they ask for the snap again: say 'its' or 'here' style, 2-3 words, NO username.", ShareNow: true, Why: "resend snap"}
		case d.userAdded:
			d.ended, d.endReason = true, "added on snap"
			return plan{Mood: "flirty", Instruction: "they added you on snap: say u will accept it and talk there. one short line, no question.", EndAfter: true, Why: "added"}
		case turn-d.sharedTurn >= 2 || d.userBye:
			d.ended, d.endReason = true, "done after share"
			return plan{Mood: p.Mood, Instruction: base + ", then say ttyl and that you'll be on snap. one short line, no question.", EndAfter: true, Why: "close"}
		}
		return plan{Mood: p.Mood, Instruction: base + ". remind them casually to add you on snap (do NOT write the username, it was already sent). one short line.", Why: "remind"}
	}

	// user asks for the snap / pic / says yes after the ask -> share now ---
	if d.userAskedSnap && turn == 1 {
		d.wantsSnapEarly = true
		return plan{Mood: "teasing", Instruction: "their very first message asks for your snap: tease them for being fast ('lol slow down, who even r u') and ask their asl. one line, NO username.", Why: "snap asked on msg 1 -> slow down"}
	}
	if d.userAskedSnap || d.wantsSnapEarly || (d.askedSnap && (yesRe.MatchString(strings.ToLower(userText)) || d.userPicAsk)) {
		return d.sharePlan(turn, p.Mood, "they want your snap: say something short and playful like 'ok fine' or 'took u long enough'. one line, NO username, no question.", "user wants it")
	}
	if d.hesitated && !d.userRefused {
		return d.sharePlan(turn, p.Mood, base+". then just drop it casually like 'anyway here' / 'just add me lol'. one short line, NO username, no question.", "share after reassure")
	}
	// hesitation after the ask ("idk", "why") -> reassure once, share next turn
	if d.userHesitant && !d.hesitated {
		d.hesitated = true
		return plan{Mood: "playful", Instruction: base + ". they are unsure about snap: reassure casually (it's just easier to talk there, u barely open this app). one short line, NO username, no question.", Why: "hesitant -> reassure"}
	}
	// refusal after the ask -> one nudge, then let it go ------------------
	if d.userRefused {
		if d.nudges == 0 {
			d.nudges++
			return plan{Mood: "teasing", Instruction: "they said no to snap: tease them lightly to add you anyway, say u are barely on this app. one short line, NO username.", Why: "refused -> nudge"}
		}
		d.ended, d.endReason = true, "refused twice"
		return plan{Mood: "friendly", Instruction: "they really don't want snap: no worries, say bye nicely. one short line.", EndAfter: true, Why: "refused twice"}
	}

	target := d.shareTurn(cfg)
	switch {
	case turn >= target && (d.askedSnap || d.tier == tierHot):
		// share turn: the model writes the lead-in, we append the username
		return d.sharePlan(turn, p.Mood, base+". then say u are moving to snap because this app is boring / u barely check it. one short line, NO username, no question.", "share turn")
	case turn >= target-1 && !d.askedSnap:
		// (tier may have jumped late: ask now, share next reply)
		d.askedSnap, d.askedTurn = true, turn
		p.Why = "ask snap"
		if d.lastDouble == turn-1 {
			p.Instruction = base + " and in the same line ask if they have snapchat ('u got snap?' style). one line."
			return p
		}
		p.Instruction = base + ". then ask if they have snapchat (just 'u got snap?' style). two lines: first the answer, second the question."
		p.Double = true
		d.lastDouble = turn
		return p
	}

	// middle chat, step by step -------------------------------------------
	switch {
	case d.userNameAsk && !d.toldName:
		d.toldName = true
		p.Why = "name asked"
		p.Instruction = base + ". they ask your name: tell your first name casually ('im <your name>' / 'its <your name> lol') and ask theirs only if u don't know it. no other question."
	case d.userNameAsk:
		p.Why = "name asked again"
		p.Instruction = base + ". they ask your name again: u already said it, tease them for forgetting ('u forgot already? lol') and say it once more. no other question."
	case d.userHardQ:
		p.Why = "hard question -> dodge"
		p.Instruction = base + ". they ask something nerdy / complicated: don't actually answer it, dodge like a bored college girl ('idk lol im not that deep' / 'google it lmao' / 'bro its 1am'), then flip it to something about them."
	case d.userBotQ:
		p.Why = "bot question"
		p.Instruction = base + ". they ask if u are real / a bot: laugh it off in 3-6 words (like 'lol im real') and flip it back on them with one short question. never over-explain."
	case d.userPicAsk:
		p.Why = "pic request"
		p.Instruction = base + ". they want a pic: say not on this app lol, u only send pics on snap. don't ask for snap yet, no question."
	case d.userHorny:
		p.Why = "horny"
		p.Instruction = base + ". they are being horny: tease back, playful and confident, suggestive but NOT explicit, don't shut them down. hint it would be more fun on snap, but do NOT ask for snap yet."
	case d.userFlirty:
		p.Why = "flirty"
		p.Instruction = base + ". they are flirting: flirt back, give a small compliment, keep them chasing."
	case turn == 1:
		p.Why = "opener"
		p.Instruction = base + ". greet back casually and ask one quick thing about them (age / where from / wyd)."
	case d.userAslAsk && d.userAge > 0 && d.userGender != "":
		p.Why = "asl answer"
		d.toldAsl = true
		p.Instruction = base + ". u already know theirs so NO 'u?'. just the answer, optionally one tiny casual add like 'wyd' or 'lol'. no other question."
	case d.userAslAsk:
		p.Why = "asl answer + ask theirs"
		d.toldAsl = true
		p.Instruction = base + ", then ask theirs the same short way, like 'f <age>, u?'."
	case d.userGaveAsl && d.toldAsl:
		p.Why = "asl completed"
		p.Instruction = "they just added their " + map[bool]string{true: "gender", false: "age"}[d.userAge > 0 && d.userGender != "" && len(strings.TrimSpace(userText)) <= 6 && !strings.ContainsAny(userText, "0123456789")] + ": react in 1-3 words ('ok cool' / 'nice') and ask one easy thing (wyd / what brings u here). do NOT repeat your own asl, u already said it. no 'u?'."
	case d.userGaveAsl && d.userAge > 0 && d.userGender != "":
		p.Why = "asl back (no u?)"
		d.toldAsl = true
		p.Instruction = base + ". one line like 'f 20' or 'f 20 lol' or 'f 20, wyd' — nothing more. no 'u?'."
	case d.userGaveAsl && d.userGender == "":
		p.Why = "asl back, gender missing"
		d.toldAsl = true
		p.Instruction = base + " and only ask 'm or f?' since they didn't say it. one line like 'f 20, m or f?'."
	case d.userGaveAsl && d.userAge == 0:
		p.Why = "asl back, age missing"
		d.toldAsl = true
		p.Instruction = base + " and only ask their age ('how old r u' style). one line."
	case turn == 2 && d.userAge == 0:
		p.Why = "asl"
		d.toldAsl = true
		p.Instruction = base + ". they didn't say their age/gender yet: give yours the short way ('f <age>') and ask theirs, like 'f 21, u?'."
	case d.coldStreak >= 2:
		p.Why = "cold streak -> hook"
		hooks := []string{
			"they are giving one-word answers: tease them for being so quiet (like 'ur a man of many words huh'), then ask something easy.",
			"they are giving one-word answers: share one tiny random thing about your night (snack, show, cant sleep) and ask if they're the same.",
			"they are giving one-word answers: ask a fun either/or question (like 'netflix or going out') so answering is effortless.",
			"they are giving one-word answers: call it out playfully ('ok ur boring me a lil') and ask what would actually make them talk.",
		}
		p.Instruction = base + ". " + hooks[(d.coldStreak-2)%len(hooks)]
	case d.userLonely:
		p.Why = "lonely match"
		p.Instruction = base + ". they are bored/alone too: relate (u are home alone too), make it feel like a late night chat, ask what they usually do at night."
	case d.userShared:
		p.Why = "they opened up"
		p.Instruction = base + ". show you noticed it, then ask ONE follow-up about that exact thing. mood: " + moodHints[p.Mood] + "."
	default:
		p.Why = "keep going"
		p.Instruction = base + ". then keep the chat going with ONE short question that fits what they said. mood: " + moodHints[p.Mood] + "."
	}

	// statement-only turns: real people don't end every text with a question.
	// ~1 in 3 when they are carrying the chat themselves (not a question, not cold, not a special turn).
	if (p.Why == "keep going" || p.Why == "they opened up" || p.Why == "lonely match") && !d.userQuestion && d.coldStreak == 0 && turn > 2 && d.noQuestionAt != turn-1 && turn != target-2 && d.rng.Intn(3) == 0 {
		d.noQuestionAt = turn
		p.Why += " (no question)"
		p.Instruction = base + ". this time NO question at all: just react / add your own thought or a tiny joke, like a real text, and leave it there. mood: " + moodHints[p.Mood] + "."
		return p
	}

	// plant the snap hook 2 replies before the ask, so the ask feels natural later
	if !d.hookPlanted && turn == target-2 && !d.userHorny && !d.userPicAsk && !d.userAslAsk && !d.userGaveAsl {
		d.hookPlanted = true
		p.Why += " + snap hook"
		p.Instruction += " also slip in naturally that u are basically always on snap / u post dumb stories there (ONE mention, do NOT ask for their snap, no username)."
	}

	// double lines: when it buys engagement, never twice in a row, never on reply 1
	wantDouble := false
	switch {
	case d.userHorny || d.userFlirty:
		wantDouble = true // reaction line + hook line
	case d.coldStreak >= 2:
		wantDouble = true // tease line + easy question = restart the chat
	case d.userShared && len([]rune(userText)) > 40:
		wantDouble = true // they invested, reward with more
	case d.userLowEffort:
		wantDouble = false // mirror their energy
	case turn%3 == 0:
		wantDouble = true // rhythm
	}
	if wantDouble && d.lastDouble != turn-1 && turn > 1 && !d.userPicAsk && !d.userAslAsk && !d.userGaveAsl {
		p.Double = true
		d.lastDouble = turn
		p.Instruction += " write TWO lines: first the reaction/answer, second the question."
	}
	return p
}

func (d *director) sharePlan(turn int, mood, instruction, why string) plan {
	d.shared, d.sharedTurn = true, turn
	d.askedSnap = true
	d.lastDouble = turn
	return plan{Mood: mood, Instruction: instruction, ShareNow: true, Why: why}
}

func (d *director) status(cfg config, turn int) string {
	switch {
	case d.ended:
		return fmt.Sprintf("ENDED (%s)", d.endReason)
	case d.shared:
		return fmt.Sprintf("tier=%s score=%d | snap SHARED on turn %d", d.tier, d.score, d.sharedTurn)
	}
	extra := ""
	if d.coldStreak >= 2 {
		extra = fmt.Sprintf(" cold=%d", d.coldStreak)
	}
	return fmt.Sprintf("tier=%s score=%d | snap at reply %d (asked=%v)%s", d.tier, d.score, d.shareTurn(cfg), d.askedSnap, extra)
}

// replyProblem checks the generated lines against the plan; non-empty = retry once.
func replyProblem(lines []string, d *director, userText string, history []message) string {
	allQuestions := true
	for _, l := range lines {
		if !onlyQuestion(l) {
			allQuestions = false
		}
	}
	if d.userQuestion && allQuestions {
		return "did not answer their question, only asked back"
	}
	joined := strings.ToLower(strings.Join(lines, " "))
	if aiPhraseRe.MatchString(joined) {
		return "sounds like an ai/customer-service bot (" + aiPhraseRe.FindString(joined) + ")"
	}
	if g := parrotGram(strings.ToLower(userText), joined); g != "" {
		return "parroted their own words back (\"" + g + "\")"
	}
	if (d.userGaveAsl || d.userAslAsk) && d.userAge > 0 && d.userGender != "" && regexp.MustCompile(`\b(u|you|hbu|wbu|and u|n u|urs|yours)\s*\?`).MatchString(joined) {
		return "asked 'u?' although they already gave their age/gender"
	}
	if d.userAge > 0 && regexp.MustCompile(`\b(how old|ur age|your age|asl)\b`).MatchString(joined) {
		return "asked their age again (they said " + strconv.Itoa(d.userAge) + ")"
	}
	if d.userGender != "" && regexp.MustCompile(`\b(m or f|f or m|r u a (girl|guy)|are (u|you) a (girl|guy)|guy or girl|boy or girl)\b`).MatchString(joined) {
		return "asked their gender again (they said " + d.userGender + ")"
	}
	if d.userPlace != "" && regexp.MustCompile(`\b(where (r|are) (u|you) from|where u from|where from|where (r|are) (u|you) at|wya)\b`).MatchString(joined) {
		return "asked where they are from again (they said " + d.userPlace + ")"
	}
	if d.userName != "" && regexp.MustCompile(`\b(ur name|your name|whats ur name|what's your name)\b`).MatchString(joined) {
		return "asked their name again (" + d.userName + ")"
	}
	for _, l := range lines {
		if forbiddenRe.MatchString(l) {
			return "wrote @ / link / ai-talk"
		}
		for _, m := range history {
			if m.Role == "assistant" && strings.EqualFold(strings.TrimSpace(m.Content), strings.TrimSpace(l)) {
				return "repeated an earlier line"
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------- session ---

type session struct {
	Age      int
	Country  string
	Name     string
	Major    string
	Year     string
	Tonight  string
	Show     string
	Snack    string
	System   string
	History  []message
	Turns    int
	Director *director
}

func newSession(cfg config, personaTemplate string, rng *rand.Rand) *session {
	s := &session{
		Age:      cfg.AgeMin + rng.Intn(cfg.AgeMax-cfg.AgeMin+1),
		Country:  cfg.Countries[rng.Intn(len(cfg.Countries))],
		Director: newDirector(rng),
	}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	s.Name, s.Major, s.Year = pick(lorePool.Names), pick(lorePool.Majors), pick(lorePool.Years)
	s.Tonight, s.Show, s.Snack = pick(lorePool.Tonight), pick(lorePool.Shows), pick(lorePool.Snacks)
	sys := strings.ReplaceAll(personaTemplate, "{age}", strconv.Itoa(s.Age))
	sys = strings.ReplaceAll(sys, "{country}", s.Country)
	for k, v := range map[string]string{"{name}": s.Name, "{major}": s.Major, "{year}": s.Year, "{tonight}": s.Tonight, "{show}": s.Show, "{snack}": s.Snack} {
		sys = strings.ReplaceAll(sys, k, v)
	}
	s.System = sys
	return s
}

func buildMessages(s *session, cfg config, p plan, turn int) []message {
	msgs := []message{{Role: "system", Content: s.System}}
	h := s.History
	if cfg.History > 0 && len(h) > cfg.History {
		h = h[len(h)-cfg.History:]
	}
	msgs = append(msgs, h...)
	note := fmt.Sprintf("[director note for your reply #%d — follow it, never mention it]\nmood right now: %s (%s).\nwhat this message must do: %s\n", turn, p.Mood, moodHints[p.Mood], p.Instruction)
	if facts, dontAsk := s.Director.known(); facts != "" {
		note += fmt.Sprintf("what they already told you: %s. NEVER ask again for: %s. a real person remembers.\n", facts, strings.Join(dontAsk, ", "))
	}
	if p.Double {
		note += "format: exactly TWO short lines separated by a newline, each max 8 words.\n"
	} else {
		note += "format: ONE short line, max 10 words. no newline.\n"
	}
	note += "never leave their question unanswered. never ask two questions. never write a username or @."
	msgs = append(msgs, message{Role: "system", Content: note})
	return msgs
}

// ------------------------------------------------------------------- main ---

func main() {
	dir := exeDir()
	cfg, err := loadConfig(filepath.Join(dir, "apitest.config.txt"))
	if err != nil {
		fatal(err)
	}
	personaTemplate, err := loadPersona(filepath.Join(dir, "persona.txt"))
	if err != nil {
		fatal(err)
	}
	mode := "chat"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	switch mode {
	case "ping":
		ping(cfg)
	case "chat":
		chatLoop(cfg, personaTemplate)
	case "compare":
		name := ""
		if len(os.Args) > 2 {
			name = strings.ToLower(os.Args[2])
		}
		compareRun(cfg, personaTemplate, name)
	case "scenario", "scenarios", "run":
		name := ""
		if len(os.Args) > 2 {
			name = strings.ToLower(os.Args[2])
		}
		scenarioRun(cfg, personaTemplate, name)
	default:
		fmt.Println("usage: apitest.exe [chat|ping|scenario <name|all>|compare <scenario>]")
		os.Exit(2)
	}
}

type stats struct {
	Replies, Lines, Words, Retries, Warns, Doubles int
	PromptTok, ComplTok                            int
	Seconds                                        float64
	ShareTurn                                      int
}

type runner struct {
	cfg     config
	persona string
	rng     *rand.Rand
	s       *session
	st      stats
	logf    *os.File
}

func newRunner(cfg config, personaTemplate string) *runner {
	r := &runner{cfg: cfg, persona: personaTemplate, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	r.s = newSession(cfg, personaTemplate, r.rng)
	if f, err := os.OpenFile(filepath.Join(exeDir(), "apitest.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		r.logf = f
	}
	r.logLine("=== new chat %s | model %s | persona f %d %s name %s ===", time.Now().Format("2006-01-02 15:04:05"), cfg.Model, r.s.Age, r.s.Country, r.s.Name)
	return r
}

func (r *runner) close() {
	if r.logf != nil {
		r.logf.Close()
		r.logf = nil
	}
}

func (r *runner) logLine(format string, a ...any) {
	if r.logf != nil {
		fmt.Fprintf(r.logf, format+"\n", a...)
	}
}

func (r *runner) reset() {
	r.summary()
	r.s = newSession(r.cfg, r.persona, r.rng)
	r.st = stats{}
	r.logLine("=== new chat %s | persona f %d %s name %s ===", time.Now().Format("15:04:05"), r.s.Age, r.s.Country, r.s.Name)
}

func (r *runner) summary() {
	if r.st.Replies == 0 {
		return
	}
	share := "not shared"
	if r.st.ShareTurn > 0 {
		share = fmt.Sprintf("shared on reply %d", r.st.ShareTurn)
	}
	avgW := float64(r.st.Words) / float64(max(r.st.Lines, 1))
	line := fmt.Sprintf("--- summary: %d replies | %s | tier %s | retries %d | warns %d | doubles %d | avg %.1f words/line | %.1fs avg | %d+%d tokens | %s ---",
		r.st.Replies, share, r.s.Director.tier, r.st.Retries, r.st.Warns, r.st.Doubles, avgW, r.st.Seconds/float64(r.st.Replies), r.st.PromptTok, r.st.ComplTok, r.s.Director.status(r.cfg, r.s.Turns+1))
	fmt.Println(line)
	r.logLine("%s", line)
}

// step handles one stranger message end to end and prints the bot reply.
func (r *runner) step(text string) {
	s, cfg, d := r.s, r.cfg, r.s.Director
	if d.ended {
		fmt.Println("     (chat ended: " + d.endReason + " — the bot would not reply anymore. /new for a new stranger)")
		return
	}
	d.observe(text)
	turn := s.Turns + 1
	p := d.plan(cfg, turn, text)
	s.History = append(s.History, message{Role: "user", Content: text})
	r.logLine("you> %s", text)

	var lines []string
	info := ""
	took := 0.0
	retries := 0
	if p.Fixed != "" {
		lines = []string{p.Fixed}
	} else {
		msgs := buildMessages(s, cfg, p, turn)
		if cfg.ShowPrompt {
			fmt.Println("--- request messages ---")
			for _, m := range msgs {
				fmt.Printf("[%s] %s\n", m.Role, m.Content)
			}
			fmt.Println("------------------------")
		}
		start := time.Now()
		reply, ci, err := complete(cfg, msgs)
		took = time.Since(start).Seconds()
		info = ci.Text
		r.st.PromptTok += ci.Prompt
		r.st.ComplTok += ci.Compl
		if err != nil {
			fmt.Printf("eva> (no reply)\n     ERROR after %.1fs: %v\n", took, err)
			fmt.Printf("     plan was: mood=%s double=%v share=%v | %s\n", p.Mood, p.Double, p.ShareNow, p.Instruction)
			r.logLine("ERROR: %v", err)
			return
		}
		var warn string
		lines, warn = sanitizeLines(reply, p.Double)
		if warn != "" {
			r.st.Warns++
			fmt.Printf("     WARN: %s\n     raw: %q\n", warn, strings.TrimSpace(reply))
			r.logLine("WARN: %s | raw %q", warn, strings.TrimSpace(reply))
		}
		if problem := replyProblem(lines, d, text, s.History); problem != "" {
			retries++
			r.st.Retries++
			fmt.Printf("     RETRY: %s -> asking the model once more\n     first try: %q\n", problem, strings.Join(lines, " / "))
			r.logLine("RETRY: %s | first try %q", problem, strings.Join(lines, " / "))
			fix := fmt.Sprintf("your previous draft was rejected: %s. rewrite it. their message was: %q. answer it first, keep the same mood and format, say something new.", problem, text)
			msgs = append(msgs, message{Role: "system", Content: fix})
			start2 := time.Now()
			reply2, ci2, err2 := complete(cfg, msgs)
			took += time.Since(start2).Seconds()
			r.st.PromptTok += ci2.Prompt
			r.st.ComplTok += ci2.Compl
			if err2 == nil {
				info = ci2.Text
				lines, warn = sanitizeLines(reply2, p.Double)
				if warn != "" {
					fmt.Printf("     WARN: %s\n     raw: %q\n", warn, strings.TrimSpace(reply2))
				}
				if problem2 := replyProblem(lines, d, text, s.History); problem2 != "" {
					fmt.Printf("     STILL: %s (kept anyway — the real bot would fall back to a TXT line)\n", problem2)
					r.logLine("STILL: %s", problem2)
				}
			}
		}
		lines = polish(lines, s.History)
	}
	if p.ShareNow {
		user := cfg.SnapUsername
		if user == "" {
			user = "yoursnapname"
		}
		lines = append(lines, strings.ReplaceAll(shareLines[r.rng.Intn(len(shareLines))], "%username%", user))
		if r.st.ShareTurn == 0 {
			r.st.ShareTurn = turn
		}
	}
	for _, line := range lines {
		fmt.Printf("eva> %s\n", line)
		s.History = append(s.History, message{Role: "assistant", Content: line})
		r.st.Lines++
		r.st.Words += len(strings.Fields(line))
		r.logLine("eva> %s", line)
	}
	s.Turns++
	r.st.Replies++
	r.st.Seconds += took
	if p.Double {
		r.st.Doubles++
	}
	status := fmt.Sprintf("[%.1fs%s] reply %d | mood %s | %s | %s", took, info, turn, p.Mood, p.Why, d.status(cfg, turn+1))
	if p.Double {
		status += " | double"
	}
	if p.EndAfter {
		status += " | END"
	}
	fmt.Println("     " + status)
	r.logLine("     %s", status)
}

func chatLoop(cfg config, personaTemplate string) {
	r := newRunner(cfg, personaTemplate)
	defer r.close()
	defer r.summary()
	printBanner(cfg, r.s)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for {
		fmt.Print("you> ")
		if !in.Scan() {
			return
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		s := r.s
		switch strings.ToLower(text) {
		case "/quit", "/exit", "/q":
			return
		case "/new":
			r.reset()
			fmt.Printf("--- new chat: persona f %d from %s (%s) | moods: %s ---\n", r.s.Age, r.s.Country, r.s.Name, strings.Join(r.s.Director.moods, " > "))
			continue
		case "/prompt":
			r.cfg.ShowPrompt = !r.cfg.ShowPrompt
			fmt.Printf("--- show prompt: %v ---\n", r.cfg.ShowPrompt)
			continue
		case "/system":
			fmt.Println("--- system prompt ---\n" + s.System + "\n---------------------")
			continue
		case "/state":
			facts, _ := s.Director.known()
			fmt.Printf("--- %s | knows: %q | horny %d flirty %d | turn %d | me: %s, %s %s, %s ---\n", s.Director.status(cfg, s.Turns+1), facts, s.Director.hornyHits, s.Director.flirtyHits, s.Turns, s.Name, s.Year, s.Major, s.Tonight)
			continue
		case "/history":
			for i, m := range s.History {
				fmt.Printf("%2d %-9s %s\n", i+1, m.Role+":", m.Content)
			}
			continue
		case "/summary":
			r.summary()
			continue
		case "/help":
			printHelp()
			continue
		}
		r.step(text)
	}
}

// scenarioRun feeds scripted stranger lines (scenarios.txt) through the same pipeline.
func scenarioRun(cfg config, personaTemplate string, name string) {
	scen, err := loadScenarios(filepath.Join(exeDir(), "scenarios.txt"))
	if err != nil {
		fatal(err)
	}
	var names []string
	if name == "" || name == "all" {
		for n := range scen {
			names = append(names, n)
		}
		sort.Strings(names)
	} else {
		if _, ok := scen[name]; !ok {
			var have []string
			for n := range scen {
				have = append(have, n)
			}
			sort.Strings(have)
			fatal(fmt.Errorf("scenario %q not in scenarios.txt (have: %s)", name, strings.Join(have, ", ")))
		}
		names = []string{name}
	}
	r := newRunner(cfg, personaTemplate)
	for i, n := range names {
		if i > 0 {
			r.reset()
		}
		fmt.Printf("\n################ scenario: %s  (persona f %d %s, name %s) ################\n", n, r.s.Age, r.s.Country, r.s.Name)
		r.logLine("### scenario %s", n)
		for _, line := range scen[n] {
			if r.s.Director.ended {
				fmt.Printf("you> %s\n     (chat already ended: %s)\n", line, r.s.Director.endReason)
				break
			}
			fmt.Printf("you> %s\n", line)
			r.step(line)
		}
	}
	r.summary()
	r.close()
	fmt.Println("\n(full transcript appended to apitest.log — send that file for review)")
}

// compareRun plays ONE scenario on every model in COMPARE_MODELS and prints a table.
func compareRun(cfg config, personaTemplate string, name string) {
	if len(cfg.Models) == 0 {
		fatal(errors.New("set COMPARE_MODELS=modelA,modelB,... in apitest.config.txt first"))
	}
	if name == "" {
		name = "warm"
	}
	scen, err := loadScenarios(filepath.Join(exeDir(), "scenarios.txt"))
	if err != nil {
		fatal(err)
	}
	lines, ok := scen[name]
	if !ok {
		fatal(fmt.Errorf("scenario %q not in scenarios.txt", name))
	}
	type row struct {
		model string
		st    stats
		errs  int
		end   string
	}
	var rows []row
	for _, m := range cfg.Models {
		c := cfg
		c.Model = m
		r := newRunner(c, personaTemplate)
		fmt.Printf("\n################ model: %s | scenario: %s ################\n", m, name)
		r.logLine("### compare model %s scenario %s", m, name)
		errs := 0
		for _, line := range lines {
			if r.s.Director.ended {
				break
			}
			fmt.Printf("you> %s\n", line)
			before := r.st.Replies
			r.step(line)
			if r.st.Replies == before {
				errs++
				if errs >= 2 {
					fmt.Println("     (two errors in a row - skipping this model)")
					break
				}
			}
		}
		r.summary()
		r.close()
		rows = append(rows, row{m, r.st, errs, r.s.Director.status(c, r.s.Turns+1)})
	}
	fmt.Println("\n================ compare: scenario " + name + " ================")
	fmt.Printf("%-45s %7s %6s %7s %5s %9s %7s %8s  %s\n", "model", "replies", "share", "retries", "warns", "words/ln", "avg s", "tokens", "end state")
	for _, r := range rows {
		share := "-"
		if r.st.ShareTurn > 0 {
			share = strconv.Itoa(r.st.ShareTurn)
		}
		avgW, avgS := 0.0, 0.0
		if r.st.Lines > 0 {
			avgW = float64(r.st.Words) / float64(r.st.Lines)
		}
		if r.st.Replies > 0 {
			avgS = r.st.Seconds / float64(r.st.Replies)
		}
		state := r.end
		if r.errs > 0 {
			state = fmt.Sprintf("%d errors | %s", r.errs, state)
		}
		fmt.Printf("%-45s %7d %6s %7d %5d %9.1f %7.1f %8d  %s\n", trim(r.model, 45), r.st.Replies, share, r.st.Retries, r.st.Warns, avgW, avgS, r.st.PromptTok+r.st.ComplTok, trim(state, 40))
	}
	fmt.Println("lower retries/warns + short words/ln + share on the expected reply = better model for this job. transcripts in apitest.log")
}

func loadScenarios(path string) (map[string][]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scenarios.txt missing next to apitest.exe: %w", err)
	}
	out := map[string][]string{}
	cur := ""
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			cur = strings.ToLower(strings.TrimSpace(l[1 : len(l)-1]))
			continue
		}
		if cur != "" {
			out[cur] = append(out[cur], l)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("scenarios.txt has no [sections]")
	}
	return out, nil
}

// ---------------------------------------------------------------- API ---

type callInfo struct {
	Text          string
	Prompt, Compl int
}

func complete(cfg config, msgs []message) (string, callInfo, error) {
	var ci callInfo
	status, body, err := rawCall(cfg, msgs, cfg.MaxTokens)
	if err != nil {
		return "", ci, err
	}
	var parsed chatResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		return "", ci, fmt.Errorf("HTTP %d, non-JSON body: %s", status, trim(string(body), 300))
	}
	if status >= 400 {
		msg := trim(string(body), 300)
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		if status == 403 && strings.Contains(strings.ToLower(msg), "gated") {
			msg += "\n     FIX: this model needs HuggingFace access approval. easiest: set a non-gated model in apitest.config.txt, e.g.\n       MODEL=Qwen/Qwen2.5-72B-Instruct   or   MODEL=mistralai/Mistral-Nemo-Instruct-2407   or   MODEL=NousResearch/Hermes-3-Llama-3.1-70B"
		}
		return "", ci, fmt.Errorf("HTTP %d: %s", status, msg)
	}
	if len(parsed.Choices) == 0 {
		return "", ci, errors.New("no choices in response")
	}
	if parsed.Usage != nil {
		ci.Prompt, ci.Compl = parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens
		ci.Text = fmt.Sprintf(", %d+%d tok", ci.Prompt, ci.Compl)
	}
	choice := parsed.Choices[0]
	if fr := choice.FinishReason; fr != "" && fr != "stop" {
		ci.Text += ", finish=" + fr
	}
	content := choice.Message.Content
	if strings.TrimSpace(thinkRe.ReplaceAllString(content, "")) == "" {
		reasoning := strings.TrimSpace(choice.Message.ReasoningContent)
		if reasoning == "" {
			reasoning = strings.TrimSpace(content)
		}
		hint := "model spent all tokens THINKING and never wrote the reply"
		if reasoning != "" {
			hint += fmt.Sprintf(" (reasoning %d chars: %q)", len(reasoning), trim(reasoning, 160))
		}
		hint += "\n     FIX: raise MAX_TOKENS (e.g. 1024) or disable thinking with EXTRA_JSON in apitest.config.txt, or use a non-reasoning model (e.g. meta-llama/Llama-3.3-70B-Instruct)"
		return "", ci, errors.New(hint)
	}
	return content, ci, nil
}

// noThinkFields returns request fields that switch off "thinking" for known hybrid
// reasoning families, so the model writes the chat line instead of reasoning tokens.
func noThinkFields(model string) map[string]any {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "qwen3"), strings.Contains(m, "qwen-3"), strings.Contains(m, "qwen4"):
		// Qwen3 / 3.5 / 3.6 / 3.8 hybrid thinking (vLLM chat template kwarg)
		return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}
	case strings.Contains(m, "glm-4.5"), strings.Contains(m, "glm-4.6"), strings.Contains(m, "glm-4.7"), strings.Contains(m, "glm-5"), strings.Contains(m, "glm5"):
		return map[string]any{"thinking": map[string]any{"type": "disabled"}, "chat_template_kwargs": map[string]any{"enable_thinking": false}}
	case strings.Contains(m, "deepseek-v3.1"), strings.Contains(m, "deepseek-v3.2"), strings.Contains(m, "deepseek-v4"), strings.Contains(m, "deepseek-r"):
		return map[string]any{"chat_template_kwargs": map[string]any{"thinking": false, "enable_thinking": false}, "thinking": map[string]any{"type": "disabled"}}
	case strings.Contains(m, "kimi-k2.5"), strings.Contains(m, "kimi-k3"), strings.Contains(m, "kimi-k2-thinking"):
		return map[string]any{"thinking": map[string]any{"type": "disabled"}, "chat_template_kwargs": map[string]any{"thinking": false, "enable_thinking": false}}
	case strings.Contains(m, "gpt-oss"):
		return map[string]any{"reasoning_effort": "low"} // cannot be fully disabled
	case strings.Contains(m, "minimax-m"), strings.Contains(m, "mimo"), strings.Contains(m, "step-3"), strings.Contains(m, "nemotron"):
		return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false, "thinking": false}}
	}
	return nil
}

func noThinkLabel(cfg config) string {
	if !cfg.AutoNoThink {
		return "off (AUTO_NOTHINK=0)"
	}
	f := noThinkFields(cfg.Model)
	if f == nil {
		return "not needed for this model"
	}
	b, _ := json.Marshal(f)
	return "auto " + string(b)
}

func rawCall(cfg config, msgs []message, maxTokens int) (int, []byte, error) {
	fields := map[string]any{
		"model":       cfg.Model,
		"messages":    msgs,
		"max_tokens":  maxTokens,
		"temperature": cfg.Temperature,
	}
	if cfg.AutoNoThink {
		for k, v := range noThinkFields(cfg.Model) {
			fields[k] = v
		}
	}
	for k, v := range cfg.ExtraJSON { // explicit EXTRA_JSON always wins
		fields[k] = v
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := (&http.Client{Timeout: cfg.Timeout}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func ping(cfg config) {
	fmt.Printf("POST %s/chat/completions  model=%s\n", cfg.BaseURL, cfg.Model)
	start := time.Now()
	status, body, err := rawCall(cfg, []message{
		{Role: "system", Content: "reply with exactly: pong"},
		{Role: "user", Content: "ping"},
	}, cfg.MaxTokens)
	fmt.Printf("took %.1fs\n", time.Since(start).Seconds())
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("HTTP", status)
	fmt.Println(prettyJSON(body))
	var parsed chatResponse
	if json.Unmarshal(body, &parsed) == nil && len(parsed.Choices) > 0 {
		said := strings.TrimSpace(parsed.Choices[0].Message.Content)
		fmt.Printf("\nmodel said: %q\n", said)
		if said == "" {
			fmt.Println("RESULT: API OK but EMPTY reply -> reasoning model used all tokens thinking.")
			fmt.Println("        -> set MAX_TOKENS=1024 or EXTRA_JSON to disable thinking, or pick a non-reasoning model")
			return
		}
		fmt.Println("RESULT: API OK")
		return
	}
	if status >= 400 {
		msg := "see body above"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		fmt.Printf("RESULT: API ERROR HTTP %d - %s\n", status, msg)
		switch status {
		case 401, 403:
			fmt.Println("        -> API_KEY wrong / missing (apitest.config.txt)")
		case 404:
			fmt.Println("        -> BASE_URL or MODEL id wrong")
		case 429:
			fmt.Println("        -> rate / concurrency limit of your plan - wait and retry")
		}
		os.Exit(1)
	}
	fmt.Println("RESULT: API answered but no choices -> check model id")
	os.Exit(1)
}

// ------------------------------------------------------------- sanitize ---

// sanitizeLines applies the rules the real bot would enforce and returns the
// line(s) to send (1, or 2 when a double line was requested).
func sanitizeLines(raw string, double bool) ([]string, string) {
	text := strings.TrimSpace(thinkRe.ReplaceAllString(raw, ""))
	var warns []string
	if strings.Contains(raw, "<think>") {
		warns = append(warns, "model emitted <think> reasoning (stripped)")
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "\"'“”`"))
		l = strings.TrimLeft(l, "-•*0123456789. ")
		for _, prefix := range []string{"you:", "me:", "eva:", "bot:", "line 1:", "line 2:", "line1:", "line2:"} {
			if strings.HasPrefix(strings.ToLower(l), prefix) {
				l = strings.TrimSpace(l[len(prefix):])
			}
		}
		l = strings.ReplaceAll(l, "—", ", ")
		l = strings.ReplaceAll(l, " - ", ", ")
		l = strings.Join(strings.Fields(l), " ")
		l = strings.ReplaceAll(l, "!", "")
		l = trailingDotRe.ReplaceAllString(l, " $1")
		l = strings.TrimRight(strings.Join(strings.Fields(l), " "), ".!, ")
		if l != "" {
			lines = append(lines, strings.ToLower(l))
		}
	}
	if len(lines) == 0 {
		return []string{"(empty)"}, "empty reply -> bot would use a TXT fallback line"
	}
	want := 1
	if double {
		want = 2
	}
	if len(lines) > want {
		warns = append(warns, fmt.Sprintf("model wrote %d lines, kept %d", len(lines), want))
		lines = lines[:want]
	}
	for _, l := range lines {
		if len([]rune(l)) > 120 {
			warns = append(warns, "line too long (>120 chars) -> bot would fall back")
		}
		if n := len(strings.Fields(l)); n > 14 {
			warns = append(warns, fmt.Sprintf("%d words (target <=10)", n))
		}
		if forbiddenRe.MatchString(l) {
			warns = append(warns, "contains @ / link / ai-talk -> bot would fall back")
		}
		if strings.Count(l, "?") > 1 {
			warns = append(warns, "two questions in one line")
		}
	}
	return lines, strings.Join(warns, "; ")
}

// onlyQuestion: the line is nothing but a question (starts with a question word, ends with ?).
// "nothing much, u?" is an answer + question -> false. "what about u?" -> true.
func onlyQuestion(l string) bool {
	l = strings.TrimSpace(strings.ToLower(l))
	if !strings.HasSuffix(l, "?") {
		return false
	}
	f := strings.Fields(strings.Trim(l, "?"))
	if len(f) == 0 {
		return true
	}
	switch strings.Trim(f[0], ",.") {
	case "what", "how", "where", "why", "who", "when", "which", "wyd", "hbu", "wbu", "r", "are", "do", "did", "does", "u", "you", "can", "could", "would", "will", "is", "got", "any", "so", "and", "ur", "your", "whats", "what's", "hows", "how's", "wanna", "want", "ever", "have", "wya", "asl":
		return len(f) <= 7
	}
	return false
}

// parrotGram returns a 4-word run of the stranger's message that appears verbatim in the reply.
func parrotGram(user, reply string) string {
	clean := func(t string) []string {
		return strings.Fields(regexp.MustCompile(`[^a-z0-9' ]+`).ReplaceAllString(t, " "))
	}
	uw := clean(user)
	rw := " " + strings.Join(clean(reply), " ") + " "
	for i := 0; i+4 <= len(uw); i++ {
		g := strings.Join(uw[i:i+4], " ")
		if strings.Contains(rw, " "+g+" ") {
			return g
		}
	}
	return ""
}

// polish removes bot-tells that are easier to fix than to retry:
// same emoji twice, emoji in two of the last three replies, "lol"/"haha" opener two replies in a row.
func polish(lines []string, history []message) []string {
	var recent []string
	for i := len(history) - 1; i >= 0 && len(recent) < 3; i-- {
		if history[i].Role == "assistant" {
			recent = append(recent, history[i].Content)
		}
	}
	recentEmoji := false
	usedEmoji := map[rune]bool{}
	for _, m := range history {
		if m.Role == "assistant" {
			for _, r := range m.Content {
				if isEmoji(r) {
					usedEmoji[r] = true
				}
			}
		}
	}
	for _, r := range recent {
		for _, c := range r {
			if isEmoji(c) {
				recentEmoji = true
			}
		}
	}
	lastOpeners := map[string]bool{}
	for i, r := range recent {
		if i > 1 {
			break
		}
		if f := strings.Fields(r); len(f) > 0 {
			if w := strings.Trim(f[0], ",."); w == "lol" || w == "haha" || w == "lmao" || w == "omg" {
				lastOpeners[w] = true
			}
		}
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		var b strings.Builder
		kept := false
		for _, c := range l {
			if isEmoji(c) {
				if recentEmoji || usedEmoji[c] || kept {
					continue
				}
				kept = true
			}
			b.WriteRune(c)
		}
		l = strings.TrimRight(strings.TrimSpace(strings.Join(strings.Fields(b.String()), " ")), ".,! ")
		if f := strings.Fields(l); len(f) > 1 && lastOpeners[strings.Trim(f[0], ",.")] {
			l = strings.TrimSpace(strings.TrimLeft(strings.Join(f[1:], " "), ", "))
		}
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return lines
	}
	return out
}

func isEmoji(r rune) bool {
	return r >= 0x1F300 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF || r == 0x2764 || r == 0xFE0F
}

func genderAge(low string) (string, int, bool) {
	for i, re := range ageRe {
		m := re.FindStringSubmatch(low)
		if len(m) < 2 {
			continue
		}
		var g, a string
		switch i {
		case 0, 2:
			g, a = m[1], m[2]
		case 1, 3:
			a, g = m[1], m[2]
		default:
			a = m[1]
		}
		switch g {
		case "male", "guy", "boy", "man", "dude":
			g = "m"
		case "female", "girl", "woman":
			g = "f"
		}
		n, err := strconv.Atoi(a)
		if err == nil && n >= 13 && n <= 99 {
			return g, n, true
		}
	}
	return "", 0, false
}

// -------------------------------------------------------------- config ---

func loadConfig(path string) (config, error) {
	cfg := config{
		AutoNoThink: true,
		BaseURL:     "https://api.featherless.ai/v1",
		Model:       "deepseek-ai/DeepSeek-V3-0324",
		Timeout:     60 * time.Second,
		MaxTokens:   1024,
		Temperature: 0.9,
		History:     30,
		AgeMin:      20,
		AgeMax:      25,
		Countries:   []string{"uk", "germany", "france", "italy", "spain", "netherlands", "sweden", "poland", "ireland", "norway"},
		ShareHot:    4,
		ShareWarm:   6,
		ShareCold:   8,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, fmt.Errorf("%s not found (apitest.bat creates it) - fill in API_KEY first", filepath.Base(path))
		}
		return cfg, err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		intVal := func(min int) (int, bool) {
			n, err := strconv.Atoi(value)
			return n, err == nil && n >= min
		}
		switch key {
		case "BASE_URL":
			cfg.BaseURL = strings.TrimRight(value, "/")
		case "MODEL":
			cfg.Model = value
		case "API_KEY":
			cfg.APIKey = value
		case "SNAP_USERNAME":
			cfg.SnapUsername = strings.TrimPrefix(value, "@")
		case "TIMEOUT_SEC":
			if n, ok := intVal(1); ok {
				cfg.Timeout = time.Duration(n) * time.Second
			}
		case "MAX_TOKENS":
			if n, ok := intVal(1); ok {
				cfg.MaxTokens = n
			}
		case "TEMPERATURE":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				cfg.Temperature = f
			}
		case "HISTORY":
			if n, ok := intVal(0); ok {
				cfg.History = n
			}
		case "AGE_MIN":
			if n, ok := intVal(18); ok {
				cfg.AgeMin = n
			}
		case "AGE_MAX":
			if n, ok := intVal(18); ok {
				cfg.AgeMax = n
			}
		case "SHARE_HOT":
			if n, ok := intVal(2); ok {
				cfg.ShareHot = n
			}
		case "SHARE_WARM":
			if n, ok := intVal(2); ok {
				cfg.ShareWarm = n
			}
		case "SHARE_COLD":
			if n, ok := intVal(2); ok {
				cfg.ShareCold = n
			}
		case "EXTRA_JSON":
			var extra map[string]any
			if err := json.Unmarshal([]byte(value), &extra); err != nil {
				return cfg, fmt.Errorf("EXTRA_JSON is not valid JSON: %v", err)
			}
			cfg.ExtraJSON = extra
		case "AUTO_NOTHINK":
			cfg.AutoNoThink = value != "0" && !strings.EqualFold(value, "false") && !strings.EqualFold(value, "off")
		case "COMPARE_MODELS", "MODELS":
			for _, m := range strings.Split(value, ",") {
				if m = strings.TrimSpace(m); m != "" {
					cfg.Models = append(cfg.Models, m)
				}
			}
		case "COUNTRIES":
			var list []string
			for _, c := range strings.Split(value, ",") {
				if c = strings.TrimSpace(strings.ToLower(c)); c != "" {
					list = append(list, c)
				}
			}
			if len(list) > 0 {
				cfg.Countries = list
			}
		}
	}
	if cfg.AgeMax < cfg.AgeMin {
		cfg.AgeMax = cfg.AgeMin
	}
	if cfg.APIKey == "" || strings.Contains(cfg.APIKey, "your-") {
		return cfg, errors.New("API_KEY is empty in apitest.config.txt - paste your key and run again")
	}
	if cfg.Model == "" || cfg.BaseURL == "" {
		return cfg, errors.New("MODEL / BASE_URL must not be empty")
	}
	return cfg, nil
}

func loadPersona(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("persona.txt: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", errors.New("persona.txt is empty")
	}
	return text, nil
}

// --------------------------------------------------------------- misc ---

func printBanner(cfg config, s *session) {
	fmt.Println("==========================================================")
	fmt.Println(" apitest - chat with the AI persona (you are the stranger)")
	fmt.Printf(" api:     %s\n model:   %s\n thinking-off: %s\n", cfg.BaseURL, cfg.Model, noThinkLabel(cfg))
	fmt.Printf(" persona: f %d from %s, name %s (only told if asked)   (persona.txt, /new = re-roll)\n", s.Age, s.Country, s.Name)
	fmt.Printf(" snap at: HOT reply %d | WARM %d | COLD %d   username: %s\n", cfg.ShareHot, cfg.ShareWarm, cfg.ShareCold, orDefault(cfg.SnapUsername, "(set SNAP_USERNAME)"))
	fmt.Printf(" moods:   %s\n", strings.Join(s.Director.moods, " > "))
	fmt.Println(" type a message, or /help for commands")
	fmt.Println("==========================================================")
}

func printHelp() {
	fmt.Println(" /new      new chat with a new random persona + mood order")
	fmt.Println(" /state    interest tier / score / when the snap will be shared")
	fmt.Println(" /system   show the current system prompt")
	fmt.Println(" /prompt   toggle printing the full request (incl. director note) before each call")
	fmt.Println(" /history  show the conversation memory sent to the model")
	fmt.Println(" /summary  stats of this chat so far (retries, tokens, share turn)")
	fmt.Println(" /quit     exit")
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func exeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

func prettyJSON(b []byte) string {
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") != nil {
		return trim(string(b), 2000)
	}
	return trim(out.String(), 4000)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func fatal(err error) {
	fmt.Println("ERROR:", err)
	os.Exit(1)
}
