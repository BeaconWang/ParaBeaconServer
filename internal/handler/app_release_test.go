package handler

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/model"
)

type appReleaseHandlerTestService struct {
	release       *model.AppRelease
	path          string
	name          string
	code          int64
	notes         string
	bytes         string
	downloadCount int
}

func (s *appReleaseHandlerTestService) ListPublished(context.Context) ([]model.AppRelease, error) {
	if s.release == nil {
		return []model.AppRelease{}, nil
	}
	return []model.AppRelease{*s.release}, nil
}

func (s *appReleaseHandlerTestService) Latest(context.Context) (*model.AppRelease, error) {
	return s.release, nil
}

func (s *appReleaseHandlerTestService) Publish(_ context.Context, versionName string, versionCode int64, notes string, source io.Reader) (*model.AppRelease, error) {
	content, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	s.name, s.code, s.notes, s.bytes = versionName, versionCode, notes, string(content)
	return s.release, nil
}

func (s *appReleaseHandlerTestService) RecordDownload(context.Context, int64) error {
	s.downloadCount++
	return nil
}

func (s *appReleaseHandlerTestService) Delete(context.Context, int64) error {
	return nil
}

func (s *appReleaseHandlerTestService) FindPublished(context.Context, int64) (*model.AppRelease, string, error) {
	return s.release, s.path, nil
}

func TestLatestAppReleaseReturnsDownloadMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	release := &model.AppRelease{
		VersionName: "1.2.3",
		VersionCode: 123,
		PackageName: "com.beacon.parabeacon",
		SizeBytes:   4096,
		SHA256:      strings.Repeat("a", 64),
	}
	engine := gin.New()
	engine.GET("/api/v1/app/releases/latest", NewAppReleaseHandler(&appReleaseHandlerTestService{release: release}).Latest)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/app/releases/latest", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q", got)
	}
	if got := response.Header().Get("Expires"); got != "0" {
		t.Fatalf("Expires = %q", got)
	}
	for _, value := range []string{`"version_name":"1.2.3"`, `"version_code":123`, `"sha256":"` + strings.Repeat("a", 64) + `"`, `"download_url":"/api/v1/app/releases/123/download"`} {
		if !strings.Contains(response.Body.String(), value) {
			t.Errorf("response missing %s: %s", value, response.Body.String())
		}
	}
}

func TestListAppReleasesReturnsProtectedHistoryMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	published := ptrTime(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	release := &model.AppRelease{
		VersionName: "1.2.3",
		VersionCode: 123,
		PackageName: "com.beacon.parabeacon",
		SizeBytes:   4096,
		SHA256:      strings.Repeat("c", 64),
		PublishedAt: published,
	}
	engine := gin.New()
	engine.GET("/api/v1/admin/app/releases", NewAppReleaseHandler(&appReleaseHandlerTestService{release: release}).List)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/app/releases", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q", got)
	}
	for _, value := range []string{`"releases"`, `"version_name":"1.2.3"`, `"published_at":"2026-11-13T10:00:00Z"`} {
		if !strings.Contains(response.Body.String(), value) {
			t.Errorf("response missing %s: %s", value, response.Body.String())
		}
	}
}

func TestDeleteAppReleaseReturnsNoContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.DELETE("/api/v1/admin/app/releases/:version_code", NewAppReleaseHandler(&appReleaseHandlerTestService{}).Delete)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/app/releases/123", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestDownloadAppReleaseUsesAttachmentAndAPKContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	file := filepath.Join(t.TempDir(), "release.apk")
	if err := os.WriteFile(file, []byte("apk-test"), 0600); err != nil {
		t.Fatal(err)
	}
	release := &model.AppRelease{VersionName: "1.2.3", VersionCode: 123, SHA256: strings.Repeat("b", 64)}
	service := &appReleaseHandlerTestService{release: release, path: file}
	engine := gin.New()
	engine.GET("/api/v1/app/releases/:version_code/download", NewAppReleaseHandler(service).Download)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/app/releases/123/download", nil))
	if response.Code != http.StatusOK || response.Body.String() != "apk-test" {
		t.Fatalf("download status=%d body=%q", response.Code, response.Body.String())
	}
	if service.downloadCount != 1 {
		t.Fatalf("download count = %d, want 1", service.downloadCount)
	}
	if response.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate" || response.Header().Get("Pragma") != "no-cache" || response.Header().Get("Expires") != "0" {
		t.Fatalf("download cache headers = Cache-Control:%q Pragma:%q Expires:%q", response.Header().Get("Cache-Control"), response.Header().Get("Pragma"), response.Header().Get("Expires"))
	}
	if response.Header().Get("ETag") != "" {
		t.Fatalf("download unexpectedly has ETag = %q", response.Header().Get("ETag"))
	}
	if response.Header().Get("Content-Type") != "application/vnd.android.package-archive" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if !strings.Contains(response.Header().Get("Content-Disposition"), "parabeacon-1.2.3.apk") {
		t.Fatalf("Content-Disposition = %q", response.Header().Get("Content-Disposition"))
	}
}

func TestDownloadAppReleaseSupportsResumableByteRanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	file := filepath.Join(t.TempDir(), "release.apk")
	if err := os.WriteFile(file, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	service := &appReleaseHandlerTestService{
		release: &model.AppRelease{VersionName: "1.2.3", VersionCode: 123},
		path:    file,
	}
	engine := gin.New()
	engine.GET("/api/v1/app/releases/:version_code/download", NewAppReleaseHandler(service).Download)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/app/releases/123/download", nil)
	request.Header.Set("Range", "bytes=4-6")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "456" {
		t.Fatalf("range download status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Range") != "bytes 4-6/10" {
		t.Fatalf("Content-Range = %q", response.Header().Get("Content-Range"))
	}
	if service.downloadCount != 0 {
		t.Fatalf("partial chunk inflated download count to %d", service.downloadCount)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/app/releases/123/download", nil)
	request.Header.Set("Range", "bytes=0-3")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "0123" {
		t.Fatalf("initial range status=%d body=%q", response.Code, response.Body.String())
	}
	if service.downloadCount != 1 {
		t.Fatalf("initial range download count = %d, want 1", service.downloadCount)
	}
}

func TestPublishAppReleaseReadsVersionHeadersAndAPKBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	release := &model.AppRelease{VersionName: "1.2.3", VersionCode: 123}
	service := &appReleaseHandlerTestService{release: release}
	engine := gin.New()
	engine.POST("/api/v1/admin/app/releases", NewAppReleaseHandler(service).Publish)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/app/releases", strings.NewReader("test-apk"))
	request.Header.Set("Content-Type", "application/vnd.android.package-archive")
	request.Header.Set("X-APK-Version", "1.2.3")
	request.Header.Set("X-APK-Version-Code", "123")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if service.name != "1.2.3" || service.code != 123 || service.bytes != "test-apk" {
		t.Fatalf("published release = %q / %d / %q", service.name, service.code, service.bytes)
	}
}

func TestPublishAppReleaseAcceptsMultipartNotes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &appReleaseHandlerTestService{release: &model.AppRelease{VersionName: "1.2.3", VersionCode: 123}}
	engine := gin.New()
	engine.POST("/api/v1/admin/app/releases", NewAppReleaseHandler(service).Publish)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("version_name", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("version_code", "123"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("notes", "## What's new\\n- Faster launch"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("apk", "app-release.apk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("test-apk")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/app/releases", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if service.name != "1.2.3" || service.code != 123 || service.notes != "## What's new\\n- Faster launch" || service.bytes != "test-apk" {
		t.Fatalf("published multipart release = %#v / %d / %q / %q", service.name, service.code, service.notes, service.bytes)
	}
}

func TestPublishAppReleaseRejectsOversizedBodyFromContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/v1/admin/app/releases", NewAppReleaseHandler(&appReleaseHandlerTestService{release: &model.AppRelease{PublishedAt: ptrTime(time.Now())}}).Publish)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/app/releases", strings.NewReader("x"))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-APK-Version", "1.0.0")
	request.Header.Set("X-APK-Version-Code", "1")
	request.ContentLength = 600 << 20
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
