package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
	domain "qingye/backend/internal/domain/registration"
	"time"
)

type RegistrationLimiter struct {
	Client     *redis.Client
	DailyLimit int
	Prefix     string
}
type smsBudget struct {
	key    string
	max    int
	window time.Duration
}

var registrationBudgetScript = redis.NewScript(`
for i,k in ipairs(KEYS) do
 if tonumber(redis.call('GET',k) or '0') >= tonumber(ARGV[(i-1)*2+1]) then return 0 end
end
for i,k in ipairs(KEYS) do
 local n=redis.call('INCR',k)
 if n==1 then redis.call('PEXPIRE',k,ARGV[(i-1)*2+2]) end
end
return 1`)

func (l RegistrationLimiter) allow(ctx context.Context, b ...smsBudget) error {
	keys := make([]string, 0, len(b))
	args := make([]interface{}, 0, len(b)*2)
	for _, v := range b {
		keys = append(keys, "qingye:r21:"+l.Prefix+v.key)
		args = append(args, v.max, v.window.Milliseconds())
	}
	n, err := registrationBudgetScript.Run(ctx, l.Client, keys, args...).Int()
	if err != nil {
		return domain.ErrUnavailable
	}
	if n != 1 {
		return domain.ErrLimited
	}
	return nil
}
func (l RegistrationLimiter) Challenge(ctx context.Context, s domain.Subject) error {
	return l.allow(ctx,
		smsBudget{"challenge:global", 600, time.Hour}, smsBudget{"challenge:ip:" + s.IPKey, 20, 10 * time.Minute}, smsBudget{"challenge:browser:" + s.Binding, 10, 10 * time.Minute}, smsBudget{"challenge:phone:" + s.PhoneKey, 10, 10 * time.Minute})
}
func (l RegistrationLimiter) Send(ctx context.Context, s domain.Subject) error {
	return l.allow(ctx,
		smsBudget{"send:global", l.DailyLimit, 24 * time.Hour}, smsBudget{"send:cooldown:" + s.PhoneKey, 1, time.Minute}, smsBudget{"send:hour:" + s.PhoneKey, 5, time.Hour}, smsBudget{"send:day:" + s.PhoneKey, 10, 24 * time.Hour}, smsBudget{"send:ip:" + s.IPKey, 20, time.Hour}, smsBudget{"send:browser:" + s.Binding, 10, time.Hour})
}
func (l RegistrationLimiter) Check(ctx context.Context, s domain.Subject) error {
	return l.allow(ctx,
		smsBudget{"check:phone:" + s.PhoneKey, 20, 15 * time.Minute}, smsBudget{"check:ip:" + s.IPKey, 60, 15 * time.Minute}, smsBudget{"check:browser:" + s.Binding, 20, 15 * time.Minute})
}
