package cache

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	domain "qingye/backend/internal/domain/security"
	"strconv"
	"time"
)

const readPrefix = "qingye:r201:"

type MediaReadStore struct {
	Client *redis.Client
	Policy domain.ReadPolicy
	// 仅用于隔离验收；运行装配不接受外部提供的命名空间。
	Namespace string
}

func (s *MediaReadStore) prefix() string {
	if s.Namespace != "" {
		return s.Namespace
	}
	return readPrefix
}
func (s *MediaReadStore) readKey(id string) string { return s.prefix() + "s:" + id }
func (s *MediaReadStore) keys(w domain.ReadSubject) []string {
	p := s.prefix()
	return []string{s.readKey(w.Key), p + "i:" + w.IPKey, p + "global", p + "subjects", p + "l:s:" + w.Key, p + "l:i:" + w.IPKey, p + "l:global", p + "metrics"}
}

// 时间、令牌桶、空间上限与并发租约在同一Redis脚本内判定。硬额度不因验证码通过而清零。
var mediaReadBegin = redis.NewScript(`
local tm=redis.call('TIME'); local now=tonumber(tm[1])+tonumber(tm[2])/1000000
local s=KEYS[1];local ip=KEYS[2];local g=KEYS[3]
local function num(k,f,d) return tonumber(redis.call('HGET',k,f)) or d end
local function remember(state,code,seconds)
 if redis.call('HGET',s,'reason')~=code then redis.call('HINCRBY',s,'version',1) end
 redis.call('HSET',s,'state',state,'reason',code,'until',math.ceil(now+math.max(5,seconds)))
 redis.call('HINCRBY',KEYS[8],state,1);redis.call('EXPIRE',KEYS[8],86400)
 return {state,code,tostring(math.max(1,math.ceil(seconds))),tostring(math.ceil(now+seconds))}
end
if redis.call('EXISTS',s)==0 then
 redis.call('ZREMRANGEBYSCORE',KEYS[4],'-inf',now)
 if redis.call('ZCARD',KEYS[4])>=tonumber(ARGV[14]) then return {'limited','MEDIA_CAPACITY', '60','0'} end
 local hour=math.floor(now/3600)
 if num(ip,'allocation_hour',-1)~=hour then redis.call('HSET',ip,'allocation_hour',hour,'allocations',0) end
 if num(ip,'allocations',0)>=256 then return {'limited','MEDIA_CAPACITY','60','0'} end
 redis.call('HINCRBY',ip,'allocations',1)
 redis.call('HSET',s,'surface',ARGV[2],'kind',ARGV[3],'version',1)
end
redis.call('ZADD',KEYS[4],now+3600,ARGV[15]);redis.call('EXPIRE',KEYS[4],7200)
for _,k in ipairs({s,ip,g}) do redis.call('HSET',k,'touched',now);redis.call('EXPIRE',k,3600) end
redis.call('HINCRBY',s,'requests',1)
local manual=num(s,'manual_until',0)
if manual>now then return remember('restricted','MEDIA_RESTRICTED',manual-now) end
local function bucket(k,name,cap,rate,cost)
 local last=num(k,name..'_at',now); local n=math.min(cap,num(k,name,cap)+math.max(0,now-last)*rate)
 redis.call('HSET',k,name,n,name..'_at',now)
 if n<cost then return false,(cost-n)/rate end
 return true,n-cost
end
local values={}
for _,b in ipairs({{s,'hard',tonumber(ARGV[8]),tonumber(ARGV[9]),1},{ip,'requests',tonumber(ARGV[10]),tonumber(ARGV[11]),1},{s,'bytes',tonumber(ARGV[12]),tonumber(ARGV[13]),tonumber(ARGV[5])},{ip,'bytes',tonumber(ARGV[12])*4,tonumber(ARGV[13])*4,tonumber(ARGV[5])},{g,'bytes',tonumber(ARGV[12])*8,tonumber(ARGV[13])*4,tonumber(ARGV[5])}}) do
 local ok,n=bucket(b[1],b[2],b[3],b[4],b[5]);if not ok then return remember('limited','MEDIA_RATE_LIMITED',n) end
 table.insert(values,{b[1],b[2],n})
end
local verified=redis.call('HGET',s,'verified_binding')==ARGV[1] and num(s,'verified_until',0)>now
local ok,left=bucket(s,'soft',tonumber(ARGV[6])*(verified and 2 or 1),tonumber(ARGV[7])*(verified and 2 or 1),1)
if (num(s,'needs_challenge',0)==1 or not ok) and not verified then
 redis.call('HSET',s,'needs_challenge',1);return remember('challenge','MEDIA_VERIFICATION_REQUIRED',300)
end
if not ok then return remember('limited','MEDIA_RATE_LIMITED',left) end
for i,limit in ipairs({6,16,24}) do
 local k=KEYS[i+4];redis.call('ZREMRANGEBYSCORE',k,'-inf',now)
 if redis.call('ZCARD',k)>=limit then return remember('limited','MEDIA_CONCURRENCY',1) end
end
for _,v in ipairs(values) do redis.call('HSET',v[1],v[2],v[3]) end
redis.call('HSET',s,'soft',left)
if num(s,'until',0)<=now then redis.call('HSET',s,'state','allowed','reason','','until',0) end
for i=5,7 do redis.call('ZADD',KEYS[i],now+75,ARGV[4]);redis.call('EXPIRE',KEYS[i],90) end
redis.call('HINCRBY',KEYS[8],'allowed',1);redis.call('HINCRBY',KEYS[8],'reserved_bytes',ARGV[5]);redis.call('EXPIRE',KEYS[8],86400)
return {'allowed','','0','0'}
`)

