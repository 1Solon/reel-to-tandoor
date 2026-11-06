// Command build-shortcut writes an unsigned "Save to Tandoor" Shortcut file.
//
// iOS only imports signed Shortcuts, so sign the output on a Mac before sending it to a phone:
//
//	go run ./cmd/build-shortcut -url https://reel-to-tandoor.example.com -api-key KEY
//	shortcuts sign --mode anyone --input "Save to Tandoor.unsigned.shortcut" --output "Save to Tandoor.shortcut"
//
// Without a Mac, build the Shortcut by hand from the README instead.
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// obj is Shortcuts' placeholder character for an inline variable.
const obj = "￼"

type dict = map[string]any

func text(value string) dict {
	return dict{"Value": dict{"string": value}, "WFSerializationType": "WFTextTokenString"}
}

func attachment(outputUUID, name string) dict {
	return dict{"OutputUUID": outputUUID, "Type": "ActionOutput", "OutputName": name}
}

func textWith(variable dict) dict {
	return dict{
		"Value":               dict{"string": obj, "attachmentsByRange": dict{"{0, 1}": variable}},
		"WFSerializationType": "WFTextTokenString",
	}
}

func variable(outputUUID, name string) dict {
	return dict{"Value": attachment(outputUUID, name), "WFSerializationType": "WFTextTokenAttachment"}
}

func dictionary(key string, value dict) dict {
	return dict{
		"Value": dict{"WFDictionaryFieldValueItems": []any{
			dict{"WFItemType": 0, "WFKey": text(key), "WFValue": value},
		}},
		"WFSerializationType": "WFDictionaryFieldValue",
	}
}

func action(identifier string, params dict) dict {
	return dict{"WFWorkflowActionIdentifier": "is.workflow.actions." + identifier, "WFWorkflowActionParameters": params}
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}

func build(serviceURL, apiKey string) dict {
	request, message, link, group := newUUID(), newUUID(), newUUID(), newUUID()
	return dict{
		"WFWorkflowClientVersion":              "2607.0.2",
		"WFWorkflowMinimumClientVersion":       900,
		"WFWorkflowMinimumClientVersionString": "900",
		"WFWorkflowIcon":                       dict{"WFWorkflowIconStartColor": 4292093695, "WFWorkflowIconGlyphNumber": 59511},
		"WFWorkflowTypes":                      []any{"ActionExtension"},
		"WFWorkflowInputContentItemClasses":    []any{"WFURLContentItem", "WFStringContentItem"},
		"WFWorkflowHasShortcutInputVariables":  true,
		"WFWorkflowImportQuestions":            []any{},
		"WFWorkflowActions": []any{
			action("downloadurl", dict{
				"UUID":           request,
				"WFURL":          strings.TrimRight(serviceURL, "/") + "/save",
				"WFHTTPMethod":   "POST",
				"ShowHeaders":    true,
				"WFHTTPHeaders":  dictionary("X-API-Key", text(apiKey)),
				"WFHTTPBodyType": "JSON",
				"WFJSONValues":   dictionary("url", textWith(dict{"Type": "ExtensionInput"})),
			}),
			action("getvalueforkey", dict{
				"UUID": message, "CustomOutputName": "Message", "WFDictionaryKey": "message",
				"WFInput": variable(request, "Contents of URL"),
			}),
			action("getvalueforkey", dict{
				"UUID": link, "CustomOutputName": "Recipe Link", "WFDictionaryKey": "recipe_url",
				"WFInput": variable(request, "Contents of URL"),
			}),
			action("conditional", dict{
				"GroupingIdentifier": group, "WFControlFlowMode": 0,
				"WFCondition": 100, // "has any value"
				"WFInput":     dict{"Type": "Variable", "Variable": variable(link, "Recipe Link")},
			}),
			action("notification", dict{
				"WFNotificationActionTitle": "Tandoor",
				"WFNotificationActionBody":  textWith(attachment(message, "Message")),
			}),
			action("openurl", dict{"WFInput": variable(link, "Recipe Link")}),
			action("conditional", dict{"GroupingIdentifier": group, "WFControlFlowMode": 1}),
			action("alert", dict{
				"WFAlertActionTitle":             "Couldn't save the recipe",
				"WFAlertActionMessage":           textWith(attachment(message, "Message")),
				"WFAlertActionCancelButtonShown": false,
			}),
			action("conditional", dict{"GroupingIdentifier": group, "WFControlFlowMode": 2}),
		},
	}
}

func main() {
	serviceURL := flag.String("url", "", "base URL of the service, e.g. https://reel-to-tandoor.example.com (required)")
	apiKey := flag.String("api-key", "PASTE-YOUR-API-KEY", "the person's API key")
	out := flag.String("out", "Save to Tandoor.unsigned.shortcut", "file to write")
	flag.Parse()
	if *serviceURL == "" {
		flag.Usage()
		os.Exit(2)
	}
	data, err := marshalPlist(build(*serviceURL, *apiKey))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, data, 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %s. Sign it on a Mac with `shortcuts sign` before importing.\n", *out)
}
