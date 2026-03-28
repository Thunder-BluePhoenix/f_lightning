# Phase 7: AI Search Layer (Hybrid Strategy)

**Goal:** Add an intelligence layer on top of the deterministic rule engine — starting with local embeddings for semantic search (no LLM required), and providing a clean toggle to cloud LLM for users who want full natural language understanding. The rule engine always runs first. AI is a configurable enhancement.

---

## Philosophy: "AI-Feel Without AI Dependency"

The core principle of this phase:

> **80% of what users expect from "AI search" can be delivered with deterministic engineering. Reserve LLM for what only LLMs can do.**

| Capability | Without LLM | With LLM |
|---|---|---|
| `"unpaid invoices above 10k"` | ✅ Rule engine handles perfectly | Overkill |
| `"bill from amazon last month"` | ✅ Synonyms + intent + date rule | Overkill |
| `"customers who might churn"` | ❌ Requires reasoning | ✅ LLM needed |
| `"show me stuff I forgot to collect"` | ❌ Too ambiguous | ✅ LLM needed |
| `"now filter that by region"` | ❌ Requires memory | ✅ LLM needed |

**Verdict:** Ship without LLM. Design the architecture so LLM is a switch, not a rewrite.

---

## AI_MODE Feature Flag

```yaml
# config.yaml
ai_mode: off  # off | local | cloud
```

```go
// config/config.go
type AIConfig struct {
    Mode         string `yaml:"ai_mode"`      // off | local | cloud
    LocalModel   string `yaml:"local_model"`  // path to sentence-transformers model
    CloudProvider string `yaml:"cloud_provider"` // openai | gemini | ollama
    CloudAPIKey  string `yaml:"cloud_api_key"`
    VectorDimension int `yaml:"vector_dimension"` // 384 for MiniLM, 1536 for OpenAI
}
```

The system behavior per mode:

| Mode | Behaviour |
|---|---|
| `off` | Rule engine only. Fastest. Zero external deps. Default. |
| `local` | Rule engine first → augment with local embedding similarity |
| `cloud` | Rule engine first → optionally rerank/expand via LLM API |

---

## Mode 1: `off` — Rule Engine Only (Default)

The deterministic pipeline from Phase 5. Always executes. Zero latency overhead. Zero cost.

```
User query → Tokenizer → Intent → Rules → Meilisearch DSL → Results
```

---

## Mode 2: `local` — Local Embeddings + Semantic Search

No API calls. No cloud. Privacy-safe. Works offline.

### How It Works

1. At startup, load a lightweight embedding model (e.g., `all-MiniLM-L6-v2`, 384 dimensions)
2. When a document is indexed, generate its embedding vector and store it in Meilisearch's vector field
3. At query time, embed the user's query → find semantically similar documents

### Embedding Service (Go sidecar calling Python)

Since Go doesn't have native sentence-transformers, we run a lightweight Python embedding server:

```python
# frappe_lightning/ai/embedding_server.py
from sentence_transformers import SentenceTransformer
from flask import Flask, request, jsonify

app = Flask(__name__)
model = SentenceTransformer('all-MiniLM-L6-v2')  # 80MB, fast

@app.route('/embed', methods=['POST'])
def embed():
    texts = request.json['texts']
    vectors = model.encode(texts, normalize_embeddings=True).tolist()
    return jsonify({'vectors': vectors})

if __name__ == '__main__':
    app.run(host='127.0.0.1', port=5001)
```

```bash
# Start embedding server
python frappe_lightning/ai/embedding_server.py &
```

### Go Embedding Client

```go
// ai/embedder.go
package ai

import (
    "bytes"
    "encoding/json"
    "net/http"
)

type Embedder struct {
    serverURL string
}

func NewEmbedder(url string) *Embedder {
    return &Embedder{serverURL: url}
}

func (e *Embedder) Embed(texts []string) ([][]float32, error) {
    body, _ := json.Marshal(map[string]interface{}{"texts": texts})
    resp, err := http.Post(e.serverURL+"/embed", "application/json", bytes.NewBuffer(body))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var result struct {
        Vectors [][]float32 `json:"vectors"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    return result.Vectors, nil
}
```

### Indexing with Vectors

```go
// search/vector_indexer.go
func (s *SyncEngine) indexWithVector(indexName string, doc map[string]interface{}) {
    if s.cfg.AI.Mode != "local" {
        s.batcher.Add(indexName, doc)
        return
    }

    // Concatenate searchable text for embedding
    text := buildSearchText(doc)
    vectors, err := s.embedder.Embed([]string{text})
    if err == nil && len(vectors) > 0 {
        doc["_vectors"] = map[string]interface{}{
            "default": vectors[0],
        }
    }
    s.batcher.Add(indexName, doc)
}

