package robotspolicy

import "testing"

func TestRulesExtraction(t *testing.T) {
	data, err := FromString("User-agent: *\nDisallow: /private\nAllow: /private/public\nUser-agent: bot\nDisallow: /bot-only\n")
	if err != nil {
		t.Fatal(err)
	}
	rules := data.Rules("*")
	if len(rules) != 2 {
		t.Fatalf("rules=%+v", rules)
	}
	bot := data.Rules("bot")
	if len(bot) != 1 || bot[0].Path != "/bot-only" {
		t.Fatalf("bot rules=%+v", bot)
	}
}

func TestTestRulesMostSpecificWins(t *testing.T) {
	rules := []Rule{
		{Path: "/private", Allow: false},
		{Path: "/private/public", Allow: true},
	}
	if TestRules(rules, "/private/x") {
		t.Fatal("/private/x should be denied")
	}
	if !TestRules(rules, "/private/public/x") {
		t.Fatal("/private/public/x should be allowed")
	}
	if !TestRules(rules, "/other") {
		t.Fatal("default should be allow")
	}
}

func TestTestRulesPattern(t *testing.T) {
	data, err := FromString("User-agent: *\nDisallow: /*.pdf$\n")
	if err != nil {
		t.Fatal(err)
	}
	rules := data.Rules("*")
	if TestRules(rules, "/docs/a.pdf") {
		t.Fatal("pdf should be denied")
	}
	if !TestRules(rules, "/docs/a.html") {
		t.Fatal("html should be allowed")
	}
}

func TestTestRulesMatchesGroupSemantics(t *testing.T) {
	const body = "User-agent: *\nDisallow: /a\nDisallow: /a/b/c\nAllow: /a/b\n"
	data, err := FromString(body)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/a", "/a/b", "/a/b/c", "/a/b/c/d", "/x"}
	for _, p := range paths {
		want := data.TestAgent(p, "*")
		got := TestRules(data.Rules("*"), p)
		if want != got {
			t.Fatalf("path %s: TestAgent=%v TestRules=%v", p, want, got)
		}
	}
}