func (s *MediaReadStore) Begin(ctx context.Context, w domain.ReadSubject, lease string, size int64) (domain.ReadDecision, error) {
	if size < 0 || size > 64<<20 {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	// 缺失历史大小也不得形成零成本下载；现网就绪媒体已核对均有实际大小。
	if size == 0 {
		size = 64 << 20
	}
	p := s.Policy
	out, err := mediaReadBegin.Run(ctx, s.Client, s.keys(w), w.Binding, w.Surface, w.Kind, lease, size, p.Burst, p.PerSecond, p.HardBurst, p.HardPerSecond, p.IPBurst, p.IPPerSecond, p.ByteBurst, p.BytesPerSecond, p.MaxSubjects, w.Key).StringSlice()
	if err != nil || len(out) != 4 {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	retry, _ := strconv.Atoi(out[2])
	until, _ := strconv.ParseInt(out[3], 10, 64)
	return domain.ReadDecision{State: out[0], Code: out[1], RetryAfter: retry, Until: until}, nil
}

var mediaReadFinish = redis.NewScript(`
local removed=redis.call('ZREM',KEYS[1],ARGV[1]);redis.call('ZREM',KEYS[2],ARGV[1]);redis.call('ZREM',KEYS[3],ARGV[1])
if removed==1 then
 redis.call('HINCRBY',KEYS[4],'bytes_sent',ARGV[2]);redis.call('EXPIRE',KEYS[4],86400)
 if redis.call('EXISTS',KEYS[5])==1 then redis.call('HINCRBY',KEYS[5],'bytes_sent',ARGV[2]) end
end
return removed`)

// 旧缩略图转码后可能大于原图；在发送任何正文前补齐差额，不能只按原图计费。
var mediaReadExpand = redis.NewScript(`
local tm=redis.call('TIME');local now=tonumber(tm[1])+tonumber(tm[2])/1000000;local values={}
if redis.call('EXISTS',KEYS[1])==0 then return {'limited','MEDIA_CAPACITY','5','0'} end
for _,b in ipairs({{KEYS[1],tonumber(ARGV[2]),tonumber(ARGV[3])},{KEYS[2],tonumber(ARGV[2])*4,tonumber(ARGV[3])*4},{KEYS[3],tonumber(ARGV[2])*8,tonumber(ARGV[3])*4}}) do
 local at=tonumber(redis.call('HGET',b[1],'bytes_at')) or now
 local value=math.min(b[2],(tonumber(redis.call('HGET',b[1],'bytes')) or b[2])+math.max(0,now-at)*b[3])
 if value<tonumber(ARGV[1]) then
  local seconds=math.max(1,math.ceil((tonumber(ARGV[1])-value)/b[3]));local untilAt=math.ceil(now+math.max(5,seconds))
  redis.call('HSET',KEYS[1],'state','limited','reason','MEDIA_RATE_LIMITED','until',untilAt);redis.call('HINCRBY',KEYS[1],'version',1)
  redis.call('HINCRBY',KEYS[4],'limited',1);redis.call('EXPIRE',KEYS[4],86400)
  return {'limited','MEDIA_RATE_LIMITED',tostring(seconds),tostring(untilAt)}
 end
 table.insert(values,{b[1],value-tonumber(ARGV[1])})
end
for _,v in ipairs(values) do redis.call('HSET',v[1],'bytes',v[2],'bytes_at',now);redis.call('EXPIRE',v[1],3600) end
redis.call('HINCRBY',KEYS[4],'reserved_bytes',ARGV[1]);redis.call('EXPIRE',KEYS[4],86400)
return {'allowed','','0','0'}
`)

func (s *MediaReadStore) Expand(ctx context.Context, w domain.ReadSubject, extra int64) (domain.ReadDecision, error) {
	if extra <= 0 {
		return domain.AllowedRead(), nil
	}
	if extra > 64<<20 {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	k := s.keys(w)
	out, e := mediaReadExpand.Run(ctx, s.Client, []string{k[0], k[1], k[2], k[7]}, extra, s.Policy.ByteBurst, s.Policy.BytesPerSecond).StringSlice()
	if e != nil || len(out) != 4 {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	retry, _ := strconv.Atoi(out[2])
	until, _ := strconv.ParseInt(out[3], 10, 64)
	return domain.ReadDecision{State: out[0], Code: out[1], RetryAfter: retry, Until: until}, nil
}

func (s *MediaReadStore) Finish(ctx context.Context, w domain.ReadSubject, lease string, n int64) error {
	k := s.keys(w)
	return mediaReadFinish.Run(ctx, s.Client, []string{k[4], k[5], k[6], k[7], k[0]}, lease, n).Err()
}
func (s *MediaReadStore) Status(ctx context.Context, w domain.ReadSubject) (domain.ReadDecision, error) {
	m, err := s.Client.HGetAll(ctx, s.readKey(w.Key)).Result()
	if err != nil {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	now, err := s.Client.Time(ctx).Result()
	if err != nil {
		return domain.ReadDecision{}, domain.ErrUnavailable
	}
	until, _ := strconv.ParseInt(m["manual_until"], 10, 64)
	if until > now.Unix() {
		return domain.ReadDecision{State: "restricted", Code: "MEDIA_RESTRICTED", RetryAfter: int(until - now.Unix()), Until: until}, nil
	}
	verified, _ := strconv.ParseInt(m["verified_until"], 10, 64)
	if m["needs_challenge"] == "1" && (m["verified_binding"] != w.Binding || verified <= now.Unix()) {
		return domain.ReadDecision{State: "challenge", Code: "MEDIA_VERIFICATION_REQUIRED", RetryAfter: 1}, nil
	}
	until, _ = strconv.ParseInt(m["until"], 10, 64)
	if m["state"] == "limited" && until > now.Unix() {
		return domain.ReadDecision{State: "limited", Code: m["reason"], RetryAfter: int(until - now.Unix()), Until: until}, nil
	}
	return domain.AllowedRead(), nil
}
func (s *MediaReadStore) Records(ctx context.Context) ([]domain.ReadRecord, map[string]string, error) {
	ids, err := s.Client.ZRevRange(ctx, s.prefix()+"subjects", 0, 199).Result()
	if err != nil {
		return nil, nil, domain.ErrUnavailable
	}
	pipe := s.Client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, pipe.HGetAll(ctx, s.readKey(id)))
	}
	stats := pipe.HGetAll(ctx, s.prefix()+"metrics")
	_, err = pipe.Exec(ctx)
	if err != nil {
		return nil, nil, domain.ErrUnavailable
	}
	out := make([]domain.ReadRecord, 0, len(ids))
	for i, c := range cmds {
		m := c.Val()
		if len(m) == 0 {
			continue
		}
		u, _ := strconv.ParseInt(m["until"], 10, 64)
		if u <= time.Now().Unix() && m["needs_challenge"] != "1" {
			m["reason"] = ""
		}
		n, _ := strconv.ParseInt(m["requests"], 10, 64)
		b, _ := strconv.ParseInt(m["bytes_sent"], 10, 64)
		out = append(out, domain.ReadRecord{Subject: ids[i], Surface: m["surface"], Kind: m["kind"], Reason: m["reason"], Until: u, Version: m["version"], Requests: n, BytesSent: b})
	}
	return out, stats.Val(), nil
}

var mediaControl = redis.NewScript(`
if redis.call('HGET',KEYS[1],'version')~=ARGV[1] then return 0 end
local now=tonumber(redis.call('TIME')[1]);local duration=tonumber(ARGV[2]);local untilAt=duration>0 and now+duration or 0
redis.call('HSET',KEYS[1],'manual_until',untilAt,'until',untilAt,'state',duration>0 and 'restricted' or 'allowed','reason',duration>0 and 'MEDIA_RESTRICTED' or '')
redis.call('HINCRBY',KEYS[1],'version',1);redis.call('EXPIRE',KEYS[1],math.max(3600,duration+60));return 1`)

func (s *MediaReadStore) Control(ctx context.Context, id, version string, duration int) error {
	n, err := mediaControl.Run(ctx, s.Client, []string{s.readKey(id)}, version, duration).Int()
	if err != nil {
		return domain.ErrUnavailable
	}
	if n != 1 {
		return fmt.Errorf("version conflict")
	}
	return nil
}

var createHumanChallenge = redis.NewScript(`
if redis.call('EXISTS',KEYS[1])==0 then return '' end
local old=redis.call('HGET',KEYS[1],'pending_challenge')
if old then local k=ARGV[6]..old;if redis.call('HGET',k,'binding')==ARGV[2] and redis.call('HGET',k,'state')=='new' and redis.call('HEXISTS',k,'token')==0 and redis.call('TTL',k)>10 then return old end;redis.call('DEL',k) end
local now=tonumber(redis.call('TIME')[1])
redis.call('HSET',KEYS[2],'binding',ARGV[2],'subject',ARGV[3],'action',ARGV[4],'hostname',ARGV[5],'state','new','expires',now+300)
redis.call('EXPIRE',KEYS[2],300);redis.call('HSET',KEYS[1],'pending_challenge',ARGV[1]);return ARGV[1]`)

func (s *MediaReadStore) challenge(ctx context.Context, id string) (domain.HumanChallenge, error) {
	m, err := s.Client.HGetAll(ctx, s.prefix()+"c:"+id).Result()
	if err != nil {
		return domain.HumanChallenge{}, domain.ErrUnavailable
	}
	if len(m) == 0 {
		return domain.HumanChallenge{}, domain.ErrChallenge
	}
	expires, _ := strconv.ParseInt(m["expires"], 10, 64)
	return domain.HumanChallenge{ID: id, Action: m["action"], Hostname: m["hostname"], Binding: m["binding"], Subject: m["subject"], ExpiresAt: time.Unix(expires, 0), Attempt: m["attempt"]}, nil
}
func (s *MediaReadStore) CreateChallenge(ctx context.Context, w domain.ReadSubject, id, action, host string) (domain.HumanChallenge, error) {
	out, err := createHumanChallenge.Run(ctx, s.Client, []string{s.readKey(w.Key), s.prefix() + "c:" + id}, id, w.Binding, w.Key, action, host, s.prefix()+"c:").Text()
	if err != nil {
		return domain.HumanChallenge{}, domain.ErrUnavailable
	}
	if out == "" {
		return domain.HumanChallenge{}, domain.ErrChallenge
	}
	return s.challenge(ctx, out)
}

var claimHumanChallenge = redis.NewScript(`
if redis.call('HGET',KEYS[1],'binding')~=ARGV[1] or redis.call('HGET',KEYS[1],'subject')~=ARGV[2] or redis.call('HGET',KEYS[1],'state')~='new' then return 0 end
local now=tonumber(redis.call('TIME')[1]);if tonumber(redis.call('HGET',KEYS[1],'expires') or '0')<=now then return 0 end
local token=redis.call('HGET',KEYS[1],'token');if token and token~=ARGV[3] then return 0 end
if tonumber(redis.call('HGET',KEYS[1],'claimed_until') or '0')>now then return 2 end
redis.call('HSET',KEYS[1],'token',ARGV[3],'attempt',ARGV[4],'claimed_until',now+20);return 1`)

func (s *MediaReadStore) ClaimChallenge(ctx context.Context, w domain.ReadSubject, id, token, attempt string) (domain.HumanChallenge, error) {
	n, err := claimHumanChallenge.Run(ctx, s.Client, []string{s.prefix() + "c:" + id}, w.Binding, w.Key, token, attempt).Int()
	if err != nil {
		return domain.HumanChallenge{}, domain.ErrUnavailable
	}
	if n == 2 {
		return domain.HumanChallenge{}, domain.ErrBusy
	}
	if n != 1 {
		return domain.HumanChallenge{}, domain.ErrChallenge
	}
	return s.challenge(ctx, id)
}

var completeHumanChallenge = redis.NewScript(`
local now=tonumber(redis.call('TIME')[1])
if redis.call('HGET',KEYS[1],'state')~='new' or redis.call('HGET',KEYS[1],'attempt')~=ARGV[1] or redis.call('HGET',KEYS[1],'binding')~=ARGV[2] or tonumber(redis.call('HGET',KEYS[1],'claimed_until') or '0')<now or redis.call('EXISTS',KEYS[2])==0 then return 0 end
if ARGV[4]=='1' then redis.call('HSET',KEYS[1],'claimed_until',0);return 1 end
redis.call('HSET',KEYS[1],'state','used','claimed_until',0)
if ARGV[3]=='1' then
 redis.call('HSET',KEYS[2],'verified_binding',ARGV[2],'verified_until',now+900,'needs_challenge',0,'soft',math.min(tonumber(redis.call('HGET',KEYS[2],'soft') or '0')+30,tonumber(ARGV[5])*2))
 if tonumber(redis.call('HGET',KEYS[2],'manual_until') or '0')<=now and redis.call('HGET',KEYS[2],'state')=='challenge' then redis.call('HSET',KEYS[2],'state','allowed','reason','','until',0) end
 redis.call('HINCRBY',KEYS[3],'verified',1);redis.call('EXPIRE',KEYS[3],86400)
end
return 1`)

func (s *MediaReadStore) CompleteChallenge(ctx context.Context, w domain.ReadSubject, c domain.HumanChallenge, ok, retry bool) error {
	bit := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	n, err := completeHumanChallenge.Run(ctx, s.Client, []string{s.prefix() + "c:" + c.ID, s.readKey(w.Key), s.prefix() + "metrics"}, c.Attempt, w.Binding, bit(ok), bit(retry), s.Policy.Burst).Int()
	if err != nil {
		return domain.ErrUnavailable
	}
	if n != 1 {
		return domain.ErrChallenge
	}
	return nil
}
