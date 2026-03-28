# Phase 5: NLP Rule Engine & Advanced Query Language

**Goal:** Build a **Deterministic Query Interpreter** — a Go module that converts messy user input into structured Meilisearch filters without any LLM, giving users 80% of the AI "feel" at 0% of the cost or latency.

---

## The Philosophy

> "You're not building NLP like GPT. You're building a **Deterministic Query Interpreter** disguised as natural language."

No LLM. No cost. No latency. Full control. Enterprise-friendly. Privacy-safe.

### What you CAN do without LLM ✅

| User Input | What Happens |
|---|---|
| `"unpaid invoices"` | status filter → `Overdue` |
| `"above 10k"` | amount filter → `grand_total > 10000` |
| `"above 5 lakhs"` | amount filter → `grand_total > 500000` |
| `"last month"` | date filter → precise time boundaries |
| `"this year"` | date filter → Jan 1 to today |
| `"amazon"` | free-text → passed to Meilisearch full-text engine |
| `"purchase orders"` | intent → DocType: `Purchase Order` |
| `"overdue suppliers"` | intent + status → Supplier, payment_status=Overdue |

### What you CANNOT do without LLM ❌

| User Input | Why It Fails |
|---|---|
| `"customers who might churn"` | Requires reasoning, not pattern matching |
| `"show me stuff I forgot to collect money from"` | Too ambiguous / requires context |
| `"now filter that by region"` | Requires conversational memory |

> 💡 **Strategy:** Build the rule engine now. Design it to be LLM-pluggable later via `AI_MODE=off/local/cloud`.

---

## Architecture

```
User Query (raw string)
        │
        ▼
┌──────────────────┐
│  Advanced Parser │  Detects explicit DSL (field:"value" AND ...)
└──────┬───────────┘
       │ (if no explicit syntax detected)
       ▼
┌──────────────┐
│  Tokenizer   │  lowercase, strip punctuation, split on whitespace
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Intent      │  DocType Detection (invoices → "Sales Invoice")
│  Extractor   │
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Rule Engine │  Amount, Status, Date, Operator rules (pluggable)
│  (pluggable) │  Remaining tokens → free-text field
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Query       │  Builds structured Query{} DSL
│  Builder     │
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Meili       │  Translates DSL → Meilisearch filter strings + params
│  Translator  │
└──────────────┘
```

---

## Module Structure

```
frappe_lightning/nlp/
├── models.go       ← Query{} and Filter{} structs (the DSL)
├── tokenizer.go    ← Input normalization and splitting
├── intent.go       ← DocType synonym dictionary + detection
├── rules.go        ← Amount, Status, Date rule functions
├── advanced.go     ← Explicit DSL parser (field:value AND/OR/NOT)
├── builder.go      ← Pluggable rules registry + BuildQuery()
├── meili.go        ← Translates Query → Meilisearch params map
└── builder_test.go ← Table-driven tests for every rule
```

---

## The DSL (Internal Format)

```go
// models.go
package nlp

// Query is the internal structured representation of a user's search intent
type Query struct {
    DocType string   `json:"doctype"`
    Filters []Filter `json:"filters"`
    Text    string   `json:"text"`   // Remaining free-text for Meili's fuzzy engine
    Sort    string   `json:"sort"`
    Limit   int      `json:"limit"`
}

// Filter represents a single field-level constraint
type Filter struct {
    Field string      `json:"field"`
    Op    string      `json:"op"`    // = > < >= <= between
    Value interface{} `json:"value"` // string, int, float64, []time.Time
}
```

---

## 1. Tokenizer

```go
// tokenizer.go
package nlp

import (
    "regexp"
    "strings"
)

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9\s]`)

func Tokenize(input string) []string {
    lower := strings.ToLower(input)
    clean := nonAlphaNum.ReplaceAllString(lower, " ")
    return strings.Fields(clean)
}

