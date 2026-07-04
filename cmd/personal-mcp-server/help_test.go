package main

import "testing"

func TestCommandHelpHasCoreTopics(t *testing.T) {
	topics := []string{
		"top",
		"serve",
		"doctor",
		"init",
		"config",
		"config validate",
		"project",
		"project init",
		"approvals",
		"audit",
		"service",
		"upgrade",
		"upgrade local",
	}
	for _, topic := range topics {
		if commandHelp[topic] == "" {
			t.Fatalf("missing command help topic %q", topic)
		}
	}
}

func TestIsHelpArg(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		if !isHelpArg(arg) {
			t.Fatalf("%q should be a help arg", arg)
		}
	}
	if isHelpArg("helps") {
		t.Fatal("helps should not be a help arg")
	}
}
