package catalog

import "testing"

func TestHotKeywordDictionaryRejectsInvalidText(t *testing.T) {
	for _, value := range []string{"", " \t", "bad\x00value", "bad\nvalue", string([]byte{0xff})} {
		for _, field := range []string{"value", "label"} {
			option := DictionaryOption{Value: "code", Label: "永夜头", Enabled: true}
			if field == "value" {
				option.Value = value
			} else {
				option.Label = value
			}
			if ValidateDictionary("keyword", DictionaryInput{ExpectedVersion: 1, Options: []DictionaryOption{option}}) == nil {
				t.Fatalf("invalid %s accepted: %q", field, value)
			}
		}
	}
	if err := ValidateDictionary("keyword", DictionaryInput{ExpectedVersion: 1, Options: []DictionaryOption{{Value: "literal", Label: "100%_珍藏", Enabled: true}}}); err != nil {
		t.Fatalf("valid literal keyword rejected: %v", err)
	}
	if err := ValidateDictionary("keyword", DictionaryInput{ExpectedVersion: 1}); err != nil {
		t.Fatalf("administrator cannot clear hot keywords: %v", err)
	}
}