// Example:
// "Unpaid Invoices, last month above 10k!"
// → ["unpaid", "invoices", "last", "month", "above", "10k"]
```

---

## 2. Intent Detection (DocType Mapping)

```go
// intent.go
package nlp

// DocTypeMap maps user terms → canonical Frappe DocType names
var DocTypeMap = map[string]string{
    "invoice":        "Sales Invoice",
    "invoices":       "Sales Invoice",
    "sinv":           "Sales Invoice",
    "bill":           "Sales Invoice",
    "bills":          "Sales Invoice",
    "customer":       "Customer",
    "customers":      "Customer",
    "client":         "Customer",
    "clients":        "Customer",
    "item":           "Item",
    "items":          "Item",
    "product":        "Item",
    "products":       "Item",
    "sku":            "Item",
    "po":             "Purchase Order",
    "purchase":       "Purchase Order",
    "order":          "Purchase Order",
    "orders":         "Purchase Order",
    "supplier":       "Supplier",
    "suppliers":      "Supplier",
    "vendor":         "Supplier",
    "vendors":        "Supplier",
    "lead":           "Lead",
    "leads":          "Lead",
    "opportunity":    "Opportunity",
    "opportunities":  "Opportunity",
}

func DetectDocType(tokens []string) string {
    for _, t := range tokens {
        if dt, ok := DocTypeMap[t]; ok {
            return dt
        }
    }
    return "" // Unknown → search across all indexes
}
```

---

## 3. Rule Engine

### Status Rules

```go
// rules.go (partial)
package nlp

var StatusMap = map[string]string{
    "unpaid":    "Overdue",
    "overdue":   "Overdue",
    "paid":      "Paid",
    "draft":     "Draft",
    "submitted": "Submitted",
    "cancelled": "Cancelled",
    "open":      "Open",
    "closed":    "Closed",
    "pending":   "Pending",
}

func StatusRule(tokens []string) *Filter {
    for _, t := range tokens {
        if status, ok := StatusMap[t]; ok {
            return &Filter{Field: "status", Op: "=", Value: status}
        }
    }
    return nil
}
```

### Amount Rules

```go
func ParseAmount(token string) (float64, bool) {
    token = strings.ToLower(strings.TrimSpace(token))
    multipliers := map[string]float64{
        "k":      1_000,
        "l":      100_000,
        "lakh":   100_000,
        "lakhs":  100_000,
        "m":      1_000_000,
        "cr":     10_000_000,
        "crore":  10_000_000,
        "crores": 10_000_000,
    }
    for suffix, mult := range multipliers {
        if strings.HasSuffix(token, suffix) {
            raw := strings.TrimSuffix(token, suffix)
            val, err := strconv.ParseFloat(raw, 64)
            if err == nil {
                return val * mult, true
            }
        }
    }
    val, err := strconv.ParseFloat(token, 64)
    return val, err == nil
}

