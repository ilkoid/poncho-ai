
# LLM Pipeline Architecture Improvements (Extended)

## Overview

This document provides an extended architectural analysis and recommendations for stabilizing LLM output handling in a Go-based pipeline.

---

## Pipeline Diagram

```
                +------------------+
                |   LLM (Gemini)   |
                +--------+---------+
                         |
                         v
                +------------------+
                |  RAW RESPONSE    |
                | (string output)  |
                +--------+---------+
                         |
                         v
                +------------------+
                |   SANITIZATION   |
                | trim, encoding   |
                +--------+---------+
                         |
                         v
                +------------------+
                |  JSON EXTRACTION |
                | find {...} block |
                +--------+---------+
                         |
                         v
                +------------------+
                |  NORMALIZATION   |
                | types, arrays    |
                +--------+---------+
                         |
                         v
                +------------------+
                |   VALIDATION     |
                | json.Valid()     |
                +--------+---------+
                         |
                         v
                +------------------+
                |  GO STRUCT MAP   |
                +------------------+
```

---

## Key Failure Modes

### 1. Truncated JSON
- Cause: token limits / timeout
- Fix: reduce max_tokens, shorten prompt

### 2. Garbage Before JSON
- Cause: LLM adds text prefix
- Fix: extract substring between first `{` and last `}`

### 3. Type Drift
- Cause: LLM outputs dynamic types
- Fix: normalization layer

---

## Architecture Enhancements

### 1. LLM Output Adapter (Core Layer)

```go
func NormalizeLLMOutput(raw string) (string, error) {
    cleaned := strings.TrimSpace(raw)

    start := strings.Index(cleaned, "{")
    end := strings.LastIndex(cleaned, "}")

    if start == -1 || end == -1 {
        return "", errors.New("no json found")
    }

    jsonPart := cleaned[start : end+1]

    return jsonPart, nil
}
```

---

### 2. Adaptive Retry

| Attempt | Strategy |
|--------|----------|
| 1 | normal prompt |
| 2 | enforce JSON-only |
| 3 | minimal schema |

---

### 3. Type Normalization

```go
type FlexibleIssues []Issue

func NormalizeIssues(v interface{}) []Issue {
    switch t := v.(type) {
    case []interface{}:
        // parse properly
    case string:
        return []Issue{}
    default:
        return []Issue{}
    }
}
```

---

### 4. Error Classification

```go
type ErrorType int

const (
    ErrJSONSyntax ErrorType = iota
    ErrJSONType
    ErrTruncated
    ErrEncoding
)
```

---

### 5. Concurrency Control

- dynamic worker pool
- backoff on latency spike

---

### 6. Fallback Strategy

Always return safe structure:

```json
{
  "discrepancy": false,
  "issues": [],
  "summary": "parse_failed"
}
```

---

## Performance Recommendations

- max_tokens: 1500–2000
- concurrency: 2–3 (adaptive)
- timeout: 60–90s

---

## Key Insight

LLM output must be treated as **untrusted, probabilistic data**.

---

## Final Architecture

```
LLM → Adapter → Normalizer → Validator → Go Struct → DB
```

