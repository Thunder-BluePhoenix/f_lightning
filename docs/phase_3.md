# Phase 3: The Search API Proxy & Permission-Aware Auth

**Goal:** Build a high-performance, secure HTTP server in Go that sits between the Frappe UI and Meilisearch — validating user sessions against Redis, applying RBAC filters, running the NLP engine, and returning results in <10ms. This is where everything wires together.

---

## API Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/search?q=...&doctype=...` | Full-text + NLP structured search |
| `GET` | `/api/v1/suggest?q=...` | Query autocompletion (top 5 quick matches) |
| `GET` | `/api/v1/autocomplete?q=...` | Faceted autocomplete by DocType |
| `GET` | `/api/v1/health` | Service health check (uptime, Meilisearch status, Redis status) |
| `POST` | `/api/v1/analytics/click` | Record a result click event for ranking signals |

---

## Server Setup (Fiber)

```go
// api/server.go
package api

import (
    "time"
    "github.com/gofiber/fiber/v2"
    "github.com/gofiber/fiber/v2/middleware/cors"
    "github.com/gofiber/fiber/v2/middleware/limiter"
    "github.com/gofiber/fiber/v2/middleware/logger"
    "github.com/gofiber/fiber/v2/middleware/recover"
)

func NewServer(cfg *config.Config) *fiber.App {
    app := fiber.New(fiber.Config{
        ReadTimeout:  5 * time.Second,
        WriteTimeout: 10 * time.Second,
        ErrorHandler: customErrorHandler,
    })

    // Global middleware
    app.Use(recover.New())           // Recover from panics
    app.Use(logger.New())            // Structured access logging
    app.Use(cors.New(cors.Config{
        AllowOrigins: cfg.AllowedOrigins, // e.g. "https://erp.yoursite.com"
        AllowHeaders: "Origin, Content-Type, Accept",
        AllowCredentials: true,
    }))
    app.Use(limiter.New(limiter.Config{
        Max:        100,              // 100 requests per minute per IP
        Expiration: 1 * time.Minute,
    }))

    // Public routes
    app.Get("/api/v1/health", handlers.Health)

    // Authenticated routes
    api := app.Group("/api/v1", AuthMiddleware)
    api.Get("/search",            handlers.Search)
    api.Get("/suggest",           handlers.Suggest)
    api.Get("/autocomplete",      handlers.Autocomplete)
    api.Post("/analytics/click",  handlers.RecordClick)

    return app
}

func customErrorHandler(c *fiber.Ctx, err error) error {
    code := fiber.StatusInternalServerError
    if e, ok := err.(*fiber.Error); ok {
        code = e.Code
    }
    return c.Status(code).JSON(fiber.Map{"error": err.Error()})
}
```

---

## Authentication Middleware

The Go proxy validates the incoming Frappe session by reading directly from the **Redis session cache** — no round-trip to the Python app needed, keeping auth overhead under 1ms.

```go
// api/middleware/auth.go
package middleware

import (
    "context"
    "encoding/json"
    "github.com/gofiber/fiber/v2"
    "github.com/redis/go-redis/v9"
)

type FrappeSession struct {
    User  string   `json:"user"`
    Roles []string `json:"roles"`
}

func AuthMiddleware(c *fiber.Ctx) error {
    sid := c.Cookies("sid")
    if sid == "" || sid == "Guest" {
        return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
            "error": "unauthenticated",
        })
    }

    // Read Frappe session from Redis (Frappe stores sessions as frappe:session:<sid>)
    ctx := context.Background()
    userJSON, err := redisClient.Get(ctx, "frappe:session:"+sid).Result()
    if err == redis.Nil {
        return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
            "error": "invalid or expired session",
        })
    }
    if err != nil {
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
            "error": "session lookup failed",
        })
    }

    var session FrappeSession
    if err := json.Unmarshal([]byte(userJSON), &session); err != nil {
        return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
            "error": "malformed session",
        })
    }

    // Store in request context for downstream handlers
    c.Locals("user", session.User)
    c.Locals("roles", session.Roles)
    return c.Next()
}
```

---

## Role-Based Access Control (RBAC)

After authentication, the proxy builds **Meilisearch filter expressions** based on the user's Frappe roles — ensuring results are scoped correctly before the query even reaches Meilisearch.

```go
// api/rbac.go
package api

import "fmt"

// RoleFilterMap defines what filter to inject for each role combination
type RoleFilter struct {
    DocType string
    Filter  string
}

func BuildPermissionFilters(user string, roles []string, doctype string) []string {
    var filters []string

    isSystemManager  := hasRole(roles, "System Manager")
    isAccountsManager := hasRole(roles, "Accounts Manager")

    switch doctype {
    case "Sales Invoice":
        // Non-accounts users can only see their own company's invoices
        if !isSystemManager && !isAccountsManager {
            company := getUserCompany(user)
            if company != "" {
                filters = append(filters, fmt.Sprintf("company = '%s'", company))
            }
        }

    case "Customer":
        // Territory managers only see their territory
        if territory := getUserTerritory(roles); territory != "" && !isSystemManager {
            filters = append(filters, fmt.Sprintf("territory = '%s'", territory))
        }
    }

    return filters
}

func hasRole(roles []string, target string) bool {
    for _, r := range roles {
        if r == target {
            return true
        }
    }
    return false
}
```

These filters are **merged** with the NLP-generated filters before querying Meilisearch — the user's data never reaches the client.

---

## The Search Handler

