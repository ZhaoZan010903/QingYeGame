package member

import (
	"qingye/backend/internal/domain/fault"
	"slices"
)

// Actor 由认证入口构造；管理能力不能由请求正文指定。
type Actor struct {
	ID, Role string
	Admin    bool
}
type ListInput struct {
	Offset, Limit int
	Status        string
	Favorites     bool
}
type TransitionInput struct {
	ExpectedVersion int `json:"expected_version"`
}
type AppendImagesInput struct {
	MediaIDs        []string `json:"media_ids"`
	ExpectedVersion int      `json:"expected_version"`
	RequestID       string   `json:"request_id"`
}
type ChangeImageInput struct {
	ExpectedVersion int     `json:"expected_version"`
	BeforeMediaID   *string `json:"before_media_id"`
}
type GalleryInput struct {
	Offset, Limit   int
	ExpectedVersion *int
	ReviewID        string
}

func ValidateActor(a Actor) error {
	if a.ID == "" {
		return fault.New(fault.Unauthenticated, "UNAUTHENTICATED", "请先登录")
	}
	if a.Admin && a.Role != "admin" || !a.Admin && a.Role != "member" {
		return fault.New(fault.Forbidden, "FORBIDDEN", "需要管理员权限")
	}
	return nil
}
func ValidatePage(offset, limit int) error {
	if offset < 0 || offset > 1000000 {
		return fault.New(fault.Invalid, "INVALID_CURSOR", "分页参数无效")
	}
	if limit < 1 || limit > 50 {
		return fault.New(fault.Invalid, "INVALID_LIMIT", "每页应为1至50条")
	}
	return nil
}
func ValidateList(in ListInput) error {
	if err := ValidatePage(in.Offset, in.Limit); err != nil {
		return err
	}
	if !in.Favorites && in.Status != "" && !slices.Contains([]string{"draft", "pending", "publishing", "published", "rejected", "rejected_final", "offline", "sold", "rented"}, in.Status) {
		return fault.New(fault.Invalid, "INVALID_STATUS", "状态无效")
	}
	return nil
}
func CheckVersion(actual, wanted int) error {
	if wanted < 1 || actual != wanted {
		return fault.New(fault.Conflict, "VERSION_CONFLICT", "资料已更新，请重新加载后操作")
	}
	return nil
}

// 商品内容变化后必须复审；状态转换规则集中在此处。
func NextStatus(status string, submit bool) (string, error) {
	if submit {
		if status != "draft" && status != "rejected" && status != "offline" {
			return "", fault.New(fault.Conflict, "INVALID_STATUS", "当前状态不能提交审核")
		}
		return "pending", nil
	}
	if status == "published" || status == "publishing" {
		return "offline", nil
	}
	if status != "pending" {
		return "", fault.New(fault.Conflict, "INVALID_STATUS", "当前状态不能撤回")
	}
	return "draft", nil
}
