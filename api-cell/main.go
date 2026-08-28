package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/BananaLabs-OSS/Fiber/pulp/cellconfig"
	pulpgin "github.com/BananaLabs-OSS/Fiber/pulp/gin"
	"github.com/BananaLabs-OSS/Fiber/pulp/gin/middleware"
	"github.com/BananaLabs-OSS/Fiber/pulp/workflow"
)

const orchestratorCell = "lua-orchestrator"

func main() {}

func init() { pulp.OnInit(bootstrap) }

type appConfig struct {
	APIAddr      string
	ServiceToken string
}

type routeListResult struct {
	Routes map[string]string `msgpack:"routes"`
}

func parseConfig(data []byte) (appConfig, error) {
	if len(data) == 0 {
		return appConfig{}, fmt.Errorf("missing [config]")
	}
	var raw struct {
		APIAddr      string `json:"api_addr"`
		ServiceToken string `json:"service_token"`
	}
	if err := cellconfig.Decode(data, &raw); err != nil {
		return appConfig{}, err
	}
	if raw.APIAddr == "" {
		raw.APIAddr = ":8080"
	}
	if port := os.Getenv("HTTP_PORT"); port != "" {
		raw.APIAddr = ":" + port
	}
	if token := os.Getenv("SERVICE_TOKEN"); token != "" {
		raw.ServiceToken = token
	}
	return appConfig(raw), nil
}

func bootstrap(configBytes []byte) error {
	cfg, err := parseConfig(configBytes)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	defaultPort := os.Getenv("HTTP_PORT")
	if defaultPort == "" || cfg.APIAddr != ":"+defaultPort {
		if err := pulp.HTTP.Listen(cfg.APIAddr); err != nil {
			return fmt.Errorf("http listen %s: %w", cfg.APIAddr, err)
		}
	}
	engine := pulpgin.New()
	registerRoutes(engine, workflow.NewClient(orchestratorCell), cfg.ServiceToken)
	return engine.Run()
}

func commandID(c *pulpgin.Context, operation string) string {
	if key := c.GetHeader("Idempotency-Key"); key != "" {
		return operation + ":" + key
	}
	return operation + ":http-" + strconv.FormatUint(c.Request().ID, 10)
}

func dispatch[T any](client *workflow.Client, event string, payload any) (T, error) {
	result, err := client.Dispatch(workflow.DispatchRequest{Event: event, Payload: payload})
	if err != nil {
		var zero T
		return zero, err
	}
	return workflow.DecodeValue[T](result)
}

func registerRoutes(engine *pulpgin.Engine, client *workflow.Client, serviceToken string) {
	var mutating *pulpgin.RouterGroup
	if serviceToken != "" {
		mutating = engine.Group("", middleware.ServiceAuth(serviceToken))
	} else {
		mutating = engine.Group("")
	}

	mutating.POST("/routes", func(c *pulpgin.Context) {
		var request struct {
			PlayerIP string `json:"player_ip"`
			Backend  string `json:"backend"`
		}
		if err := c.BindJSON(&request); err != nil {
			c.String(400, "invalid json\n")
			return
		}
		if request.PlayerIP == "" || request.Backend == "" {
			c.String(400, "player_ip and backend required\n")
			return
		}
		if !validBackendAddr(request.Backend) {
			c.String(400, "invalid backend address\n")
			return
		}
		response, err := dispatch[map[string]any](client, "peel.http.route.set.v1", map[string]any{
			"id": commandID(c, "route-set"), "player_ip": request.PlayerIP, "backend": request.Backend,
		})
		if err != nil {
			c.String(500, "composition error: %v\n", err)
			return
		}
		writeJSONWithNewline(c, 200, response)
	})

	mutating.DELETE("/routes/:playerIP", func(c *pulpgin.Context) {
		playerIP := c.Param("playerIP")
		if playerIP == "" {
			c.String(400, "player_ip required\n")
			return
		}
		response, err := dispatch[map[string]any](client, "peel.http.route.delete.v1", map[string]any{
			"id": commandID(c, "route-delete"), "player_ip": playerIP,
		})
		if err != nil {
			c.String(500, "composition error: %v\n", err)
			return
		}
		writeJSONWithNewline(c, 200, response)
	})

	mutating.DELETE("/sessions/:playerIP", func(c *pulpgin.Context) {
		playerIP := c.Param("playerIP")
		if playerIP == "" {
			c.String(400, "player_ip required\n")
			return
		}
		response, err := dispatch[map[string]any](client, "peel.http.session.close.v1", map[string]any{
			"id": commandID(c, "session-close"), "player_ip": playerIP,
		})
		if err != nil {
			c.String(500, "composition error: %v\n", err)
			return
		}
		writeJSONWithNewline(c, 200, response)
	})

	engine.GET("/routes", func(c *pulpgin.Context) {
		response, err := dispatch[routeListResult](client, "peel.http.route.list.v1", map[string]any{})
		if err != nil {
			c.String(500, "composition error: %v\n", err)
			return
		}
		if response.Routes == nil {
			response.Routes = map[string]string{}
		}
		body, err := json.Marshal(response.Routes)
		if err != nil {
			c.String(500, "marshal error: %v", err)
			return
		}
		c.Data(200, "application/json", append(body, '\n'))
	})

	engine.GET("/health", func(c *pulpgin.Context) {
		response, err := dispatch[map[string]any](client, "peel.http.health.v1", map[string]any{})
		if err != nil {
			c.String(500, "composition error: %v\n", err)
			return
		}
		writeJSONWithNewline(c, 200, response)
	})
}

func writeJSONWithNewline(c *pulpgin.Context, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		c.String(500, "marshal error: %v", err)
		return
	}
	c.Data(status, "application/json; charset=utf-8", append(body, '\n'))
}
