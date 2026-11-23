// Package legacycatalog contains the stable legacy import identity and source format.
package legacycatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"qingye/backend/internal/domain/catalog"
)

const CDNHost = "cdn.xuansiwei.com"

var sourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func ValidSourceID(value string) bool { return sourceID.MatchString(value) }

// CanonicalImageURL accepts only the legacy storefront's fixed image origin.
func CanonicalImageURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || !strings.EqualFold(u.Host, CDNHost) ||
		(u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" ||
		u.Path == "" || u.Opaque != "" {
		return "", errors.New("invalid legacy CDN image URL")
	}
	u.Scheme, u.Host = "https", CDNHost
	return u.String(), nil
}

type LegacyRecord struct {
	ID       string      `json:"_id"`
	Title    string      `json:"title"`
	Code     string      `json:"product_id"`
	Price    json.Number `json:"price"`
	Category string      `json:"categorys_name"`
	Status   string      `json:"status"`
	Content  string      `json:"content"`
	Deleted  bool        `json:"is_del"`
	Created  time.Time   `json:"create_time"`
	Updated  time.Time   `json:"update_time"`
	Server   string      `json:"游戏区服"`
	Gender   string      `json:"角色性别"`
	Level    string      `json:"XVIP等级"`
	Tags     []string    `json:"热门筛选"`
	Labels   []string    `json:"labels"`
}
type LegacyCategory struct {
	ID, Name, Trade string
	PriceUnit       string
	Units           []string
}

var LegacyCategories = []LegacyCategory{
	{"legacy-sale", "QQ炫舞买号", "sale", "account", []string{"account"}},
	{"legacy-short-rent", "时日租号", "rent", "day", []string{"day"}},
	{"legacy-long-rent", "周月租号", "rent", "month", []string{"month"}},
}

// Legacy source values remain untouched. Only a known source category and an
// exact, nonnegative decimal fitting numeric(12,2) become a comparable quote.
func ConfirmedLegacyPrice(category *LegacyCategory, original string, allowedUnits []string) (string, *string) {
	if category == nil || !catalog.MoneyPattern.MatchString(original) {
		return "unknown", nil
	}
	if len(allowedUnits) > 0 {
		allowed := false
		for _, unit := range allowedUnits {
			if unit == category.PriceUnit {
				allowed = true
				break
			}
		}
		if !allowed {
			return "unknown", nil
		}
	}
	return category.PriceUnit, &original
}

func DeterministicID(value string) string {
	h := sha256.Sum256([]byte(value))
	b := h[:16]
	b[6] = (b[6] & 15) | 80
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:])
}
func LegacyMediaLocation(source string) (string, string, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "//") {
		source = "https:" + source
	}
	u, e := url.Parse(source)
	if e != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return "", "", errors.New("invalid legacy image URL")
	}
	h := sha256.Sum256([]byte(source))
	return source, "legacy/" + hex.EncodeToString(h[:]), nil
}
func LegacyAttributes(p LegacyRecord) map[string]any {
	gender := p.Gender
	if gender == "女" {
		gender = "female"
	}
	if gender == "男" {
		gender = "male"
	}
	return map[string]any{"game": "QQ炫舞", "server": p.Server, "gender": gender, "level": p.Level, "tags": p.Tags, "legacy_labels": p.Labels}
}

// A comma may be part of an OSS image transform; only a following URL starts a new image.
func LegacyImageURLs(content string) []string {
	parts := []string{}
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] != ',' {
			continue
		}
		tail := content[i+1:]
		if strings.HasPrefix(tail, "//") || strings.HasPrefix(tail, "https://") || strings.HasPrefix(tail, "http://") {
			parts = append(parts, content[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(content[start:]) != "" {
		parts = append(parts, content[start:])
	}
	return parts
}
