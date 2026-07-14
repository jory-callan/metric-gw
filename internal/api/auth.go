package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"metric-gw/internal/config"

	"github.com/labstack/echo/v4"
)

// AuthMiddleware 创建认证中间件，支持 apikey / basic / none 三种模式
func AuthMiddleware(authCfg config.AuthConfig) echo.MiddlewareFunc {
	switch authCfg.Mode {
	case "apikey":
		return apiKeyAuth(authCfg.APIKey)
	case "basic":
		return basicAuth(authCfg.Username, authCfg.Password)
	default:
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return next
		}
	}
}

// apiKeyAuth X-API-Key header 认证
func apiKeyAuth(expectedKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			provided := c.Request().Header.Get("X-API-Key")
			if provided == "" {
				// 也支持 Authorization: Bearer <key>
				authHeader := c.Request().Header.Get("Authorization")
				if strings.HasPrefix(authHeader, "Bearer ") {
					provided = strings.TrimPrefix(authHeader, "Bearer ")
				}
			}

			if subtle.ConstantTimeCompare([]byte(provided), []byte(expectedKey)) != 1 {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid or missing API key",
				})
			}
			return next(c)
		}
	}
}

// basicAuth HTTP Basic Auth
func basicAuth(expectedUser, expectedPass string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user, pass, ok := c.Request().BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(expectedPass)) != 1 {
				c.Response().Header().Set("WWW-Authenticate", `Basic realm="metric-gw"`)
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid or missing credentials",
				})
			}
			return next(c)
		}
	}
}
