package main

import (
	"github.com/gofiber/fiber/v2"

	freeproxy "github.com/brainplusplus/go-free-proxy-libserver"
)

// ProxyResponse wraps a single proxy result.
type ProxyResponse struct {
	Data  *freeproxy.FreeProxy `json:"data,omitempty"`
	Error string               `json:"error,omitempty"`
}

// ProxyListResponse wraps a list of proxies.
type ProxyListResponse struct {
	Data  []freeproxy.FreeProxy `json:"data"`
	Total int                   `json:"total"`
	Error string                `json:"error,omitempty"`
}

func getProxyHandler(c *fiber.Ctx) error {
	param := freeproxy.FreeProxyParameter{
		CategoryCode: c.Query("category_code"),
		TargetUrl:    c.Query("target_url"),
	}

	proxy, err := freeproxy.GetProxy(param)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ProxyResponse{
			Error: err.Error(),
		})
	}

	return c.JSON(ProxyResponse{Data: proxy})
}

func getProxyListHandler(c *fiber.Ctx) error {
	param := freeproxy.FreeProxyParameter{
		CategoryCode: c.Query("category_code"),
		TargetUrl:    c.Query("target_url"),
	}

	list, err := freeproxy.GetProxyList(param)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ProxyListResponse{
			Data:  []freeproxy.FreeProxy{},
			Total: 0,
			Error: err.Error(),
		})
	}

	if list == nil {
		list = []freeproxy.FreeProxy{}
	}

	return c.JSON(ProxyListResponse{
		Data:  list,
		Total: len(list),
	})
}
