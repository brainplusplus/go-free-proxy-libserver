package main

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// buildSchemaFromStruct generates an OpenAPI 2.0 schema object from a Go struct via reflection.
func buildSchemaFromStruct(v interface{}) map[string]interface{} {
	if v == nil {
		return map[string]interface{}{"type": "object"}
	}

	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return map[string]interface{}{"type": "object"}
	}

	properties := map[string]interface{}{}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name := strings.Split(jsonTag, ",")[0]

		swaggerProp := map[string]interface{}{}

		switch field.Type.Kind() {
		case reflect.String:
			swaggerProp["type"] = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			swaggerProp["type"] = "integer"
		case reflect.Bool:
			swaggerProp["type"] = "boolean"
		case reflect.Slice:
			swaggerProp["type"] = "array"
			swaggerProp["items"] = map[string]interface{}{"type": "string"}
		case reflect.Struct:
			if field.Type.String() == "time.Time" {
				swaggerProp["type"] = "string"
				swaggerProp["format"] = "date-time"
			} else {
				swaggerProp = buildSchemaFromStruct(reflect.New(field.Type).Interface())
			}
		default:
			swaggerProp["type"] = "string"
		}

		properties[name] = swaggerProp
	}

	return map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
}

// swaggerSpec builds the OpenAPI 2.0 specification automatically from routeRegistry.
func swaggerSpec() map[string]interface{} {
	paths := map[string]interface{}{}
	hasAuth := false

	for _, r := range routeRegistry {
		parameters := []map[string]interface{}{}

		if r.QueryStruct != nil {
			qt := reflect.TypeOf(r.QueryStruct)
			for qt.Kind() == reflect.Ptr {
				qt = qt.Elem()
			}

			if qt.Kind() == reflect.Struct {
				for i := 0; i < qt.NumField(); i++ {
					field := qt.Field(i)
					jsonTag := field.Tag.Get("json")
					if jsonTag == "" || jsonTag == "-" {
						continue
					}
					name := strings.Split(jsonTag, ",")[0]

					param := map[string]interface{}{
						"name":        name,
						"in":          "query",
						"required":    false,
						"type":        "string",
						"description": "",
					}

					if name == "category_code" {
						param["enum"] = []string{"EN", "UK", "US", "SSL"}
						param["description"] = "Filter by category. Omit for all categories."
					}
					if name == "target_url" {
						param["description"] = "Target URL to test/pool key. Supports http, https, ws, wss. Default: https://example.com"
					}

					parameters = append(parameters, param)
				}
			}
		}

		responseSchema := buildSchemaFromStruct(r.ResponseStruct)

		routeSpec := map[string]interface{}{
			"summary":    r.Summary,
			"tags":       []string{"proxy"},
			"parameters": parameters,
			"responses": map[string]interface{}{
				"200": map[string]interface{}{
					"description": "Success",
					"schema":      responseSchema,
				},
				"500": map[string]interface{}{
					"description": "Error",
					"schema":      responseSchema,
				},
			},
		}

		if r.RequireAuth {
			hasAuth = true
			routeSpec["security"] = []map[string]interface{}{
				{"ApiKeyAuth": []string{}},
			}
			routeSpec["responses"].(map[string]interface{})["401"] = map[string]interface{}{
				"description": "Unauthorized - invalid or missing API key",
			}
		}

		// Fix 5: merge methods into existing path map instead of overwrite
		pathKey := "/api/v1" + r.Path
		if _, ok := paths[pathKey]; !ok {
			paths[pathKey] = map[string]interface{}{}
		}
		paths[pathKey].(map[string]interface{})[r.Method] = routeSpec
	}

	spec := map[string]interface{}{
		"swagger": "2.0",
		"info": map[string]interface{}{
			"title":       "FreeProxy API",
			"description": "REST API for fetching and validating free proxies scraped from free-proxy-list.net",
			"version":     "1.0.0",
		},
		// Fix 2: no "host" field — Swagger UI detects origin automatically
		"basePath": "/",
		"schemes":  []string{"http", "https"},
		"consumes": []string{"application/json"},
		"produces": []string{"application/json"},
		"paths":    paths,
	}

	if hasAuth {
		spec["securityDefinitions"] = map[string]interface{}{
			"ApiKeyAuth": map[string]interface{}{
				"type":        "apiKey",
				"in":          "header",
				"name":        "X-API-Key",
				"description": "API key. Also accepted as ?api_key= query param.",
			},
		}
	}

	return spec
}

func swaggerJSONHandler(c *fiber.Ctx) error {
	c.Set("Content-Type", "application/json")
	data, _ := json.MarshalIndent(swaggerSpec(), "", "  ")
	return c.Send(data)
}

// swaggerUIHandler serves Swagger UI via CDN — no binary assets needed.
func swaggerUIHandler(c *fiber.Ctx) error {
	html := `<!DOCTYPE html>
<html>
<head>
  <title>FreeProxy API - Swagger UI</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  SwaggerUIBundle({
    url: "/swagger.json",
    dom_id: '#swagger-ui',
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true
  });
</script>
</body>
</html>`
	c.Set("Content-Type", "text/html")
	return c.SendString(html)
}
