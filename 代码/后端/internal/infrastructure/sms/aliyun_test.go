package sms

import (
	"context"
	"errors"
	pnvs "github.com/alibabacloud-go/dypnsapi-20170525/v3/client"
	"github.com/alibabacloud-go/tea/dara"
	"io"
	"net/http"
	"net/http/httptest"
	"qingye/backend/internal/config"
	"strings"
	"testing"
)

type fakeRPC struct {
	send  func(*pnvs.SendSmsVerifyCodeRequest, *dara.RuntimeOptions) (*pnvs.SendSmsVerifyCodeResponse, error)
	check func(*pnvs.CheckSmsVerifyCodeRequest, *dara.RuntimeOptions) (*pnvs.CheckSmsVerifyCodeResponse, error)
}

func (f fakeRPC) SendSmsVerifyCodeWithOptions(r *pnvs.SendSmsVerifyCodeRequest, o *dara.RuntimeOptions) (*pnvs.SendSmsVerifyCodeResponse, error) {
	return f.send(r, o)
}
func (f fakeRPC) CheckSmsVerifyCodeWithOptions(r *pnvs.CheckSmsVerifyCodeRequest, o *dara.RuntimeOptions) (*pnvs.CheckSmsVerifyCodeResponse, error) {
	return f.check(r, o)
}
func TestRegistrationProviderParametersAndStrictPASS(t *testing.T) {
	a := NewAliyun(config.RegistrationConfig{SignName: "test-sign", Template: "100001", Scheme: "test-register"})
	f := fakeRPC{send: func(r *pnvs.SendSmsVerifyCodeRequest, o *dara.RuntimeOptions) (*pnvs.SendSmsVerifyCodeResponse, error) {
		if dara.StringValue(r.TemplateParam) != `{"code":"##code##","min":"5"}` || dara.Int64Value(r.CodeLength) != 6 || dara.Int64Value(r.ValidTime) != 300 || dara.Int64Value(r.Interval) != 60 || dara.BoolValue(r.ReturnVerifyCode) || dara.Int64Value(r.DuplicatePolicy) != 1 || dara.BoolValue(o.Autoretry) || dara.IntValue(o.MaxAttempts) != 1 || dara.StringValue(r.SchemeName) != "test-register" || dara.StringValue(r.TemplateCode) != "100001" || dara.StringValue(r.SignName) != "test-sign" {
			t.Fatal("unsafe provider parameters")
		}
		return &pnvs.SendSmsVerifyCodeResponse{Body: &pnvs.SendSmsVerifyCodeResponseBody{Code: dara.String("OK"), Success: dara.Bool(true), Model: &pnvs.SendSmsVerifyCodeResponseBodyModel{BizId: dara.String("biz")}}}, nil
	}}
	a.client = func(context.Context) (rpc, error) { return f, nil }
	if r, e := a.Send(context.Background(), "13800000000", "id"); e != nil || r.BizID != "biz" {
		t.Fatal(r, e)
	}
	for _, result := range []string{"PASS", "FAIL", "", "pass"} {
		t.Run(result, func(t *testing.T) {
			f.check = func(r *pnvs.CheckSmsVerifyCodeRequest, _ *dara.RuntimeOptions) (*pnvs.CheckSmsVerifyCodeResponse, error) {
				if dara.StringValue(r.SchemeName) != "test-register" || dara.StringValue(r.VerifyCode) != "123456" {
					t.Fatal("check parameters")
				}
				return &pnvs.CheckSmsVerifyCodeResponse{Body: &pnvs.CheckSmsVerifyCodeResponseBody{Code: dara.String("OK"), Success: dara.Bool(true), Model: &pnvs.CheckSmsVerifyCodeResponseBodyModel{VerifyResult: dara.String(result)}}}, nil
			}
			ok, e := a.Check(context.Background(), "13800000000", "123456", "id")
			if e != nil || ok != (result == "PASS") {
				t.Fatal(ok, e)
			}
		})
	}
	f.check = func(*pnvs.CheckSmsVerifyCodeRequest, *dara.RuntimeOptions) (*pnvs.CheckSmsVerifyCodeResponse, error) {
		return nil, errors.New("private request data")
	}
	if ok, e := a.Check(context.Background(), "13800000000", "123456", "id"); ok || e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal("provider errors must be sanitized")
	}
}
func TestRegistrationProviderContextAndConcurrency(t *testing.T) {
	a := NewAliyun(config.RegistrationConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := a.acquire(ctx); e == nil {
		t.Fatal("accepted canceled call")
	}
	for i := 0; i < 4; i++ {
		if e := a.acquire(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if e := a.acquire(context.Background()); e == nil {
		t.Fatal("unbounded calls")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected provider request") }))
	defer server.Close()
	h := contextualHTTP{ctx: context.Background(), client: server.Client()}
	r, _ := http.NewRequest("GET", server.URL, nil)
	if _, e := h.Call(r, nil); e == nil {
		t.Fatal("arbitrary endpoint accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRegistrationProviderSDKTransportCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := contextualHTTP{ctx: ctx, client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Context() != ctx {
			t.Fatal("lost caller context")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}}
	r, _ := http.NewRequest("POST", "https://dypnsapi.aliyuncs.com/", nil)
	res, e := h.Call(r, nil)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	cancel()
}
