package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domainmcp "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/mcp"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type previewTestRepo struct {
	servers []domainmcp.Server
}

func (r previewTestRepo) CreateServer(context.Context, repository.CreateMCPServerInput) (*domainmcp.Server, error) {
	return nil, nil
}

func (r previewTestRepo) UpdateServer(context.Context, uint, repository.UpdateMCPServerInput) (*domainmcp.Server, error) {
	return nil, nil
}

func (r previewTestRepo) ListServers(context.Context) ([]domainmcp.Server, error) {
	return r.servers, nil
}

func (r previewTestRepo) GetServer(context.Context, uint) (*domainmcp.Server, error) {
	return nil, nil
}

func (r previewTestRepo) DeleteServer(context.Context, uint) error { return nil }

func (r previewTestRepo) ReplaceServerTools(context.Context, uint, []domainmcp.Tool) error {
	return nil
}

func (r previewTestRepo) ListTools(context.Context, uint, bool) ([]domainmcp.Tool, error) {
	return nil, nil
}

func (r previewTestRepo) ListToolsByIDs(context.Context, []uint) ([]domainmcp.Tool, error) {
	return nil, nil
}

func (r previewTestRepo) UpdateTool(context.Context, uint, repository.UpdateMCPToolInput) (*domainmcp.Tool, error) {
	return nil, nil
}

func (r previewTestRepo) UpdateServerToolsStatus(context.Context, uint, []uint, string) ([]domainmcp.Tool, error) {
	return nil, nil
}

func (r previewTestRepo) ReorderServersWithTools(context.Context, []repository.ReorderMCPServerInput) ([]domainmcp.ServerWithTools, error) {
	return nil, nil
}

func TestBuildRAGFlowPreviewURLMapsDefaultMCPPort(t *testing.T) {
	got, err := buildRAGFlowPreviewURL("http://192.168.20.108:9382/mcp", "doc_123")
	if err != nil {
		t.Fatalf("buildRAGFlowPreviewURL returned error: %v", err)
	}
	want := "http://192.168.20.108:9380/api/v1/documents/doc_123/preview"
	if got != want {
		t.Fatalf("preview url = %q, want %q", got, want)
	}
}

func TestOpenRAGFlowDocumentPreviewForwardsTokenAndMetadata(t *testing.T) {
	const dataEncryptionKey = "test-data-encryption-key-value-32"
	const apiToken = "ragflow-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/documents/doc123/preview" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiToken {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="manual.pdf"`)
		_, _ = io.WriteString(w, "preview-data")
	}))
	defer server.Close()

	encrypted, err := secretbox.EncryptString(dataEncryptionKey, apiToken)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	service := NewServiceWithRuntime(
		config.NewRuntime(config.Config{Env: "dev", DataEncryptionKey: dataEncryptionKey}),
		previewTestRepo{servers: []domainmcp.Server{{
			Name:         "RAGFlow",
			BaseURL:      server.URL + "/mcp",
			AuthTokenEnc: encrypted,
			Status:       "active",
		}}},
		nil,
	)

	result, err := service.OpenRAGFlowDocumentPreview(context.Background(), "doc123")
	if err != nil {
		t.Fatalf("OpenRAGFlowDocumentPreview returned error: %v", err)
	}
	defer result.Reader.Close() //nolint:errcheck
	body, err := io.ReadAll(result.Reader)
	if err != nil {
		t.Fatalf("read preview body: %v", err)
	}
	if string(body) != "preview-data" {
		t.Fatalf("preview body = %q", body)
	}
	if result.ContentType != "application/pdf" {
		t.Fatalf("content type = %q", result.ContentType)
	}
	if !strings.Contains(result.ContentDisposition, "manual.pdf") {
		t.Fatalf("content disposition = %q", result.ContentDisposition)
	}
}

func TestOpenRAGFlowDocumentPreviewRequiresToken(t *testing.T) {
	service := NewServiceWithRuntime(
		config.NewRuntime(config.Config{Env: "dev", DataEncryptionKey: "test-data-encryption-key-value-32"}),
		previewTestRepo{servers: []domainmcp.Server{{
			Name:    "RAGFlow",
			BaseURL: "http://127.0.0.1:9382/mcp",
			Status:  "active",
		}}},
		nil,
	)

	_, err := service.OpenRAGFlowDocumentPreview(context.Background(), "doc123")
	if err != ErrRAGFlowTokenRequired {
		t.Fatalf("error = %v, want %v", err, ErrRAGFlowTokenRequired)
	}
}