func AmountRule(tokens []string) *Filter {
    opMap := map[string]string{
        "above":   ">",
        "over":    ">",
        "greater": ">",
        "more":    ">",
        "below":   "<",
        "under":   "<",
        "less":    "<",
        "exactly": "=",
    }
    for i, t := range tokens {
        if op, ok := opMap[t]; ok {
            if i+1 < len(tokens) {
                if val, ok := ParseAmount(tokens[i+1]); ok {
                    return &Filter{Field: "grand_total", Op: op, Value: val}
                }
            }
        }
    }
    return nil
}
```

### Date Rules

```go
func DateRule(tokens []string) *Filter {
    now := time.Now()

    patterns := map[string]func() (time.Time, time.Time){
        "today": func() (time.Time, time.Time) {
            sod := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
            return sod, now
        },
        "yesterday": func() (time.Time, time.Time) {
            y := now.AddDate(0, 0, -1)
            sod := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, y.Location())
            eod := sod.Add(24*time.Hour - time.Second)
            return sod, eod
        },
        "week|this_week": func() (time.Time, time.Time) {
            return now.AddDate(0, 0, -7), now
        },
        "month|this_month": func() (time.Time, time.Time) {
            start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
            return start, now
        },
        "year|this_year": func() (time.Time, time.Time) {
            start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
            return start, now
        },
    }

    // "last N days/weeks/months"
    for i, t := range tokens {
        if t == "last" && i+1 < len(tokens) {
            switch tokens[i+1] {
            case "month":
                start := now.AddDate(0, -1, 0)
                return &Filter{Field: "posting_date", Op: "between", Value: []int64{start.Unix(), now.Unix()}}
            case "week":
                start := now.AddDate(0, 0, -7)
                return &Filter{Field: "posting_date", Op: "between", Value: []int64{start.Unix(), now.Unix()}}
            case "year":
                start := now.AddDate(-1, 0, 0)
                return &Filter{Field: "posting_date", Op: "between", Value: []int64{start.Unix(), now.Unix()}}
            case "30", "7", "14", "90":
                n, _ := strconv.Atoi(tokens[i+1])
                if i+2 < len(tokens) && tokens[i+2] == "days" {
                    start := now.AddDate(0, 0, -n)
                    return &Filter{Field: "posting_date", Op: "between", Value: []int64{start.Unix(), now.Unix()}}
                }
            }
        }
    }
    return nil
}
```

---

## 4. Query Builder (Pluggable Registry)

```go
// builder.go
package nlp

// RuleFn is a function that inspects tokens and optionally returns a filter
type RuleFn func(tokens []string) *Filter

// Rules is the global pluggable rule registry.
// Developers can append their own rules here.
var Rules = []RuleFn{
    StatusRule,
    AmountRule,
    DateRule,
}

// BuildQuery is the main entry point — converts raw user input to a structured Query
func BuildQuery(input string) Query {
    // 1. Try Advanced DSL first
    if q, ok := ParseAdvanced(input); ok {
        return q
    }

    // 2. NLP pipeline
    tokens := Tokenize(input)

    q := Query{
        DocType: DetectDocType(tokens),
        Limit:   20,
    }

    usedTokens := map[int]bool{}

    for _, rule := range Rules {
        if f := rule(tokens); f != nil {
            q.Filters = append(q.Filters, *f)
            // Mark consumed tokens (simplified — full impl tracks by index)
        }
    }

    // Remaining tokens become free-text for Meilisearch
    var textTokens []string
    for i, t := range tokens {
        if !usedTokens[i] && DocTypeMap[t] == "" && StatusMap[t] == "" {
            textTokens = append(textTokens, t)
        }
    }
    q.Text = strings.Join(textTokens, " ")

    return q
}
```

### Custom Rule Registration

```go
// Register a domain-specific rule without touching core code
nlp.Rules = append(nlp.Rules, func(tokens []string) *nlp.Filter {
    for _, t := range tokens {
        if t == "urgent" || t == "priority" {
            return &nlp.Filter{Field: "priority", Op: "=", Value: "High"}
        }
    }
    return nil
})
```

---

## 5. Meilisearch Translator

```go
// meili.go
package nlp

import "fmt"

