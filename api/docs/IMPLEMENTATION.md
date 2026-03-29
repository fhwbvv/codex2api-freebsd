# API Enhancement Implementation Guide

## Overview

This document describes the API enhancements implemented for the Codex2API project.

## Enhancements

### 1. API Version Management

- **Path-based versioning**: `/v1/` prefix for all API endpoints
- **Version headers**: `X-API-Version` and `X-API-Supported-Versions` headers
- **Future-proof**: Supports multiple major versions with backward compatibility

### 2. Request Validation Enhancement

Located in `api/validation.go`:

- **Required field validation**: Ensures mandatory fields are present
- **Type validation**: Validates field types (string, number, boolean, array, object)
- **Format validation**: Pattern matching, length limits, range checks
- **Enum validation**: Ensures values are in allowed sets
- **Array validation**: Min/max items, item type validation
- **Nested validation**: Validates message structures, input arrays

**Validation Rules:**
```go
map[string][]ValidationRule{
    "model":        {Required(), TypeString(), MaxLength(64)},
    "messages":     {Required(), TypeArray(), MinItems(1), MaxItems(4096)},
    "temperature":  {TypeNumber(), Range(0, 2)},
    // ...
}
```

### 3. Response Format Standardization

Located in `api/responses.go`:

**Error Response:**
```json
{
  "error": {
    "code": "INVALID_REQUEST",
    "message": "Invalid request",
    "type": "invalid_request_error",
    "details": {}
  }
}
```

**Success Response:**
- Consistent structure across all endpoints
- Metadata support (request ID, API version)
- Paginated list responses
- Streaming response chunks

### 4. Error Code Standardization

Located in `api/errors.go`:

**Categories:**
- Authentication errors: `missing_api_key`, `invalid_api_key`
- Request errors: `invalid_request`, `invalid_parameter`, `missing_field`
- Server errors: `server_error`, `service_unavailable`, `upstream_error`
- Rate limiting: `rate_limit_reached`

**Features:**
- Machine-readable error codes
- Human-readable messages
- Error categorization (type)
- Optional details field for context
- HTTP status code mapping

### 5. Middleware Stack

Located in `api/middleware.go`:

- **VersionMiddleware**: Adds version headers to responses
- **RequestContextMiddleware**: Tracks request metadata (ID, timing, API key)
- **BodyCacheMiddleware**: Caches request body for multiple reads
- **RecoveryMiddleware**: Enhanced panic recovery with standardized errors
- **LoggingMiddleware**: Structured JSON request logging
- **CORSMiddleware**: Cross-origin resource sharing support
- **SecurityHeadersMiddleware**: Security headers (CSP, X-Frame-Options, etc.)
- **ContentTypeMiddleware**: Content-Type validation

### 6. OpenAPI/Swagger Documentation

Located in `api/openapi.yaml`:

- Complete OpenAPI 3.0.3 specification
- All endpoints documented
- Request/response schemas
- Error response schemas
- Authentication scheme
- Model definitions

## File Structure

```
api/
├── errors.go           # Standardized error handling
├── responses.go        # Standardized response formats
├── validation.go       # Request validation utilities
├── middleware.go       # API middleware stack
├── swagger.go          # Swagger/OpenAPI annotations
├── openapi.yaml        # Complete OpenAPI specification
├── README.md           # API documentation
└── docs/
    └── errors.md       # Error codes reference
```

## Usage

### Using Validation

```go
import "github.com/codex2api/api"

validator := api.NewValidator(body)
result := validator.ValidateRequest(api.ChatCompletionValidationRules())

if !result.Valid {
    api.SendError(c, validator.ToAPIError())
    return
}
```

### Sending Standardized Errors

```go
api.SendError(c, api.NewAPIError(
    api.ErrCodeInvalidAPIKey,
    "API key is invalid",
    api.ErrorTypeAuthentication,
))
```

### Sending Validation Errors

```go
api.SendValidationError(c, "temperature", "must be between 0 and 2")
```

## Backward Compatibility

The enhancements maintain backward compatibility:
- Existing response formats are preserved where possible
- New fields are additive only
- Legacy error handling still works
- All changes are opt-in through the new api package

## Future Enhancements

Potential future additions:
- JSON Schema validation
- Request/response examples in OpenAPI
- API versioning deprecation notices
- Request signing/verification
- API client SDK generation
