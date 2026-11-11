package recommendation

import (
	"sort"
)

type Category struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
}
type Profile struct {
	Enabled     bool                          `json:"enabled"`
	SampleCount int                           `json:"sample_count"`
	Categories  []Category                    `json:"categories"`
	Weights     map[string]map[string]float64 `json:"-"`
	Products    map[string]float64            `json:"-"`
}
type View struct {
	ID, Category, Name, Trade, Server, Price string
	Weight                                   float64
}

func NewProfile() Profile {
	return Profile{Enabled: true, Categories: []Category{}, Weights: map[string]map[string]float64{"category": {}, "trade": {}, "server": {}, "price": {}}, Products: map[string]float64{}}
}

// 单商品按近期浏览权重归一化，内部画像不进入公开 JSON。
func (p *Profile) ApplyHistory(views []View) {
	names := map[string]string{}
	total := 0.0
	for _, v := range views {
		for group, value := range map[string]string{"category": v.Category, "trade": v.Trade, "server": v.Server, "price": v.Price} {
			if value != "" {
				p.Weights[group][value] += v.Weight
			}
		}
		names[v.Category] = v.Name
		p.Products[v.ID] = v.Weight
		p.SampleCount++
		total += v.Weight
	}
	if total > 0 {
		for id, w := range p.Products {
			p.Products[id] = w / total
		}
		for _, group := range p.Weights {
			for key, w := range group {
				group[key] = w / total
			}
		}
	}
	for id, w := range p.Weights["category"] {
		p.Categories = append(p.Categories, Category{id, names[id], w})
	}
	sort.Slice(p.Categories, func(i, j int) bool {
		if p.Categories[i].Weight == p.Categories[j].Weight {
			return p.Categories[i].ID < p.Categories[j].ID
		}
		return p.Categories[i].Weight > p.Categories[j].Weight
	})
}
