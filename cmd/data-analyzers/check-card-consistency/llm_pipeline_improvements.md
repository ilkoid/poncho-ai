# LLM Pipeline Architecture Improvements (Go Utility)

## Overview

This document summarizes architectural improvements for stabilizing LLM
output processing in a Go-based pipeline that analyzes product cards
(Wildberries).

------------------------------------------------------------------------

## Key Problems Observed

### 1. Invalid JSON

-   `unexpected end of JSON input`
-   Caused by truncation or incomplete responses

### 2. Encoding Issues

-   `invalid character 'Ð'`
-   Caused by garbage text before JSON

### 3. Type Mismatch

-   `cannot unmarshal array into ... string`
-   LLM output does not match Go struct expectations

------------------------------------------------------------------------

## Core Architectural Issue

LLM output is: - non-deterministic - loosely structured

Go expects: - strict schema - deterministic JSON

➡️ A normalization layer is required.

------------------------------------------------------------------------

## Recommended Architecture

### Pipeline

LLM → RAW STRING → sanitize → extract JSON → normalize → validate → Go
struct

------------------------------------------------------------------------

## Required Changes

### 1. Add Normalization Layer

``` go
raw → clean → extract → repair → unmarshal
```

------------------------------------------------------------------------

### 2. Adaptive Retry Strategy

Attempt 1: normal prompt\
Attempt 2: enforce strict JSON\
Attempt 3: minimal JSON response

------------------------------------------------------------------------

### 3. Fix Schema Mismatch

Use:

``` go
Attributes json.RawMessage
```

or define full struct.

------------------------------------------------------------------------

### 4. Reduce Token Pressure

-   max_tokens: 2000
-   shorter prompts
-   allow summary truncation

------------------------------------------------------------------------

### 5. Add JSON Validation

``` go
if !json.Valid(data) {
    // repair
}
```

------------------------------------------------------------------------

### 6. Introduce Error Classification

``` go
ErrJSONSyntax
ErrJSONType
ErrLLMTruncated
ErrEncoding
```

------------------------------------------------------------------------

### 7. Add Fallback Response

``` json
{
  "discrepancy": false,
  "issues": [],
  "summary": "parse_failed"
}
```

------------------------------------------------------------------------

### 8. Control Concurrency

-   adaptive concurrency
-   exponential backoff

------------------------------------------------------------------------

## Key Insight

LLM ≠ deterministic API\
LLM = probabilistic, noisy output

➡️ Always normalize before parsing.

------------------------------------------------------------------------

## Final Recommendation

Do NOT rely on direct `json.Unmarshal` from LLM output.

Introduce a dedicated **LLM Output Adapter layer**.
