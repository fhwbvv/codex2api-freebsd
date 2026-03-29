// Package api provides OpenAPI/Swagger documentation
package api

// @title Codex2API
// @version 1.0.0
// @description OpenAI-compatible API proxy for Codex with enhanced features including multi-account pooling,
// automatic rotation, usage tracking, and administrative management.
//
// @contact.name API Support
// @contact.url https://github.com/codex2api
// @contact.email support@codex2api.local
//
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
//
// @host localhost:8080
// @BasePath /v1
// @schemes http https
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Enter your API key with Bearer prefix. Example: "Bearer sk-..."

// SwaggerInfo holds the OpenAPI specification
type SwaggerInfo struct {
	Title       string
	Version     string
	Description string
	Host        string
	BasePath    string
	Schemes     []string
}

// GetSwaggerInfo returns the swagger information
func GetSwaggerInfo() SwaggerInfo {
	return SwaggerInfo{
		Title:       "Codex2API",
		Version:     "1.0.0",
		Description: "OpenAI-compatible API proxy for Codex",
		Host:        "localhost:8080",
		BasePath:    "/v1",
		Schemes:     []string{"http", "https"},
	}
}
