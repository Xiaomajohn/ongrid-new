package pluginhost

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pkg/llm"
	"github.com/ongridio/ongrid/internal/pkg/notify"
	"github.com/ongridio/ongrid/internal/skill"
)

type Deps struct {
	DB     *gorm.DB
	Logger *slog.Logger
	Ctx    context.Context
	Mux    *chi.Mux

	Skills         SkillRegistrar
	Notifiers      NotifyRegistrar
	LLMProviders   LLMRegistrar
	EmbedProviders EmbeddingRegistrar
	Evaluators     EvaluatorRegistrar
	Workflow       WorkflowRegistrar

	Audit        AuditSink
	HostServices HostServices

	PluginDirs    []string
	Config        *PluginConfig
	TenantFromCtx func(context.Context) uint64
}

type HostServices struct {
	Loki   LokiQueryAPI
	Prom   PromQueryAPI
	Tempo  TempoQueryAPI
	Qdrant QdrantQueryAPI

	Skills   SkillLookupAPI
	LLM      LLMCompleteAPI
	Embedder EmbeddingAPI

	Edge     EdgeCommandAPI
	Audit    AuditAPI
	Topology TopologyAPI
	Notify   NotifyAPI
	Config   ConfigAPI
	Vault    VaultAPI
	Metrics  MetricsAPI
}

type PluginConfig struct {
	RateLimitPerMinute int
	InvokeTimeout      time.Duration
	AssetDir           string
	TrashDir           string
	EnableStaging      bool
}

type SkillRegistrar interface {
	RegisterOne(skill.Executor) error
}

type NotifyRegistrar interface {
	RegisterSender(notify.Sender) error
}

// LLMProvider is the current A-side provider surface. Phase 3 may replace
// this alias with llm.Provider when that exported abstraction is introduced.
type LLMProvider = llm.Client

type LLMRegistrar interface {
	RegisterProvider(name string, p LLMProvider) error
}

type Embedder interface {
	Dim() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type EmbeddingRegistrar interface {
	RegisterEmbedder(name string, e Embedder) error
}

// Evaluator and Node remain abstract until A exports the corresponding
// evaluator and workflow-node contracts in Phase 3.
type Evaluator interface{}
type Node interface{}

type EvaluatorRegistrar interface {
	RegisterEvaluator(name string, e Evaluator) error
}

type WorkflowRegistrar interface {
	RegisterNode(name string, n Node) error
}

type AuditSink interface {
	Write(ctx context.Context, comp, action, resource string, latency time.Duration, err error) error
}

type LokiQueryAPI interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) (json.RawMessage, error)
	QueryInstant(ctx context.Context, query string, at time.Time, limit int) (json.RawMessage, error)
}

type PromQueryAPI interface {
	Query(ctx context.Context, promql string) (json.RawMessage, error)
	QueryRange(ctx context.Context, promql string, start, end time.Time, step string) (json.RawMessage, error)
}

type TempoQueryAPI interface {
	Search(ctx context.Context, query string, limit int) (json.RawMessage, error)
	Trace(ctx context.Context, traceID string) (json.RawMessage, error)
}

type QdrantQueryAPI interface {
	Search(ctx context.Context, collection string, vector []float32, limit int) (json.RawMessage, error)
	Upsert(ctx context.Context, collection, id string, vector []float32, payload json.RawMessage) error
}

type SkillLookupAPI interface {
	List(ctx context.Context) ([]skill.Metadata, error)
	Execute(ctx context.Context, key string, params json.RawMessage) (json.RawMessage, error)
}

type LLMChatRequest interface {
	pluginhostLLMRequest()
}

type LLMChatResponse interface {
	pluginhostLLMResponse()
}

type LLMCompleteAPI interface {
	Chat(ctx context.Context, req LLMChatRequest) (LLMChatResponse, error)
	ChatStream(ctx context.Context, req LLMChatRequest, emit func(LLMChatResponse) error) error
}

type EmbeddingAPI interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type EdgeCommandAPI interface {
	RunShell(ctx context.Context, edgeID, cmd string, timeout time.Duration) (string, error)
	CopyFile(ctx context.Context, edgeID, src, dst string) error
	ListDir(ctx context.Context, edgeID, path string) ([]string, error)
}

type AuditAPI interface {
	Write(ctx context.Context, comp, action, resource, actor string, details map[string]any) error
}

type TopologyAPI interface {
	Get(ctx context.Context, id string) (json.RawMessage, error)
	Query(ctx context.Context, expr string) (json.RawMessage, error)
}

type NotifyAPI interface {
	Send(ctx context.Context, channel string, msg json.RawMessage) error
	SendVia(ctx context.Context, channelKind, name string, msg json.RawMessage) error
}

type ConfigAPI interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

type VaultAPI interface {
	GetRef(ctx context.Context, name string) (string, error)
}

type MetricsAPI interface {
	Write(ctx context.Context, name string, value float64, labels map[string]string) error
	Query(ctx context.Context, promql string) (json.RawMessage, error)
}
