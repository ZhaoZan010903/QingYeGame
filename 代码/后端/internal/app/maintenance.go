package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	_ "golang.org/x/image/webp"
)

// SeedUser deliberately accepts credentials through a private file or stdin, never argv.
// Existing users are not overwritten: a retry cannot reset an account silently.
func (a *App) SeedUser(ctx context.Context, reader io.Reader) (string, error) {
	var in struct {
		Phone    string `json:"phone"`
		Nickname string `json:"nickname"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	d := json.NewDecoder(io.LimitReader(reader, 16384))
	d.DisallowUnknownFields()
	if e := d.Decode(&in); e != nil {
		return "", e
	}
	if len(in.Phone) < 6 || len(in.Phone) > 20 || len(in.Password) < 12 || len(in.Password) > 72 || strings.TrimSpace(in.Nickname) == "" || len(in.Nickname) > 120 || in.Role != "member" && in.Role != "admin" {
		return "", errors.New("seed requires valid phone, nickname, 12–72 byte password and member/admin role")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if e != nil {
		return "", e
	}
	id := newID()
	tag, e := a.DB.Exec(ctx, "INSERT INTO users(id,phone,nickname,password_hash,role) VALUES($1,$2,$3,$4,$5) ON CONFLICT(phone) DO NOTHING", id, in.Phone, in.Nickname, string(hash), in.Role)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() == 0 {
		e = a.DB.QueryRow(ctx, "SELECT id FROM users WHERE phone=$1", in.Phone).Scan(&id)
	}
	return id, e
}

type legacyRecord struct {
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
type legacyCategory struct {
	ID, Name, Trade string
	PriceUnit       string
	Units           []string
}

var legacyCategories = []legacyCategory{
	{"legacy-sale", "QQ炫舞买号", "sale", "account", []string{"account"}},
	{"legacy-short-rent", "时日租号", "rent", "day", []string{"day"}},
	{"legacy-long-rent", "周月租号", "rent", "month", []string{"month"}},
}

// Legacy source values remain untouched. Only a known source category and an
// exact, nonnegative decimal fitting numeric(12,2) become a comparable quote.
func confirmedLegacyPrice(category *legacyCategory, original string, allowedUnits []string) (string, *string) {
	if category == nil || !moneyPattern.MatchString(original) {
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

func deterministicID(value string) string {
	h := sha256.Sum256([]byte(value))
	b := h[:16]
	b[6] = (b[6] & 15) | 80
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:])
}
func legacyMediaLocation(source string) (string, string, error) {
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
func legacyAttributes(p legacyRecord) map[string]any {
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
func legacyImageURLs(content string) []string {
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

// ImportLegacy is append-only and idempotent by the original product id.
// An existing product, including any administrator corrections, is never overwritten.
func (a *App) ImportLegacy(ctx context.Context, reader io.Reader) (int, error) {
	var raw []json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(reader, 128<<20))
	if e := decoder.Decode(&raw); e != nil {
		return 0, e
	}
	records := make([]legacyRecord, len(raw))
	for i, b := range raw {
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.UseNumber()
		if e := d.Decode(&records[i]); e != nil {
			return 0, fmt.Errorf("record %d: %w", i, e)
		}
		if records[i].ID == "" {
			return 0, fmt.Errorf("record %d missing legacy id", i)
		}
	}
	// Filter choices are derived from the snapshot, not guessed from prototype options.
	filters := map[string]map[string]map[string]bool{}
	for _, c := range legacyCategories {
		filters[c.Name] = map[string]map[string]bool{"server": {}, "gender": {}, "level": {}}
	}
	for _, p := range records {
		if group := filters[p.Category]; group != nil {
			attrs := legacyAttributes(p)
			for key, values := range group {
				v, _ := attrs[key].(string)
				if v != "" {
					values[v] = true
				}
			}
		}
	}
	for index, c := range legacyCategories {
		groups := []filterGroup{}
		for _, key := range []string{"server", "gender", "level"} {
			values := []string{}
			for v := range filters[c.Name][key] {
				values = append(values, v)
			}
			sort.Strings(values)
			g := filterGroup{Key: key, Label: map[string]string{"server": "游戏区服", "gender": "角色性别", "level": "XVIP等级"}[key], Multiple: true, Options: []filterOption{}}
			for _, v := range values {
				label := v
				if v == "female" {
					label = "女角色"
				}
				if v == "male" {
					label = "男角色"
				}
				g.Options = append(g.Options, filterOption{Value: v, Label: label})
			}
			groups = append(groups, g)
		}
		b, _ := json.Marshal(groups)
		u, _ := json.Marshal(c.Units)
		_, e := a.DB.Exec(ctx, "INSERT INTO categories(id,name,trade_type,price_units,filter_config,sort_order) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING", c.ID, c.Name, c.Trade, u, b, index)
		if e != nil {
			return 0, e
		}
	}
	// A generic category also supports new member submissions without inheriting a legacy label.
	combined := []filterGroup{}
	for _, key := range []string{"server", "gender", "level"} {
		values := map[string]bool{}
		for _, category := range filters {
			for v := range category[key] {
				values[v] = true
			}
		}
		sorted := []string{}
		for v := range values {
			sorted = append(sorted, v)
		}
		sort.Strings(sorted)
		g := filterGroup{Key: key, Label: map[string]string{"server": "游戏区服", "gender": "角色性别", "level": "XVIP等级"}[key], Multiple: true, Options: []filterOption{}}
		for _, v := range sorted {
			label := v
			if v == "female" {
				label = "女角色"
			}
			if v == "male" {
				label = "男角色"
			}
			g.Options = append(g.Options, filterOption{Value: v, Label: label})
		}
		combined = append(combined, g)
	}
	combinedJSON, _ := json.Marshal(combined)
	_, e := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units,filter_config) VALUES('qq-dance','QQ炫舞','all','["account","hour","day","week","month"]',$1) ON CONFLICT(id) DO NOTHING`, combinedJSON)
	if e != nil {
		return 0, e
	}
	imported := 0
	for index, p := range records {
		if p.Deleted {
			continue
		}
		var category *legacyCategory
		for i := range legacyCategories {
			if legacyCategories[i].Name == p.Category {
				category = &legacyCategories[i]
			}
		}
		if category == nil {
			return imported, fmt.Errorf("unsupported category on product %s", p.ID)
		}
		tx, e := a.DB.Begin(ctx)
		if e != nil {
			return imported, e
		}
		id := deterministicID("legacy-product:" + p.ID)
		attrs, _ := json.Marshal(legacyAttributes(p))
		created, updated := p.Created, p.Updated
		if created.IsZero() {
			created = time.Now().UTC()
		}
		if updated.IsZero() {
			updated = created
		}
		status := "published"
		if p.Status != "出售中" {
			status = "offline"
		}
		var allowedUnits []string
		if e = tx.QueryRow(ctx, "SELECT price_units FROM categories WHERE id=$1 FOR SHARE", category.ID).Scan(&allowedUnits); e != nil {
			tx.Rollback(ctx)
			return imported, e
		}
		unit, amount := confirmedLegacyPrice(category, p.Price.String(), allowedUnits)
		tag, e := tx.Exec(ctx, `INSERT INTO products(id,legacy_id,product_code,category_id,title,trade_type,price_unit,price_amount,attributes,description,status,source,version,approved_version,legacy_price,legacy_category,legacy_payload,verification_status,created_at,updated_at,published_at)
   VALUES($1,$2,$3,$4,$5,$6,$14,$15,$7,$5,$8,'legacy',1,1,$9,$10,$11,'unprovided',$12,$13,$12) ON CONFLICT(legacy_id) DO NOTHING`, id, p.ID, p.Code, category.ID, p.Title, category.Trade, attrs, status, p.Price.String(), p.Category, raw[index], created, updated, unit, amount)
		if e != nil {
			tx.Rollback(ctx)
			return imported, e
		}
		if tag.RowsAffected() == 0 {
			tx.Rollback(ctx)
			continue
		}
		batch := &pgx.Batch{}
		seen := map[string]bool{}
		position := 0
		for _, part := range legacyImageURLs(p.Content) {
			if strings.TrimSpace(part) == "" {
				continue
			}
			source, key, err := legacyMediaLocation(part)
			if err != nil {
				tx.Rollback(ctx)
				return imported, fmt.Errorf("product %s: %w", p.ID, err)
			}
			if seen[source] {
				continue
			}
			seen[source] = true
			mediaID := deterministicID("legacy-media:" + p.ID + ":" + source)
			position++
			batch.Queue(`INSERT INTO media_files(id,filename,storage_key,source_url,status) VALUES($1,$2,$3,$4,'pending') ON CONFLICT(id) DO NOTHING`, mediaID, fmt.Sprintf("%s · 图片 %d", p.Code, position), key, source)
			batch.Queue("INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", id, mediaID, position)
		}
		br := tx.SendBatch(ctx, batch)
		e = br.Close()
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			tx.Rollback(ctx)
		}
		if e != nil {
			return imported, e
		}
		imported++
	}
	return imported, nil
}

