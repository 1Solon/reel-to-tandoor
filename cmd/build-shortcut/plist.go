package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
)

// marshalPlist encodes maps, slices, strings, ints and bools as an XML property list.
func marshalPlist(v any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	if err := writeValue(&b, v, 0); err != nil {
		return nil, err
	}
	b.WriteString("</plist>\n")
	return b.Bytes(), nil
}

func writeString(b *bytes.Buffer, tag, s string, depth int) {
	b.WriteString(strings.Repeat("\t", depth) + "<" + tag + ">")
	_ = xml.EscapeText(b, []byte(s))
	b.WriteString("</" + tag + ">\n")
}

func writeValue(b *bytes.Buffer, v any, depth int) error {
	indent := strings.Repeat("\t", depth)
	switch v := v.(type) {
	case map[string]any:
		b.WriteString(indent + "<dict>\n")
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			writeString(b, "key", k, depth+1)
			if err := writeValue(b, v[k], depth+1); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
		b.WriteString(indent + "</dict>\n")
	case []any:
		b.WriteString(indent + "<array>\n")
		for _, item := range v {
			if err := writeValue(b, item, depth+1); err != nil {
				return err
			}
		}
		b.WriteString(indent + "</array>\n")
	case string:
		writeString(b, "string", v, depth)
	case int:
		writeString(b, "integer", fmt.Sprint(v), depth)
	case bool:
		if v {
			b.WriteString(indent + "<true/>\n")
		} else {
			b.WriteString(indent + "<false/>\n")
		}
	default:
		return fmt.Errorf("can't encode %T in a plist", v)
	}
	return nil
}
