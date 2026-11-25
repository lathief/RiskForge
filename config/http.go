package config

import (
	"fmt"
	"strings"

	"github.com/lathief/RiskForge/app/facades"

	fiberfacades "github.com/goravel/fiber/facades"
	"github.com/goravel/framework/contracts/route"
)

func init() {
	config := facades.Config()
	proxyHeader := strings.TrimSpace(fmt.Sprint(config.Env("HTTP_PROXY_HEADER", "")))
	trustedRaw := strings.TrimSpace(fmt.Sprint(config.Env("HTTP_TRUSTED_PROXIES", "")))
	var trustedProxies []string
	for _, p := range strings.Split(trustedRaw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			trustedProxies = append(trustedProxies, p)
		}
	}
	config.Add("http", map[string]any{
		// Read by support.ValidateConfig; the driver reads the same values from drivers.fiber.
		"proxy_header_env":  proxyHeader,
		"trusted_proxy_env": strings.Join(trustedProxies, ","),
		"default":           "fiber",
		// HTTP Drivers
		"drivers": map[string]any{
			"fiber": map[string]any{
				// immutable mode, see https://docs.gofiber.io/#zero-allocation
				// WARNING: This option is dangerous. Only change it if you fully understand the potential consequences.
				"immutable": true,
				// prefork mode, see https://docs.gofiber.io/api/fiber/#config
				"prefork": false,
				// Unit is KiB (the Fiber driver shifts it left by 10). All RiskForge payloads are small JSON.
				"body_limit":   64,
				"header_limit": 4096,
				// Behind a reverse proxy the client address is in a header (for example
				// X-Forwarded-For). It is only believed when the TCP peer is one of
				// HTTP_TRUSTED_PROXIES (comma separated addresses or CIDRs); startup refuses a
				// header without proxies, because any client could then choose its own address
				// and escape the login limit. Both empty (the default): the TCP peer is the client.
				"proxy_header":               proxyHeader,
				"trusted_proxies":            trustedProxies,
				"enable_trusted_proxy_check": len(trustedProxies) > 0,
				"route": func() (route.Route, error) {
					return fiberfacades.Route("fiber"), nil
				},
			},
		},
		// HTTP URL
		"url": config.Env("APP_URL", "http://localhost"),
		// HTTP Host
		"host": config.Env("APP_HOST", "127.0.0.1"),
		// HTTP Port
		"port": config.Env("APP_PORT", "3000"),
		// HTTP Timeout, default is 3 seconds
		"request_timeout": 3,
		// Requests handled at once. Beyond it a request is refused at once with 503
		// SERVICE_BUSY and Retry-After: 1, because a queue longer than the request timeout
		// only produces 408 for everybody (docs/STRESS.md). Health endpoints are exempt.
		// 0 switches the limit off.
		"max_in_flight": config.Env("HTTP_MAX_IN_FLIGHT", 64),
		// HTTPS Configuration
		"tls": map[string]any{
			// HTTPS Host
			"host": config.Env("APP_HOST", "127.0.0.1"),
			// HTTPS Port
			"port": config.Env("APP_PORT", "3000"),
			// SSL Certificate, you can put the certificate in /public folder
			"ssl": map[string]any{
				// ca.pem
				"cert": "",
				// ca.key
				"key": "",
			},
		},
		// Default Client Name
		//
		// This determines which client is used when you call facades.Http() or
		// facades.Http().Client() without passing a specific name.
		"default_client": config.Env("HTTP_CLIENT_DEFAULT", "default"),
		// Client Configurations
		//
		// Here you may define multiple independent client configurations.
		// For example, you might have a "github" client with a specific base URL
		// and a "stripe" client with a longer timeout.
		"clients": map[string]any{
			"default": map[string]any{
				// The base URL for the client. All requests made using this client
				// will be relative to this URL.
				"base_url": config.Env("HTTP_CLIENT_BASE_URL", ""),
				// The maximum amount of time a request can take, including connection
				// establishment, redirects, and reading the response body.
				"timeout": config.Env("HTTP_CLIENT_TIMEOUT", "30s"),
				// The maximum number of idle (keep-alive) connections to keep across
				// ALL hosts. Increasing this helps reuse TCP connections.
				"max_idle_conns": config.Env("HTTP_CLIENT_MAX_IDLE_CONNS", 100),
				// The maximum number of idle (keep-alive) connections to keep PER host.
				// By default, Go sets this to 2, which is often a bottleneck.
				// Increase this value for high-throughput applications.
				"max_idle_conns_per_host": config.Env("HTTP_CLIENT_MAX_IDLE_CONNS_PER_HOST", 2),
				// The maximum total number of connections (active + idle) allowed per host.
				// A value of 0 means no limit.
				"max_conns_per_host": config.Env("HTTP_CLIENT_MAX_CONN_PER_HOST", 0),
				// The maximum amount of time an idle (keep-alive) connection will remain
				// in the pool before closing itself.
				"idle_conn_timeout": config.Env("HTTP_CLIENT_IDLE_CONN_TIMEOUT", "90s"),
			},
		},
	})
}
