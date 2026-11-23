package legacysync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/legacycatalog"
)

const (
	Preview   = "preview"
	Automatic = "automatic"
	Paused    = "paused"

	MaxImageBytes = 16 << 20
)

var (
	ErrBusy  = errors.New("商品同步正在运行")
	ErrImage = errors.New("旧站图片无法读取或校验")
)

type Record struct {
	legacycatalog.LegacyRecord
	PriceUnit string `json:"price_unit"`
}

type Product struct {
	ID             string                `json:"id"`
	LegacyID       string                `json:"legacy_id"`
	Code           string                `json:"code"`
	Category       string                `json:"category"`
	SourceCategory string                `json:"source_category"`
	Title          string                `json:"title"`
	Description    string                `json:"description"`
	Trade          string                `json:"trade"`
	RentalCategory string                `json:"rental_category"`
	Attributes     map[string]any        `json:"attributes"`
	Amount         *string               `json:"amount"`
	Unit           string                `json:"unit"`
	Quotes         []catalog.RentalQuote `json:"quotes"`
	Status         string                `json:"status"`
	Approved       bool                  `json:"approved"`
	Version        int                   `json:"version"`
	CreatedAt      time.Time             `json:"created_at"`
	PublishedAt    *time.Time            `json:"published_at"`
	Images         []string              `json:"images"`
}

type Current struct {
	Product
	Deleted     bool
	Legacy      bool
	Baseline    json.RawMessage
	LastApplied *Product
	Ownership   Ownership
	ImagesByURL map[string]string
}

type Ownership struct {
	Price      bool     `json:"price"`
	Status     bool     `json:"status"`
	Title      bool     `json:"title"`
	Gallery    bool     `json:"gallery"`
	Attributes []string `json:"attributes"`
}

type Item struct {
	LegacyID        string     `json:"legacy_id"`
	Code            string     `json:"code"`
	Action          string     `json:"action"`
	SourcePrice     string     `json:"source_price"`
	SourceUpdatedAt *time.Time `json:"source_updated_at"`
	Notices         []string   `json:"notices"`
	Before          *Product   `json:"before,omitempty"`
	After           *Product   `json:"after,omitempty"`
	Ownership       Ownership  `json:"-"`
}

type Summary struct {
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	Removed  int `json:"removed"`
	Priced   int `json:"priced"`
	Imaged   int `json:"imaged"`
	Review   int `json:"review"`
	Missing  int `json:"missing"`
	BadImage int `json:"bad_image"`
}

type Batch struct {
	ID            string     `json:"id"`
	Mode          string     `json:"-"`
	PlannedAt     *time.Time `json:"planned_at"`
	StartedAt     time.Time  `json:"started_at"`
	SourceUpdated *time.Time `json:"source_updated_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	AppliedAt     *time.Time `json:"applied_at"`
	RolledBackAt  *time.Time `json:"rolled_back_at"`
	RollbackUntil *time.Time `json:"rollback_until"`
	Status        string     `json:"status"`
	SourceCount   int        `json:"source_count"`
	Summary       Summary    `json:"summary"`
	Reason        string     `json:"reason"`
}

type Controls struct {
	Mode      string    `json:"mode"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Inventory struct {
	Records []Record
	Latest  *time.Time
}

type Source interface {
	Catalog(context.Context) (Inventory, error)
	Image(context.Context, string, io.Writer) (int64, error)
}

type Asset struct {
	ID, Key, MIME, SHA256 string
	Size                  int64
	Ready                 bool
}

type Store interface {
	BeginBatch(context.Context, string, *time.Time) (Batch, bool, error)
	FailBatch(context.Context, string, string) error
	LoadCatalog(context.Context) (map[string]Current, map[string]catalog.CategoryRules, map[string]string, error)
	SavePreview(context.Context, Batch, []Item) error
	ApplyBatch(context.Context, string, string) error
	FindAsset(context.Context, string, string, string) (Asset, bool, error)
	ReserveAsset(context.Context, string, string, string, Asset) error
	ReadyAsset(context.Context, string) error
}

type PreparedImage struct {
	Body      io.ReadCloser
	RawSHA256 string
	MIME      string
	SHA256    string
	Size      int64
}

type ImagePreparer interface {
	Prepare(context.Context, int64, func(context.Context, io.Writer) (int64, error)) (PreparedImage, int64, error)
}

type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64, string, string) error
	Verify(context.Context, string, int64, string) error
}

func TimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