```go
// api/handlers/search.go
package handlers

import (
    "fmt"
    "time"
    "github.com/gofiber/fiber/v2"
    "frappe_lightning/nlp"
    "frappe_lightning/ranking"
    "frappe_lightning/analytics"
)

func Search(c *fiber.Ctx) error {
    start := time.Now()

    rawQuery := c.Query("q", "")
    doctype  := c.Query("doctype", "")
    user     := c.Locals("user").(string)
    roles    := c.Locals("roles").([]string)

    // 1. Run NLP Engine — converts natural language → structured Query{}
    parsed := nlp.BuildQuery(rawQuery)
    if doctype != "" {
        parsed.DocType = doctype // Explicit override takes precedence
    }

    // 2. Build base Meilisearch params from NLP output
    meiliParams := nlp.ToMeili(parsed)

    // 3. Inject Smart Ranking signals (field boosts, usage scores)
    rankingParams := ranking.BuildParams(user, parsed.DocType)
    meiliParams["rankingScoreThreshold"] = rankingParams.ScoreThreshold
    meiliParams["sort"] = rankingParams.SortRules

    // 4. Inject RBAC permission filters
    permFilters := rbac.BuildPermissionFilters(user, roles, parsed.DocType)
    existingFilters, _ := meiliParams["filter"].([]string)
    meiliParams["filter"] = append(existingFilters, permFilters...)

    // 5. Select correct namespaced index
    site := c.Hostname()
    indexName := fmt.Sprintf("%s_%s", slugify(site), slugify(parsed.DocType))

    // 6. Execute search
    results, err := meiliClient.Index(indexName).Search(rawQuery, &meiliParams)
    if err != nil {
        return c.Status(500).JSON(fiber.Map{"error": "search failed"})
    }

    tookMs := time.Since(start).Milliseconds()

    // 7. Log analytics event asynchronously (does not block response)
    go analytics.Log(analytics.SearchEvent{
        User:        user,
        Query:       rawQuery,
        DocType:     parsed.DocType,
        ResultCount: int(results.EstimatedTotalHits),
        TookMs:      int(tookMs),
    })

    // 8. Return formatted response
    return c.JSON(formatResponse(rawQuery, parsed, results, tookMs))
}
```

---

## Response Format

```json
{
  "query": "unpaid invoices last month above 10k",
  "parsed": {
    "doctype": "Sales Invoice",
    "filters": [
      { "field": "status", "op": "=", "value": "Overdue" },
      { "field": "grand_total", "op": ">", "value": 10000 },
      { "field": "posting_date", "op": "between", "value": [1738368000, 1740787199] }
    ],
    "text": ""
  },
  "hits": [
    {
      "name": "ACC-SINV-2026-00123",
      "doctype": "Sales Invoice",
      "customer_name": "Amazon India Pvt Ltd",
      "grand_total": 125000,
      "grand_total_display": "₹1,25,000",
      "status": "Overdue",
      "posting_date": 1739500800,
      "_score": 0.98
    }
  ],
  "total": 47,
  "took_ms": 6
}
```

---

## Suggest & Autocomplete Handlers

```go
// api/handlers/suggest.go
func Suggest(c *fiber.Ctx) error {
    q := c.Query("q", "")
    roles := c.Locals("roles").([]string)
    user := c.Locals("user").(string)

    // Search across all indexes with a small limit for speed
    var suggestions []SuggestionHit
    for _, schema := range config.DocTypes {
        indexName := fmt.Sprintf("%s_%s", site, schema.IndexSuffix)
        res, _ := meiliClient.Index(indexName).Search(q, &meilisearch.SearchRequest{
            Limit: 3,
            // Always apply permissions even in suggest
            Filter: rbac.BuildPermissionFilters(user, roles, schema.Name),
        })
        for _, hit := range res.Hits {
            suggestions = append(suggestions, toSuggestion(hit, schema.Name))
        }
    }

    return c.JSON(fiber.Map{"suggestions": suggestions[:min(5, len(suggestions))]})
}
```

---

## Health Check

```go
// api/handlers/health.go
func Health(c *fiber.Ctx) error {
    _, meiliErr := meiliClient.Health()
    _, redisErr := redisClient.Ping(context.Background()).Result()

    status := "ok"
    if meiliErr != nil || redisErr != nil {
        status = "degraded"
    }

    return c.JSON(fiber.Map{
        "status":    status,
        "uptime_s":  time.Since(startTime).Seconds(),
        "meilisearch": meiliErr == nil,
        "redis":       redisErr == nil,
        "version":   "1.0.0",
    })
}
```

---

## Security Considerations

| Concern | Protection |
|---|---|
| **CORS** | Strictly whitelisted to the Frappe site domain |
| **Rate Limiting** | 100 requests/minute per IP (configurable) |
| **Session TTL** | Redis session check respects Frappe's native session expiry |
| **No raw Meilisearch keys exposed** | Only the Go proxy holds the Meilisearch API key |
| **RBAC enforced always** | Permission filters injected on every request, no bypass possible |
| **Panic recovery** | Fiber recover middleware prevents crashes from exposing internals |
| **Input sanitization** | All user query strings are passed through Meilisearch's own sanitization |

---

## Validation Checklist

- [ ] `/api/v1/health` returns 200 with Meilisearch and Redis status
- [ ] Request with no `sid` cookie returns 401
- [ ] Request with expired or invalid session returns 401
- [ ] Valid session → search results returned in <10ms (p99)
- [ ] User with restricted roles only sees permitted records (test with company filter)
- [ ] CORS blocks requests from non-whitelisted origins
- [ ] Rate limiter blocks >100 req/min per IP
- [ ] `/suggest` returns max 5 results across indexed DocTypes
- [ ] Click event logged correctly via `/analytics/click`
- [ ] Panic in handler is recovered — service stays up, returns 500
