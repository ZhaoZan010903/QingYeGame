package dictionary

import (
	"context"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/management"
)

type Reader interface {
	Dictionaries(context.Context, bool) ([]map[string]any, error)
}
type Writer interface {
	SaveDictionary(context.Context, management.Actor, string, catalog.DictionaryInput) (map[string]any, error)
}
type Service struct {
	Reader Reader
	Writer Writer
}

func (s Service) Public(ctx context.Context) (map[string]any, error) {
	rows, err := s.Reader.Dictionaries(ctx, false)
	return map[string]any{"data": rows}, err
}
func (s Service) Admin(ctx context.Context, actor management.Actor) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	rows, err := s.Reader.Dictionaries(ctx, true)
	return map[string]any{"data": rows}, err
}
func (s Service) Save(ctx context.Context, actor management.Actor, key string, in catalog.DictionaryInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	return s.Writer.SaveDictionary(ctx, actor, key, in)
}
