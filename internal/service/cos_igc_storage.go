package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

type COSIGCObjectStore struct {
	client *cos.Client
}

func NewCOSIGCObjectStore(bucketURL, secretID, secretKey string) (*COSIGCObjectStore, error) {
	parsed, err := url.Parse(strings.TrimSpace(bucketURL))
	if err != nil || strings.TrimSpace(secretID) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, errors.New("COS bucket URL and credentials are required")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".cos.") || !strings.HasSuffix(host, ".myqcloud.com") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("COS bucket URL must be an HTTPS Tencent COS bucket host")
	}
	transport := &cos.AuthorizationTransport{
		SecretID:  strings.TrimSpace(secretID),
		SecretKey: strings.TrimSpace(secretKey),
		Transport: http.DefaultTransport,
	}
	client := cos.NewClient(&cos.BaseURL{BucketURL: parsed}, &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	})
	return &COSIGCObjectStore{client: client}, nil
}

func (s *COSIGCObjectStore) PutIGC(ctx context.Context, objectKey string, content []byte) error {
	if s == nil || s.client == nil || len(content) == 0 || len(content) > MaxFlightArchiveBytes {
		return errors.New("invalid IGC object")
	}
	cleanKey := path.Clean(objectKey)
	if cleanKey == "." || cleanKey != objectKey || strings.HasPrefix(cleanKey, "/") || strings.Contains(cleanKey, "\\") || strings.ContainsAny(cleanKey, "\r\n\x00") {
		return errors.New("invalid IGC object key")
	}
	_, err := s.client.Object.Put(ctx, cleanKey, bytes.NewReader(content), &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{ContentType: "application/vnd.fai.igc"},
	})
	if err != nil {
		return fmt.Errorf("upload IGC to Tencent COS: %w", err)
	}
	return nil
}
