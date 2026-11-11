package catalog

import (
	"qingye/backend/internal/domain/fault"
	"strings"
	"unicode/utf8"
)

type DictionaryOption struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`
}
type DictionaryInput struct {
	ExpectedVersion int                `json:"expected_version"`
	Options         []DictionaryOption `json:"options"`
}

func DictionaryKey(key string) bool {
	return key == "server" || key == "gender" || key == "level" || key == "kind"
}
func ValidateDictionary(key string, in DictionaryInput) error {
	if !DictionaryKey(key) || in.ExpectedVersion < 1 || len(in.Options) > 500 {
		return fault.New(fault.Invalid, "INVALID_DICTIONARY", "请检查字典类型、版本和选项数量")
	}
	seen := map[string]bool{}
	enabled := 0
	for _, o := range in.Options {
		if strings.TrimSpace(o.Value) == "" || strings.TrimSpace(o.Label) == "" || strings.TrimSpace(o.Value) != o.Value || utf8.RuneCountInString(o.Value) > 80 || utf8.RuneCountInString(o.Label) > 80 || seen[o.Value] || o.SortOrder < 0 || o.SortOrder > 10000 {
			return fault.New(fault.Invalid, "INVALID_DICTIONARY", "选项编码和名称须为1至80字，编码不能重复")
		}
		seen[o.Value] = true
		if o.Enabled {
			enabled++
		}
	}
	if (key == "server" || key == "gender") && enabled == 0 {
		return fault.New(fault.Invalid, "DICTIONARY_REQUIRED_OPTIONS", "区服和性别至少保留一个启用选项")
	}
	return nil
}
