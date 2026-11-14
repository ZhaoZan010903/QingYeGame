package responsecrypto

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Keys are random, scoped to a server-derived context, and expire without a cleanup worker.
type RedisStore struct {
	Client    *redis.Client
	Namespace string
}

func (s *RedisStore) prefix() string {
	if s.Namespace != "" {
		return s.Namespace
	}
	return "qingye:r24:response:"
}

var currentKey = redis.NewScript(`
local now=tonumber(redis.call('TIME')[1]); local epoch=math.floor(now/3600)
local base=ARGV[1]..ARGV[2];local pointer=base..':current'
local old=redis.call('GET',pointer)
if old then
 local raw=redis.call('GET',base..':keys:'..old)
 if raw then return raw end
end
local cap=ARGV[1]..'alloc:'..tostring(epoch)
if tonumber(redis.call('GET',cap) or '0')>=10000 then return '' end
local rotate=now+3600;local retain=rotate+120
local id=tostring(now)..'-'..ARGV[3];local key=base..':keys:'..id
local value=cjson.encode({context_id=ARGV[2],key_id=id,key=ARGV[4],rotate_at=rotate,retain_until=retain})
redis.call('SET',key,value,'EXAT',retain)
redis.call('SET',pointer,id,'EXAT',rotate)
redis.call('INCR',cap);redis.call('EXPIREAT',cap,(epoch+1)*3600+3720)
return value
`)

func (s *RedisStore) Current(ctx context.Context, contextID string) (Key, error) {
	if !ValidID(contextID, 64, 64) {
		return Key{}, ErrKey
	}
	raw, err := currentKey.Run(ctx, s.Client, nil, s.prefix(), contextID, RandomID(), RandomKey()).Text()
	if err != nil {
		return Key{}, err
	}
	if raw == "" {
		return Key{}, ErrCapacity
	}
	var key Key
	if json.Unmarshal([]byte(raw), &key) != nil || key.ContextID != contextID || ValidateKey(key) != nil {
		return Key{}, ErrKey
	}
	return key, nil
}

var lookupKey = redis.NewScript(`
local raw=redis.call('GET',KEYS[1]);if not raw then return '' end
local ok,value=pcall(cjson.decode,raw);if not ok then return '' end
local now=tonumber(redis.call('TIME')[1])
if tonumber(value.retain_until or 0)<=now then return '' end
return raw
`)

func (s *RedisStore) Lookup(ctx context.Context, contextID, id string) (Key, error) {
	if !ValidID(contextID, 64, 64) || !ValidID(id, 34, 64) {
		return Key{}, ErrKey
	}
	part, _, ok := strings.Cut(id, "-")
	if !ok {
		return Key{}, ErrKey
	}
	epoch, err := strconv.ParseInt(part, 10, 64)
	if err != nil || epoch < 0 || strconv.FormatInt(epoch, 10) != part {
		return Key{}, ErrKey
	}
	raw, err := lookupKey.Run(ctx, s.Client, []string{s.prefix() + contextID + ":keys:" + id}).Text()
	if errors.Is(err, redis.Nil) {
		return Key{}, ErrKey
	}
	if err != nil {
		return Key{}, err
	}
	var key Key
	if json.Unmarshal([]byte(raw), &key) != nil || key.ContextID != contextID || key.ID != id || ValidateKey(key) != nil {
		return Key{}, ErrKey
	}
	return key, nil
}
