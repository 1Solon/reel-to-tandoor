package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestShortcutIsWellFormedPlist(t *testing.T) {
	data, err := marshalPlist(build("https://reel.example.com/", "key&<1>"))
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		if _, err := dec.Token(); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("invalid XML: %v", err)
		}
	}
	s := string(data)
	for _, want := range []string{
		"<string>https://reel.example.com/save</string>",
		"<string>key&amp;&lt;1&gt;</string>",
		"<string>is.workflow.actions.downloadurl</string>",
		"<string>ActionExtension</string>",
		"<key>{0, 1}</key>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist lacks %s", want)
		}
	}
	if got := strings.Count(s, "<key>WFWorkflowActionIdentifier</key>"); got != 10 {
		t.Errorf("%d actions, want 10", got)
	}
	if strings.Contains(s, "WFTextActionText</string>") {
		t.Error("an embedded key shouldn't add an import question")
	}
}

func TestSharedShortcutAsksForKey(t *testing.T) {
	data, err := marshalPlist(build("https://reel.example.com", ""))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"<string>WFTextActionText</string>",
		"<string>Paste your Save to Tandoor API key</string>",
		"<string>is.workflow.actions.gettext</string>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist lacks %s", want)
		}
	}
}

func TestMarshalRejectsUnknownTypes(t *testing.T) {
	if _, err := marshalPlist(map[string]any{"x": 1.5}); err == nil {
		t.Error("float accepted")
	}
}