type ReconcileResult struct{ Checked, Ready, Pending, Invalid int }

// Reconciliation only opens deterministic local keys, and never fetches a URL.
func (a *App) ReconcileMedia(ctx context.Context, limit int) (ReconcileResult, error) {
	result := ReconcileResult{}
	query := "SELECT id,storage_key FROM media_files WHERE status='pending' AND source_url<>'' ORDER BY id"
	args := []any{}
	if limit > 0 {
		query += " LIMIT $1"
		args = append(args, limit)
	}
	rows, e := a.DB.Query(ctx, query, args...)
	if e != nil {
		return result, e
	}
	type pendingFile struct{ id, key string }
	pending := []pendingFile{}
	for rows.Next() {
		var f pendingFile
		if e = rows.Scan(&f.id, &f.key); e != nil {
			rows.Close()
			return result, e
		}
		pending = append(pending, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	for _, entry := range pending {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Checked++
		if !regexpLegacyKey(entry.key) {
			return result, errors.New("invalid local legacy storage key")
		}
		f, e := os.Open(filepath.Join(a.Config.MediaDir, filepath.FromSlash(entry.key)))
		if errors.Is(e, os.ErrNotExist) {
			result.Pending++
			continue
		}
		if e != nil {
			return result, e
		}
		stat, e := f.Stat()
		if e != nil {
			f.Close()
			return result, e
		}
		reader := bufio.NewReader(f)
		header, e := reader.Peek(512)
		if e != nil && e != io.EOF {
			f.Close()
			return result, e
		}
		mime := http.DetectContentType(header)
		cfg, format, decodeErr := image.DecodeConfig(reader)
		valid := decodeErr == nil && cfg.Width > 0 && cfg.Height > 0 && stat.Size() > 0 && strings.HasPrefix(mime, "image/")
		if !valid {
			f.Close()
			result.Invalid++
			continue
		}
		if format == "webp" {
			mime = "image/webp"
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			f.Close()
			return result, e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, f)
		f.Close()
		if e != nil {
			return result, e
		}
		_, e = a.DB.Exec(ctx, "UPDATE media_files SET status='ready',mime_type=$2,byte_size=$3,sha256=$4 WHERE id=$1 AND status='pending'", entry.id, mime, stat.Size(), hex.EncodeToString(hash.Sum(nil)))
		if e != nil {
			return result, e
		}
		result.Ready++
	}
	return result, nil
}
func regexpLegacyKey(key string) bool {
	if !strings.HasPrefix(key, "legacy/") || len(key) != 71 {
		return false
	}
	_, e := hex.DecodeString(key[7:])
	return e == nil
}
