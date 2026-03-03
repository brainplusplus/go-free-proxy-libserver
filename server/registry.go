package main

import "github.com/gofiber/fiber/v2"

// RouteMeta holds metadata for a registered route.
// Used by swaggerSpec() to auto-generate OpenAPI documentation.
type RouteMeta struct {
	Method         string
	Path           string
	Summary        string
	QueryStruct    interface{}
	ResponseStruct interface{}
	RequireAuth    bool
}

var routeRegistry []RouteMeta

// RegisterGET registers a GET route and records its metadata for Swagger auto-generation.
func RegisterGET(
	group fiber.Router,
	path string,
	summary string,
	handler fiber.Handler,
	queryStruct interface{},
	responseStruct interface{},
	requireAuth bool,
) {
	group.Get(path, handler)
	routeRegistry = append(routeRegistry, RouteMeta{
		Method:         "get",
		Path:           path,
		Summary:        summary,
		QueryStruct:    queryStruct,
		ResponseStruct: responseStruct,
		RequireAuth:    requireAuth,
	})
}