func buildSearchText(doc map[string]interface{}) string {
    fields := []string{"name", "customer_name", "item_name", "description", "remarks"}
    var parts []string
    for _, f := range fields {
        if v, ok := doc[f].(string); ok && v != "" {
            parts = append(parts, v)
        }
    }
    return strings.Join(parts, " ")
}
```

### Semantic Search at Query Time

```go
// api/handlers/search.go (when ai_mode=local)
func semanticSearch(query string, indexName string, embedder *ai.Embedder) (*meilisearch.SearchResponse, error) {
    vectors, err := embedder.Embed([]string{query})
    if err != nil || len(vectors) == 0 {
        return nil, err
    }

    return meiliClient.Index(indexName).Search("", &meilisearch.SearchRequest{
        Vector: vectors[0],     // Meilisearch vector search
        Hybrid: &meilisearch.SearchRequestHybrid{
            SemanticRatio: 0.7, // 70% semantic, 30% keyword
            Embedder:      "default",
        },
        Limit: 20,
    })
}
```

### What Semantic Search Unlocks

| User Query | Keyword Match | Semantic Match |
|---|---|---|
| `"unpaid bill"` | ❌ (no "bill" field) | ✅ "invoice" is semantically similar |
| `"company car"` | ❌ | ✅ matches "Vehicle" items |
| `"client from bangalore"` | ❌ "client" not a field | ✅ Customer with city=Bangalore |
| `"expensive items"` | ❌ "expensive" not indexed | ✅ Items with high `standard_rate` |

---

## Mode 3: `cloud` — LLM-Powered Query Understanding

For users who want the full natural language experience. Optional. Never required.

### Supported Providers

```yaml
ai_mode: cloud
cloud_provider: openai   # openai | gemini | ollama
cloud_api_key: sk-...
cloud_model: gpt-4o-mini
```

### Flow

```
User query → Rule Engine (fast path, always)
           ↓ (if rule engine confidence is low)
           → LLM: "Convert to Meilisearch filter JSON"
           → Validate + sanitize LLM output
           → Merge with rule engine filters
           → Meilisearch
```

### LLM Filter Generator

```go
// ai/llm.go
package ai

import (
    "context"
    "encoding/json"
    openai "github.com/sashabaranov/go-openai"
)

const systemPrompt = `You are a search query parser for a Frappe ERP system.
Convert the user's natural language query into a JSON object with this schema:
{
  "doctype": "<Sales Invoice|Customer|Item|Purchase Order|Supplier>",
  "filters": [
    {"field": "<field_name>", "op": "<= >= > < = between>", "value": <value>}
  ],
  "text": "<remaining free text for full-text search>"
}
Only return valid JSON. If unsure, return {"text": "<original query>"}.`

func (l *LLMClient) ParseQuery(ctx context.Context, query string) (*nlp.Query, error) {
    resp, err := l.openai.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
        Model: l.model,
        Messages: []openai.ChatCompletionMessage{
            {Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
            {Role: openai.ChatMessageRoleUser, Content: query},
        },
        MaxTokens:   256,
        Temperature: 0,  // Deterministic output
    })
    if err != nil { return nil, err }

    var q nlp.Query
    if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &q); err != nil {
        // LLM returned invalid JSON — fall back to rule engine only
        return nil, nil
    }
    return &q, nil
}
```

### Query Merging (Rule Engine + LLM)

```go
func mergeQueries(ruleQ, llmQ *nlp.Query) nlp.Query {
    final := *ruleQ // Start with deterministic rule engine output

    if llmQ == nil { return final } // LLM failed → pure rule engine

    // LLM DocType overrides rule engine only if rule engine didn't detect one
    if final.DocType == "" && llmQ.DocType != "" {
        final.DocType = llmQ.DocType
    }

    // LLM filters are additive — merged, not replaced
    final.Filters = append(final.Filters, llmQ.Filters...)

    return final
}
```

---

## Result Summarization (No LLM Required)

Template-based summaries feel intelligent without any AI:

```go
// ai/summarizer.go
package ai

import "fmt"

func Summarize(doctype string, hits []map[string]interface{}, filters []nlp.Filter) string {
    count := len(hits)
    if count == 0 {
        return "No results found."
    }

    switch doctype {
    case "Sales Invoice":
        total := sumField(hits, "grand_total")
        return fmt.Sprintf("%d %s invoice%s totalling %s",
            count,
            statusLabel(filters),
            pluralize(count),
            formatINR(total),
        )
    case "Customer":
        return fmt.Sprintf("%d customer%s found", count, pluralize(count))
    default:
        return fmt.Sprintf("%d result%s found", count, pluralize(count))
    }
}

// Example output:
// "12 unpaid invoices totalling ₹14,82,500"
// "3 customers found"
```

---

## Validation Checklist

- [ ] `AI_MODE=off` — system works identically to Phase 5, no AI code path invoked
- [ ] Embedding server starts and returns correct vectors for test texts
- [ ] Vector field stored in Meilisearch document after indexing in `local` mode
- [ ] Semantic search: query `"unpaid bill"` returns Sales Invoices even without literal "bill" match
- [ ] `AI_MODE=cloud` — LLM query is sent and parsed correctly
- [ ] LLM failure (timeout/invalid JSON) → graceful fallback to rule engine, no error to user
- [ ] Rule engine + LLM filters are merged correctly, no duplicate filters
- [ ] Result summarization returns correct count and total for Sales Invoices
- [ ] End-to-end semantic search latency <500ms in `local` mode
- [ ] Cloud LLM mode total search latency tracked in analytics
