package cache

import (
	"context"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/security"
)

func (s *MediaReadStore) deliveryKeys(w domain.ReadSubject, token string) []string {
	k := s.keys(w)
	return append(k, s.prefix()+"grant:"+token, s.prefix()+"grants:s:"+w.Key, s.prefix()+"grants:i:"+w.IPKey, s.prefix()+"grants:global")
}

var mediaPlanComplete = redis.NewScript(`
local now=tonumber(redis.call('TIME')[1])
if not redis.call('ZSCORE',KEYS[5],ARGV[1]) then return 0 end
if ARGV[4]=='go' then
 for i,limit in ipairs({48,256,1024}) do
  local key=KEYS[i+9];redis.call('ZREMRANGEBYSCORE',key,'-inf',now)
  if redis.call('ZCARD',key)>=limit then return 2 end
 end
 if not redis.call('SET',KEYS[9],ARGV[3],'NX','EX',30) then return 0 end
 for i=10,12 do redis.call('ZADD',KEYS[i],now+30,ARGV[2]);redis.call('EXPIRE',KEYS[i],60) end
end
for i=5,7 do redis.call('ZREM',KEYS[i],ARGV[1]) end
redis.call('HINCRBY',KEYS[8],ARGV[4]..'_plans_issued',1)
redis.call('HINCRBY',KEYS[8],ARGV[4]..'_planned_bytes',ARGV[5]);redis.call('EXPIRE',KEYS[8],86400)
return 1`)

func (s *MediaReadStore) CompletePlan(ctx context.Context, w domain.ReadSubject, lease, token, descriptor, mode string, size int64) error {
	if (mode != "go" && mode != "oss") || size < 0 || size > 64<<20 || !validDeliveryToken(token) || len(descriptor) != 64 {
		return domain.ErrUnavailable
	}
	n, err := mediaPlanComplete.Run(ctx, s.Client, s.deliveryKeys(w, token), lease, token, deliveryBinding(w, descriptor), mode, size).Int()
	if err != nil || n == 0 {
		return domain.ErrUnavailable
	}
	if n == 2 {
		return fault.New(fault.RateLimited, "MEDIA_PLAN_CAPACITY", "待加载图片过多，请稍后重试")
	}
	return nil
}

var mediaGrantBegin = redis.NewScript(`
local tm=redis.call('TIME');local now=tonumber(tm[1])+tonumber(tm[2])/1000000
if redis.call('GET',KEYS[9])~=ARGV[1] or redis.call('EXISTS',KEYS[1])==0 then return {'invalid','','0','0'} end
local function num(f) return tonumber(redis.call('HGET',KEYS[1],f)) or 0 end
local untilAt=num('manual_until')
if untilAt>now then return {'restricted','MEDIA_RESTRICTED',tostring(math.ceil(untilAt-now)),tostring(untilAt)} end
if num('needs_challenge')==1 and (redis.call('HGET',KEYS[1],'verified_binding')~=ARGV[4] or num('verified_until')<=now) then return {'challenge','MEDIA_VERIFICATION_REQUIRED','1','0'} end
untilAt=num('until')
if redis.call('HGET',KEYS[1],'state')=='limited' and untilAt>now then return {'limited',redis.call('HGET',KEYS[1],'reason') or 'MEDIA_RATE_LIMITED',tostring(math.ceil(untilAt-now)),tostring(untilAt)} end
for i,limit in ipairs({6,16,24}) do
 local key=KEYS[i+4];redis.call('ZREMRANGEBYSCORE',key,'-inf',now)
 if redis.call('ZCARD',key)>=limit then return {'limited','MEDIA_CONCURRENCY','1','0'} end
end
redis.call('DEL',KEYS[9]);for i=10,12 do redis.call('ZREM',KEYS[i],ARGV[3]) end
for i=5,7 do redis.call('ZADD',KEYS[i],now+75,ARGV[2]);redis.call('EXPIRE',KEYS[i],90) end
redis.call('HINCRBY',KEYS[8],'go_transfers_started',1);redis.call('EXPIRE',KEYS[8],86400)
return {'allowed','','0','0'}`)

func validDeliveryToken(token string) bool {
	return len(token) == 64 && strings.IndexFunc(token, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) == -1
}

func deliveryBinding(w domain.ReadSubject, descriptor string) string {
	return w.Key + ":" + w.Binding + ":" + w.IPKey + ":" + w.Surface + ":" + descriptor
}

func (s *MediaReadStore) BeginGrant(ctx context.Context, w domain.ReadSubject, token, descriptor, lease string) (domain.ReadDecision, error) {
	invalid := func() (domain.ReadDecision, error) {
		return domain.ReadDecision{}, fault.New(fault.Forbidden, "MEDIA_GRANT_EXPIRED", "图片访问凭据已过期，请重新加载")
	}
	if !validDeliveryToken(token) || len(descriptor) != 64 {
		return invalid()
	}
	out, err := mediaGrantBegin.Run(ctx, s.Client, s.deliveryKeys(w, token), deliveryBinding(w, descriptor), lease, token, w.Binding).StringSlice()
	if err != nil || len(out) != 4 {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	if out[0] == "invalid" {
		return invalid()
	}
	retry, _ := strconv.Atoi(out[2])
	until, _ := strconv.ParseInt(out[3], 10, 64)
	return domain.ReadDecision{State: out[0], Code: out[1], RetryAfter: retry, Until: until}, nil
}
