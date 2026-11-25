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

const MaxImageBytes = 16 << 20

const (
	ReadSource   = "read_source"
	VerifySource = "verify_source"
	Compare      = "compare"
	PrepareMedia = "prepare_media"
	Commit       = "commit"
	Done         = "done"
)

var (
	ErrBusy        = errors.New("商品同步正在运行")
	ErrImage       = errors.New("旧站图片无法读取或校验")
	ErrMediaBudget = errors.New("本批新图已达到下载上限，重试时会复用已校验的图片")
	ErrLeaseLost   = errors.New("同步执行已中断，请重新发起")
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
	SyncHidden     bool                  `json:"sync_hidden,omitempty"`
}

func (p Product) Input() catalog.ProductInput {
	return catalog.ProductInput{
		CategoryID: p.Category, Title: p.Title, Description: p.Description, TradeType: p.Trade,
		PriceUnit: p.Unit, PriceAmount: p.Amount, RentalQuotes: p.Quotes, RentalCategory: &p.RentalCategory,
		Attributes: p.Attributes, LegacyAttributes: true,
	}
}

type Current struct {
	Product
	Deleted     bool
	Legacy      bool
	Baseline    json.RawMessage
	LastApplied *Product
	Ownership   Ownership
	ImagesByURL map[string]string
	ReadyImages int
}

func (c Current) hasReadyGallery() bool {
	return len(c.Images) > 0 && len(c.Images) == c.ReadyImages
}

type Ownership struct {
	Price      bool     `json:"price"`
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
	Blocking        bool       `json:"blocking"`
	Before          *Product   `json:"before,omitempty"`
	After           *Product   `json:"after,omitempty"`
	Ownership       Ownership  `json:"-"`
}

type Summary struct {
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	Removed  int `json:"removed"`
	Restored int `json:"restored"`
	Priced   int `json:"priced"`
	Imaged   int `json:"imaged"`
	Review   int `json:"review"`
	Missing  int `json:"missing"`
}

type Progress struct {
	Stage     string `json:"stage"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
}

type Batch struct {
	ID            string     `json:"id"`
	PlannedAt     *time.Time `json:"planned_at"`
	StartedAt     time.Time  `json:"started_at"`
	SourceUpdated *time.Time `json:"source_updated_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	AppliedAt     *time.Time `json:"applied_at"`
	RolledBackAt  *time.Time `json:"rolled_back_at"`
	RollbackUntil *time.Time `json:"rollback_until"`
	Status        string     `json:"status"`
	SourceCount   int        `json:"source_count"`
	SourceDigest  string     `json:"-"`
	Summary       Summary    `json:"summary"`
	Reason        string     `json:"reason"`
	Progress      Progress   `json:"progress"`
}

type Controls struct {
	ScheduleEnabled bool      `json:"schedule_enabled"`
	Version         int64     `json:"version"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Inventory struct {
	Records []Record
	Latest  *time.Time
}

type Source interface {
	Catalog(context.Context, func(completed, total int) error) (Inventory, error)
	Image(context.Context, string, io.Writer) (int64, error)
}

type Asset struct {
	ID, Key, MIME, SHA256 string
	Size                  int64
	Ready                 bool
}

type Store interface {
	BeginScheduledBatch(context.Context, string, time.Time) (Batch, bool, error)
	BeginManualBatch(context.Context, string, string) (Batch, bool, error)
	ReportProgress(context.Context, string, Progress) error
	Heartbeat(context.Context, string) error
	LoadCatalog(context.Context) (map[string]Current, map[string]catalog.CategoryRules, map[string]string, error)
	CloseBatch(context.Context, Batch, []Item) (Batch, error)
	CommitBatch(context.Context, Batch, []Item, string) (Batch, error)
	FindAsset(context.Context, string, string, string) (Asset, bool, error)
	RememberAssetURL(ctx context.Context, mediaID, source string) error
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
