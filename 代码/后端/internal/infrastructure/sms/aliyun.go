package sms

import (
	"context"
	"errors"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	pnvs "github.com/alibabacloud-go/dypnsapi-20170525/v3/client"
	"github.com/alibabacloud-go/tea/dara"
	"io"
	"net"
	"net/http"
	"qingye/backend/internal/config"
	domain "qingye/backend/internal/domain/registration"
	"time"
)

type rpc interface {
	SendSmsVerifyCodeWithOptions(*pnvs.SendSmsVerifyCodeRequest, *dara.RuntimeOptions) (*pnvs.SendSmsVerifyCodeResponse, error)
	CheckSmsVerifyCodeWithOptions(*pnvs.CheckSmsVerifyCodeRequest, *dara.RuntimeOptions) (*pnvs.CheckSmsVerifyCodeResponse, error)
}
type Aliyun struct {
	config config.RegistrationConfig
	slots  chan struct{}
	client func(context.Context) (rpc, error)
}
type contextualHTTP struct {
	ctx    context.Context
	client *http.Client
}

func (h contextualHTTP) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "dypnsapi.aliyuncs.com" {
		return nil, domain.ErrUnavailable
	}
	res, err := h.client.Do(req.Clone(h.ctx))
	if err == nil {
		res.Body = &limitedBody{Reader: io.LimitReader(res.Body, 128<<10), closer: res.Body}
	}
	return res, err
}

type limitedBody struct {
	io.Reader
	closer io.Closer
}

func (b *limitedBody) Close() error { return b.closer.Close() }
func NewAliyun(c config.RegistrationConfig) *Aliyun {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 4
	transport.MaxIdleConnsPerHost = 4
	transport.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 2 * time.Second
	transport.ResponseHeaderTimeout = 4 * time.Second
	httpClient := &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a := &Aliyun{config: c, slots: make(chan struct{}, 4)}
	a.client = func(ctx context.Context) (rpc, error) {
		return pnvs.NewClient(&openapi.Config{AccessKeyId: dara.String(c.AccessKeyID), AccessKeySecret: dara.String(c.AccessKeySecret), Endpoint: dara.String("dypnsapi.aliyuncs.com"), Protocol: dara.String("https"), HttpClient: contextualHTTP{ctx: ctx, client: httpClient}})
	}
	return a
}
func options() *dara.RuntimeOptions {
	return &dara.RuntimeOptions{Autoretry: dara.Bool(false), MaxAttempts: dara.Int(1), ConnectTimeout: dara.Int(2000), ReadTimeout: dara.Int(7000)}
}
func (a *Aliyun) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return domain.ErrUnavailable
	}
	select {
	case a.slots <- struct{}{}:
		return nil
	default:
		return domain.ErrBusy
	}
}
func (a *Aliyun) Send(ctx context.Context, phone, id string) (domain.Receipt, error) {
	if err := a.acquire(ctx); err != nil {
		return domain.Receipt{}, err
	}
	defer func() { <-a.slots }()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, err := a.client(ctx)
	if err != nil {
		return domain.Receipt{}, domain.ErrUnavailable
	}
	c := a.config
	response, err := client.SendSmsVerifyCodeWithOptions(&pnvs.SendSmsVerifyCodeRequest{PhoneNumber: dara.String(phone), CountryCode: dara.String("86"), SchemeName: dara.String(c.Scheme), SignName: dara.String(c.SignName), TemplateCode: dara.String(c.Template), TemplateParam: dara.String(`{"code":"##code##","min":"5"}`), CodeLength: dara.Int64(6), CodeType: dara.Int64(1), ValidTime: dara.Int64(300), Interval: dara.Int64(60), DuplicatePolicy: dara.Int64(1), ReturnVerifyCode: dara.Bool(false), AutoRetry: dara.Int64(0), OutId: dara.String(id)}, options())
	// Provider errors may contain request parameters; never expose or log them.
	if err != nil || response == nil || response.Body == nil {
		return domain.Receipt{}, domain.ErrUnavailable
	}
	b := response.Body
	if !dara.BoolValue(b.Success) || dara.StringValue(b.Code) != "OK" || b.Model == nil || dara.StringValue(b.Model.BizId) == "" {
		return domain.Receipt{}, domain.ErrUnavailable
	}
	return domain.Receipt{BizID: dara.StringValue(b.Model.BizId), RequestID: dara.StringValue(b.RequestId)}, nil
}
func (a *Aliyun) Check(ctx context.Context, phone, code, id string) (bool, error) {
	if err := a.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-a.slots }()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, err := a.client(ctx)
	if err != nil {
		return false, domain.ErrUnavailable
	}
	response, err := client.CheckSmsVerifyCodeWithOptions(&pnvs.CheckSmsVerifyCodeRequest{PhoneNumber: dara.String(phone), CountryCode: dara.String("86"), SchemeName: dara.String(a.config.Scheme), VerifyCode: dara.String(code), OutId: dara.String(id)}, options())
	var providerErr dara.BaseError
	if errors.As(err, &providerErr) && dara.StringValue(providerErr.GetCode()) == "isv.ValidateFail" {
		return false, nil
	}
	if err != nil || response == nil || response.Body == nil {
		return false, domain.ErrUnavailable
	}
	b := response.Body
	if dara.StringValue(b.Code) == "isv.ValidateFail" {
		return false, nil
	}
	if !dara.BoolValue(b.Success) || dara.StringValue(b.Code) != "OK" || b.Model == nil {
		return false, domain.ErrUnavailable
	}
	return dara.StringValue(b.Model.VerifyResult) == "PASS", nil
}