func ToMeili(q Query) map[string]interface{} {
    filters := []string{}

    for _, f := range q.Filters {
        switch f.Op {
        case "=":
            filters = append(filters, fmt.Sprintf("%s = '%v'", f.Field, f.Value))
        case ">":
            filters = append(filters, fmt.Sprintf("%s > %v", f.Field, f.Value))
        case "<":
            filters = append(filters, fmt.Sprintf("%s < %v", f.Field, f.Value))
        case ">=":
            filters = append(filters, fmt.Sprintf("%s >= %v", f.Field, f.Value))
        case "<=":
            filters = append(filters, fmt.Sprintf("%s <= %v", f.Field, f.Value))
        case "between":
            vals := f.Value.([]int64)
            filters = append(filters, fmt.Sprintf("%s %d TO %d", f.Field, vals[0], vals[1]))
        }
    }

    params := map[string]interface{}{
        "q":     q.Text,
        "limit": q.Limit,
    }
    if len(filters) > 0 {
        params["filter"] = filters
    }
    if q.Sort != "" {
        params["sort"] = []string{q.Sort}
    }
    return params
}
```

---

## 6. Advanced Query Language (Power Users)

Beyond natural language, power users can write an explicit query DSL:

```
customer:"Amazon" AND status:"Overdue"
amount > 50000
date: last_30_days
grand_total between 10000 50000
posting_date >= 2026-01-01
```

Parsed by `advanced.go` which runs before the NLP tokenizer. If explicit field syntax is detected, it bypasses NLP rules entirely and constructs filters directly.

```go
// advanced.go (excerpt)
package nlp

import (
    "regexp"
    "strings"
)

var fieldValueRe = regexp.MustCompile(`(\w+):\s*"([^"]+)"`)

func ParseAdvanced(input string) (Query, bool) {
    matches := fieldValueRe.FindAllStringSubmatch(input, -1)
    if len(matches) == 0 {
        return Query{}, false
    }

    q := Query{Limit: 20}
    for _, m := range matches {
        field, value := m[1], m[2]
        if field == "doctype" {
            q.DocType = value
        } else {
            q.Filters = append(q.Filters, Filter{Field: field, Op: "=", Value: value})
        }
    }
    return q, true
}
```

---

## 7. Example: Full Flow

**Input:** `"unpaid invoices last month above 10k"`

**Step 1 — Tokenize:**
```
["unpaid", "invoices", "last", "month", "above", "10k"]
```

**Step 2 — Intent:**
```
"invoices" → DocType: "Sales Invoice"
```

**Step 3 — Rules:**
```
StatusRule:  "unpaid"       → { field: "status", op: "=", value: "Overdue" }
AmountRule:  "above", "10k" → { field: "grand_total", op: ">", value: 10000 }
DateRule:    "last month"   → { field: "posting_date", op: "between", value: [unix_start, unix_end] }
```

**Step 4 — Meilisearch Params:**
```json
{
  "q": "",
  "filter": [
    "status = 'Overdue'",
    "grand_total > 10000",
    "posting_date 1738368000 TO 1740787199"
  ],
  "limit": 20
}
```

---

## Future: AI Mode (Feature Flag)

```go
// config/config.go
type Config struct {
    AIMode string `yaml:"ai_mode"` // off | local | cloud
}
```

| Mode | Behaviour |
|---|---|
| `off` | Rule engine only (default, always fastest) |
| `local` | Run through `sentence-transformers` for vector similarity; rule engine is fallback |
| `cloud` | Send raw query to OpenAI/Gemini to generate filter JSON; validate + sanitize it |

The rule engine always runs first as a deterministic, zero-latency fallback.

---

## Validation Checklist

- [x] `TestBuildQuery` passes: "unpaid invoices last month above 10k" → correct DocType, 3 filters
- [ ] Amount parser handles `10k`, `5l`, `1m`, `5 lakhs`, `2 crore`, and raw integers
- [ ] Status parser covers all standard Frappe statuses (Draft, Submitted, Cancelled, Overdue, Paid, Open, Closed)
- [ ] Date parser handles `last month`, `last week`, `this year`, `last 30 days`, `last 7 days`, `today`, `yesterday`
- [ ] Advanced query parser correctly handles `field:"value"` and `amount > N` syntax
- [ ] NLP engine integrated into `/search` endpoint (Phase 3 connector)
- [ ] Custom rule registration is documented and tested
- [ ] `AI_MODE=local` flag loads embedding model and produces vector search results
- [ ] Remaining free-text tokens correctly passed to Meilisearch `q` field
