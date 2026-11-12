package mediaobject

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Config struct {
	Environment, Endpoint, Bucket, AccessKeyID, AccessKeySecret, TempDir, CapacityPath string
	MinFreeBytes                                                                       int64
}

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func validConfig(c Config) bool {
	return segment.MatchString(c.Environment) && bucketPattern.MatchString(c.Bucket) && c.AccessKeyID != "" && c.AccessKeySecret != "" && c.TempDir != ""
}
func NewS3(c Config) (*Store, error) {
	u, err := url.Parse(c.Endpoint)
	if !validConfig(c) || err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, errors.New("invalid private staging configuration")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 4
	transport.MaxIdleConnsPerHost = 4
	transport.ResponseHeaderTimeout = 15 * time.Second
	client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(c.Endpoint), UsePathStyle: true, Credentials: awscredentials.NewStaticCredentialsProvider(c.AccessKeyID, c.AccessKeySecret, ""), RetryMaxAttempts: 2, HTTPClient: &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	return &Store{backend: &s3Backend{client: client, bucket: c.Bucket}, environment: c.Environment, temp: c.TempDir, minFree: c.MinFreeBytes, capacityPath: c.CapacityPath}, nil
}

type s3Backend struct {
	client *s3.Client
	bucket string
}

func (b *s3Backend) put(ctx context.Context, key string, r io.ReadSeeker, size int64, mime, sha string) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &b.bucket, Key: &key, Body: r, ContentLength: &size, ContentType: &mime, Metadata: map[string]string{"sha256": sha}, CacheControl: aws.String("private, max-age=60"), IfNoneMatch: aws.String("*")})
	return err
}
func (b *s3Backend) open(ctx context.Context, key string) (io.ReadCloser, error) {
	r, e := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
	if e != nil {
		return nil, e
	}
	return r.Body, nil
}
func (b *s3Backend) remove(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &b.bucket, Key: &key})
	return err
}
func (b *s3Backend) sign(context.Context, string, time.Duration) (string, error) {
	return "", errors.New("private staging has no browser URL")
}

func NewOSS(c Config) (*Store, error) {
	if !validConfig(c) || (c.Endpoint != "https://oss-cn-beijing.aliyuncs.com" && c.Endpoint != "https://oss-cn-beijing-internal.aliyuncs.com") {
		return nil, errors.New("invalid Beijing OSS configuration")
	}
	newClient := func(endpoint string) *oss.Client {
		return oss.NewClient(oss.LoadDefaultConfig().WithRegion("cn-beijing").WithEndpoint(endpoint).WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider(c.AccessKeyID, c.AccessKeySecret)).WithRetryMaxAttempts(2).WithConnectTimeout(3 * time.Second).WithReadWriteTimeout(45 * time.Second).WithMaxConnections(4).WithEnabledRedirect(false))
	}
	client := newClient(c.Endpoint)
	signer := client
	if strings.Contains(c.Endpoint, "-internal.") {
		signer = newClient("https://oss-cn-beijing.aliyuncs.com")
	}
	return &Store{backend: &ossBackend{client: client, signer: signer, bucket: c.Bucket}, environment: c.Environment, temp: c.TempDir, minFree: c.MinFreeBytes}, nil
}

type ossBackend struct {
	client, signer *oss.Client
	bucket         string
}

func (b *ossBackend) put(ctx context.Context, key string, r io.ReadSeeker, size int64, mime, sha string) error {
	_, err := b.client.PutObject(ctx, &oss.PutObjectRequest{Bucket: &b.bucket, Key: &key, Body: r, ContentLength: &size, ContentType: &mime, Metadata: map[string]string{"sha256": sha}, CacheControl: oss.Ptr("private, max-age=60"), ForbidOverwrite: oss.Ptr("true")})
	return err
}
func (b *ossBackend) open(ctx context.Context, key string) (io.ReadCloser, error) {
	r, e := b.client.GetObject(ctx, &oss.GetObjectRequest{Bucket: &b.bucket, Key: &key})
	if e != nil {
		return nil, e
	}
	return r.Body, nil
}
func (b *ossBackend) remove(ctx context.Context, key string) error {
	_, e := b.client.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: &b.bucket, Key: &key})
	return e
}
func (b *ossBackend) sign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	r, e := b.signer.Presign(ctx, &oss.GetObjectRequest{Bucket: &b.bucket, Key: &key}, oss.PresignExpires(ttl))
	if e != nil {
		return "", e
	}
	return r.URL, nil
}
